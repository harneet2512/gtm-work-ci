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
	for _, r := range results {
		resolved, err := ResolveRefs(ctx, db, r.EvidenceRefs)
		if err != nil {
			return err
		}
		r.EvidenceRefs = resolved
		r = r.Finalize()
		if err := r.Validate(); err != nil {
			return err
		}
		refs, _ := json.Marshal(r.EvidenceRefs)
		grader, _ := json.Marshal(r.Grader)
		if _, err := tx.ExecContext(ctx, `
INSERT INTO gate_results (decision_episode_id, gate, sub_gate, label, judged_type, judged_id, span_id, verdict, question, observed, why, evidence_refs, improves, grader)
VALUES ($1::uuid, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12::jsonb, $13, $14::jsonb)
ON CONFLICT (decision_episode_id, gate, sub_gate, judged_type, judged_id) DO UPDATE SET
  label = EXCLUDED.label, span_id = EXCLUDED.span_id, verdict = EXCLUDED.verdict, question = EXCLUDED.question,
  observed = EXCLUDED.observed, why = EXCLUDED.why, evidence_refs = EXCLUDED.evidence_refs, improves = EXCLUDED.improves,
  grader = EXCLUDED.grader, created_at = now()`,
			episodeID, r.Gate, r.SubGate, r.Label, r.JudgedType, r.JudgedID, r.SpanID, string(r.Verdict), r.Question, r.Observed,
			r.Why, string(refs), r.Improves, string(grader)); err != nil {
			return fmt.Errorf("bucket2: save %s %s: %w", r.Gate, r.SubGate, err)
		}
	}
	return tx.Commit()
}

// Load returns the stored results of an episode in flow order (D1 to D10).
func Load(ctx context.Context, db *sql.DB, episodeID string) ([]Stored, error) {
	rows, err := db.QueryContext(ctx, `
SELECT id::text, gate, sub_gate, label, judged_type, judged_id, span_id, verdict, question, observed, why, evidence_refs::text, improves, grader::text, calibrated
FROM gate_results WHERE decision_episode_id = $1::uuid
ORDER BY CASE left(gate, 1) WHEN 'B' THEN 1 WHEN 'D' THEN 2 ELSE 3 END, substring(gate from 2)::int, sub_gate, judged_id`, episodeID)
	if err != nil {
		return nil, fmt.Errorf("bucket2: load results of %s: %w", episodeID, err)
	}
	defer rows.Close()
	var out []Stored
	for rows.Next() {
		var s Stored
		var verdict, refs, grader string
		s.EpisodeID = episodeID
		if err := rows.Scan(&s.ID, &s.Gate, &s.SubGate, &s.Label, &s.JudgedType, &s.JudgedID, &s.SpanID, &verdict, &s.Question,
			&s.Observed, &s.Why, &refs, &s.Improves, &grader, &s.Calibrated); err != nil {
			return nil, fmt.Errorf("bucket2: scan result: %w", err)
		}
		s.Verdict = ParseVerdict(verdict)
		if err := json.Unmarshal([]byte(refs), &s.EvidenceRefs); err != nil {
			return nil, fmt.Errorf("bucket2: decode evidence of %s: %w", s.ID, err)
		}
		if err := json.Unmarshal([]byte(grader), &s.Grader); err != nil {
			return nil, fmt.Errorf("bucket2: decode grader of %s: %w", s.ID, err)
		}
		out = append(out, s)
	}
	return out, rows.Err()
}
