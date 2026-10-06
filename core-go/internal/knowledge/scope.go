package knowledge

import "strings"

// MinScopeFields is how many distinct situation fields a learned knowledge object's scope (signature plus
// applicability conditions) must constrain before it may be applicable. One edit and one reply is one
// observation: a scope of a single field (typically transition.status) would make it apply to every
// account that ever reaches that status (HAR-97 B9).
const MinScopeFields = 2

// ScopeFields lists the distinct situation fields the knowledge's scope constrains, signature first.
func ScopeFields(k Knowledge) []string {
	seen := map[string]bool{}
	var out []string
	for _, group := range [][]Condition{k.SituationSignature, k.ApplicabilityConditions} {
		for _, c := range group {
			if !seen[c.Field] {
				seen[c.Field] = true
				out = append(out, c.Field)
			}
		}
	}
	return out
}

// StateScopeFields lists the scope fields that read the account's state rather than the open transition:
// relationship state, stage and the like. An is_unknown condition constrains nothing and is left out.
func StateScopeFields(k Knowledge) []string {
	seen := map[string]bool{}
	var out []string
	for _, group := range [][]Condition{k.SituationSignature, k.ApplicabilityConditions} {
		for _, c := range group {
			if strings.HasPrefix(c.Field, "transition.") || c.Op == OpIsUnknown || seen[c.Field] {
				continue
			}
			seen[c.Field] = true
			out = append(out, c.Field)
		}
	}
	return out
}

// ScopeTooBroad reports whether the scope is too broad to apply: fewer than MinScopeFields distinct fields, or
// none that reads the account's state. A transition alone (status, from, to) is "transition.status alone" in
// effect: every account that ever reaches that transition would match.
func ScopeTooBroad(k Knowledge) bool {
	return len(ScopeFields(k)) < MinScopeFields || len(StateScopeFields(k)) < 1
}
