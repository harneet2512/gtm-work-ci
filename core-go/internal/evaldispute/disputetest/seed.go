// Package disputetest seeds EvalResults (eval_runs rows) to dispute. Test support only; nothing in cmd/
// imports it. The run and its drafts come from strategytest.Seed, which writes drafts 1..3.
package disputetest

import (
	"database/sql"
	"testing"

	"github.com/harneet2512/gtm-work/core-go/internal/strategystore/strategytest"
)

// Result describes one seeded EvalResult.
type Result struct {
	RunID      string
	DraftIndex int
	EvalType   string
	Verdict    string
	Blocking   bool
	SendTime   bool // recorded by the send-time re-evaluation rather than at generation
}

// SeedResult writes one semantic EvalResult for r and returns its id. A blocking result must be a fail
// (eval_runs_blocking_is_fail).
func SeedResult(t testing.TB, db *sql.DB, r Result) string {
	t.Helper()
	id := strategytest.NewID()
	phase := "generation"
	if r.SendTime {
		phase = "send"
	}
	if _, err := db.Exec(`INSERT INTO eval_runs (id, agent_run_id, draft_index, evaluator, evaluator_version, kind, verdict, blocking,
  rationale, evidence_class, model, phase)
 VALUES ($1::uuid, $2::uuid, $3, $4, $5, 'semantic', $6, $7, 'seeded for a dispute test', 'methodology', 'test-judge', $8)`,
		id, r.RunID, r.DraftIndex, r.EvalType, r.EvalType+":v1", r.Verdict, r.Blocking, phase); err != nil {
		t.Fatalf("disputetest: seed eval result: %v", err)
	}
	return id
}
