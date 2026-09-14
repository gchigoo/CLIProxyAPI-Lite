package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRemovedPluginManagementAndResourceRoutesReturn404(t *testing.T) {
	t.Setenv("MANAGEMENT_PASSWORD", "test-management-key")

	server := newTestServer(t)

	// 1. Plugin management routes must return 404 even with valid management auth.
	pluginManagementRoutes := []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/v0/management/plugins"},
		{http.MethodGet, "/v0/management/plugin-store"},
		{http.MethodPost, "/v0/management/plugin-store/sample/install"},
		{http.MethodDelete, "/v0/management/plugins/sample"},
		{http.MethodPatch, "/v0/management/plugins/sample/enabled"},
		{http.MethodGet, "/v0/management/plugins/sample/config"},
		{http.MethodPut, "/v0/management/plugins/sample/config"},
		{http.MethodPatch, "/v0/management/plugins/sample/config"},
	}

	for _, route := range pluginManagementRoutes {
		t.Run("management_"+route.method+"_"+route.path, func(t *testing.T) {
			req := httptest.NewRequest(route.method, route.path, strings.NewReader(`{}`))
			req.Header.Set("Authorization", "Bearer test-management-key")
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			server.engine.ServeHTTP(rec, req)

			if rec.Code != http.StatusNotFound {
				t.Fatalf("%s %s status = %d, want %d; body = %s", route.method, route.path, rec.Code, http.StatusNotFound, rec.Body.String())
			}
		})
	}

	// 2. Plugin resource routes must return 404 without route bypass exemptions.
	pluginResourceRoutes := []string{
		"/v0/resource/plugins/sample/logo.png",
		"/v0/resource/plugins/sample/manifest.json",
		"/v0/resource/plugins/",
	}

	for _, path := range pluginResourceRoutes {
		t.Run("resource_"+path, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, path, nil)
			req.Header.Set("Authorization", "Bearer test-management-key")
			rec := httptest.NewRecorder()
			server.engine.ServeHTTP(rec, req)

			if rec.Code != http.StatusNotFound {
				t.Fatalf("GET %s status = %d, want %d; body = %s", path, rec.Code, http.StatusNotFound, rec.Body.String())
			}
		})
	}

	// 3. Dynamic plugin auth endpoints must return 404.
	pluginOAuthRoutes := []string{
		"/v0/management/sample-auth-url",
		"/v0/management/gemini-cli-auth-url",
		"/v0/management/custom-provider-auth-url",
	}

	for _, path := range pluginOAuthRoutes {
		t.Run("oauth_"+path, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, path, nil)
			req.Header.Set("Authorization", "Bearer test-management-key")
			rec := httptest.NewRecorder()
			server.engine.ServeHTTP(rec, req)

			if rec.Code != http.StatusNotFound {
				t.Fatalf("GET %s status = %d, want %d; body = %s", path, rec.Code, http.StatusNotFound, rec.Body.String())
			}
		})
	}
}

func TestNativeManagementRouteWorksAndExposesSupportHeaderZero(t *testing.T) {
	t.Setenv("MANAGEMENT_PASSWORD", "test-management-key")

	server := newTestServer(t)

	// 1. Unauthenticated management request returns 401 and X-CPA-SUPPORT-PLUGIN: 0
	{
		req := httptest.NewRequest(http.MethodGet, "/v0/management/config", nil)
		rec := httptest.NewRecorder()
		server.engine.ServeHTTP(rec, req)

		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("unauthenticated status = %d, want %d", rec.Code, http.StatusUnauthorized)
		}
		if got := rec.Header().Get("X-CPA-SUPPORT-PLUGIN"); got != "0" {
			t.Fatalf("X-CPA-SUPPORT-PLUGIN = %q, want %q", got, "0")
		}
	}

	// 2. Authenticated management request returns 200 and X-CPA-SUPPORT-PLUGIN: 0
	{
		req := httptest.NewRequest(http.MethodGet, "/v0/management/config", nil)
		req.Header.Set("Authorization", "Bearer test-management-key")
		rec := httptest.NewRecorder()
		server.engine.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("authenticated status = %d, want %d; body = %s", rec.Code, http.StatusOK, rec.Body.String())
		}
		if got := rec.Header().Get("X-CPA-SUPPORT-PLUGIN"); got != "0" {
			t.Fatalf("X-CPA-SUPPORT-PLUGIN = %q, want %q", got, "0")
		}
	}
}
