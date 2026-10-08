package executor

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/registry"
	sdkauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/auth"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/executor"
	sdktranslator "github.com/router-for-me/CLIProxyAPI/v7/sdk/translator"
)

type metaFailingStore struct{}

func (metaFailingStore) List(context.Context) ([]*cliproxyauth.Auth, error) { return nil, nil }
func (metaFailingStore) Save(context.Context, *cliproxyauth.Auth) (string, error) {
	return "", errors.New("disk full")
}
func (metaFailingStore) Delete(context.Context, string) error { return nil }

func executeMetaWithoutPanic(manager *cliproxyauth.Manager) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("panic: %v", recovered)
		}
	}()
	req := cliproxyexecutor.Request{
		Model:   "muse-spark-1.3",
		Payload: []byte(`{"model":"muse-spark-1.3","messages":[{"role":"user","content":"hi"}]}`),
	}
	_, err = manager.Execute(context.Background(), []string{"meta"}, req, cliproxyexecutor.Options{SourceFormat: sdktranslator.FromString("openai")})
	return err
}

// A Meta mint that cannot be persisted must fail the request with an error,
// not panic in the conductor.
func TestMetaPreparePersistFailureReturnsError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/key" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"api_key":"LLM|minted"}`))
			return
		}
		writeMetaResponsesOK(w, "hi")
	}))
	defer server.Close()
	t.Setenv("META_MINT_URL", server.URL+"/key")

	manager := cliproxyauth.NewManager(metaFailingStore{}, nil, nil)
	manager.RegisterExecutor(NewMetaExecutor(&config.Config{}))
	auth := &cliproxyauth.Auth{
		ID:         "meta-persist-failure",
		Provider:   "meta",
		Metadata:   map[string]any{"type": "meta", "dca_token": "dca:persist", "auth_kind": "oauth"},
		Attributes: map[string]string{"base_url": server.URL},
	}
	if _, errRegister := manager.Register(cliproxyauth.WithSkipPersist(context.Background()), auth); errRegister != nil {
		t.Fatal(errRegister)
	}
	registry.GetGlobalRegistry().RegisterClient(auth.ID, "meta", []*registry.ModelInfo{{ID: "muse-spark-1.3"}})
	t.Cleanup(func() { registry.GetGlobalRegistry().UnregisterClient(auth.ID) })

	err := executeMetaWithoutPanic(manager)
	if err == nil {
		t.Fatal("Execute() error = nil, want the persist failure")
	}
	if strings.HasPrefix(err.Error(), "panic:") {
		t.Fatalf("Execute() panicked: %v", err)
	}
}

// A Meta account removed while its first mint is in flight must fail the
// request with an error, not panic in the conductor.
func TestMetaPrepareRemovedDuringMintReturnsError(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/key" {
			close(started)
			<-release
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"api_key":"LLM|minted"}`))
			return
		}
		writeMetaResponsesOK(w, "hi")
	}))
	defer server.Close()
	t.Setenv("META_MINT_URL", server.URL+"/key")

	manager := cliproxyauth.NewManager(nil, nil, nil)
	manager.RegisterExecutor(NewMetaExecutor(&config.Config{}))
	auth := &cliproxyauth.Auth{
		ID:         "meta-removed-during-mint",
		Provider:   "meta",
		Metadata:   map[string]any{"type": "meta", "dca_token": "dca:removed", "auth_kind": "oauth"},
		Attributes: map[string]string{"base_url": server.URL},
	}
	if _, errRegister := manager.Register(context.Background(), auth); errRegister != nil {
		t.Fatal(errRegister)
	}
	registry.GetGlobalRegistry().RegisterClient(auth.ID, "meta", []*registry.ModelInfo{{ID: "muse-spark-1.3"}})
	t.Cleanup(func() { registry.GetGlobalRegistry().UnregisterClient(auth.ID) })

	done := make(chan error, 1)
	go func() { done <- executeMetaWithoutPanic(manager) }()
	<-started
	manager.Remove(context.Background(), auth.ID)
	close(release)

	err := <-done
	if err == nil {
		t.Fatal("Execute() error = nil, want the removed-credential failure")
	}
	if strings.HasPrefix(err.Error(), "panic:") {
		t.Fatalf("Execute() panicked: %v", err)
	}
}

// A watcher reload keeps the registration epoch and arrives through Update.
// A mint that started before the reload must not replace the reloaded key.
func TestMetaPrepareKeepsKeyReloadedDuringMint(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-release
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"api_key":"LLM|obsolete"}`))
	}))
	defer server.Close()
	t.Setenv("META_MINT_URL", server.URL)

	dir := t.TempDir()
	store := sdkauth.NewFileTokenStore()
	store.SetBaseDir(dir)
	manager := cliproxyauth.NewManager(store, nil, nil)
	path := filepath.Join(dir, "meta-reload.json")
	auth, errRegister := manager.Register(context.Background(), &cliproxyauth.Auth{
		ID:         "meta-reload.json",
		Provider:   "meta",
		Attributes: map[string]string{cliproxyauth.AttributePath: path},
		Metadata:   map[string]any{"type": "meta", "access_token": "dca:initial", "dca_token": "dca:initial"},
	})
	if errRegister != nil {
		t.Fatal(errRegister)
	}

	done := make(chan error, 1)
	go func() {
		_, errPrepare := manager.PrepareRequestAuth(context.Background(), NewMetaExecutor(nil), auth.Clone())
		done <- errPrepare
	}()
	<-started
	if _, errUpdate := manager.Update(context.Background(), &cliproxyauth.Auth{
		ID:         "meta-reload.json",
		Provider:   "meta",
		Attributes: map[string]string{cliproxyauth.AttributePath: path},
		Metadata:   map[string]any{"type": "meta", "api_key": "LLM|reloaded", "access_token": "LLM|reloaded", "dca_token": "dca:reloaded"},
	}); errUpdate != nil {
		t.Fatal(errUpdate)
	}
	close(release)
	<-done

	live, ok := manager.GetByID("meta-reload.json")
	if !ok || live == nil {
		t.Fatal("reloaded credential missing")
	}
	if _, token := metaCreds(live); token != "LLM|reloaded" {
		t.Fatalf("live request token = %q, want the reloaded key", token)
	}
}
