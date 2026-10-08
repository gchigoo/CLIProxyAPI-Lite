package auth

import (
	"regexp"
	"strings"
)

// xaiModelAliasChannel is the OAuth model alias channel of xAI auths.
const xaiModelAliasChannel = "xai"

// xaiImplicitAliasModel matches the Grok models that get an implicit
// cpa-x<major><minor> alias. The major version is a single digit so that every
// alias maps back to exactly one model.
var xaiImplicitAliasModel = regexp.MustCompile(`^grok-(\d)\.(\d+)$`)

// xaiImplicitAlias matches an implicit alias and captures the model version.
var xaiImplicitAlias = regexp.MustCompile(`^cpa-x(\d)(\d+)$`)

// XAIImplicitModelAlias returns the implicit alias of an xAI model, for example
// cpa-x47 for grok-4.7. Configured aliases take precedence over it.
func XAIImplicitModelAlias(modelID string) (string, bool) {
	match := xaiImplicitAliasModel.FindStringSubmatch(strings.ToLower(strings.TrimSpace(modelID)))
	if match == nil {
		return "", false
	}
	return "cpa-x" + match[1] + match[2], true
}

// resolveXAIImplicitModelAlias resolves an implicit alias request to its Grok
// model and keeps a thinking suffix exactly as configured aliases do.
func resolveXAIImplicitModelAlias(requestedModel string) OAuthModelAliasResult {
	requestResult, _ := modelAliasLookupCandidates(requestedModel)
	base := requestResult.ModelName
	if base == "" {
		base = strings.TrimSpace(requestedModel)
	}
	match := xaiImplicitAlias.FindStringSubmatch(strings.ToLower(base))
	if match == nil {
		return OAuthModelAliasResult{}
	}
	return OAuthModelAliasResult{
		UpstreamModel: preserveResolvedModelSuffix("grok-"+match[1]+"."+match[2], requestResult),
		OriginalAlias: requestedModel,
	}
}
