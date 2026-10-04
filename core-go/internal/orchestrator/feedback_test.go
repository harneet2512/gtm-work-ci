package orchestrator_test

import (
	"testing"

	"github.com/harneet2512/gtm-work/core-go/internal/ctxfixture"
)

// HAR-119: an explained send-time correction is queued in generator_feedback, rides the account's next
// /v1/strategies request, and is consumed by exactly one run — a later run is offered nothing.
func queueOneFeedback(t *testing.T, sc scene, evalType, instruction string) (evalRunID, deltaID string) {
	t.Helper()
	// The correction came from an earlier episode of the account: its run is synthetic and never driven
	// (decision_episodes.agent_run_id is UNIQUE, so it cannot share the run under test). InsertRun, not
	// FreshRun: the latter would cancel the open run under test.
	earlierRun, _, err := ctxfixture.InsertRun(bg, env.DB, sc.AccountID, "recorded")
	if err != nil {
		t.Fatal(err)
	}
	tx, err := env.DB.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.Exec(`INSERT INTO agent_run_drafts (agent_run_id, draft_index, source, output)
 VALUES ($1::uuid, 1, 'strategy_generator', '{}')`, earlierRun); err != nil {
		t.Fatal(err)
	}
	if err := tx.QueryRow(`INSERT INTO eval_runs
 (agent_run_id, evaluator, evaluator_version, kind, verdict, draft_index, evidence_class)
 VALUES ($1::uuid, $2::eval_type, $2::text || ':v1', 'semantic', 'fail', 1, 'methodology') RETURNING id::text`,
		earlierRun, evalType).Scan(&evalRunID); err != nil {
		t.Fatal(err)
	}
	var episodeID string
	if err := tx.QueryRow(`INSERT INTO decision_episodes (agent_run_id, account_id, state_version, status)
 VALUES ($1::uuid, $2::uuid, 1, 'awaiting_choice') RETURNING id::text`, earlierRun, sc.AccountID).Scan(&episodeID); err != nil {
		t.Fatal(err)
	}
	// The explained delta and its explaining eval link land in one transaction: the deferred trigger
	// checks the cite at commit.
	if err := tx.QueryRow(`INSERT INTO human_deltas (decision_episode_id, literal_changes, unexplained)
 VALUES ($1::uuid, '[{"kind":"cta_changed","after":"softer ask"}]'::jsonb, false) RETURNING id::text`,
		episodeID).Scan(&deltaID); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(`INSERT INTO human_delta_explanations (human_delta_id, eval_run_id)
 VALUES ($1::uuid, $2::uuid)`, deltaID, evalRunID); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(`INSERT INTO generator_feedback
 (account_id, human_delta_id, decision_episode_id, eval_run_id, eval_type, instruction)
 VALUES ($1::uuid, $2::uuid, $3::uuid, $4::uuid, $5, $6)`,
		sc.AccountID, deltaID, episodeID, evalRunID, evalType, instruction); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	return evalRunID, deltaID
}

func TestGeneratorFeedbackRidesTheNextStrategiesCall(t *testing.T) {
	sc := newScene(t)
	evalRunID, _ := queueOneFeedback(t, sc, "cta_calibration", "Keep the CTA proportional to the buyer's readiness.")

	fw := newFake(sc)
	out := mustRun(t, service(t, fw), sc.RunID)
	if !out.Published {
		t.Fatalf("outcome %+v", out)
	}
	if len(fw.requests) != 1 {
		t.Fatalf("%d strategies calls", len(fw.requests))
	}
	fb := fw.requests[0].GeneratorFeedback
	if len(fb) != 1 || fb[0].EvalResultID != evalRunID || fb[0].EvalType != "cta_calibration" ||
		fb[0].Instruction != "Keep the CTA proportional to the buyer's readiness." {
		t.Fatalf("generator feedback on the request = %+v", fb)
	}
	// The run that received the correction consumed it.
	if got := scalar(t, `SELECT consumed_by_run_id::text FROM generator_feedback WHERE eval_run_id = $1::uuid`,
		evalRunID); got != sc.RunID {
		t.Fatalf("consumed_by_run_id = %s, want the run that received it %s", got, sc.RunID)
	}

	// A second run of the same account is offered nothing: the row was consumed.
	sc2 := newScene(t)
	fw2 := newFake(sc2)
	out2 := mustRun(t, service(t, fw2), sc2.RunID)
	if !out2.Published {
		t.Fatalf("second outcome %+v", out2)
	}
	if fb := fw2.requests[0].GeneratorFeedback; len(fb) != 0 {
		t.Fatalf("a consumed correction was re-offered: %+v", fb)
	}
}

func TestStrategiesRequestOmitsEmptyFeedback(t *testing.T) {
	sc := newScene(t)
	fw := newFake(sc)
	if out := mustRun(t, service(t, fw), sc.RunID); !out.Published {
		t.Fatalf("outcome %+v", out)
	}
	if len(fw.requests) != 1 || fw.requests[0].GeneratorFeedback != nil {
		t.Fatalf("a run without corrections must send no feedback key: %+v", fw.requests[0].GeneratorFeedback)
	}
}
