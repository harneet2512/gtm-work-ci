package orchestrator_test

import (
	"testing"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/knowledge"
	"github.com/harneet2512/gtm-work/core-go/internal/knowledgestore"
	"github.com/harneet2512/gtm-work/core-go/internal/strategystore"
	"github.com/harneet2512/gtm-work/core-go/internal/workerclient"
)

// blockWhen makes the judge fail a candidate with a blocking verdict whenever match says so.
func blockWhen(f *fakeWorker, match func(workerclient.JudgeRequest) bool) {
	f.judgeItem = func(req workerclient.JudgeRequest) []workerclient.BundleItem {
		if match(req) {
			return []workerclient.BundleItem{semantic(req, "buyer_readiness", "fail", true, "Drop the signing ask and leave the timing to the buyer."), notRelevant("channel_appropriateness")}
		}
		return []workerclient.BundleItem{semantic(req, "buyer_readiness", "pass", false, ""), notRelevant("channel_appropriateness")}
	}
}

// revisedBody returns the revision the fake reviser hands back: the same candidate with new words.
func revisedBody(f *fakeWorker) {
	f.revise = func(req workerclient.ReviseRequest) (workerclient.Candidate, error) {
		c := req.Candidate
		c.FullActionArtifact.Body = "Revised: we will wait for your team and follow your timing."
		c.Preview, c.Rationale = c.FullActionArtifact.Body, "Revised to follow the feedback: "+req.Feedback[0].Instruction
		return c, nil
	}
}

func TestABlockingCandidateIsRevisedOnceAndReEvaluated(t *testing.T) {
	sc := newScene(t)
	fw := newFake(sc)
	blockWhen(fw, func(r workerclient.JudgeRequest) bool {
		return r.Candidate.StrategyType == "bring_in_technical_lead" && r.DraftIndex == 2
	})
	revisedBody(fw)
	out := mustRun(t, service(t, fw), sc.RunID)

	if len(fw.revises) != 1 || fw.revises[0].DraftIndex != 2 || len(fw.revises[0].Feedback) != 1 ||
		fw.revises[0].Feedback[0].Instruction != "Drop the signing ask and leave the timing to the buyer." {
		t.Fatalf("revisions = %+v", fw.revises)
	}
	// both versions are drafts: the original from the generator, the revision from the planner (index 3 + 2)
	rows := col(t, `SELECT draft_index::text || ':' || source || ':' || jsonb_array_length(revision_feedback)::text FROM agent_run_drafts
 WHERE agent_run_id = $1::uuid ORDER BY draft_index`, sc.RunID)
	want := []string{"1:strategy_generator:0", "2:strategy_generator:0", "3:strategy_generator:0", "5:revision_planner:1"}
	if len(rows) != len(want) {
		t.Fatalf("drafts = %v", rows)
	}
	for i := range want {
		if rows[i] != want[i] {
			t.Fatalf("drafts = %v, want %v", rows, want)
		}
	}
	if got := scalar(t, `SELECT draft_index::text FROM strategy_candidates WHERE agent_run_id = $1::uuid AND strategy_type = 'bring_in_technical_lead'`, sc.RunID); got != "5" {
		t.Fatalf("the set must hold the revised draft, got draft %s", got)
	}
	if blocked := count(t, `SELECT count(*) FROM strategy_candidates c JOIN eval_bundles b ON b.id = c.eval_bundle_id
 WHERE c.agent_run_id = $1::uuid AND jsonb_path_exists(b.items, '$[*] ? (@.result.blocking == true)')`, sc.RunID); blocked != 0 {
		t.Fatalf("%d candidates still block after the revision passed", blocked)
	}
	if superseded := count(t, `SELECT count(*) FROM eval_bundles WHERE agent_run_id = $1::uuid AND draft_index = 2
 AND jsonb_path_exists(items, '$[*] ? (@.result.blocking == true)')`, sc.RunID); superseded != 1 {
		t.Fatal("the superseded draft keeps its failing evals for the record")
	}
	if got := scalar(t, `SELECT rationale FROM strategy_candidates WHERE agent_run_id = $1::uuid AND strategy_type = 'bring_in_technical_lead'`, sc.RunID); got == "" || got[:7] != "Revised" {
		t.Fatalf("rationale = %q", got)
	}
	if out.PreferredID != scalar(t, `SELECT id::text FROM strategy_candidates WHERE agent_run_id = $1::uuid AND ranking = 1`, sc.RunID) {
		t.Fatal("outcome and rank 1 disagree")
	}
}

