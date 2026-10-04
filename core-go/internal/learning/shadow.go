package learning

import (
	"context"
	"fmt"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/claimstore"
	"github.com/harneet2512/gtm-work/core-go/internal/evals/deterministic"
)

// Axis is a candidate evaluator version that can shadow-run: its shadow_spec carries the literal
// changes its checks replay against a draft. Status is 'shadow' or 'active'; a candidate is never
// run — it must earn shadow through a backtest first.
type Axis struct {
	Evaluator string
	Version   int
	Status    string
	Rubric    string
	Spec      Spec
}

// LoadAxes returns the shadow and active evaluator versions whose shadow_spec binds the account (the
// literal changes person-reference only make sense on the account they were learned on).
func LoadAxes(ctx context.Context, q claimstore.DB, accountID string) ([]Axis, error) {
	rows, err := q.QueryContext(ctx, `SELECT evaluator::text, version, status, rubric, shadow_spec::text
 FROM evaluator_versions WHERE status IN ('shadow', 'active') AND shadow_spec IS NOT NULL
   AND shadow_spec ->> 'account_id' = $1 ORDER BY evaluator, version`, accountID)
	if err != nil {
		return nil, fmt.Errorf("learning: load axes of account %s: %w", accountID, err)
	}
	defer rows.Close()
	var out []Axis
	for rows.Next() {
		var a Axis
		var specRaw string
		if err := rows.Scan(&a.Evaluator, &a.Version, &a.Status, &a.Rubric, &specRaw); err != nil {
			return nil, fmt.Errorf("learning: scan axis: %w", err)
		}
		spec, err := parseSpec([]byte(specRaw))
		if err != nil {
			return nil, err
		}
		a.Spec = spec
		out = append(out, a)
	}
	return out, rows.Err()
}

// axisResult builds the EvalResult of one axis check on the input's draft: verdict fail when the draft
// still shows the pattern the human corrected, pass when the correction holds. Axis checks never block:
// a learned proxy asserts its own narrow predicates, never the catalog's full blocking rule of the eval
// family — a proxy must not refuse a send on the family's behalf.
func (a Axis) axisResult(in deterministic.Input) deterministic.EvalResult {
	violated, detail := a.Spec.RunSpec(in.Draft)
	verdict, reason := "pass", detail
	if violated {
		verdict = "fail"
		reason = fmt.Sprintf("candidate criterion %q is violated: %s", clip(a.Rubric, 200), detail)
	} else {
		reason = fmt.Sprintf("candidate criterion %q holds: %s", clip(a.Rubric, 200), detail)
	}
	return deterministic.EvalResult{
		ID: seedUUID("axis", in.AgentRunID, fmt.Sprint(in.DraftIndex), a.Evaluator, fmt.Sprint(a.Version),
			in.EvaluatedAt.UTC().Format(time.RFC3339Nano)),
		AgentRunID: in.AgentRunID, DraftIndex: in.DraftIndex,
		EvalType: deterministic.EvalType(a.Evaluator), EvalVersion: fmt.Sprintf("%s:v%d", a.Evaluator, a.Version),
		// kind 'human_delta' marks the check's provenance: a human-delta-derived candidate criterion,
		// not the deterministic engine — the discriminator that keeps a learned result on a shared
		// axis name from standing in for the canonical result once its version is active.
		Kind: "human_delta", Verdict: verdict, Blocking: false, Reason: reason,
		Diagnostics: []string{}, StateRefs: []string{}, ActivityRefs: []string{},
		EvidenceRefs: []deterministic.EvidenceRef{}, KnowledgeRefs: []string{},
		EvidenceClass: "deal_data", CreatedAt: in.EvaluatedAt.UTC(),
	}
}

// ShadowResults evaluates every executable axis of the account on the input's draft: shadow = runs
// alongside the evals but does not block (HAR-119). Axes with no executable literal changes produce no
// result — their criterion waits for a human implementation, its backtest still gates it.
func ShadowResults(in deterministic.Input, axes []Axis) []deterministic.EvalResult {
	var out []deterministic.EvalResult
	for _, a := range axes {
		if !a.Spec.Executable() {
			continue
		}
		out = append(out, a.axisResult(in))
	}
	return out
}

// ActiveVersions is the evaluator -> version map the deterministic engine emits on results (the
// '<eval_type>:v<N>' of eval_result.v1.json): the promoted row when the learning loop activated one,
// absent otherwise so the engine keeps its built-in v1. The map is scoped to the account the run is
// for — a promoted criterion was learned on one account's delta and re-versions only that account's
// results; a NULL account_id marks a global (manual or seeded) version.
func ActiveVersions(ctx context.Context, q claimstore.DB, accountID string) (map[string]int, error) {
	rows, err := q.QueryContext(ctx, `SELECT evaluator::text, version FROM evaluator_versions
 WHERE status = 'active' AND (account_id = $1::uuid OR account_id IS NULL) ORDER BY evaluator`, accountID)
	if err != nil {
		return nil, fmt.Errorf("learning: list active versions: %w", err)
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var e string
		var v int
		if err := rows.Scan(&e, &v); err != nil {
			return nil, fmt.Errorf("learning: scan active version: %w", err)
		}
		out[e] = v
	}
	return out, rows.Err()
}
