// Package learning is the downstream half of the HAR-97 evaluate/learn loop (WP21, HAR-119). HAR-139's
// send path already splits a HumanDelta into explained (an existing eval predicted the edit) and
// unexplained, and stores the candidate criterion for the unexplained case. This package consumes that
// output:
//
//   - explained: the finding is queued as generator_feedback for the account's next strategies request
//     (written by strategystore, read by the orchestrator, schema in worker.yaml);
//   - unexplained: SeedDeltaCriterion derives a candidate Knowledge row (status candidate, statement and
//     applicability from the delta and the episode's situation) and a candidate evaluator version whose
//     executable form is the delta's literal changes;
//   - verdicts: SeedVerdictCriterion turns a corrected judgment verdict into a candidate criterion of the
//     human_delta evaluator — a correction annotates the inference, it never promotes knowledge directly;
//   - candidates are backtested (Backtest) against the stored decision episodes and the WP16 gold sets
//     (fixtures/evals/cases, fixtures/evals/crmarena), the result gates candidate -> shadow; shadow axis
//     checks run alongside the deterministic evals (ShadowResults) without ever blocking a send;
//     promotion (Promote) is gated by the linked knowledge row's lifecycle status and bumps the active
//     version the deterministic engine emits (Input.Versions).
//
// Every persisted record is real SQL (migration 0028); nothing here calls a model.
package learning

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"unicode/utf8"
)

// LiteralChange is one human_delta.literal_changes[] element (human_delta.v1.json). Before and After
// keep the contract's free shapes: scalars for channel/subject edits, {"person_id","role"} objects for
// recipient edits.
type LiteralChange struct {
	Kind   string `json:"kind"`
	Before any    `json:"before,omitempty"`
	After  any    `json:"after,omitempty"`
}

// Criterion is the candidate_criterion of a HumanDelta (human_delta.v1.json): the labeler- or
// core-proposed eval axis for an unexplained correction.
type Criterion struct {
	Statement            string  `json:"statement"`
	SuggestedEvalType    string  `json:"suggested_eval_type"`
	KnowledgeCandidateID *string `json:"knowledge_candidate_id,omitempty"`
}

// Errors of the learning writes and gates.
var (
	// ErrNotCandidate: the evaluator version exists but is not a candidate or shadow the loop may drive.
	ErrNotCandidate = errors.New("learning: evaluator version is not a candidate or shadow")
	// ErrNotFound: no evaluator_versions row carries this evaluator/version pair.
	ErrNotFound = errors.New("learning: evaluator version not found")
	// ErrGate: a lifecycle gate refused the transition (its message carries the failing condition).
	ErrGate = errors.New("learning: lifecycle gate refused the transition")
	// ErrNoCriterion: the input the learning write needed was absent (e.g. candidate_criterion).
	ErrNoCriterion = errors.New("learning: no candidate criterion")
	// ErrInvalidSpec: a shadow_spec or literal change could not be read.
	ErrInvalidSpec = errors.New("learning: invalid shadow spec")
)

// evalTypeVocab is the eval_type domain (migration 0007 + 0021, eval_result.v1.json#/$defs/evalType):
// the only axes a candidate criterion may propose — an axis outside it could never hold a version row.
var evalTypeVocab = map[string]bool{
	"recipient_correctness": true, "date_commitment_consistency": true, "pricing_integrity": true,
	"crm_writeback": true, "duplicate_action": true, "provenance_coverage": true,
	"permission_policy": true, "state_transition_support": true, "buyer_readiness": true,
	"cta_calibration": true, "next_step_quality": true, "stakeholder_selection": true,
	"stakeholder_coverage": true, "economic_buyer_coverage": true, "champion_strength": true,
	"champion_continuity": true, "decision_process": true, "business_case": true, "momentum": true,
	"action_stage_fit": true, "expansion_readiness": true, "customer_risk_sensitivity": true,
	"relationship_pressure": true, "timing_cadence": true, "next_action_quality": true,
	"grounding": true, "commitment_consistency": true, "state_change_relevance": true,
	"channel_appropriateness": true, "rep_style": true, "knowledge_applicability": true,
	"exception_awareness": true, "evidence_sufficiency": true, "trajectory": true, "human_delta": true,
}

// ValidAxis reports whether evalType is a catalogued eval axis.
func ValidAxis(evalType string) bool { return evalTypeVocab[evalType] }

// deterministicTypes is the eval_catalog kind=deterministic vocabulary: the axis checks tag results
// kind=deterministic and ActiveVersions versions the same set. state_transition_support is produced
// outside this engine; it is listed so a criterion proposing it lands on the right kind.
var deterministicTypes = map[string]bool{
	"recipient_correctness": true, "date_commitment_consistency": true, "pricing_integrity": true,
	"crm_writeback": true, "duplicate_action": true, "provenance_coverage": true,
	"permission_policy": true, "state_transition_support": true,
}

// KindOf is the eval_catalog kind of an eval type (semantic is the default for the GTM axes).
func KindOf(evalType string) string {
	switch {
	case deterministicTypes[evalType]:
		return "deterministic"
	case evalType == "trajectory":
		return "trace"
	case evalType == "human_delta":
		return "human_delta"
	default:
		return "semantic"
	}
}

// seedUUID is the deterministic version-5-style UUID the learning writes derive from a source id, so a
// replayed send produces the same rows (content-derived ids are this codebase's idempotency pattern).
func seedUUID(parts ...string) string {
	sum := sha256.Sum256([]byte("har119-seed|" + join(parts, "|")))
	sum[6] = (sum[6] & 0x0f) | 0x50
	sum[8] = (sum[8] & 0x3f) | 0x80
	h := hex.EncodeToString(sum[:16])
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:32]
}

func join(xs []string, sep string) string {
	out := ""
	for i, x := range xs {
		if i > 0 {
			out += sep
		}
		out += x
	}
	return out
}

// clip bounds stored text the way the contracts do (UTF-8 safe).
func clip(s string, max int) string {
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	r := []rune(s)
	return string(r[:max-1]) + "…"
}

// gateReason formats a gate decision.
func gateReason(ok bool, format string, a ...any) error {
	if ok {
		return nil
	}
	return fmt.Errorf("%w: %s", ErrGate, fmt.Sprintf(format, a...))
}