func TestACandidateStillBlockingAfterItsOneRevisionIsKeptButNeverPreferred(t *testing.T) {
	sc := newScene(t)
	fw := newFake(sc)
	// the worker's favourite blocks on every draft: its original (draft 1) and its revision (draft 4)
	blockWhen(fw, func(r workerclient.JudgeRequest) bool { return r.Candidate.StrategyType == "send_context_and_wait" })
	revisedBody(fw)
	mustRun(t, service(t, fw), sc.RunID)

	if len(fw.revises) != 1 {
		t.Fatalf("a candidate is revised at most once, got %d revisions", len(fw.revises))
	}
	got := candidateRows(t, sc.RunID)
	fav := got["send_context_and_wait"]
	if fav["rank"] != "3" || fav["preferred"] == "true" {
		t.Fatalf("a candidate still blocking must rank last and not be preferred: %v", fav)
	}
	if count(t, `SELECT count(*) FROM strategy_candidates c JOIN eval_bundles b ON b.id = c.eval_bundle_id WHERE c.agent_run_id = $1::uuid
 AND c.ranking = 1 AND jsonb_path_exists(b.items, '$[*] ? (@.result.blocking == true)')`, sc.RunID) != 0 {
		t.Fatal("the preferred candidate has a blocking failure while alternatives exist")
	}
	// the failure stays visible, and send refuses it
	read, _ := strategystore.New(env.DB, nil)
	if _, _, err := read.RecordDecision(bg, sc.RunID, strategystore.DecisionRequest{SelectedCandidateID: scalar(t,
		`SELECT id::text FROM strategy_candidates WHERE agent_run_id = $1::uuid AND strategy_type = 'send_context_and_wait'`, sc.RunID),
		Surface: "api", ActorLabel: "tester"}); err != nil {
		t.Fatal(err)
	}
	if _, err := read.Send(bg, sc.RunID, strategystore.SendRequest{Decision: "send", Surface: "api", ActorLabel: "tester"}); err == nil {
		t.Fatal("send must refuse a candidate with a failed blocking eval")
	}
}

func TestWhenAllThreeCandidatesBlockTheSetStillPublishesInTheWorkersOrder(t *testing.T) {
	sc := newScene(t)
	fw := newFake(sc)
	blockWhen(fw, func(workerclient.JudgeRequest) bool { return true })
	revisedBody(fw)
	mustRun(t, service(t, fw), sc.RunID)
	if len(fw.revises) != 3 || count(t, `SELECT count(*) FROM strategy_candidates WHERE agent_run_id = $1::uuid`, sc.RunID) != 3 {
		t.Fatalf("%d revisions", len(fw.revises))
	}
	if got := scalar(t, `SELECT strategy_type FROM strategy_candidates WHERE agent_run_id = $1::uuid AND ranking = 1`, sc.RunID); got != "send_context_and_wait" {
		t.Fatalf("with nothing to prefer, the worker's order stands; rank 1 = %s", got)
	}
}

func TestAnUnusableRevisionKeepsTheOriginalAndTheRunStillPublishes(t *testing.T) {
	sc := newScene(t)
	fw := newFake(sc)
	blockWhen(fw, func(r workerclient.JudgeRequest) bool { return r.Candidate.StrategyType == "bring_in_technical_lead" })
	fw.revise = func(req workerclient.ReviseRequest) (workerclient.Candidate, error) {
		c := req.Candidate
		c.StrategyType = "something_else" // a revision may not change the strategy
		return c, nil
	}
	mustRun(t, service(t, fw), sc.RunID)
	if got := scalar(t, `SELECT draft_index::text FROM strategy_candidates WHERE agent_run_id = $1::uuid AND strategy_type = 'bring_in_technical_lead'`, sc.RunID); got != "2" {
		t.Fatalf("the original draft must stay in the set, got draft %s", got)
	}
}

// ---- the replay clock and the as-of knowledge filter ----------------------------------------------

// matchingKnowledge is knowledge whose signature always holds (no stage regression signal is open).
func matchingKnowledge(id string, createdAt time.Time) knowledge.Knowledge {
	return knowledge.Knowledge{ID: id, Title: "Keep it simple " + id[:4], SituationSignature: []knowledge.Condition{{Field: "diff.stage_regressed", Op: "not_exists"}},
		Guidance:   knowledge.Guidance{Summary: "Answer what was asked", Do: []string{"be brief"}, Dont: []string{"press for a date"}},
		Exceptions: []knowledge.Exception{}, EvidenceClasses: []string{"methodology"}, Provenance: knowledge.Provenance{CreatedFrom: "human_delta"}, CreatedAt: createdAt}
}

