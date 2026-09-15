package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"sync/atomic"
	"testing"

	coreauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
)

func TestDefaultAntigravityFetchBaseURLs(t *testing.T) {
	want := []string{
		antigravityBaseURLDaily,
		antigravityBaseURLProd,
		antigravitySandboxBaseURLDaily,
	}

	got := defaultAntigravityFetchBaseURLs()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("defaultAntigravityFetchBaseURLs() = %#v, want %#v", got, want)
	}
}

func TestFetchModelsRetryPerEndpoint(t *testing.T) {
	var endpoint1Calls atomic.Int32
	var endpoint2Calls atomic.Int32

	server1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		call := endpoint1Calls.Add(1)
		if call == 1 {
			http.Error(w, `{"error":"temporary server error"}`, http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{
			"models": {
				"gemini-3.6-flash": {
					"displayName": "Gemini 3.6 Flash",
					"maxTokens": 1048576,
					"maxOutputTokens": 8192
				}
			}
		}`))
	}))
	defer server1.Close()

	server2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		endpoint2Calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{
			"models": {
				"gemini-1.5-pro": {
					"displayName": "Gemini 1.5 Pro"
				}
			}
		}`))
	}))
	defer server2.Close()

	auth := &coreauth.Auth{
		Metadata: map[string]interface{}{
			"access_token": "test-token",
			"project_id":   "test-project",
		},
	}

	// Case 1: First endpoint fails on attempt 1, succeeds on attempt 2.
	// It should succeed without calling endpoint 2, and endpoint 1 should have been called 2 times.
	models := fetchModelsFromBaseURLs(context.Background(), auth, []string{server1.URL, server2.URL}, server1.Client())
	if len(models) != 1 || models[0].ID != "gemini-3.6-flash" {
		t.Fatalf("expected 1 model (gemini-3.6-flash), got: %#v", models)
	}
	if calls := endpoint1Calls.Load(); calls != 2 {
		t.Fatalf("expected endpoint 1 to be called 2 times, got %d", calls)
	}
	if calls := endpoint2Calls.Load(); calls != 0 {
		t.Fatalf("expected endpoint 2 not to be called, got %d", calls)
	}
}

