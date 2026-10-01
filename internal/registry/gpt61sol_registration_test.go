package registry

import (
	"encoding/json"
	"testing"
)

func TestGPT61SolRegistrationPreservesAccountIdentity(t *testing.T) {
	for tier, models := range map[string][]*ModelInfo{"codex-plus": GetCodexPlusModels(), "codex-pro": GetCodexProModels(), "codex-team": GetCodexTeamModels()} {
		t.Run(tier, func(t *testing.T) {
			for _, model := range models {
				if model.ID == "gpt-6.1-sol" {
					if model.Thinking == nil || len(model.Thinking.Levels) == 0 {
						t.Fatal("missing reasoning capabilities")
					}
					return
				}
			}
			t.Fatal("missing GPT-6.1 Sol")
		})
	}
	for _, model := range GetCodexFreeModels() {
		if model.ID == "gpt-6.1-sol" {
			t.Fatal("GPT-6.1 Sol must not be offered on the free plan")
		}
	}
	if got := ModelOverrideHeaders("gpt-6.1-sol"); got != nil {
		t.Fatalf("catalog must not override account identity: %#v", got)
	}
}

func TestGPT61SolInEmbeddedCodexClientCatalog(t *testing.T) {
	var catalog struct {
		Models []struct {
			Slug                 string `json:"slug"`
			MinimalClientVersion string `json:"minimal_client_version"`
		} `json:"models"`
	}
	if err := json.Unmarshal(GetCodexClientModelsJSON(), &catalog); err != nil {
		t.Fatalf("decode embedded catalog: %v", err)
	}
	for _, model := range catalog.Models {
		if model.Slug == "gpt-6.1-sol" {
			// The pinned native client identity (0.155.0) must satisfy the model's floor.
			if model.MinimalClientVersion != "0.153.0" {
				t.Fatalf("minimal_client_version = %q, want 0.153.0", model.MinimalClientVersion)
			}
			return
		}
	}
	t.Fatal("embedded Codex client catalog is missing gpt-6.1-sol")
}
