package bucket2

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
)

// Stored is a persisted gate result with its row identity.
type Stored struct {
	Result
	ID, EpisodeID string
}

// Save upserts the results of one episode (migration 0036 gate_results). A result that fails Validate is an
// error, never silently dropped: a gate that cannot be recorded must be visible. Saving the same gate and
// judged object again replaces the earlier result, so a re-run after an edit supersedes the stale one.
func Save(ctx context.Context, db *sql.DB, episodeID string, results []Result) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("bucket2: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := SaveTx(ctx, tx, episodeID, results); err != nil {
		return err
	}
	return tx.Commit()
}

// SaveTx is Save inside the caller's transaction. A pre-send gate must use it: the send transaction holds the
// episode row lock, so a write on another connection would wait on that lock forever.
func SaveTx(ctx context.Context, tx *sql.Tx, episodeID string, results []Result) error {
	for _, r := range results {
		resolved, err := ResolveRefs(ctx, tx, r.EvidenceRefs)
		if err != nil {
			return err
		}
		r.EvidenceRefs = resolved
		r = r.Finalize()
		if err := r.Validate(); err != nil {
			return err
		}
		args, err := r.columns()
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
INSERT INTO gate_results (decision_episode_id, gate, sub_gate, label, judged_type, judged_id, span_id, verdict, question, observed, why, evidence_refs, improves, grader,
  criteria, control_effect, evaluator_version, lineage, latency_ms, model_calls, tokens, cost_usd)
VALUES ($1::uuid, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12::jsonb, $13, $14::jsonb, $15::jsonb, $16, $17, $18::jsonb, $19, $20, $21, $22)
ON CONFLICT (decision_episode_id, gate, sub_gate, judged_type, judged_id) DO UPDATE SET
  label = EXCLUDED.label, span_id = EXCLUDED.span_id, verdict = EXCLUDED.verdict, question = EXCLUDED.question,
  observed = EXCLUDED.observed, why = EXCLUDED.why, evidence_refs = EXCLUDED.evidence_refs, improves = EXCLUDED.improves,
  grader = EXCLUDED.grader, criteria = EXCLUDED.criteria, control_effect = EXCLUDED.control_effect,
  evaluator_version = EXCLUDED.evaluator_version, latency_ms = EXCLUDED.latency_ms, model_calls = EXCLUDED.model_calls,
  tokens = EXCLUDED.tokens, cost_usd = EXCLUDED.cost_usd, `+LineageOnConflict+`,
  created_at = now()`,
			append([]any{episodeID}, args...)...); err != nil {
			return fmt.Errorf("bucket2: save %s %s: %w", r.Gate, r.SubGate, err)
		}
	}
	return nil
}

// LineageOnConflict is the SET clause for lineage on an upsert: only a lineage the caller states (from a real recompute or
// retry record) replaces the stored one. A changed verdict is never read as a recompute: a re-grade, a sweep or a
// non-deterministic judge changes verdicts too. Nothing in the backend states a gate-level lineage today.
const LineageOnConflict = `lineage = CASE WHEN EXCLUDED.lineage <> '{}'::jsonb THEN EXCLUDED.lineage ELSE gate_results.lineage END`

// columns is the result's values for the insert, in column order from $2 (after the episode id).
func (r Result) columns() ([]any, error) {
	refs, _ := json.Marshal(r.EvidenceRefs)
	grader, _ := json.Marshal(r.Grader)
	criteria, err := json.Marshal(r.Criteria)
	if err != nil {
		return nil, fmt.Errorf("bucket2: encode criteria of %s: %w", r.Gate, err)
	}
	lineage, _ := json.Marshal(r.Lineage)
	return []any{r.Gate, r.SubGate, r.Label, r.JudgedType, r.JudgedID, r.SpanID, string(r.Verdict), r.Question, r.Observed, r.Why,
		string(refs), r.Improves, string(grader), string(criteria), r.ControlEffect, r.EvaluatorVersion, string(lineage),
		r.LatencyMs, r.ModelCalls, r.Tokens, r.CostUSD}, nil
}

// Load returns the stored results of an episode in flow order (D1 to D10).
func Load(ctx context.Context, db *sql.DB, episodeID string) ([]Stored, error) {
	rows, err := db.QueryContext(ctx, `
SELECT id::text, gate, sub_gate, label, judged_type, judged_id, span_id, verdict, question, observed, why, evidence_refs::text, improves, grader::text, calibrated,
  criteria::text, control_effect, evaluator_version, lineage::text, latency_ms, model_calls, tokens, cost_usd::float8
FROM gate_results WHERE decision_episode_id = $1::uuid
ORDER BY CASE left(gate, 1) WHEN 'B' THEN 1 WHEN 'D' THEN 2 ELSE 3 END, substring(gate from 2)::int, sub_gate, judged_id`, episodeID)
	if err != nil {
		return nil, fmt.Errorf("bucket2: load results of %s: %w", episodeID, err)
	}
	defer rows.Close()
	var out []Stored
	for rows.Next() {
		var s Stored
		var verdict, refs, grader, criteria, lineage string
		s.EpisodeID = episodeID
		if err := rows.Scan(&s.ID, &s.Gate, &s.SubGate, &s.Label, &s.JudgedType, &s.JudgedID, &s.SpanID, &verdict, &s.Question,
			&s.Observed, &s.Why, &refs, &s.Improves, &grader, &s.Calibrated, &criteria, &s.ControlEffect, &s.EvaluatorVersion, &lineage,
			&s.LatencyMs, &s.ModelCalls, &s.Tokens, &s.CostUSD); err != nil {
			return nil, fmt.Errorf("bucket2: scan result: %w", err)
		}
		s.Verdict = ParseVerdict(verdict)
		if err := json.Unmarshal([]byte(refs), &s.EvidenceRefs); err != nil {
			return nil, fmt.Errorf("bucket2: decode evidence of %s: %w", s.ID, err)
		}
		if err := json.Unmarshal([]byte(grader), &s.Grader); err != nil {
			return nil, fmt.Errorf("bucket2: decode grader of %s: %w", s.ID, err)
		}
		if err := json.Unmarshal([]byte(criteria), &s.Criteria); err != nil {
			return nil, fmt.Errorf("bucket2: decode criteria of %s: %w", s.ID, err)
		}
		if err := json.Unmarshal([]byte(lineage), &s.Lineage); err != nil {
			return nil, fmt.Errorf("bucket2: decode lineage of %s: %w", s.ID, err)
		}
		out = append(out, s)
	}
	return out, rows.Err()
}
