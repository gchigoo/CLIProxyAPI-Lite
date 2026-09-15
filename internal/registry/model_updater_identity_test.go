package registry

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
)

func codexIdentityCatalogFixture(t *testing.T, displayName string) []byte {
	t.Helper()
	var catalog staticModelsJSON
	if err := json.Unmarshal(embeddedModelsJSON, &catalog); err != nil {
		t.Fatal(err)
	}
	for _, models := range [][]*ModelInfo{catalog.CodexFree, catalog.CodexTeam, catalog.CodexPlus, catalog.CodexPro} {
		for _, model := range models {
			model.DisplayName = displayName
			model.Config = &ModelConfig{OverrideHeader: map[string]string{
				"User-Agent": "codex-tui/0.154.0", "ORIGINATOR": "codex-tui", " Version ": "old",
				"Session-Id": "fixed", "session_id": "fixed", "Conversation_id": "fixed",
				"Thread-Id": "fixed", "X-Client-Request-Id": "fixed", "X-Codex-Installation-Id": "fixed",
				"X-Codex-Window-Id": "fixed", "X-Codex-Turn-Metadata": "fixed", "X-Capability": "retained",
			}}
		}
	}
	catalog.Claude[0].Config = &ModelConfig{OverrideHeader: map[string]string{"User-Agent": "claude-test-agent"}}
	data, err := json.Marshal(catalog)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func assertCodexCatalogIdentity(t *testing.T, catalog *staticModelsJSON, displayName string) {
	t.Helper()
	for plan, models := range map[string][]*ModelInfo{
		"free": catalog.CodexFree, "team": catalog.CodexTeam, "plus": catalog.CodexPlus, "pro": catalog.CodexPro,
	} {
		for _, model := range models {
			if model.Config == nil || !reflect.DeepEqual(model.Config.OverrideHeader, map[string]string{"X-Capability": "retained"}) {
				t.Fatalf("%s/%s overrides = %#v, want only the non-identity header", plan, model.ID, model.Config)
			}
			if model.DisplayName != displayName {
				t.Fatalf("%s/%s display name = %q, want refreshed metadata %q", plan, model.ID, model.DisplayName, displayName)
			}
		}
	}
	if got := catalog.Claude[0].Config.OverrideHeader["User-Agent"]; got != "claude-test-agent" {
		t.Fatalf("non-Codex catalog identity changed: %q", got)
	}
}

func TestLoadModelsFromBytesPreservesAccountIdentity(t *testing.T) {
	original := getModels()
	t.Cleanup(func() {
		modelsCatalogStore.mu.Lock()
		modelsCatalogStore.data = original
		modelsCatalogStore.mu.Unlock()
	})
	if err := loadModelsFromBytes(codexIdentityCatalogFixture(t, "refreshed"), "test"); err != nil {
		t.Fatal(err)
	}
	assertCodexCatalogIdentity(t, getModels(), "refreshed")
	if got := ModelOverrideHeaders("gpt-5.6-luna"); !reflect.DeepEqual(got, map[string]string{"X-Capability": "retained"}) {
		t.Fatalf("Luna lookup restored catalog identity: %v", got)
	}

	reg := GetGlobalRegistry()
	const clientID = "explicit-codex-identity-test"
	reg.RegisterClient(clientID, "codex", []*ModelInfo{{
		ID: "gpt-5.6-luna", Config: &ModelConfig{OverrideHeader: map[string]string{"User-Agent": "explicit-user-agent"}},
	}})
	t.Cleanup(func() { reg.UnregisterClient(clientID) })
	if got := ModelOverrideHeaders("gpt-5.6-luna")["User-Agent"]; got != "explicit-user-agent" {
		t.Fatalf("explicit model override was filtered: %q", got)
	}
}

func TestModelRefreshFiltersIdentityBeforePublishing(t *testing.T) {
	original, originalURLs := getModels(), modelsURLs
	refreshCallbackMu.Lock()
	originalCallback, originalPending := refreshCallback, pendingRefreshChanges
	refreshCallback, pendingRefreshChanges = nil, nil
	refreshCallbackMu.Unlock()
	t.Cleanup(func() {
		modelsCatalogStore.mu.Lock()
		modelsCatalogStore.data = original
		modelsCatalogStore.mu.Unlock()
		modelsURLs = originalURLs
		refreshCallbackMu.Lock()
		refreshCallback, pendingRefreshChanges = originalCallback, originalPending
		refreshCallbackMu.Unlock()
	})

	startupData := codexIdentityCatalogFixture(t, "startup")
	periodicData := codexIdentityCatalogFixture(t, "periodic")
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		request := requests.Add(1)
		w.Header().Set("Content-Type", "application/json")
		if request == 1 {
			_, _ = w.Write(startupData)
		} else {
			_, _ = w.Write(periodicData)
		}
	}))
	defer server.Close()
	modelsURLs = []string{server.URL}
	callbacks := 0
	SetModelRefreshCallback(func(changed []string) {
		callbacks++
		if !strings.Contains(strings.Join(changed, ","), "codex") {
			t.Fatalf("changed providers = %v, want codex", changed)
		}
		assertCodexCatalogIdentity(t, getModels(), map[int]string{1: "startup", 2: "periodic"}[callbacks])
	})
	tryStartupRefresh(context.Background())
	tryPeriodicRefresh(context.Background())
	tryPeriodicRefresh(context.Background())
	if callbacks != 2 {
		t.Fatalf("refresh callbacks = %d, want two metadata changes and no repeated notification", callbacks)
	}
}

func TestSanitizeCodexCatalogIdentityDropsEmptyConfig(t *testing.T) {
	model := &ModelInfo{ID: "gpt-5.6-luna", Config: &ModelConfig{OverrideHeader: map[string]string{
		"user-agent": "codex-tui/old", "originator": "codex-tui",
	}}}
	catalog := &staticModelsJSON{CodexPro: []*ModelInfo{model}}
	sanitizeCodexCatalogIdentity(catalog)
	if model.Config != nil {
		t.Fatalf("identity-only config = %#v, want nil", model.Config)
	}
}
