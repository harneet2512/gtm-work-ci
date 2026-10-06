package learning_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/learning"
	"github.com/harneet2512/gtm-work/core-go/internal/strategystore/strategytest"
)

// decidedWithInference gives the seeded episode a recorded human decision and Ghost's judgment inference
// (the state after a send), with the human's answer still pending.
func decidedWithInference(t *testing.T, seed strategytest.Seeded) string {
	t.Helper()
	if _, err := env.DB.Exec(`INSERT INTO human_strategy_decisions
 (decision_episode_id, agent_run_id, strategy_set_id, selected_candidate_id, original_agent_preference,
  surface, actor_label, chosen_at)
 VALUES ($1::uuid, $2::uuid, $3::uuid, $4::uuid, $5::uuid, 'api', 'alex', now())`,
		seed.EpisodeID, seed.RunID, seed.SetID, seed.Candidates[0], seed.Candidates[0]); err != nil {
		t.Fatal(err)
	}
	return strategytest.SeedInference(t, env.DB, seed)
}

// declare records the human's no-learning answer on the inference, as strategystore does.
func declare(t *testing.T, inferenceID string) {
	t.Helper()
	if _, err := env.DB.Exec(`UPDATE judgment_inferences SET human_verdict = 'no_learning', verdict_at = now(),
 verdict_surface = 'slack', verdict_actor_label = 'alex' WHERE id = $1::uuid`, inferenceID); err != nil {
		t.Fatal(err)
	}
}

func TestLearningDeclinedReadsTheNoLearningVerdict(t *testing.T) {
	seed := seedEpisode(t)
	inference := decidedWithInference(t, seed)

	declined, err := learning.LearningDeclined(ctx(), env.DB, seed.EpisodeID)
	if err != nil || declined {
		t.Fatalf("a pending inference declined = %v, %v", declined, err)
	}
	declare(t, inference)

	declined, err = learning.LearningDeclined(ctx(), env.DB, seed.EpisodeID)
	if err != nil || !declined {
		t.Fatalf("a no_learning inference declined = %v, %v", declined, err)
	}
}

