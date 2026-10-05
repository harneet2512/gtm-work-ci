package strategystore_test

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/knowledge"
	"github.com/harneet2512/gtm-work/core-go/internal/knowledgestore"
	"github.com/harneet2512/gtm-work/core-go/internal/learning"
	"github.com/harneet2512/gtm-work/core-go/internal/strategystore"
	"github.com/harneet2512/gtm-work/core-go/internal/strategystore/strategytest"
	"github.com/harneet2512/gtm-work/core-go/internal/workerclient"
)

// repoFile finds a repository file by walking up from the test's working directory.
func repoFile(t *testing.T, rel string) string {
	t.Helper()
	dir, _ := os.Getwd()
	for {
		if _, err := os.Stat(filepath.Join(dir, rel)); err == nil {
			return filepath.Join(dir, rel)
		}
		if filepath.Dir(dir) == dir {
			t.Fatalf("%s not found", rel)
		}
		dir = filepath.Dir(dir)
	}
}

// The HAR-119 acceptance story on the Acme SOC2 episode (the CRMArena-derived fixture world): Ghost's
// preferred candidate is a Marco-only review-call thread — the draft drops Priya, who Marco named the
// commercial point of contact. The rep re-adds her at send. No eval flagged the gap, so the unexplained
// correction seeds a candidate criterion; a backtest against the stored episode and the WP16 gold
// promotes it to shadow; the knowledge lifecycle gates the final promotion; and on the next episode the
// learned axis runs alongside the send-time evals, reporting the same drop without ever blocking.
func TestAcmeSoc2LearningLoopAcceptance(t *testing.T) {
	rules, err := knowledge.LoadRules(repoFile(t, "contracts/knowledge/lifecycle.v1.json"))
	if err != nil {
		t.Fatal(err)
	}
	criterion := workerclient.DeltaCriterion{
		Statement:         "When the buyer names the commercial point of contact, keep that person on the thread.",
		SuggestedEvalType: "stakeholder_selection"}
	labeler := &stubLabeler{resp: workerclient.LabelDeltaResponse{
		SemanticLabels:     []string{"added_missing_stakeholder", "kept_champion_involved"},
		CandidateCriterion: &criterion, Model: "stub-labeler-v1"}}
	f := newFixtureWith(t, strategystore.WithLabeler(labeler), strategystore.WithKnowledgeRules(rules))

	// Draft 1 is Ghost's preference: to Marco, no cc. The rep sends it with Priya cc'ed.
	if _, _, err := f.choose(f.seed.Candidates[0], nil); err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.choose(f.seed.Candidates[0], func(r *strategystore.DecisionRequest) {
		r.FinalCC = &[]strategystore.Recipient{{PersonID: f.seed.Priya, Role: "cc"}}
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.send("send"); err != nil {
		t.Fatal(err)
	}

	// The send wrote an unexplained delta (the added-Priya edit repaired no flagged eval) and the loop
	// seeded its candidate criterion.
	deltaID := scalar(t, `SELECT human_delta_id::text FROM decision_episodes WHERE id = $1::uuid`, f.seed.EpisodeID)
	var version int
	var status, knowledgeID string
	if err := env.DB.QueryRow(`SELECT version, status, knowledge_id::text FROM evaluator_versions
 WHERE evaluator = 'stakeholder_selection' AND source_human_delta_id = $1::uuid`,
		deltaID).Scan(&version, &status, &knowledgeID); err != nil {
		t.Fatalf("the Acme correction seeded no candidate: %v", err)
	}
	if status != "candidate" {
		t.Fatalf("candidate status = %s", status)
	}
	if got := scalar(t, `SELECT unexplained::text FROM human_deltas WHERE id = $1::uuid`, deltaID); got != "true" {
		t.Fatalf("the Priya edit was explained? unexpected — the catalog flagged no stakeholder gap")
	}

	// Backtest: the seed delta is the one supporting correction; the CRMArena gold (real accounts; the invented
	// fixtures/evals/cases gold is retired and would report no gold) asserts stakeholder_selection on enough cases
	// to clear the coverage gate.
	rep, err := learning.Backtest(f.ctx, env.DB, learning.BacktestOpts{
		Evaluator: "stakeholder_selection", Version: version,
		GoldDirs: []string{repoFile(t, "fixtures/evals/crmarena/cases")}, Rules: &rules, Now: time.Now().UTC()})
	if err != nil || !rep.Passed {
		t.Fatalf("acceptance backtest = %+v %v", rep, err)
	}
	if rep.Episodes.Supporting < 1 || rep.Gold.Cases < 1 {
		t.Fatalf("backtest legs = episodes %+v gold %+v", rep.Episodes, rep.Gold)
	}

	// Candidate -> shadow is audited; shadow -> active is refused while the learned knowledge is still
	// a candidate (the lifecycle needs a positive reaction on top of the decision evidence).
	if _, err := learning.Advance(f.ctx, env.DB, "stakeholder_selection", version, "acceptance"); err != nil {
		t.Fatal(err)
	}
	if got := scalar(t, `SELECT to_status FROM eval_promotions
 WHERE evaluator = 'stakeholder_selection' AND version = $1`, version); got != "shadow" {
		t.Fatalf("promotion audit = %s", got)
	}
	if _, err := learning.Promote(f.ctx, env.DB, "stakeholder_selection", version, rules, "acceptance"); !errors.Is(err, learning.ErrGate) {
		t.Fatalf("promotion skipped the knowledge lifecycle: %v", err)
	}
	tx, err := env.DB.Begin()
	if err != nil {
		t.Fatal(err)
	}
	k, _, err := knowledgestore.RecordEvidence(f.ctx, tx, knowledgeID, knowledge.Evidence{
		Kind: knowledge.EvidenceCustomerReaction, RefID: strategytest.NewID(), Polarity: "positive", At: time.Now().UTC()}, rules)
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if k.Status != knowledge.StatusProvisional {
		t.Fatalf("learned knowledge = %s, want provisional", k.Status)
	}
	if _, err := learning.Promote(f.ctx, env.DB, "stakeholder_selection", version, rules, "acceptance"); err != nil {
		t.Fatal(err)
	}
	if got := scalar(t, `SELECT status FROM evaluator_versions
 WHERE evaluator = 'stakeholder_selection' AND version = $1`, version); got != "active" {
		t.Fatalf("promoted version = %s", got)
	}

	// The next episode of the same account: another Marco-only draft. The active learned axis checks the
	// final artifact alongside the evals and reports the drop — it never blocks the send.
	f2 := newFixtureWith(t)
	if _, _, err := f.svc.RecordDecision(f.ctx, f2.seed.RunID, strategystore.DecisionRequest{
		SelectedCandidateID: f2.seed.Candidates[0], Surface: "slack", ActorLabel: "alex"}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.Send(f.ctx, f2.seed.RunID, strategystore.SendRequest{
		Decision: "send", Surface: "slack", ActorLabel: "alex"}); err != nil {
		t.Fatalf("a learned axis must never block a send: %v", err)
	}
	var verdict, blocking string
	if err := env.DB.QueryRow(`SELECT verdict, blocking::text FROM eval_runs
 WHERE agent_run_id = $1::uuid AND evaluator = 'stakeholder_selection'
   AND evaluator_version = $2`, f2.seed.RunID, fmt.Sprintf("stakeholder_selection:v%d", version)).
		Scan(&verdict, &blocking); err != nil {
		t.Fatalf("no learned-axis result on the next send: %v", err)
	}
	if verdict != "fail" || blocking != "false" {
		t.Fatalf("the repeated Priya drop scored %s blocking %s, want a non-blocking fail", verdict, blocking)
	}
}
