package learning_test

import (
	"errors"
	"testing"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/knowledge"
	"github.com/harneet2512/gtm-work/core-go/internal/knowledgestore"
	"github.com/harneet2512/gtm-work/core-go/internal/learning"
	"github.com/harneet2512/gtm-work/core-go/internal/strategystore/strategytest"
)

// HAR-97 B9: promoted_at and the promotion audit use the replay's world time, never now().
func TestReplayPromotionIsStampedWithTheWorldTime(t *testing.T) {
	_, res := seedCandidate(t, "channel_appropriateness")
	rules := testRules(t)
	if rep, err := learning.Backtest(ctx(), env.DB, learning.BacktestOpts{
		Evaluator: res.EvalType, Version: res.Version, GoldDirs: goldDirs, Rules: &rules, Now: fixedNow}); err != nil || !rep.Passed {
		t.Fatalf("backtest = %+v %v", rep, err)
	}
	if _, err := learning.AdvanceAt(ctx(), env.DB, res.EvalType, res.Version, "test", laterEp, true); err != nil {
		t.Fatal(err)
	}
	tx := beginTx(t)
	if _, _, err := knowledgestore.RecordEvidence(ctx(), tx, res.KnowledgeID, knowledge.Evidence{
		Kind: knowledge.EvidenceCustomerReaction, RefID: strategytest.NewID(), Polarity: "positive", At: laterEp}, rules); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	if _, err := learning.PromoteAt(ctx(), env.DB, res.EvalType, res.Version, rules, "test", laterEp, true); err != nil {
		t.Fatal(err)
	}

	var promoted time.Time
	if err := env.DB.QueryRow(`SELECT promoted_at FROM evaluator_versions WHERE evaluator = $1::eval_type AND version = $2`,
		res.EvalType, res.Version).Scan(&promoted); err != nil {
		t.Fatal(err)
	}
	if !promoted.UTC().Equal(laterEp) {
		t.Fatalf("promoted_at = %s, want the replay time %s", promoted.UTC(), laterEp)
	}
	var wall int
	if err := env.DB.QueryRow(`SELECT count(*) FROM eval_promotions WHERE evaluator = $1::eval_type AND version = $2
 AND created_at <> $3`, res.EvalType, res.Version, laterEp).Scan(&wall); err != nil || wall != 0 {
		t.Fatalf("%d promotion audit rows carry the wall clock (err %v)", wall, err)
	}
}

func TestReplayPromotionAndAdvanceRefuseAZeroWorldTime(t *testing.T) {
	rules := testRules(t)
	if _, err := learning.PromoteAt(ctx(), env.DB, "channel_appropriateness", 2, rules, "test", time.Time{}, true); !errors.Is(err, knowledgestore.ErrCreatedAtRequired) {
		t.Fatalf("PromoteAt without a world time = %v, want ErrCreatedAtRequired", err)
	}
	if _, err := learning.AdvanceAt(ctx(), env.DB, "channel_appropriateness", 2, "test", time.Time{}, true); !errors.Is(err, knowledgestore.ErrCreatedAtRequired) {
		t.Fatalf("AdvanceAt without a world time = %v, want ErrCreatedAtRequired", err)
	}
}
