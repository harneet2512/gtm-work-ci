package strategystore_test

import (
	"testing"

	"github.com/harneet2512/gtm-work/core-go/internal/learning"
	"github.com/harneet2512/gtm-work/core-go/internal/strategystore"
	"github.com/harneet2512/gtm-work/core-go/internal/strategystore/strategytest"
)

// seededRows counts what a learning seed writes: candidate knowledge and candidate criteria.
func seededRows(t *testing.T) (knowledge, versions string) {
	t.Helper()
	return scalar(t, `SELECT count(*)::text FROM knowledge`), scalar(t, `SELECT count(*)::text FROM evaluator_versions`)
}

func TestNoLearningVerdictIsRecordedAndSeedsNoKnowledge(t *testing.T) {
	f := sentFixture(t)
	knowledgeBefore, versionsBefore := seededRows(t)

	doc, err := f.verdict(func(r *strategystore.VerdictRequest) { r.Verdict, r.Note = "no_learning", "one-off, do not generalise" })

	if err != nil {
		t.Fatal(err)
	}
	valid(t, "judgment_inference", doc)
	m := decode(t, doc)
	if m["human_verdict"] != "no_learning" || m["corrected_statement"] != nil || m["verdict_surface"] != "slack" ||
		m["verdict_actor_label"] != "alex" || m["verdict_at"] == nil || m["human_note"] != "one-off, do not generalise" {
		t.Fatalf("inference = %v", m)
	}
	if k, v := seededRows(t); k != knowledgeBefore || v != versionsBefore {
		t.Fatalf("a no-learning verdict created learning rows: knowledge %s -> %s, criteria %s -> %s", knowledgeBefore, k, versionsBefore, v)
	}
	if got := scalar(t, `SELECT status FROM decision_episodes WHERE id = $1::uuid`, f.seed.EpisodeID); got != "judged" {
		t.Fatalf("episode status = %s, want judged: the human answered", got)
	}
	inferenceID := scalar(t, `SELECT id::text FROM judgment_inferences WHERE decision_episode_id = $1::uuid`, f.seed.EpisodeID)
	if got := scalar(t, `SELECT string_agg(verdict, '|' ORDER BY created_at) FROM judgment_verdicts WHERE judgment_inference_id = $1::uuid`, inferenceID); got != "no_learning" {
		t.Fatalf("verdict history = %q, want the no_learning row", got)
	}
}

func TestACorrectionSeedsKnowledgeWhereNoLearningDoesNot(t *testing.T) {
	// The control: the same episode, answered with a correction, does seed a candidate. Without it the test
	// above would pass on a surface that never seeds anything.
	f := sentFixture(t)
	knowledgeBefore, versionsBefore := seededRows(t)

	if _, err := f.verdict(func(r *strategystore.VerdictRequest) {
		r.Verdict, r.CorrectedStatement = "corrected", "The reviewer is new to us."
	}); err != nil {
		t.Fatal(err)
	}

	if k, v := seededRows(t); k == knowledgeBefore || v == versionsBefore {
		t.Fatalf("a correction seeded nothing: knowledge %s -> %s, criteria %s -> %s", knowledgeBefore, k, versionsBefore, v)
	}
}

func TestNoLearningIsTerminalForLearning(t *testing.T) {
	f := sentFixture(t)
	if _, err := f.verdict(func(r *strategystore.VerdictRequest) { r.Verdict = "no_learning" }); err != nil {
		t.Fatal(err)
	}
	knowledgeBefore, versionsBefore := seededRows(t)

	for _, tc := range []struct {
		name string
		edit func(*strategystore.VerdictRequest)
	}{
		{"confirm", func(r *strategystore.VerdictRequest) { r.Verdict = "confirmed" }},
		{"correct", func(r *strategystore.VerdictRequest) {
			r.Verdict, r.CorrectedStatement = "corrected", "Actually, a different reason."
		}},
	} {
		if _, err := f.verdict(tc.edit); err == nil {
			t.Fatalf("%s after no_learning was accepted", tc.name)
		} else {
			asRefused(t, err, "verdict_locked")
		}
	}

	if k, v := seededRows(t); k != knowledgeBefore || v != versionsBefore {
		t.Fatalf("a refused verdict seeded rows: knowledge %s -> %s, criteria %s -> %s", knowledgeBefore, k, versionsBefore, v)
	}
	doc, err := f.verdict(func(r *strategystore.VerdictRequest) { r.Verdict = "no_learning" })
	if err != nil || decode(t, doc)["human_verdict"] != "no_learning" {
		t.Fatalf("repeating no_learning must be accepted: %v %s", err, doc)
	}
	doc, err = f.verdict(func(r *strategystore.VerdictRequest) { r.Note = "still a note" })
	if err != nil || decode(t, doc)["human_note"] != "still a note" || decode(t, doc)["human_verdict"] != "no_learning" {
		t.Fatalf("a note after no_learning: %v %s", err, doc)
	}
}

