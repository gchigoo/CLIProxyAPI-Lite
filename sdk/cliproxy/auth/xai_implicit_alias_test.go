package auth

import (
	"testing"

	internalconfig "github.com/router-for-me/CLIProxyAPI/v7/internal/config"
)

func TestXAIImplicitModelAlias(t *testing.T) {
	t.Parallel()

	cases := []struct {
		model string
		alias string
		ok    bool
	}{
		{model: "grok-4.7", alias: "cpa-x47", ok: true},
		{model: "GROK-4.8", alias: "cpa-x48", ok: true},
		{model: "grok-4.10", alias: "cpa-x410", ok: true},
		{model: "grok-4.7-build-fast"},
		{model: "grok-4.20-0309-reasoning"},
		{model: "grok-3-mini"},
		{model: "grok-10.1"},
	}
	for _, tc := range cases {
		alias, ok := XAIImplicitModelAlias(tc.model)
		if alias != tc.alias || ok != tc.ok {
			t.Errorf("XAIImplicitModelAlias(%q) = %q, %t; want %q, %t", tc.model, alias, ok, tc.alias, tc.ok)
		}
	}
}

func TestApplyOAuthModelAlias_XAIImplicitAlias(t *testing.T) {
	t.Parallel()

	mgr := NewManager(nil, nil, nil)
	mgr.SetConfig(&internalconfig.Config{})
	auth := &Auth{ID: "xai-oauth", Provider: "xai"}

	cases := map[string]string{
		"cpa-x48":       "grok-4.8",
		"CPA-X48":       "grok-4.8",
		"cpa-x48(high)": "grok-4.8(high)",
		"cpa-x410":      "grok-4.10",
		"grok-4.8":      "grok-4.8",
		"cpa-x":         "cpa-x",
	}
	for requested, want := range cases {
		if got := mgr.applyOAuthModelAlias(auth, requested); got != want {
			t.Errorf("applyOAuthModelAlias(%q) = %q, want %q", requested, got, want)
		}
	}
}

func TestApplyOAuthModelAlias_XAIConfiguredAliasWinsOverImplicit(t *testing.T) {
	t.Parallel()

	mgr := NewManager(nil, nil, nil)
	mgr.SetConfig(&internalconfig.Config{})
	mgr.SetOAuthModelAlias(map[string][]internalconfig.OAuthModelAlias{
		"xai": {{Name: "grok-4.7-custom", Alias: "cpa-x47", Fork: true}},
	})
	auth := &Auth{ID: "xai-oauth", Provider: "xai"}

	if got := mgr.applyOAuthModelAlias(auth, "cpa-x47"); got != "grok-4.7-custom" {
		t.Fatalf("applyOAuthModelAlias(cpa-x47) = %q, want grok-4.7-custom", got)
	}
}

func TestApplyOAuthModelAlias_XAIImplicitAliasScope(t *testing.T) {
	t.Parallel()

	mgr := NewManager(nil, nil, nil)
	mgr.SetConfig(&internalconfig.Config{})
	auths := []*Auth{
		{ID: "xai-key", Provider: "xai", Attributes: map[string]string{"auth_kind": "api_key"}},
		{ID: "codex-oauth", Provider: "codex"},
	}
	for _, auth := range auths {
		if got := mgr.applyOAuthModelAlias(auth, "cpa-x48"); got != "cpa-x48" {
			t.Errorf("applyOAuthModelAlias(%s, cpa-x48) = %q, want unchanged", auth.ID, got)
		}
	}
}
