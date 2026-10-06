package learning_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/knowledge"
	"github.com/harneet2512/gtm-work/core-go/internal/knowledgestore"
	"github.com/harneet2512/gtm-work/core-go/internal/learning"
	"github.com/harneet2512/gtm-work/core-go/internal/strategystore/strategytest"
)

// seedCandidate is a channel-axis candidate on a fresh episode; the axis covers the test gold.
func seedCandidate(t *testing.T, axis string) (strategytest.Seeded, learning.SeedResult) {
	t.Helper()
	seed := seedEpisode(t)
	deltaID := insertDelta(t, seed.EpisodeID, axis, `[{"kind":"channel_changed","after":"slack"}]`)
	res := seedCriterion(t, seed.EpisodeID, seed.AccountID, deltaID, axis,
		"The human moves this thread to Slack; a draft on the wrong channel repeats the correction.",
		[]learning.LiteralChange{{Kind: "channel_changed", After: "slack"}}, fixedNow, nil)
	return seed, res
}

func TestBacktestPersistsRunAndMetrics(t *testing.T) {
	seed, res := seedCandidate(t, "channel_appropriateness")
	rules := testRules(t)
	rep, err := learning.Backtest(ctx(), env.DB, learning.BacktestOpts{
		Evaluator: res.EvalType, Version: res.Version, GoldDirs: goldDirs, Rules: &rules, Now: fixedNow})
	if err != nil {
		t.Fatal(err)
	}
	if !rep.Passed {
		t.Fatalf("backtest = %+v", rep)
	}
	// One stored unexplained delta on the axis supports the candidate (its own seed counts).
	if rep.Episodes.Supporting < 1 || rep.Episodes.Total < 1 || !rep.Episodes.Executable {
		t.Fatalf("episode metrics = %+v", rep.Episodes)
	}
	if rep.Gold.Cases != 4 || rep.Gold.Scored != 4 || rep.Gold.Agree != 2 || rep.Gold.FalsePass != 1 ||
		rep.Gold.FalseBlock != 1 || rep.Gold.Agreement == nil || *rep.Gold.Agreement != 0.5 {
		t.Fatalf("gold metrics = %+v", rep.Gold)
	}
	var stored string
	if err := env.DB.QueryRow(`SELECT metrics::text FROM eval_backtest_runs WHERE id = $1::uuid`, rep.ID).
		Scan(&stored); err != nil {
		t.Fatalf("the run is not persisted: %v", err)
	}
	if got := scalar(t, `SELECT metrics ->> 'n_cases' FROM evaluator_versions
 WHERE evaluator = $1 AND version = $2`, res.EvalType, res.Version); got != "4" {
		t.Fatalf("version metrics n_cases = %s", got)
	}
	// The seed ran without rules, so the backtest's evidence write recorded the supporting episode.
	if n := count(t, `SELECT count(*) FROM knowledge_evidence
 WHERE knowledge_id = $1::uuid AND kind = 'decision_episode' AND ref_id = $2::uuid`,
		res.KnowledgeID, seed.EpisodeID); n != 1 {
		t.Fatalf("the backtest did not record the supporting episode: %d", n)
	}
	// Status is unchanged: only Advance moves a passing candidate.
	if got := scalar(t, `SELECT status FROM evaluator_versions WHERE evaluator = $1 AND version = $2`,
		res.EvalType, res.Version); got != "candidate" {
		t.Fatalf("status = %s, a backtest never promotes on its own", got)
	}
}

func TestBacktestGateRefusesAnUnsupportedAxis(t *testing.T) {
	seed := seedEpisode(t)
	// The candidate's delta sits on a different axis: nothing supports this criterion.
	deltaID := insertDelta(t, seed.EpisodeID, "cta_calibration", `[{"kind":"cta_changed","after":"x"}]`)
	res := seedCriterion(t, seed.EpisodeID, seed.AccountID, deltaID, "grounding",
		"A criterion on an axis the stored corrections never touched.",
		[]learning.LiteralChange{{Kind: "cta_changed", After: "x"}}, fixedNow, nil)
	rep, err := learning.Backtest(ctx(), env.DB, learning.BacktestOpts{
		Evaluator: res.EvalType, Version: res.Version, GoldDirs: goldDirs, Now: fixedNow})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Passed || rep.Episodes.Supporting != 0 {
		t.Fatalf("an unsupported candidate passed: %+v", rep)
	}
	if !strings.Contains(rep.Reason, "below the gate") {
		t.Fatalf("reason = %q", rep.Reason)
	}
	// A failed gate still persists the run — the audit trail records the refusal.
	if got := scalar(t, `SELECT passed FROM eval_backtest_runs WHERE id = $1::uuid`, rep.ID); got != "false" {
		t.Fatalf("persisted passed = %s", got)
	}
	if _, err := learning.Advance(ctx(), env.DB, res.EvalType, res.Version, "test"); !errors.Is(err, learning.ErrGate) {
		t.Fatalf("advance after a failed backtest = %v, want ErrGate", err)
	}
}