func learn(t *testing.T, k knowledge.Knowledge, firstEvidence time.Time) {
	t.Helper()
	rules := config(t).Knowledge
	tx, err := env.DB.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := knowledgestore.InsertReplay(bg, tx, k, "test"); err != nil {
		t.Fatal(err)
	}
	for i, ev := range []knowledge.Evidence{
		{Kind: knowledge.EvidenceDecisionEpisode, RefID: newUUID(), At: firstEvidence},
		{Kind: knowledge.EvidenceCustomerReaction, RefID: newUUID(), Polarity: "positive", At: firstEvidence.Add(time.Hour)},
	} {
		if _, _, err := knowledgestore.RecordEvidence(bg, tx, k.ID, ev, rules); err != nil {
			t.Fatalf("evidence %d: %v", i, err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		tx, err := env.DB.Begin()
		if err != nil {
			t.Error(err)
			return
		}
		defer tx.Rollback()
		for _, q := range []string{`SET LOCAL ghost.purge_knowledge = 'on'`, `DELETE FROM knowledge_status_history WHERE knowledge_id = '` + k.ID + `'`,
			`DELETE FROM knowledge WHERE id = '` + k.ID + `'`} {
			if _, err := tx.Exec(q); err != nil {
				t.Error(err)
				return
			}
		}
		_ = tx.Commit()
	})
}

func TestGuidanceSeesTheKnowledgeOfTheReplayClockAndNothingLearnedLater(t *testing.T) {
	sc := newScene(t)
	known, future := newUUID(), newUUID()
	learn(t, matchingKnowledge(known, sc.EventTime.Add(-72*time.Hour)), sc.EventTime.Add(-48*time.Hour))
	// created after the event but long before the wall clock (2035): the wall clock would let it leak
	learn(t, matchingKnowledge(future, sc.EventTime.Add(24*time.Hour)), sc.EventTime.Add(25*time.Hour))

	fw := newFake(sc)
	mustRun(t, service(t, fw), sc.RunID)
	ids := col(t, `SELECT e ->> 'knowledge_id' FROM decision_guidance g, jsonb_array_elements(g.guidance -> 'supporting_knowledge') e
 WHERE g.agent_run_id = $1::uuid ORDER BY 1`, sc.RunID)
	if len(ids) != 1 || ids[0] != known {
		t.Fatalf("guidance considered %v, want only the knowledge that existed at the event", ids)
	}
	if got := scalar(t, `SELECT detail -> 'knowledge_attribution' -> 'retrieved' ->> 0 FROM agent_run_steps WHERE agent_run_id = $1::uuid AND step = 'build_context'`, sc.RunID); got != known {
		t.Fatalf("E7 retrieved = %q", got)
	}
	if applicable := scalar(t, `SELECT detail -> 'knowledge_attribution' -> 'applicable' ->> 0 FROM agent_run_steps WHERE agent_run_id = $1::uuid AND step = 'build_context'`, sc.RunID); applicable != known {
		t.Fatalf("E7 applicable = %q", applicable)
	}
	if got := scalar(t, `SELECT g.guidance -> 'not_recommended' -> 0 ->> 'action' FROM decision_guidance g WHERE g.agent_run_id = $1::uuid`, sc.RunID); got != "press for a date" {
		t.Fatalf("what the applying knowledge says not to do must reach the guidance, got %q", got)
	}
}

func TestEvaluationsAndGuidanceUseTheEventTimeNotTheWallClock(t *testing.T) {
	sc := newScene(t)
	mustRun(t, service(t, newFake(sc)), sc.RunID)
	var created time.Time
	if err := env.DB.QueryRow(`SELECT created_at FROM eval_runs WHERE agent_run_id = $1::uuid AND kind = 'deterministic' LIMIT 1`, sc.RunID).Scan(&created); err != nil {
		t.Fatal(err)
	}
	if !created.UTC().Equal(sc.EventTime) {
		t.Fatalf("deterministic evals were evaluated at %s, want the replay clock %s (the wall clock is %s)", created.UTC(), sc.EventTime, wallNow)
	}
	var generated time.Time
	if err := env.DB.QueryRow(`SELECT generated_at FROM strategy_sets WHERE agent_run_id = $1::uuid`, sc.RunID).Scan(&generated); err != nil {
		t.Fatal(err)
	}
	if !generated.UTC().Equal(wallNow) {
		t.Fatalf("generated_at is a wall-clock fact and must come from the injected clock, got %s", generated)
	}
}
