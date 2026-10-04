package deterministic

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

// EvalType is one of the seven deterministic eval types (eval_result.v1.json evalType).
type EvalType string

// The HAR-97 §4 deterministic evals (contracts/evals/eval_catalog.json, kind deterministic).
const (
	TypeRecipient  EvalType = "recipient_correctness"
	TypeDate       EvalType = "date_commitment_consistency"
	TypePricing    EvalType = "pricing_integrity"
	TypeCRM        EvalType = "crm_writeback"
	TypeDuplicate  EvalType = "duplicate_action"
	TypeProvenance EvalType = "provenance_coverage"
	TypePermission EvalType = "permission_policy"
)

// evaluatorVersion is bumped whenever a rule changes meaning (HAR-97 §21).
const evaluatorVersion = 1

// Version is the result's eval_version, '<eval_type>:v<N>'.
func (t EvalType) Version() string { return fmt.Sprintf("%s:v%d", t, evaluatorVersion) }

// Contract constants every deterministic result carries (eval_catalog.json).
const (
	kindDeterministic = "deterministic"
	classProductRule  = "product_rule"
	verdictPass       = "pass"
	verdictFail       = "fail"
	maxTextLen        = 2000
)

// EvalResult is eval_result.v1.json.
type EvalResult struct {
	ID                  string        `json:"id"`
	AgentRunID          string        `json:"agent_run_id"`
	DraftIndex          int           `json:"draft_index"`
	EvalType            EvalType      `json:"eval_type"`
	EvalVersion         string        `json:"eval_version"`
	Kind                string        `json:"kind"`
	Verdict             string        `json:"verdict"`
	Label               *string       `json:"label"`
	Diagnostics         []string      `json:"diagnostics"`
	Score               *float64      `json:"score"`
	Blocking            bool          `json:"blocking"`
	Reason              string        `json:"reason"`
	StateRefs           []string      `json:"state_refs"`
	ActivityRefs        []string      `json:"activity_refs"`
	EvidenceRefs        []EvidenceRef `json:"evidence_refs"`
	KnowledgeRefs       []string      `json:"knowledge_refs"`
	SuggestedCorrection *string       `json:"suggested_correction"`
	Confidence          *float64      `json:"confidence"`
	EvidenceClass       string        `json:"evidence_class"`
	Model               *string       `json:"model"`
	CreatedAt           time.Time     `json:"created_at"`
}

// Check names one HAR-97 §4 sub-check (see docs/traceability/wp17.md).
type Check string

// Remedy is what the pre-action flow does with a blocking failure (HAR-97 §7).
type Remedy string

// Remedies: revise sends the draft to the revision planner; stop ends the run.
const (
	RemedyRevise Remedy = "revise"
	RemedyStop   Remedy = "stop"
)

// Finding is one failed sub-check. Findings are values: the helpers return modified copies.
type Finding struct {
	Check        Check
	Blocking     bool
	Remedy       Remedy
	Detail       string
	Correction   string
	StateRefs    []string
	ActivityRefs []string
	EvidenceRefs []EvidenceRef
}

func failure(c Check, detail, correction string) Finding {
	return Finding{Check: c, Remedy: RemedyRevise, Detail: detail, Correction: correction}
}

func (f Finding) blocking() Finding { f.Blocking = true; return f }

func (f Finding) stopping() Finding { f.Blocking, f.Remedy = true, RemedyStop; return f }

func (f Finding) withState(refs ...string) Finding {
	f.StateRefs = append(append([]string(nil), f.StateRefs...), refs...)
	return f
}

func (f Finding) withActivities(ids ...string) Finding {
	f.ActivityRefs = append(append([]string(nil), f.ActivityRefs...), ids...)
	return f
}

func (f Finding) withEvidence(refs ...EvidenceRef) Finding {
	f.EvidenceRefs = append(append([]EvidenceRef(nil), f.EvidenceRefs...), refs...)
	return f
}

// Judgment is one eval's result plus the sub-checks it ran and the findings behind it.
type Judgment struct {
	Result   EvalResult
	Checks   []Check
	Findings []Finding
}

// judge folds the findings of one eval into its EvalResult.
func judge(in Input, t EvalType, checks []Check, stateRefs []string, findings []Finding) Judgment {
	res := EvalResult{
		ID: resultID(in, t), AgentRunID: in.AgentRunID, DraftIndex: in.DraftIndex, EvalType: t,
		EvalVersion: t.Version(), Kind: kindDeterministic, Verdict: verdictPass, Diagnostics: []string{},
		StateRefs: unique(stateRefs), ActivityRefs: []string{}, EvidenceRefs: []EvidenceRef{},
		KnowledgeRefs: []string{}, EvidenceClass: classProductRule, CreatedAt: in.EvaluatedAt.UTC(),
		Reason: passReason(checks),
	}
	if len(findings) > 0 {
		res.Verdict, res.Reason = verdictFail, failReason(findings)
		res.SuggestedCorrection = corrections(findings)
		for _, f := range findings {
			res.Blocking = res.Blocking || f.Blocking
			res.StateRefs = unique(append(res.StateRefs, f.StateRefs...))
			res.ActivityRefs = unique(append(res.ActivityRefs, f.ActivityRefs...))
			res.EvidenceRefs = append(res.EvidenceRefs, f.EvidenceRefs...)
		}
	}
	return Judgment{Result: res, Checks: append([]Check(nil), checks...), Findings: findings}
}

func passReason(checks []Check) string {
	names := make([]string, len(checks))
	for i, c := range checks {
		names[i] = string(c)
	}
	return truncate(fmt.Sprintf("All %d checks passed: %s.", len(checks), strings.Join(names, ", ")))
}

func failReason(findings []Finding) string {
	parts := make([]string, len(findings))
	for i, f := range findings {
		parts[i] = fmt.Sprintf("%s: %s", f.Check, f.Detail)
	}
	return truncate(strings.Join(parts, "; "))
}

func corrections(findings []Finding) *string {
	var parts []string
	for _, f := range findings {
		if f.Correction != "" {
			parts = append(parts, f.Correction)
		}
	}
	if len(parts) == 0 {
		return nil
	}
	s := truncate(strings.Join(unique(parts), " "))
	return &s
}

// resultID is a UUID derived from what was judged: run, draft, eval version, evaluation time and a
// hash of the whole input, so a changed input never reuses an earlier result's id while
// re-running the same evaluation stays idempotent.
func resultID(in Input, t EvalType) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s|%d|%s|%s|%s", in.AgentRunID, in.DraftIndex, t.Version(),
		in.EvaluatedAt.UTC().Format(time.RFC3339Nano), inputHash(in))))
	sum[6] = (sum[6] & 0x0f) | 0x50 // version 5 layout
	sum[8] = (sum[8] & 0x3f) | 0x80 // RFC 4122 variant
	h := hex.EncodeToString(sum[:16])
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:32]
}

// inputHash is the SHA-256 of the input's JSON. An input that cannot be encoded hashes its error
// text, so the id is still deterministic (the evals themselves never depend on the encoding).
func inputHash(in Input) string {
	raw, err := json.Marshal(in)
	if err != nil {
		raw = []byte("unencodable input: " + err.Error())
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func truncate(s string) string {
	if utf8.RuneCountInString(s) <= maxTextLen {
		return s
	}
	r := []rune(s)
	return string(r[:maxTextLen-1]) + "…"
}

func unique(xs []string) []string {
	seen := make(map[string]bool, len(xs))
	out := []string{}
	for _, x := range xs {
		if x != "" && !seen[x] {
			seen[x] = true
			out = append(out, x)
		}
	}
	return out
}