func TestAdvanceAndPromoteLifecycle(t *testing.T) {
	seed, res := seedCandidate(t, "channel_appropriateness")
	rules := testRules(t)

	// Advance is gated on a passing backtest; without one the gate refuses.
	if _, err := learning.Advance(ctx(), env.DB, res.EvalType, res.Version, "test"); !errors.Is(err, learning.ErrGate) {
		t.Fatalf("advance without a backtest = %v, want ErrGate", err)
	}
	rep, err := learning.Backtest(ctx(), env.DB, learning.BacktestOpts{
		Evaluator: res.EvalType, Version: res.Version, GoldDirs: goldDirs, Rules: &rules, Now: fixedNow})
	if err != nil || !rep.Passed {
		t.Fatalf("backtest = %+v %v", rep, err)
	}
	tr, err := learning.Advance(ctx(), env.DB, res.EvalType, res.Version, "test")
	if err != nil {
		t.Fatal(err)
	}
	if tr.FromStatus != "candidate" || tr.ToStatus != "shadow" {
		t.Fatalf("transition = %+v", tr)
	}
	var toStatus, runID string
	if err := env.DB.QueryRow(`SELECT to_status, backtest_run_id::text FROM eval_promotions
 WHERE evaluator = $1 AND version = $2`, res.EvalType, res.Version).Scan(&toStatus, &runID); err != nil {
		t.Fatal(err)
	}
	if toStatus != "shadow" || runID != rep.ID {
		t.Fatalf("promotion audit = %s run %s, want shadow %s", toStatus, runID, rep.ID)
	}

	// Promote is gated on the knowledge lifecycle: the seeded knowledge is still a candidate
	// (one decision evidence, no positive reaction yet — provisional needs both).
	if _, err := learning.Promote(ctx(), env.DB, res.EvalType, res.Version, rules, "test"); !errors.Is(err, learning.ErrGate) {
		t.Fatalf("promote without applicable knowledge = %v, want ErrGate", err)
	}
	tx, err := env.DB.BeginTx(ctx(), nil)
	if err != nil {
		t.Fatal(err)
	}
	k, _, err := knowledgestore.RecordEvidence(ctx(), tx, res.KnowledgeID, knowledge.Evidence{
		Kind: knowledge.EvidenceCustomerReaction, RefID: strategytest.NewID(), Polarity: "positive", At: fixedNow}, rules)
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if k.Status != knowledge.StatusProvisional {
		t.Fatalf("knowledge = %s, want provisional", k.Status)
	}
	tr, err = learning.Promote(ctx(), env.DB, res.EvalType, res.Version, rules, "test")
	if err != nil {
		t.Fatal(err)
	}
	if tr.FromStatus != "shadow" || tr.ToStatus != "active" {
		t.Fatalf("transition = %+v", tr)
	}
	var status string
	var promotedAt *string
	if err := env.DB.QueryRow(`SELECT status, promoted_at::text FROM evaluator_versions
 WHERE evaluator = $1 AND version = $2`, res.EvalType, res.Version).Scan(&status, &promotedAt); err != nil {
		t.Fatal(err)
	}
	if status != "active" || promotedAt == nil {
		t.Fatalf("promoted row = %s %v", status, promotedAt)
	}
	versions, err := learning.ActiveVersions(ctx(), env.DB, seed.AccountID)
	if err != nil || versions[res.EvalType] != res.Version {
		t.Fatalf("active versions = %+v %v", versions, err)
	}
	// The promotion re-versions only the account the criterion was learned on.
	if other, err := learning.ActiveVersions(ctx(), env.DB, strategytest.NewID()); err != nil || other[res.EvalType] != 0 {
		t.Fatalf("another account sees %v %v — a learned version leaked globally", other[res.EvalType], err)
	}

	// A later learned version promoted on the same axis retires this one atomically.
	seed2 := seedEpisode(t)
	deltaID2 := insertDelta(t, seed2.EpisodeID, res.EvalType, `[{"kind":"channel_changed","after":"slack"}]`)
	res2 := seedCriterion(t, seed2.EpisodeID, seed2.AccountID, deltaID2, res.EvalType,
		"The same correction recurred on a second episode.",
		[]learning.LiteralChange{{Kind: "channel_changed", After: "slack"}}, fixedNow, nil)
	if res2.Version != res.Version+1 {
		t.Fatalf("second candidate = v%d, want v%d (deterministic bump)", res2.Version, res.Version+1)
	}
	if _, err := learning.Backtest(ctx(), env.DB, learning.BacktestOpts{
		Evaluator: res2.EvalType, Version: res2.Version, GoldDirs: goldDirs, Rules: &rules, Now: fixedNow.Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	if _, err := learning.Advance(ctx(), env.DB, res2.EvalType, res2.Version, "test"); err != nil {
		t.Fatal(err)
	}
	tx, err = env.DB.BeginTx(ctx(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := knowledgestore.RecordEvidence(ctx(), tx, res2.KnowledgeID, knowledge.Evidence{
		Kind: knowledge.EvidenceCustomerReaction, RefID: strategytest.NewID(), Polarity: "positive", At: fixedNow}, rules); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if _, err := learning.Promote(ctx(), env.DB, res2.EvalType, res2.Version, rules, "test"); err != nil {
		t.Fatal(err)
	}
	if got := scalar(t, `SELECT status FROM evaluator_versions WHERE evaluator = $1 AND version = $2`,
		res.EvalType, res.Version); got != "retired" {
		t.Fatalf("prior active = %s, want retired by the atomic promotion", got)
	}
	if n := count(t, `SELECT count(*) FROM evaluator_versions WHERE evaluator = $1 AND status = 'active'`, res.EvalType); n != 1 {
		t.Fatalf("%d active versions of %s", n, res.EvalType)
	}
	if n := count(t, `SELECT count(*) FROM eval_promotions WHERE evaluator = $1`, res.EvalType); n < 4 {
		t.Fatalf("audit rows = %d, want advance+promote for both versions", n)
	}
}

func TestBacktestVerdictCandidatePassesWithoutGold(t *testing.T) {
	seed := seedEpisode(t)
	// The judgment inference the verdict corrects sits on a recorded human decision.
	if _, err := env.DB.Exec(`INSERT INTO human_strategy_decisions
 (decision_episode_id, agent_run_id, strategy_set_id, selected_candidate_id, original_agent_preference,
  surface, actor_label, chosen_at)
 VALUES ($1::uuid, $2::uuid, $3::uuid, $4::uuid, $5::uuid, 'api', 'alex', now())`,
		seed.EpisodeID, seed.RunID, seed.SetID, seed.Candidates[0], seed.Candidates[0]); err != nil {
		t.Fatal(err)
	}
	inferenceID := strategytest.SeedInference(t, env.DB, seed)
	if _, err := env.DB.Exec(`INSERT INTO judgment_verdicts
 (judgment_inference_id, verdict, corrected_statement, surface, actor_label)
 VALUES ($1::uuid, 'corrected', 'The buyer asked for a slower cadence.', 'api', 'alex')`, inferenceID); err != nil {
		t.Fatal(err)
	}
	rules := testRules(t)
	tx, err := env.DB.BeginTx(ctx(), nil)
	if err != nil {
		t.Fatal(err)
	}
	res, err := learning.SeedVerdictCriterion(ctx(), tx, seed.EpisodeID,
		"The buyer asked for a slower cadence.", fixedNow, false, &rules)
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	rep, err := learning.Backtest(ctx(), env.DB, learning.BacktestOpts{
		Evaluator: res.EvalType, Version: res.Version, GoldDirs: goldDirs, Rules: &rules, Now: fixedNow})
	if err != nil {
		t.Fatal(err)
	}
	// human_delta is a trace axis: the corrected verdict is its support and no gold asserts it.
	if !rep.Passed || rep.Episodes.Corrected < 1 || rep.Gold.Cases != 0 {
		t.Fatalf("verdict backtest = %+v", rep)
	}
}

func TestRetireAndStatus(t *testing.T) {
	_, res := seedCandidate(t, "channel_appropriateness")
	tr, err := learning.Retire(ctx(), env.DB, res.EvalType, res.Version, "test", "superseded by a clearer criterion")
	if err != nil {
		t.Fatal(err)
	}
	if tr.FromStatus != "candidate" || tr.ToStatus != "retired" {
		t.Fatalf("transition = %+v", tr)
	}
	if _, err := learning.Retire(ctx(), env.DB, res.EvalType, 9999, "test", "x"); !errors.Is(err, learning.ErrNotFound) {
		t.Fatalf("retire missing = %v, want ErrNotFound", err)
	}
	// A retired version cannot leave the table's lifecycle (the trigger refuses retired -> shadow).
	if _, err := env.DB.Exec(`UPDATE evaluator_versions SET status = 'shadow' WHERE evaluator = $1 AND version = $2`,
		res.EvalType, res.Version); err == nil {
		t.Fatal("the transition trigger allowed retired -> shadow")
	}
	rows, err := learning.Status(ctx(), env.DB)
	if err != nil || len(rows) == 0 {
		t.Fatalf("status = %+v %v", rows, err)
	}
	var found bool
	for _, r := range rows {
		if r.Evaluator == res.EvalType && r.Version == res.Version {
			found = true
			if r.Status != "retired" {
				t.Fatalf("status row = %+v", r)
			}
		}
	}
	if !found {
		t.Fatalf("status report misses %s:v%d", res.EvalType, res.Version)
	}
}