func TestSeedsRefuseAnEpisodeTheHumanDeclinedToLearnFrom(t *testing.T) {
	seed := seedEpisode(t)
	declare(t, decidedWithInference(t, seed))
	rules := testRules(t)
	knowledgeBefore := count(t, `SELECT count(*) FROM knowledge`)
	versionsBefore := count(t, `SELECT count(*) FROM evaluator_versions`)

	tx, err := env.DB.BeginTx(ctx(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := learning.SeedVerdictCriterion(ctx(), tx, seed.EpisodeID, "The buyer asked for a slower cadence.", fixedNow, false, &rules); !errors.Is(err, learning.ErrLearningDeclined) {
		t.Fatalf("verdict seed err = %v, want ErrLearningDeclined", err)
	}
	sit, err := learning.EpisodeSituation(ctx(), tx, seed.EpisodeID)
	if err != nil {
		t.Fatal(err)
	}
	_, err = learning.SeedDeltaCriterion(ctx(), tx, learning.DeltaSeed{
		DeltaID: "0d000000-0000-4000-8000-000000000001", EpisodeID: seed.EpisodeID, AccountID: seed.AccountID,
		Criterion: learning.Criterion{Statement: "The human changed the channel.", SuggestedEvalType: "channel_appropriateness"},
		Changes:   []learning.LiteralChange{{Kind: "channel_changed", After: "slack"}}, Situation: sit, At: fixedNow}, &rules)
	if !errors.Is(err, learning.ErrLearningDeclined) {
		t.Fatalf("delta seed err = %v, want ErrLearningDeclined", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	if n := count(t, `SELECT count(*) FROM knowledge`); n != knowledgeBefore {
		t.Fatalf("a declined episode created candidate knowledge: %d -> %d", knowledgeBefore, n)
	}
	if n := count(t, `SELECT count(*) FROM evaluator_versions`); n != versionsBefore {
		t.Fatalf("a declined episode created a candidate criterion: %d -> %d", versionsBefore, n)
	}
}

func TestDeclineEpisodeRetiresTheCandidatesItAlreadySeeded(t *testing.T) {
	seed, res := seedCandidate(t, "channel_appropriateness") // the send-time delta seed, before any verdict
	inference := decidedWithInference(t, seed)
	declare(t, inference)

	tx, err := env.DB.BeginTx(ctx(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	retired, err := learning.DeclineEpisode(ctx(), tx, seed.EpisodeID, "slack:U1 (alex)", fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	if retired != 1 {
		t.Fatalf("retired = %d, want the one candidate the send seeded", retired)
	}
	if got := scalar(t, `SELECT status FROM evaluator_versions WHERE evaluator = $1 AND version = $2`, res.EvalType, res.Version); got != "retired" {
		t.Fatalf("candidate status = %s, want retired", got)
	}
	if got := scalar(t, `SELECT reason FROM eval_promotions WHERE evaluator = $1 AND version = $2 AND to_status = 'retired'`, res.EvalType, res.Version); !strings.Contains(got, "declined") {
		t.Fatalf("audit reason = %q, want it to say learning was declined", got)
	}
	// The knowledge row stays a candidate: nothing applicable was created or changed.
	if got := scalar(t, `SELECT status FROM knowledge WHERE id = $1::uuid`, res.KnowledgeID); got != "candidate" {
		t.Fatalf("knowledge status = %s, want candidate", got)
	}
	if _, err := learning.Advance(ctx(), env.DB, res.EvalType, res.Version, "test"); err == nil {
		t.Fatal("a retired candidate was advanced")
	}
}

func TestDeclineEpisodeLeavesOtherEpisodesAndActiveVersionsAlone(t *testing.T) {
	_, other := seedCandidate(t, "channel_appropriateness")
	seed := seedEpisode(t)
	declare(t, decidedWithInference(t, seed))

	tx, err := env.DB.BeginTx(ctx(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	retired, err := learning.DeclineEpisode(ctx(), tx, seed.EpisodeID, "slack:U1 (alex)", fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	if retired != 0 {
		t.Fatalf("retired = %d, want 0: this episode seeded nothing", retired)
	}
	if got := scalar(t, `SELECT status FROM evaluator_versions WHERE evaluator = $1 AND version = $2`, other.EvalType, other.Version); got != "candidate" {
		t.Fatalf("another episode's candidate = %s, want it untouched", got)
	}
}

func TestADeclinedEpisodeIsNeverTheSupportThatPromotesKnowledge(t *testing.T) {
	// The send seeded a candidate from the episode's delta; the human then declined learning from it.
	const axis = "stakeholder_coverage"
	seed, res := seedCandidate(t, axis)
	declare(t, decidedWithInference(t, seed))
	rules := testRules(t)

	rep, err := learning.Backtest(ctx(), env.DB, learning.BacktestOpts{
		Evaluator: res.EvalType, Version: res.Version, GoldDirs: goldDirs, Rules: &rules, Now: fixedNow,
		Gate: learning.Gate{MinSupportingEpisodes: 1, MinGoldCases: 0}})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Passed || rep.Episodes.Supporting != 0 {
		t.Fatalf("a declined episode was counted as support: %+v", rep)
	}
	if n := count(t, `SELECT count(*) FROM knowledge_evidence WHERE knowledge_id = $1::uuid AND ref_id = $2::uuid`,
		res.KnowledgeID, seed.EpisodeID); n != 0 {
		t.Fatalf("the declined episode was recorded as evidence on the knowledge: %d", n)
	}
	if _, err := learning.Advance(ctx(), env.DB, res.EvalType, res.Version, "test"); !errors.Is(err, learning.ErrGate) {
		t.Fatalf("advance on declined support = %v, want ErrGate", err)
	}
	if got := scalar(t, `SELECT status FROM knowledge WHERE id = $1::uuid`, res.KnowledgeID); got != "candidate" {
		t.Fatalf("knowledge status = %s, want candidate", got)
	}
}

func TestADeclinedCorrectionDoesNotCountAsACorrectedVerdict(t *testing.T) {
	_, res := seedCandidate(t, "human_delta")
	rules := testRules(t)
	runs := 0
	backtest := func() learning.BacktestReport {
		runs++ // a run id derives from the clock: each run needs its own instant
		rep, err := learning.Backtest(ctx(), env.DB, learning.BacktestOpts{
			Evaluator: res.EvalType, Version: res.Version, GoldDirs: goldDirs, Rules: &rules,
			Now: fixedNow.Add(time.Duration(runs) * time.Minute)})
		if err != nil {
			t.Fatal(err)
		}
		return rep
	}
	before := backtest().Episodes.Corrected

	seed := seedEpisode(t)
	inference := decidedWithInference(t, seed)
	if _, err := env.DB.Exec(`INSERT INTO judgment_verdicts
 (judgment_inference_id, verdict, corrected_statement, surface, actor_label)
 VALUES ($1::uuid, 'corrected', 'An earlier correction.', 'api', 'alex')`, inference); err != nil {
		t.Fatal(err)
	}
	if got := backtest().Episodes.Corrected; got != before+1 {
		t.Fatalf("control: a corrected verdict counts: %d -> %d", before, got)
	}
	declare(t, inference)
	if got := backtest().Episodes.Corrected; got != before {
		t.Fatalf("Corrected = %d, want %d: the declined episode's correction is excluded", got, before)
	}
}

func TestDeclineEpisodeKeepsAShadowOtherEpisodesEvidenceHelpedPass(t *testing.T) {
	seed, res := seedCandidate(t, "channel_appropriateness")
	// A passing backtest moves the candidate to shadow on the strength of every supporting episode.
	rules := testRules(t)
	if _, err := learning.Backtest(ctx(), env.DB, learning.BacktestOpts{
		Evaluator: res.EvalType, Version: res.Version, GoldDirs: goldDirs, Rules: &rules, Now: fixedNow}); err != nil {
		t.Fatal(err)
	}
	if _, err := learning.Advance(ctx(), env.DB, res.EvalType, res.Version, "test"); err != nil {
		t.Fatal(err)
	}
	declare(t, decidedWithInference(t, seed))

	tx, err := env.DB.BeginTx(ctx(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	retired, err := learning.DeclineEpisode(ctx(), tx, seed.EpisodeID, "slack:U1 (alex)", fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if retired != 0 {
		t.Fatalf("retired = %d: a shadow may rest on other episodes' evidence and is not retired by one opt-out", retired)
	}
	if got := scalar(t, `SELECT status FROM evaluator_versions WHERE evaluator = $1 AND version = $2`, res.EvalType, res.Version); got != "shadow" {
		t.Fatalf("status = %s, want shadow", got)
	}
}

func TestDeclineEpisodeAuditsAtWorldTime(t *testing.T) {
	seed, res := seedCandidate(t, "channel_appropriateness")
	declare(t, decidedWithInference(t, seed))
	world := fixedNow.Add(-72 * time.Hour)

	tx, err := env.DB.BeginTx(ctx(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := learning.DeclineEpisode(ctx(), tx, seed.EpisodeID, "slack:U1 (alex)", world); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	got := scalar(t, `SELECT to_char(created_at AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS') FROM eval_promotions
 WHERE evaluator = $1 AND version = $2 AND to_status = 'retired'`, res.EvalType, res.Version)
	if want := world.UTC().Format("2006-01-02T15:04:05"); got != want {
		t.Fatalf("retirement audited at %s, want the world time %s", got, want)
	}
}
