package ingest

import "strings"

// splitLines splits on "\n", dropping empty lines.
func splitLines(s string) []string {
	var out []string
	for _, l := range strings.Split(s, "\n") {
		if l != "" {
			out = append(out, l)
		}
	}
	return out
}

// MappingKey translates a participant's raw identity into the (source_system, source_key) pair
// used by entity_source_mappings:
//
//	crm:contact:817          -> ("crm", "contact:817")
//	call:C19:speaker_02      -> ("call", "C19:speaker_02")
//	slack:U02DANA            -> ("slack", "U02DANA")
//	priya@acme.com           -> ("email", "priya@acme.com")
//
// Identities that match no scheme (e.g. "integration:enrich") return empty strings and are
// never linked to a person.
func MappingKey(raw string) (system, key string) {
	for _, prefix := range []string{"crm", "call", "slack"} {
		if rest, ok := strings.CutPrefix(raw, prefix+":"); ok {
			if rest == "" {
				return "", ""
			}
			return prefix, rest
		}
	}
	if strings.Contains(raw, "@") {
		return "email", raw
	}
	return "", ""
}
