// Package evalstore writes EvalResults to the eval_runs table (migration 0005 + 0007).
package evalstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/harneet2512/gtm-work/core-go/internal/evals/deterministic"
)

// DB is the subset of *sql.DB and *sql.Tx that Save uses.
type DB interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// insert maps the contract to the columns: eval_type=evaluator, eval_version=evaluator_version,
// reason=rationale. A result id is derived from what was judged, so saving twice is a no-op.
const insert = `
INSERT INTO eval_runs (id, agent_run_id, draft_index, evaluator, evaluator_version, kind, verdict, label,
                       diagnostics, score, blocking, rationale, state_refs, activity_refs, evidence_refs,
                       knowledge_refs, suggested_correction, confidence, evidence_class, model, created_at)
VALUES ($1::uuid, $2::uuid, $3, $4, $5, $6, $7, $8,
        ARRAY(SELECT jsonb_array_elements_text($9::jsonb)), $10, $11, $12,
        ARRAY(SELECT jsonb_array_elements_text($13::jsonb)),
        ARRAY(SELECT jsonb_array_elements_text($14::jsonb))::uuid[], $15::jsonb,
        ARRAY(SELECT jsonb_array_elements_text($16::jsonb))::uuid[], $17, $18, $19, $20, $21)
ON CONFLICT (id) DO NOTHING`

// Save writes results and returns how many rows were new. It stops at the first error and wraps it
// with the result's eval type; run it in a transaction to make the set all-or-nothing.
func Save(ctx context.Context, db DB, results []deterministic.EvalResult) (int, error) {
	saved := 0
	for _, r := range results {
		args, err := arguments(r)
		if err != nil {
			return saved, fmt.Errorf("evalstore: encode %s: %w", r.EvalType, err)
		}
		res, err := db.ExecContext(ctx, insert, args...)
		if err != nil {
			return saved, fmt.Errorf("evalstore: save %s for run %s draft %d: %w", r.EvalType, r.AgentRunID, r.DraftIndex, err)
		}
		n, err := res.RowsAffected()
		if err != nil {
			return saved, fmt.Errorf("evalstore: rows affected for %s: %w", r.EvalType, err)
		}
		saved += int(n)
	}
	return saved, nil
}

func arguments(r deterministic.EvalResult) ([]any, error) {
	lists := make([][]byte, 0, 5)
	for _, v := range []any{nonNil(r.Diagnostics), nonNil(r.StateRefs), nonNil(r.ActivityRefs), nonNilRefs(r.EvidenceRefs), nonNil(r.KnowledgeRefs)} {
		raw, err := json.Marshal(v)
		if err != nil {
			return nil, err
		}
		lists = append(lists, raw)
	}
	return []any{r.ID, r.AgentRunID, r.DraftIndex, string(r.EvalType), r.EvalVersion, r.Kind, r.Verdict, r.Label,
		string(lists[0]), r.Score, r.Blocking, r.Reason, string(lists[1]), string(lists[2]), string(lists[3]),
		string(lists[4]), r.SuggestedCorrection, r.Confidence, r.EvidenceClass, r.Model, r.CreatedAt}, nil
}

func nonNil(xs []string) []string {
	if xs == nil {
		return []string{}
	}
	return xs
}

func nonNilRefs(xs []deterministic.EvidenceRef) []deterministic.EvidenceRef {
	if xs == nil {
		return []deterministic.EvidenceRef{}
	}
	return xs
}
