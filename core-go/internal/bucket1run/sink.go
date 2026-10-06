// Package bucket1run runs the Bucket 1 gates (B1-B9) of a real episode once the run is published: it loads the
// episode from the database, makes ONE model call per gate that has a semantic assertion through the worker's
// normal LLM path (so the one-time recording captures it), grades B1 to B9 and persists the results through a
// GateResultSink. The sink is the gate_results table the Bucket 2 gates already use.
package bucket1run

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/harneet2512/gtm-work/core-go/internal/bucket1"
)

// GateResultSink stores the Bucket 1 results of one episode. The SQL implementation writes the shared gate_results
// table (migration 0036, widened to B1-B9 by 0037); a result that cannot be stored is an error, never dropped.
type GateResultSink interface {
	Save(ctx context.Context, episodeID string, results []bucket1.Result) error
	// Has reports whether a result of the gate is already stored for the episode (a model gate is never re-judged).
	Has(ctx context.Context, episodeID, gate string) (bool, error)
}

// SQLSink is the gate_results implementation of GateResultSink.
type SQLSink struct{ DB *sql.DB }

type graderJSON struct {
	Kind  string `json:"kind"`
	Model string `json:"model,omitempty"`
}

// refStrings turns the resolvable evidence of a result into gate_results evidence_refs strings.
func refStrings(refs []bucket1.Ref) []string {
	out := []string{} // a JSON array even when empty (the table requires one)
	seen := map[string]bool{}
	add := func(s string) {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	for _, r := range refs {
		switch {
		case r.ClaimID != "":
			add("claim:" + r.ClaimID)
		case r.ActivityID != "":
			add("activity:" + r.ActivityID)
		case r.StepID != "":
			add("agent_run_step:" + r.StepID)
		}
	}
	return out
}

// Save upserts the results (one row per gate and judged object).
func (s SQLSink) Save(ctx context.Context, episodeID string, results []bucket1.Result) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("bucket1run: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	for _, r := range results {
		refs := refStrings(r.EvidenceRefs)
		verdict := bucket1.NormalizeVerdict(r.Verdict)
		if verdict != bucket1.Unknown && len(refs) == 0 {
			return fmt.Errorf("bucket1run: %s says %s with no resolvable evidence (rule R1)", r.Gate, verdict)
		}
		if r.Observed == "" || r.Why == "" {
			return fmt.Errorf("bucket1run: %s result needs what was observed and why", r.Gate)
		}
		refJSON, _ := json.Marshal(refs)
		g := graderJSON{Kind: "deterministic"}
		if r.Grader != bucket1.GraderDeterministic {
			g.Kind, g.Model = "model", r.Model
		}
		graderRaw, _ := json.Marshal(g)
		if _, err := tx.ExecContext(ctx, `
INSERT INTO gate_results (decision_episode_id, gate, sub_gate, label, judged_type, judged_id, span_id, verdict, question, observed, why, evidence_refs, improves, grader)
VALUES ($1::uuid, $2, '', '', $3, $4, $5, $6, $7, $8, $9, $10::jsonb, $11, $12::jsonb)
ON CONFLICT (decision_episode_id, gate, sub_gate, judged_type, judged_id) DO UPDATE SET
  span_id = EXCLUDED.span_id, verdict = EXCLUDED.verdict, question = EXCLUDED.question, observed = EXCLUDED.observed,
  why = EXCLUDED.why, evidence_refs = EXCLUDED.evidence_refs, improves = EXCLUDED.improves, grader = EXCLUDED.grader, created_at = now()`,
			episodeID, r.Gate, r.JudgedObject.Type, r.JudgedObject.ID, r.SpanID, verdict, r.Question, r.Observed, r.Why,
			string(refJSON), r.Improves, string(graderRaw)); err != nil {
			return fmt.Errorf("bucket1run: save %s: %w", r.Gate, err)
		}
	}
	return tx.Commit()
}

// Has reports whether a result of the gate is stored for the episode.
func (s SQLSink) Has(ctx context.Context, episodeID, gate string) (bool, error) {
	var ok bool
	if err := s.DB.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM gate_results WHERE decision_episode_id = $1::uuid AND gate = $2)`,
		episodeID, gate).Scan(&ok); err != nil {
		return false, fmt.Errorf("bucket1run: look up %s of %s: %w", gate, episodeID, err)
	}
	return ok, nil
}