func TestNoLearningAfterConfirmedIsAcceptedAndAfterCorrectedIsLocked(t *testing.T) {
	confirmed := sentFixture(t)
	if _, err := confirmed.verdict(func(r *strategystore.VerdictRequest) { r.Verdict = "confirmed" }); err != nil {
		t.Fatal(err)
	}
	doc, err := confirmed.verdict(func(r *strategystore.VerdictRequest) { r.Verdict = "no_learning" })
	if err != nil || decode(t, doc)["human_verdict"] != "no_learning" {
		t.Fatalf("confirmed to no_learning: %v %s", err, doc)
	}

	corrected := sentFixture(t)
	if _, err := corrected.verdict(func(r *strategystore.VerdictRequest) {
		r.Verdict, r.CorrectedStatement = "corrected", "The reviewer is new to us."
	}); err != nil {
		t.Fatal(err)
	}
	_, err = corrected.verdict(func(r *strategystore.VerdictRequest) { r.Verdict = "no_learning" })
	if err == nil {
		t.Fatal("no_learning withdrew a correction that already seeded a candidate")
	}
	asRefused(t, err, "verdict_locked")
}

func TestNoLearningRetiresTheCandidateTheSendAlreadySeeded(t *testing.T) {
	f := sentFixture(t)
	// The send seeds the unexplained delta's candidate before the human sees Message 3.
	deltaID := scalar(t, `INSERT INTO human_deltas (decision_episode_id, literal_changes, unexplained, candidate_criterion)
VALUES ($1::uuid, '[{"kind":"channel_changed","after":"slack"}]', true,
        '{"statement":"The human moved the thread to Slack.","suggested_eval_type":"channel_appropriateness"}')
RETURNING id::text`, f.seed.EpisodeID)
	seedTx, err := env.DB.BeginTx(f.ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = seedTx.Rollback() }()
	sit, err := learning.EpisodeSituation(f.ctx, seedTx, f.seed.EpisodeID)
	if err != nil {
		t.Fatal(err)
	}
	seeded, err := learning.SeedDeltaCriterion(f.ctx, seedTx, learning.DeltaSeed{
		DeltaID: deltaID, EpisodeID: f.seed.EpisodeID, AccountID: f.seed.AccountID,
		Criterion: learning.Criterion{Statement: "The human moved the thread to Slack.", SuggestedEvalType: "channel_appropriateness"},
		Changes:   []learning.LiteralChange{{Kind: "channel_changed", After: "slack"}}, Situation: sit}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := seedTx.Commit(); err != nil {
		t.Fatal(err)
	}

	if _, err := f.verdict(func(r *strategystore.VerdictRequest) { r.Verdict = "no_learning" }); err != nil {
		t.Fatal(err)
	}

	if got := scalar(t, `SELECT status FROM evaluator_versions WHERE evaluator = $1 AND version = $2`, seeded.EvalType, seeded.Version); got != "retired" {
		t.Fatalf("the send's candidate = %s, want retired by the no-learning verdict", got)
	}
	if got := scalar(t, `SELECT status FROM knowledge WHERE id = $1::uuid`, seeded.KnowledgeID); got != "candidate" {
		t.Fatalf("the send's knowledge = %s, want it left as an inert candidate", got)
	}
}

func TestNoLearningWithdrawsTheGeneratorFeedbackNoRunHasConsumed(t *testing.T) {
	f := explainedEditFixture(t)
	strategytest.SeedInference(t, env.DB, f.seed)
	pending := `SELECT count(*)::text FROM generator_feedback WHERE decision_episode_id = $1::uuid
 AND consumed_by_run_id IS NULL AND withdrawn_at IS NULL`
	if n := scalar(t, pending, f.seed.EpisodeID); n == "0" {
		t.Fatal("the explained send queued no feedback; the test has nothing to withdraw")
	}

	if _, err := f.verdict(func(r *strategystore.VerdictRequest) { r.Verdict = "no_learning" }); err != nil {
		t.Fatal(err)
	}

	if n := scalar(t, pending, f.seed.EpisodeID); n != "0" {
		t.Fatalf("%s offerable feedback rows survived a no-learning verdict: the next strategies call would still learn from it", n)
	}
	// The audit trail stays: the rows are withdrawn with a reason, never deleted.
	withdrawn := `SELECT count(*)::text FROM generator_feedback WHERE decision_episode_id = $1::uuid
 AND withdrawn_at IS NOT NULL AND withdrawn_reason LIKE '%declined%'`
	if n := scalar(t, withdrawn, f.seed.EpisodeID); n == "0" {
		t.Fatal("the withdrawn feedback left no audit trail")
	}
}
