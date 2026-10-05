package orchestrator_test

import (
	"encoding/json"
	"errors"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/orchestrator"
	"github.com/harneet2512/gtm-work/core-go/internal/strategystore"
	"github.com/harneet2512/gtm-work/core-go/internal/transitions"
	"github.com/harneet2512/gtm-work/core-go/internal/workerclient"
)

// Opus review of PR #45: the label must not bypass the CANDIDATE policy, E7, malformed judge output, the
// one-revision bound across resume, replay-time evidence, "no acceptable candidate" and routing by action.

const rolloutCTA = "Great to hear it. Can we plan the rollout to your other offices this quarter?"

// mislabelFirst makes the favourite a REPLY whose text is an expansion CTA: the label says nothing about it.
func mislabelFirst(f *fakeWorker) {
	f.build = func(int) []workerclient.Candidate {
		cs := f.threeCandidates()
		cs[0] = f.candidate(1, "plan_the_rollout", "REPLY", "send_email", []string{f.sc.Contact1}, nil, rolloutCTA)
		return cs
	}
}

// keepRevision answers a revision request with the candidate unchanged: the tests below are about the policy, not the reviser.
func keepRevision(f *fakeWorker) {
	f.revise = func(req workerclient.ReviseRequest) (workerclient.Candidate, error) { return req.Candidate, nil }
}

func TestAMislabelledReplyCarryingAnExpansionCTAIsRestrictedAndNeverPreferred(t *testing.T) {
	sc := newScene(t)
	withTransition(t, sc, transitions.StatusCandidate)
	fw := newFake(sc)
	mislabelFirst(fw)
	out := mustRun(t, service(t, fw), sc.RunID)

	row := candidateRows(t, sc.RunID)["plan_the_rollout"]
	if row["policy"] != "restricted" || row["preferred"] == "true" || row["rank"] == "1" {
		t.Fatalf("a REPLY with an expansion CTA must be restricted and never preferred: %v", row)
	}
	var policy map[string]any
	if err := json.Unmarshal([]byte(row["policy_doc"]), &policy); err != nil {
		t.Fatal(err)
	}
	reasons := toStrings(policy["reasons"])
	if !slices.Contains(reasons, "expansion_motion") || !slices.Contains(reasons, "expansion_label_mismatch") || policy["requires_human_review"] != true {
		t.Fatalf("the label/text mismatch must be in the verdict reasons: %v", policy)
	}
	if scalar(t, `SELECT strategy_type FROM strategy_candidates WHERE id = $1::uuid`, out.PreferredID) == "plan_the_rollout" {
		t.Fatal("the mislabelled expansion is the preferred candidate")
	}
	if scalar(t, `SELECT no_acceptable_candidate::text FROM strategy_sets WHERE agent_run_id = $1::uuid`, sc.RunID) != "false" {
		t.Fatal("two allowed candidates remain, so the set has an acceptable one")
	}
}

func TestUnderACandidateTransitionASetWithNoAllowedCandidateIsRegeneratedOnce(t *testing.T) {
	sc := newScene(t)
	withTransition(t, sc, transitions.StatusCandidate)
	fw := newFake(sc)
	keepRevision(fw)
	allExpansion := func() []workerclient.Candidate {
		cs := fw.threeCandidates()
		cs[0] = fw.candidate(1, "plan_the_rollout", "REPLY", "send_email", []string{sc.Contact1}, nil, rolloutCTA)
		cs[1] = fw.candidate(2, "upgrade_the_plan", "MEETING", "schedule_meeting", []string{sc.Contact1, sc.Contact3}, nil, "Could we meet to talk through an upgrade to the enterprise plan?")
		cs[2] = fw.candidate(3, "extra_seats", "SHARE_DOCUMENT", "share_document", []string{sc.Contact2}, nil, "Attached is a one-pager on extra seats for your new hires.")
		return cs
	}
	fw.build = func(call int) []workerclient.Candidate {
		if call == 1 {
			return allExpansion()
		}
		return fw.threeCandidates()
	}
	out := mustRun(t, service(t, fw), sc.RunID)
	if fw.strategies != 2 {
		t.Fatalf("a set with no allowed candidate is regenerated once, got %d generations", fw.strategies)
	}
	if got := scalar(t, `SELECT count(*) FROM strategy_candidates WHERE agent_run_id = $1::uuid AND strategy_type = 'plan_the_rollout'`, sc.RunID); got != "0" {
		t.Fatal("the rejected first set must not be published")
	}
	if scalar(t, `SELECT no_acceptable_candidate::text FROM strategy_sets WHERE agent_run_id = $1::uuid`, sc.RunID) != "false" || out.PreferredID == "" {
		t.Fatal("the regenerated set has an acceptable candidate")
	}
}

