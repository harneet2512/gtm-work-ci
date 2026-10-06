package strategystore_test

import (
	"testing"

	"github.com/harneet2512/gtm-work/core-go/internal/strategystore"
	"github.com/harneet2512/gtm-work/core-go/internal/strategystore/strategytest"
)

// HAR-97 E11: the send-time re-evaluation batch of the FINAL artifact is linked to the decision it belongs to,
// so the recomputation read can tell it from the results a refused send persisted before the human repaired the
// draft. eval_runs rows are content-addressed and cannot say which batch was sent.

func linkedIDs(t *testing.T, runID string) string {
	t.Helper()
	return scalar(t, `SELECT COALESCE(string_agg(l.eval_run_id::text, ',' ORDER BY l.eval_run_id), '') FROM send_eval_results l
 JOIN human_strategy_decisions d ON d.id = l.human_strategy_decision_id WHERE d.agent_run_id = $1::uuid`, runID)
}

func TestASendLinksItsSendTimeBatchAndNothingElse(t *testing.T) {
	f := newFixture(t)
	if _, _, err := f.choose(f.seed.Candidates[1], nil); err != nil {
		t.Fatal(err)
	}
	if got := linkedIDs(t, f.seed.RunID); got != "" {
		t.Fatalf("a decision that was not sent has linked evals: %s", got)
	}
	if _, err := f.send("send"); err != nil {
		t.Fatal(err)
	}
	linked := scalar(t, `SELECT count(*)::text FROM send_eval_results l JOIN human_strategy_decisions d ON d.id = l.human_strategy_decision_id
 WHERE d.agent_run_id = $1::uuid`, f.seed.RunID)
	persisted := scalar(t, `SELECT count(*)::text FROM eval_runs WHERE agent_run_id = $1::uuid`, f.seed.RunID)
	if linked == "0" || linked != persisted {
		t.Fatalf("linked %s of %s send-time results: every result of the sent batch is linked", linked, persisted)
	}
}

func TestARefusedSendBatchIsNeverLinkedAndTheRepairedSendLinksOnlyItsOwn(t *testing.T) {
	f := newFixture(t)
	noEmail := strategytest.NewID()
	if _, err := env.DB.Exec(`INSERT INTO people (id, kind, display_name, account_id) VALUES ($1::uuid, 'contact', 'No Email (link)', $2::uuid)`,
		noEmail, f.seed.AccountID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.choose(f.seed.Candidates[1], nil); err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.choose(f.seed.Candidates[1], func(r *strategystore.DecisionRequest) {
		r.FinalTo = &[]strategystore.Recipient{{PersonID: noEmail, Role: "to"}}
	}); err != nil {
		t.Fatal(err)
	}
	_, err := f.send("send")
	asRefused(t, err, "blocking_eval")
	refused := scalar(t, `SELECT string_agg(id::text, ',' ORDER BY id) FROM eval_runs WHERE agent_run_id = $1::uuid`, f.seed.RunID)
	if got := linkedIDs(t, f.seed.RunID); got != "" {
		t.Fatalf("a refused send linked %s", got)
	}

	subject := "Repaired subject"
	if _, _, err := f.choose(f.seed.Candidates[1], func(r *strategystore.DecisionRequest) {
		r.FinalTo = &[]strategystore.Recipient{{PersonID: f.seed.Marco, Role: "to"}}
		r.FinalArtifact = &strategystore.Artifact{Channel: "email", Subject: &subject, Body: "Hi Marco,\n\nLet us know what timing suits.\n\nBest,\nDana"}
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.send("send"); err != nil {
		t.Fatalf("the repaired send: %v", err)
	}
	linked := linkedIDs(t, f.seed.RunID)
	if linked == "" {
		t.Fatal("the repaired send linked nothing")
	}
	// Rows are content-addressed, so an eval whose input did not change is the same row in both batches and
	// legitimately belongs to the sent one. What must never be linked is a blocking failure: a sent batch has none, and the refused artifact
	// produced one.
	failing := scalar(t, `SELECT count(*)::text FROM send_eval_results l JOIN human_strategy_decisions d ON d.id = l.human_strategy_decision_id
 JOIN eval_runs r ON r.id = l.eval_run_id WHERE d.agent_run_id = $1::uuid AND r.blocking`, f.seed.RunID)
	if failing != "0" {
		t.Fatalf("%s failing results of the refused batch were linked to the sent decision", failing)
	}
	if refused == linked {
		t.Fatal("the sent batch is the refused batch")
	}
}

func TestADiscardLinksNothing(t *testing.T) {
	f := newFixture(t)
	if _, _, err := f.choose(f.seed.Candidates[0], nil); err != nil {
		t.Fatal(err)
	}
	if _, err := f.send("discard"); err != nil {
		t.Fatal(err)
	}
	if got := linkedIDs(t, f.seed.RunID); got != "" {
		t.Fatalf("a discard linked %s: nothing was evaluated", got)
	}
}
