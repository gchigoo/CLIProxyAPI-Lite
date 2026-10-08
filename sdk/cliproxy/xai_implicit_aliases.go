package cliproxy

import (
	"strings"

	coreauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/config"
)

// withXAIImplicitModelAliases adds a cpa-x<major><minor> fork alias for every
// grok-<major>.<minor> model. Configured entries own their model and alias
// names, so a model or alias that already has a configured entry is skipped.
// The configured slice is never modified.
func withXAIImplicitModelAliases(aliases []config.OAuthModelAlias, models []*ModelInfo) []config.OAuthModelAlias {
	configuredNames := make(map[string]struct{}, len(aliases))
	configuredAliases := make(map[string]struct{}, len(aliases))
	for _, entry := range aliases {
		if name := strings.ToLower(strings.TrimSpace(entry.Name)); name != "" {
			configuredNames[name] = struct{}{}
		}
		if alias := strings.ToLower(strings.TrimSpace(entry.Alias)); alias != "" {
			configuredAliases[alias] = struct{}{}
		}
	}

	var implicit []config.OAuthModelAlias
	for _, model := range models {
		if model == nil {
			continue
		}
		id := strings.TrimSpace(model.ID)
		alias, ok := coreauth.XAIImplicitModelAlias(id)
		if !ok {
			continue
		}
		if _, owned := configuredNames[strings.ToLower(id)]; owned {
			continue
		}
		if _, taken := configuredAliases[alias]; taken {
			continue
		}
		configuredAliases[alias] = struct{}{}
		implicit = append(implicit, config.OAuthModelAlias{Name: id, Alias: alias, Fork: true})
	}
	if len(implicit) == 0 {
		return aliases
	}
	out := make([]config.OAuthModelAlias, 0, len(aliases)+len(implicit))
	out = append(out, aliases...)
	return append(out, implicit...)
}