func TestFetchModelsFallbackAfterTwoAttempts(t *testing.T) {
	var endpoint1Calls atomic.Int32
	var endpoint2Calls atomic.Int32

	server1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		endpoint1Calls.Add(1)
		http.Error(w, `{"error":"unavailable"}`, http.StatusServiceUnavailable)
	}))
	defer server1.Close()

	server2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		endpoint2Calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{
			"models": {
				"gemini-2.5-flash": {
					"displayName": "Gemini 2.5 Flash"
				}
			}
		}`))
	}))
	defer server2.Close()

	auth := &coreauth.Auth{
		Metadata: map[string]interface{}{
			"access_token": "test-token",
		},
	}

	// Case 2: First endpoint fails all 2 attempts, then falls back to endpoint 2.
	models := fetchModelsFromBaseURLs(context.Background(), auth, []string{server1.URL, server2.URL}, server1.Client())
	if len(models) != 1 || models[0].ID != "gemini-2.5-flash" {
		t.Fatalf("expected 1 model (gemini-2.5-flash), got: %#v", models)
	}
	if calls := endpoint1Calls.Load(); calls != 2 {
		t.Fatalf("expected endpoint 1 to be called 2 times before fallback, got %d", calls)
	}
	if calls := endpoint2Calls.Load(); calls != 1 {
		t.Fatalf("expected endpoint 2 to be called 1 time, got %d", calls)
	}
}

func TestResolveCatalogAuthDir(t *testing.T) {
	wd := t.TempDir()
	local := filepath.Join(wd, "auths")
	if err := os.Mkdir(local, 0o700); err != nil {
		t.Fatal(err)
	}
	canonical, err := filepath.EvalSymlinks(local)
	if err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(wd, "missing")
	if got := resolveCatalogAuthDir(wd, missing, false); got != canonical {
		t.Fatalf("default missing directory = %q, want local fallback %q", got, canonical)
	}
	if got := resolveCatalogAuthDir(wd, missing, true); got != missing {
		t.Fatalf("explicit directory was silently replaced: %q", got)
	}
	t.Run("symlink", func(t *testing.T) {
		link := filepath.Join(wd, "linked-auths")
		if err := os.Symlink(local, link); err != nil {
			t.Skipf("symlink unavailable: %v", err)
		}
		if got := resolveCatalogAuthDir(wd, link, true); got != canonical {
			t.Fatalf("symlink directory = %q, want %q", got, canonical)
		}
	})
}

func TestEnabledAntigravityAuths(t *testing.T) {
	active := &coreauth.Auth{ID: "active.json", Provider: " Antigravity "}
	backup := &coreauth.Auth{ID: "account.backup.json", Provider: "antigravity"}
	disabled := &coreauth.Auth{ID: "disabled.json", Provider: "antigravity", Disabled: true}
	other := &coreauth.Auth{ID: "codex.json", Provider: "codex"}
	if got := enabledAntigravityAuths([]*coreauth.Auth{nil, backup, disabled, other, active}); !reflect.DeepEqual(got, []*coreauth.Auth{active}) {
		t.Fatalf("active candidates = %#v", got)
	}
	if got := enabledAntigravityAuths([]*coreauth.Auth{disabled, backup, other}); !reflect.DeepEqual(got, []*coreauth.Auth{backup}) {
		t.Fatalf("backup candidates = %#v", got)
	}
	if got := enabledAntigravityAuths([]*coreauth.Auth{nil, disabled, other}); len(got) != 0 {
		t.Fatalf("unexpected eligible auths: %#v", got)
	}
}

func TestFetchModelsWithAuthFallback(t *testing.T) {
	var expiredCalls, validCalls, unusedCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Header.Get("Authorization") {
		case "Bearer expired":
			expiredCalls.Add(1)
			http.Error(w, "expired test token", http.StatusUnauthorized)
		case "Bearer valid":
			validCalls.Add(1)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"models":{"gemini-test":{"displayName":"Test model"}}}`))
		default:
			unusedCalls.Add(1)
			http.Error(w, "unexpected auth", http.StatusBadRequest)
		}
	}))
	defer server.Close()
	var auths []*coreauth.Auth
	for _, token := range []string{"expired", "valid", "unused"} {
		auths = append(auths, &coreauth.Auth{ID: token, Provider: "antigravity", Metadata: map[string]any{"access_token": token}})
	}
	var attempts []context.Context
	models := fetchModelsWithAuthFallback(context.Background(), auths, func(ctx context.Context, auth *coreauth.Auth) []modelEntry {
		if _, ok := ctx.Deadline(); !ok {
			t.Fatal("catalog attempt lost its deadline")
		}
		attempts = append(attempts, ctx)
		return fetchModelsFromBaseURLs(ctx, auth, []string{server.URL}, server.Client())
	})
	if len(models) != 1 || models[0].ID != "gemini-test" || expiredCalls.Load() == 0 || validCalls.Load() != 1 || unusedCalls.Load() != 0 {
		t.Fatalf("fallback models=%v calls=%d/%d/%d", models, expiredCalls.Load(), validCalls.Load(), unusedCalls.Load())
	}
	for _, ctx := range attempts {
		if ctx.Err() == nil {
			t.Fatal("finished catalog attempt did not release its context")
		}
	}
}

func TestFetchModelsWithAuthFallbackStopsOnCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	calls := 0
	models := fetchModelsWithAuthFallback(ctx, []*coreauth.Auth{{ID: "first"}, {ID: "second"}}, func(context.Context, *coreauth.Auth) []modelEntry {
		calls++
		cancel()
		return nil
	})
	if calls != 1 || len(models) != 0 {
		t.Fatalf("canceled fallback calls=%d models=%v", calls, models)
	}
}
