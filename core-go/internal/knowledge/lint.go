package knowledge

import (
	"regexp"
)

// LintFinding is a condition that is well-formed but cannot express what its author meant: a prose
// description inside `contains` of structure the matcher only reads from fields (an item's status or
// owner). Such a condition is a plain substring test that real item text almost never contains, so the
// knowledge would silently never fire. Lint is advisory (ADR-0013): the matcher still evaluates it.
type LintFinding struct {
	Where     string `json:"where"` // "signature", "applicability" or the exception description
	Condition string `json:"condition"`
	Rule      string `json:"rule"`
	Message   string `json:"message"`
}

// Lint rules for contains-text that describes structure instead of matching text.
var lintRules = []struct {
	rule    string
	pattern *regexp.Regexp
	message string
}{
	{"status_in_text", regexp.MustCompile(`(?i)\b(open|resolved|overdue|fulfilled)\b`),
		"names an item status inside the text; use an item pattern {\"text\": ..., \"status\": ...}"},
	{"owner_in_text", regexp.MustCompile(`(?i)\b(owned by|owner|our rep|seller)\b`),
		"names an item owner inside the text; ownership is not expressible as item text (no owner key yet)"},
}

// Lint reports conditions whose `contains` text describes item structure. It never errors: malformed
// conditions are Validate's job.
func Lint(k Knowledge) []LintFinding {
	var out []LintFinding
	check := func(where string, conds []Condition) {
		for _, c := range conds {
			text, ok := c.Value.(string)
			if c.Op != OpContains || !ok || !listFieldNames[c.Field] {
				continue
			}
			for _, r := range lintRules {
				if r.pattern.MatchString(text) {
					out = append(out, LintFinding{Where: where, Condition: render(c), Rule: r.rule, Message: r.message})
				}
			}
		}
	}
	check("signature", k.SituationSignature)
	check("applicability", k.ApplicabilityConditions)
	for _, x := range k.Exceptions {
		check(x.Description, x.Conditions)
	}
	return out
}
