package registry

import "testing"

func TestSol47RegistrationPreservesAccountIdentity(t *testing.T) {
	for tier, models := range map[string][]*ModelInfo{"codex-plus": GetCodexPlusModels(), "codex-pro": GetCodexProModels(), "codex-team": GetCodexTeamModels()} {
		t.Run(tier, func(t *testing.T) {
			found := false
			for _, model := range models {
				if model.ID == "gpt-6-sol" {
					found = true
					if model.Thinking == nil || len(model.Thinking.Levels) == 0 {
						t.Fatal("missing reasoning capabilities")
					}
				}
			}
			if !found {
				t.Fatal("missing GPT-6 Sol")
			}
		})
	}
	if got := ModelOverrideHeaders("gpt-6-sol"); got != nil {
		t.Fatalf("catalog must not override account identity: %#v", got)
	}
	for _, model := range GetXAIModels() {
		if model.ID == "grok-4.7" {
			if model.ContextLength != 500000 || model.MaxCompletionTokens != 500000 {
				t.Fatal("Grok 4.7 must not inherit the old fixed output cap")
			}
			return
		}
	}
	t.Fatal("missing Grok 4.7")
}
