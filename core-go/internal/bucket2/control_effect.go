package bucket2

import "strings"

// Control effects (HAR-149): what a stored verdict does in the backend today. The vocabulary is fixed by
// contracts/evals/eval_registry.json control_effect_rules; most gates only record their verdict, and the UI must never
// imply otherwise.
const (
	EffectContinue    = "CONTINUE"
	EffectBlock       = "BLOCK"
	EffectRecordOnly  = "RECORD ONLY"
	EffectMarkUnknown = "MARK UNKNOWN"
)

// d8Blocks reports whether a D8 result is one that can refuse a send: a deterministic result of a sub-gate with a catalog
// blocking rule (see D8Blockers). The model judgment of D8 and every other D8 result are stored and shown, never a block.
func d8Blocks(subGate, graderKind string) bool {
	_, ok := d8BlockingEvals[subGate]
	return ok && graderKind == Deterministic.Kind
}

// ControlEffect is the effect a stored result has, from what the code does with it. B9 only records: knowledge/lifecycle.go
// holds learned knowledge at candidate by evaluating its own scope predicate, never by reading the B9 result. S2 marks the
// capability unreliable (web/lib/evals/quality-report.ts). An unknown verdict marks the result unknown.
func ControlEffect(gate, subGate, graderKind string, v Verdict) string {
	switch {
	case gate == "D8" && d8Blocks(subGate, graderKind) && v == Fail:
		return EffectBlock
	case gate == "D8" && d8Blocks(subGate, graderKind) && v == Pass:
		return EffectContinue
	case gate == "S2" && (v == Warn || v == Fail):
		return EffectMarkUnknown
	case v == Unknown:
		return EffectMarkUnknown
	}
	return EffectRecordOnly
}

// EvaluatorVersion names the judge that produced a result: the model judge's prompt version, or the gate's
// deterministic code at its contract version. Never a guess: a model result with no prompt version says so.
func EvaluatorVersion(gate string, g Grader) string {
	if g.Kind == "model" {
		if g.PromptVersion != "" {
			return g.PromptVersion
		}
		return gate + ":model:unversioned"
	}
	return gate + ":deterministic:v1"
}

// humanLabel turns a criterion id (grounding, cta_strength) into a sentence-case label.
func humanLabel(id string) string {
	s := strings.TrimSpace(strings.ReplaceAll(id, "_", " "))
	if s == "" {
		return ""
	}
	return strings.ToUpper(s[:1]) + s[1:]
}
