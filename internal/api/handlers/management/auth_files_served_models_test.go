package management

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/servedmodel"
	coreauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
	coreusage "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/usage"
)

func listAuthFileEntries(t *testing.T, manager *coreauth.Manager) map[string]map[string]any {
	t.Helper()
	h := NewHandlerWithoutConfigFilePath(&config.Config{AuthDir: t.TempDir()}, manager)
	h.tokenStore = &memoryAuthStore{}

	rec := httptest.NewRecorder()
	ginCtx, _ := gin.CreateTestContext(rec)
	ginCtx.Request = httptest.NewRequest(http.MethodGet, "/v0/management/auth-files", nil)
	h.ListAuthFiles(ginCtx)
	if rec.Code != http.StatusOK {
		t.Fatalf("list status = %d, body %s", rec.Code, rec.Body.String())
	}
	var payload struct {
		Files []map[string]any `json:"files"`
	}
	if errUnmarshal := json.Unmarshal(rec.Body.Bytes(), &payload); errUnmarshal != nil {
		t.Fatalf("decode list payload: %v", errUnmarshal)
	}
	entries := make(map[string]map[string]any, len(payload.Files))
	for _, entry := range payload.Files {
		id, _ := entry["id"].(string)
		entries[id] = entry
	}
	return entries
}

func TestListAuthFiles_IncludesServedModelsWhenObserved(t *testing.T) {
	t.Setenv("MANAGEMENT_PASSWORD", "")

	manager := coreauth.NewManager(nil, nil, nil)
	for _, id := range []string{"served-models-observed", "served-models-unobserved"} {
		record := &coreauth.Auth{
			ID:         id,
			Provider:   "codex",
			Attributes: map[string]string{"runtime_only": "true"},
			Metadata:   map[string]any{"type": "codex"},
		}
		if _, errRegister := manager.Register(context.Background(), record); errRegister != nil {
			t.Fatalf("register %s: %v", id, errRegister)
		}
	}

	ctx := context.Background()
	tracker := servedmodel.Default()
	tracker.HandleUsage(ctx, coreusage.Record{AuthID: "served-models-observed", Model: "gpt-5.6-sol", ResponseModel: "gpt-5.6-sol"})
	tracker.HandleUsage(ctx, coreusage.Record{AuthID: "served-models-observed", Model: "gpt-5.6-sol", ResponseModel: "gpt-5.5-mini"})

	entries := listAuthFileEntries(t, manager)

	if _, ok := entries["served-models-unobserved"]["served_models"]; ok {
		t.Fatalf("served_models must be absent without observations: %#v", entries["served-models-unobserved"])
	}
	raw, ok := entries["served-models-observed"]["served_models"].([]any)
	if !ok || len(raw) != 2 {
		t.Fatalf("served_models = %#v, want two entries", entries["served-models-observed"]["served_models"])
	}
	latest, ok := raw[0].(map[string]any)
	if !ok {
		t.Fatalf("served_models[0] = %#v, want object", raw[0])
	}
	if latest["requested_model"] != "gpt-5.6-sol" || latest["served_model"] != "gpt-5.5-mini" || latest["substituted"] != true || latest["count"] != float64(1) {
		t.Fatalf("latest served model entry = %#v", latest)
	}
	for _, field := range []string{"first_seen", "last_seen"} {
		if value, _ := latest[field].(string); value == "" {
			t.Fatalf("served model entry missing %s: %#v", field, latest)
		}
	}
	if previous := raw[1].(map[string]any); previous["substituted"] != false {
		t.Fatalf("matching served model entry = %#v, want substituted false", previous)
	}
}
