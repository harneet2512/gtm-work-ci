package bucket2

import "github.com/harneet2512/gtm-work/core-go/internal/gateresult"

// Control effects (HAR-149). The vocabulary and the shared rules live in gateresult; Bucket 2 adds what only it knows, which D8
// results can refuse a send.
const (
	EffectContinue    = gateresult.EffectContinue
	EffectBlock       = gateresult.EffectBlock
	EffectRecordOnly  = gateresult.EffectRecordOnly
	EffectMarkUnknown = gateresult.EffectMarkUnknown
)

// d8Blocks reports whether a D8 result is one that can refuse a send: a deterministic result of a sub-gate with a catalog
// blocking rule (see D8Blockers). The model judgment of D8 and every other D8 result are stored and shown, never a block.
func d8Blocks(subGate, graderKind string) bool {
	_, ok := d8BlockingEvals[subGate]
	return ok && graderKind == Deterministic.Kind
}

// ControlEffect is the effect a stored result has, from what the code does with it. B9 only records: knowledge/lifecycle.go
// holds learned knowledge at candidate by evaluating its own scope predicate, never by reading the B9 result.
func ControlEffect(gate, subGate, graderKind string, v Verdict) string {
	return gateresult.ControlEffect(gate, gate == "D8" && d8Blocks(subGate, graderKind), string(v))
}

// EvaluatorVersion names the judge that produced a result (see gateresult.EvaluatorVersion).
func EvaluatorVersion(gate string, g Grader) string {
	return gateresult.EvaluatorVersion(gate, g.Kind, g.PromptVersion)
}

// humanLabel turns a criterion id (grounding, cta_strength) into a sentence-case label.
func humanLabel(id string) string { return gateresult.HumanLabel(id) }
