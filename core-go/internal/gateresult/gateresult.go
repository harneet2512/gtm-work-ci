// Package gateresult holds the pieces of a stored gate result (gate_result.v1) that Bucket 1 and Bucket 2 share: the per-criterion
// result, the control effect a verdict has, the evaluator version and the lineage upsert clause. It depends on neither bucket, so
// bucket1run and bucket2 can both persist into gate_results without importing each other.
package gateresult

import "strings"

// Control effects (HAR-149): what a stored verdict does in the backend today. The vocabulary is fixed by
// contracts/evals/eval_registry.json control_effect_rules; most gates only record their verdict, and the UI must never imply otherwise.
const (
	EffectContinue    = "CONTINUE"
	EffectBlock       = "BLOCK"
	EffectRecordOnly  = "RECORD ONLY"
	EffectMarkUnknown = "MARK UNKNOWN"
)

// Criterion is one checked statement under a gate and its own result.
type Criterion struct {
	ID           string   `json:"id"`
	Label        string   `json:"label"`
	Result       string   `json:"result"` // pass, warn, fail, unknown or not_applicable
	Why          string   `json:"why"`
	EvidenceRefs []string `json:"evidence_refs"`
}

// LineageOnConflict is the SET clause for lineage on a gate_results upsert: only a lineage the caller states (from a real
// recompute or retry) replaces the stored one; an ordinary re-save keeps it.
const LineageOnConflict = `lineage = CASE WHEN EXCLUDED.lineage <> '{}'::jsonb THEN EXCLUDED.lineage ELSE gate_results.lineage END`

// ControlEffect is the effect a stored verdict has, from what the code does with it. blocksSend says the result is one that can
// refuse a send (a deterministic D8 sub-gate with a blocking rule); the caller decides that, because only Bucket 2 knows the rules.
// S2 marks the capability unreliable; an unknown verdict marks the result unknown; everything else only records.
func ControlEffect(gate string, blocksSend bool, verdict string) string {
	switch {
	case gate == "D8" && blocksSend && verdict == "fail":
		return EffectBlock
	case gate == "D8" && blocksSend && verdict == "pass":
		return EffectContinue
	case gate == "S2" && (verdict == "warn" || verdict == "fail"):
		return EffectMarkUnknown
	case verdict == "unknown":
		return EffectMarkUnknown
	}
	return EffectRecordOnly
}

// EvaluatorVersion names the judge that produced a result: a model judge's prompt version, or the gate's deterministic code at its
// contract version. Never a guess: a model result with no prompt version says so.
func EvaluatorVersion(gate, graderKind, promptVersion string) string {
	if graderKind == "model" {
		if promptVersion != "" {
			return promptVersion
		}
		return gate + ":model:unversioned"
	}
	return gate + ":deterministic:v1"
}

// HumanLabel turns a criterion id (grounding, cta_strength) into a sentence-case label.
func HumanLabel(id string) string {
	s := strings.TrimSpace(strings.ReplaceAll(id, "_", " "))
	if s == "" {
		return ""
	}
	return strings.ToUpper(s[:1]) + s[1:]
}