func TestIfTheRegeneratedSetStillHasNoAllowedCandidateTheSetIsMarkedNoAcceptableCandidate(t *testing.T) {
	sc := newScene(t)
	withTransition(t, sc, transitions.StatusCandidate)
	fw := newFake(sc)
	keepRevision(fw)
	var failed atomic.Bool
	fw.judgeErr = func(workerclient.JudgeRequest) error {
		if failed.CompareAndSwap(false, true) {
			return &workerclient.Error{Status: 503, Code: "unavailable", Message: "overloaded", Retryable: true}
		}
		return nil
	}
	fw.build = func(int) []workerclient.Candidate {
		cs := fw.threeCandidates()
		cs[0] = fw.candidate(1, "plan_the_rollout", "REPLY", "send_email", []string{sc.Contact1}, nil, rolloutCTA)
		cs[1] = fw.candidate(2, "upgrade_the_plan", "MEETING", "schedule_meeting", []string{sc.Contact1, sc.Contact3}, nil, "Could we meet to talk through an upgrade to the enterprise plan?")
		cs[2] = fw.candidate(3, "extra_seats", "SHARE_DOCUMENT", "share_document", []string{sc.Contact2}, nil, "Attached is a one-pager on extra seats for your new hires.")
		return cs
	}
	svc := service(t, fw)
	if _, err := svc.Run(bg, sc.RunID); !orchestrator.IsTransient(err) {
		t.Fatalf("first run = %v", err)
	}
	out, err := svc.Resume(bg, sc.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if fw.strategies != 2 {
		t.Fatalf("regenerate exactly once, and never again on resume: %d generations", fw.strategies)
	}
	if scalar(t, `SELECT no_acceptable_candidate::text FROM strategy_sets WHERE agent_run_id = $1::uuid`, sc.RunID) != "true" || out.PreferredID != "" {
		t.Fatalf("every candidate is restricted: the set is flagged and nothing is preferred (preferred %q)", out.PreferredID)
	}
	if n := count(t, `SELECT count(*) FROM strategy_candidates WHERE agent_run_id = $1::uuid`, sc.RunID); n != 3 {
		t.Fatalf("the set still shows its %d candidates", n)
	}
	read, _ := strategystore.New(env.DB, nil)
	doc, err := read.Strategies(bg, sc.RunID)
	if err != nil {
		t.Fatal(err)
	}
	var served struct {
		Set json.RawMessage `json:"strategy_set"`
	}
	if err := json.Unmarshal(doc, &served); err != nil {
		t.Fatal(err)
	}
	validDoc(t, "strategy_set", served.Set)
	if decodeMap(t, served.Set)["no_acceptable_candidate"] != true {
		t.Fatal("the read model must carry the flag")
	}
}

func TestWhenEveryCandidateIsBlockedNothingIsPreferred(t *testing.T) {
	sc := newScene(t)
	fw := newFake(sc)
	blockWhen(fw, func(workerclient.JudgeRequest) bool { return true })
	revisedBody(fw)
	out := mustRun(t, service(t, fw), sc.RunID)
	if scalar(t, `SELECT no_acceptable_candidate::text FROM strategy_sets WHERE agent_run_id = $1::uuid`, sc.RunID) != "true" || out.PreferredID != "" {
		t.Fatalf("all blocked: flagged and no preferred candidate, got %q", out.PreferredID)
	}
}

// ---- malformed judge output ----------------------------------------------------------------------------

func malformedJudge(req workerclient.JudgeRequest) []workerclient.BundleItem {
	bad := semantic(req, "buyer_readiness", "pass", false, "")
	bad.Result = json.RawMessage("null") // a verdict with no usable result
	return []workerclient.BundleItem{bad, notRelevant("channel_appropriateness")}
}

func TestMalformedJudgeOutputIsRetriedOnceThenFailsTheRunPermanently(t *testing.T) {
	sc := newScene(t)
	fw := newFake(sc)
	fw.judgeItem = malformedJudge
	svc := service(t, fw)
	_, err := svc.Run(bg, sc.RunID)
	if !orchestrator.IsPermanent(err) {
		t.Fatalf("run = %v, want a permanent failure", err)
	}
	if len(fw.judges) != 6 {
		t.Fatalf("each of the 3 candidates is judged twice (one retry), got %d judge calls", len(fw.judges))
	}
	if scalar(t, `SELECT status FROM agent_runs WHERE id = $1::uuid`, sc.RunID) != "failed" || openRun(t, sc.AccountID) {
		t.Fatal("the run must be failed and the account free, not held open forever")
	}
	if kind, phase := stepError(t, sc.RunID); kind != "permanent" || phase != "judge" {
		t.Fatalf("recorded failure = %s in %s", kind, phase)
	}
	if _, err := svc.Run(bg, sc.RunID); !errors.Is(err, orchestrator.ErrNotRunnable) {
		t.Fatalf("a failed run is final, got %v", err)
	}
}

func TestAMalformedJudgeAnswerFollowedByAValidOneIsAccepted(t *testing.T) {
	sc := newScene(t)
	fw := newFake(sc)
	var calls atomic.Int32
	fw.judgeItem = func(req workerclient.JudgeRequest) []workerclient.BundleItem {
		if calls.Add(1) == 1 {
			return malformedJudge(req)
		}
		return []workerclient.BundleItem{semantic(req, "buyer_readiness", "pass", false, ""), notRelevant("channel_appropriateness")}
	}
	out := mustRun(t, service(t, fw), sc.RunID)
	if !out.Published || len(fw.judges) != 4 {
		t.Fatalf("published %v after %d judge calls (3 candidates and one retry)", out.Published, len(fw.judges))
	}
}

// ---- the one-revision bound across resume --------------------------------------------------------------

func TestResumeNeverJudgesOrRevisesAgainWhatWasAlreadyEvaluated(t *testing.T) {
	sc := newScene(t)
	fw := newFake(sc)
	blockWhen(fw, func(r workerclient.JudgeRequest) bool {
		return r.Candidate.StrategyType == "bring_in_technical_lead" && r.DraftIndex == 2
	})
	revisedBody(fw)
	var failed atomic.Bool
	fw.judgeErr = func(req workerclient.JudgeRequest) error {
		// the third candidate's judge fails once, after the others may already have finished
		if req.Candidate.StrategyType == "ask_internal_owner" && failed.CompareAndSwap(false, true) {
			return &workerclient.Error{Status: 503, Code: "unavailable", Message: "overloaded", Retryable: true}
		}
		return nil
	}
	svc := service(t, fw)
	if _, err := svc.Run(bg, sc.RunID); !orchestrator.IsTransient(err) {
		t.Fatalf("first run = %v", err)
	}
	judgedBefore, revisedBefore := len(fw.judges), len(fw.revises)
	if revisedBefore != 1 {
		t.Fatalf("the blocking candidate is revised once, got %d", revisedBefore)
	}
	out, err := svc.Resume(bg, sc.RunID)
	if err != nil || !out.Published {
		t.Fatalf("resume = %+v, %v", out, err)
	}
	if len(fw.revises) != revisedBefore {
		t.Fatalf("resume revised again: %d revisions", len(fw.revises))
	}
	after := fw.judges[judgedBefore:]
	for _, j := range after {
		if j.Candidate.StrategyType != "ask_internal_owner" {
			t.Fatalf("resume judged %s draft %d again", j.Candidate.StrategyType, j.DraftIndex)
		}
	}
	if got := scalar(t, `SELECT draft_index::text FROM strategy_candidates WHERE agent_run_id = $1::uuid AND strategy_type = 'bring_in_technical_lead'`, sc.RunID); got != "5" {
		t.Fatalf("the set must hold the revised draft saved before the failure, got draft %s", got)
	}
	if n := count(t, `SELECT count(*) FROM agent_run_drafts WHERE agent_run_id = $1::uuid`, sc.RunID); n != 4 {
		t.Fatalf("%d drafts: three originals and one revision", n)
	}
}

// ---- cited evidence is bounded by the replay clock -----------------------------------------------------

// activityAfterTheClock moves another activity of the account to an hour after the run's replay clock (restored when
// the test ends) and returns it: the account's own evidence from the future.
func activityAfterTheClock(t *testing.T, sc scene) string {
	t.Helper()
	// Every activity at the run's newest instant is a trigger (ADR-0019): moving one of those would move the
	// replay clock with it, leaving nothing from the future to reject. The future evidence must therefore be an
	// activity the run does not trigger on.
	other := scalar(t, `SELECT id::text FROM activities WHERE account_id = $1::uuid AND id <> $2::uuid
 AND id NOT IN (SELECT unnest(trigger_activity_ids) FROM agent_runs WHERE id = $3::uuid)
 ORDER BY occurred_at DESC, id DESC LIMIT 1`, sc.AccountID, sc.Activity, sc.RunID)
	if other == "" {
		t.Fatal("the account needs a second activity")
	}
	var was time.Time
	if err := env.DB.QueryRow(`SELECT occurred_at FROM activities WHERE id = $1::uuid`, other).Scan(&was); err != nil {
		t.Fatal(err)
	}
	if _, err := env.DB.Exec(`UPDATE activities SET occurred_at = $2 WHERE id = $1::uuid`, other, sc.EventTime.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := env.DB.Exec(`UPDATE activities SET occurred_at = $2 WHERE id = $1::uuid`, other, was); err != nil {
			t.Error(err)
		}
	})
	return other
}

