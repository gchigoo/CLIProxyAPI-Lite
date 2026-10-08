package helps

// servedModelEquivalents lists upstream model names that serve a requested model
// under a different name. They are not substitutions. Keys and values use the
// normalized form: lower case, no thinking suffix, no provider prefix.
var servedModelEquivalents = map[string][]string{
	// xAI's Grok CLI chat proxy answers grok-4.7 requests as grok-4.7-build.
	"grok-4.7": {"grok-4.7-build"},
}

// isKnownServedModelEquivalent reports whether served is a known equivalent name
// for requested.
func isKnownServedModelEquivalent(requested, served string) bool {
	requested = stripModelProviderPrefix(normalizeModelName(requested))
	served = stripModelProviderPrefix(normalizeModelName(served))
	for _, candidate := range servedModelEquivalents[requested] {
		if served == candidate {
			return true
		}
	}
	return false
}
