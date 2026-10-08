package helps

import "regexp"

// grokVersionedModel matches the versioned Grok models that xAI's Grok CLI chat
// proxy serves under a "-build" name, for example grok-4.7 as grok-4.7-build.
var grokVersionedModel = regexp.MustCompile(`^grok-\d+\.\d+$`)

// isKnownServedModelEquivalent reports whether served is a known equivalent name
// for requested, which is not a substitution. Both names are compared in the
// normalized form: lower case, no thinking suffix, no provider prefix.
func isKnownServedModelEquivalent(requested, served string) bool {
	requested = stripModelProviderPrefix(normalizeModelName(requested))
	served = stripModelProviderPrefix(normalizeModelName(served))
	return grokVersionedModel.MatchString(requested) && served == requested+"-build"
}