func TestEvidenceFromAfterTheReplayClockIsRejected(t *testing.T) {
	sc := newScene(t)
	later := activityAfterTheClock(t, sc)
	fw := newFake(sc)
	fw.build = func(int) []workerclient.Candidate {
		cs := fw.threeCandidates()
		cs[1].EvidenceRefs = append(cs[1].EvidenceRefs, workerclient.EvidenceRef{ActivityID: later})
		return cs
	}
	_, err := service(t, fw).Run(bg, sc.RunID)
	if !orchestrator.IsPermanent(err) || fw.strategies != 2 {
		t.Fatalf("evidence from the future is invalid output (retried once, then permanent): %v after %d generations", err, fw.strategies)
	}
	if count(t, `SELECT count(*) FROM strategy_candidates WHERE agent_run_id = $1::uuid`, sc.RunID) != 0 {
		t.Fatal("nothing citing the future may be stored")
	}
}

func TestEvidenceAtOrBeforeTheReplayClockIsAccepted(t *testing.T) {
	sc := newScene(t)
	out := mustRun(t, service(t, newFake(sc)), sc.RunID)
	if !out.Published {
		t.Fatal("the trigger activity itself is evidence at the replay clock")
	}
}

// ---- routing by action, the decision class and guidance rows -------------------------------------------

