package cliproxy

import (
	"reflect"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/config"
)

func xaiImplicitAliasTestModels() []*ModelInfo {
	return []*ModelInfo{
		{ID: "grok-4.7", Name: "models/grok-4.7", OwnedBy: "xai", Created: 1789948800},
		{ID: "grok-4.8", Name: "models/grok-4.8", OwnedBy: "xai", Created: 1790000000},
		{ID: "grok-4.7-build-fast", OwnedBy: "xai"},
		{ID: "grok-3-mini", OwnedBy: "xai"},
	}
}

func xaiImplicitAliasTestIDs(models []*ModelInfo) []string {
	ids := make([]string, 0, len(models))
	for _, model := range models {
		ids = append(ids, model.ID)
	}
	return ids
}

func TestApplyOAuthModelAlias_XAIImplicitAliases(t *testing.T) {
	out := applyOAuthModelAlias(&config.Config{}, "xai", "oauth", xaiImplicitAliasTestModels())

	want := []string{"grok-4.7", "cpa-x47", "grok-4.8", "cpa-x48", "grok-4.7-build-fast", "grok-3-mini"}
	if got := xaiImplicitAliasTestIDs(out); !reflect.DeepEqual(got, want) {
		t.Fatalf("model IDs = %v, want %v", got, want)
	}
	alias := out[3]
	if alias.OwnedBy != "xai" || alias.Created != 1790000000 || alias.MetadataModelID != "grok-4.8" || alias.Name != "models/cpa-x48" {
		t.Fatalf("implicit alias = %+v, want grok-4.8 metadata under cpa-x48", alias)
	}
}

func TestApplyOAuthModelAlias_XAIConfiguredForkKeepsDisplayName(t *testing.T) {
	cfg := &config.Config{OAuthModelAlias: map[string][]config.OAuthModelAlias{
		"xai": {{Name: "grok-4.7", Alias: "cpa-x47", Fork: true, DisplayName: "Configured X47"}},
	}}
	out := applyOAuthModelAlias(cfg, "xai", "oauth", xaiImplicitAliasTestModels())

	want := []string{"grok-4.7", "cpa-x47", "grok-4.8", "cpa-x48", "grok-4.7-build-fast", "grok-3-mini"}
	if got := xaiImplicitAliasTestIDs(out); !reflect.DeepEqual(got, want) {
		t.Fatalf("model IDs = %v, want %v", got, want)
	}
	if out[1].DisplayName != "Configured X47" {
		t.Fatalf("cpa-x47 display name = %q, want the configured one", out[1].DisplayName)
	}
}

func TestApplyOAuthModelAlias_XAIConfiguredRenameOwnsModel(t *testing.T) {
	cfg := &config.Config{OAuthModelAlias: map[string][]config.OAuthModelAlias{
		"xai": {{Name: "grok-4.7", Alias: "my-grok"}},
	}}
	out := applyOAuthModelAlias(cfg, "xai", "oauth", xaiImplicitAliasTestModels())

	want := []string{"my-grok", "grok-4.8", "cpa-x48", "grok-4.7-build-fast", "grok-3-mini"}
	if got := xaiImplicitAliasTestIDs(out); !reflect.DeepEqual(got, want) {
		t.Fatalf("model IDs = %v, want %v", got, want)
	}
}

func TestApplyOAuthModelAlias_XAIImplicitAliasesSkipAPIKey(t *testing.T) {
	out := applyOAuthModelAlias(&config.Config{}, "xai", "apikey", xaiImplicitAliasTestModels())

	want := []string{"grok-4.7", "grok-4.8", "grok-4.7-build-fast", "grok-3-mini"}
	if got := xaiImplicitAliasTestIDs(out); !reflect.DeepEqual(got, want) {
		t.Fatalf("model IDs = %v, want %v", got, want)
	}
}

func TestApplyOAuthModelAlias_XAIImplicitAliasesDoNotMutateConfig(t *testing.T) {
	configured := make([]config.OAuthModelAlias, 1, 4)
	configured[0] = config.OAuthModelAlias{Name: "grok-4.6", Alias: "cpa-x46", Fork: true}
	cfg := &config.Config{OAuthModelAlias: map[string][]config.OAuthModelAlias{"xai": configured}}

	applyOAuthModelAlias(cfg, "xai", "oauth", xaiImplicitAliasTestModels())

	if len(cfg.OAuthModelAlias["xai"]) != 1 || configured[:2][1] != (config.OAuthModelAlias{}) {
		t.Fatalf("configured aliases were mutated: %+v", configured[:2])
	}
}