func TestTheEvalSuiteIsSelectedPerCandidateByTransitionStateAndAction(t *testing.T) {
	sc := newScene(t)
	withTransitionRoute(t, sc, transitions.StatusCandidate, "EXPANSION", "REORG") // base route: conservative
	fw := newFake(sc)
	mislabelFirst(fw)
	mustRun(t, service(t, fw), sc.RunID)

	got := candidateRows(t, sc.RunID)
	for typ, want := range map[string]string{"plan_the_rollout": "expansion_candidate", "bring_in_technical_lead": "conservative", "ask_internal_owner": "conservative"} {
		if got[typ]["suite"] != want {
			t.Errorf("%s bundle suite = %q, want %q", typ, got[typ]["suite"], want)
		}
		if col := scalar(t, `SELECT coalesce(selected_eval_suite, '') FROM strategy_candidates WHERE agent_run_id = $1::uuid AND strategy_type = $2`, sc.RunID, typ); col != want {
			t.Errorf("%s candidate suite = %q, want %q (recorded on the candidate as well as its bundle)", typ, col, want)
		}
	}
	for _, j := range fw.judges {
		want := "conservative"
		if j.Candidate.StrategyType == "plan_the_rollout" {
			want = "expansion_candidate"
		}
		if j.EvalSuite == nil || j.EvalSuite.Name != want {
			t.Errorf("%s was judged under %+v, want %s", j.Candidate.StrategyType, j.EvalSuite, want)
		}
	}
	// the episode keeps the run-level route (transition status + state)
	if scalar(t, `SELECT selected_eval_suite FROM decision_episodes WHERE agent_run_id = $1::uuid`, sc.RunID) != "conservative" {
		t.Fatal("the episode records the transition's own suite")
	}
}

func TestTheDecisionClassSurvivesTheMappingToAnExecutableAction(t *testing.T) {
	sc := newScene(t)
	fw := newFake(sc)
	fw.build = func(int) []workerclient.Candidate {
		cs := fw.threeCandidates()
		cs[2] = fw.candidate(3, "update_the_crm", "CRM_UPDATE", "internal_note", []string{sc.Rep}, nil, "Record in the CRM that the customer asked for the integration details.")
		return cs
	}
	mustRun(t, service(t, fw), sc.RunID)
	got := col(t, `SELECT decision ->> 'action' || '/' || coalesce(decision ->> 'action_class', '') FROM agent_run_drafts
 WHERE agent_run_id = $1::uuid ORDER BY draft_index`, sc.RunID)
	if !slices.Equal(got, []string{"send_email/REPLY", "schedule_meeting/MEETING", "internal_note/CRM_UPDATE"}) {
		t.Fatalf("draft decisions = %v: CRM_UPDATE maps onto internal_note but the class must stay on the draft", got)
	}
}

func TestAMissingBuildContextStepFailsTheRunInsteadOfBeingIgnored(t *testing.T) {
	sc := newScene(t)
	if _, err := env.DB.Exec(`DELETE FROM agent_run_steps WHERE agent_run_id = $1::uuid AND step = 'build_context'`, sc.RunID); err != nil {
		t.Fatal(err)
	}
	fw := newFake(sc)
	if _, err := service(t, fw).Run(bg, sc.RunID); !orchestrator.IsPermanent(err) {
		t.Fatalf("run = %v, want a permanent failure: guidance cannot be recorded", err)
	}
	if fw.strategies != 0 {
		t.Fatal("nothing may be generated for a run whose context was not recorded")
	}
}
