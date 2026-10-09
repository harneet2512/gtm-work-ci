package bucket2run_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/bucket2"
	"github.com/harneet2512/gtm-work/core-go/internal/bucket2run"
	"github.com/harneet2512/gtm-work/core-go/internal/clock"
	"github.com/harneet2512/gtm-work/core-go/internal/ctxfixture"
	"github.com/harneet2512/gtm-work/core-go/internal/recompute"
	"github.com/harneet2512/gtm-work/core-go/internal/store/storetest"
	"github.com/harneet2512/gtm-work/core-go/internal/strategystore"
	"github.com/harneet2512/gtm-work/core-go/internal/strategystore/strategytest"
	"github.com/harneet2512/gtm-work/core-go/internal/workerclient"
)

var env *storetest.Env

func TestMain(m *testing.M) { os.Exit(storetest.Main(m, func(e *storetest.Env) { env = e })) }

// scriptedJudge answers every judge call from the evidence ids it was offered: pass with the first id on every
// dimension. It is a replay stand-in for the model (no live call); the worker's own tests cover the model side.
type scriptedJudge struct {
	calls []string
	fail  map[string]bool
	// verdicts overrides one dimension's verdict, keyed "kind/dimension" (a double that can warn and fail).
	verdicts map[string]string
	// delay holds every call that long, then fails it if its context has expired (a slow model under a deadline).
	delay time.Duration
	// hang blocks the kinds listed until their own context ends (a model call that never answers).
	hang map[string]bool
	// usage, when set, is what the double reports for every call (a live or a replayed worker).
	usage *workerclient.Usage
}

var dimsByKind = map[string][]string{
	"intent_fit":        {"intent_follows_state", "timing", "quiet_move_considered"},
	"set_quality":       {"materially_different", "coverage", "no_dominated"},
	"candidate_quality": {"fit", "grounding", "cta"},
	"ranking":           {"supported_by_evidence_state", "no_blocked_preferred"},
	"final_artifact":    {"intent_preserved", "claims_grounded", "cta_timing_correct"},
}

func (s *scriptedJudge) DecisionJudge(ctx context.Context, req workerclient.DecisionJudgeRequest) (workerclient.DecisionJudgeResponse, error) {
	s.calls = append(s.calls, req.Kind)
	if s.hang[req.Kind] {
		<-ctx.Done()
		return workerclient.DecisionJudgeResponse{}, ctx.Err()
	}
	if s.delay > 0 {
		time.Sleep(s.delay)
		if err := ctx.Err(); err != nil {
			return workerclient.DecisionJudgeResponse{}, err
		}
	}
	if s.usage != nil {
		workerclient.ReportUsage(ctx, workerclient.UsageRecord{Stage: "judge", Usage: *s.usage})
	}
	if s.fail[req.Kind] {
		return workerclient.DecisionJudgeResponse{}, errors.New("judge down")
	}
	resp := workerclient.DecisionJudgeResponse{Kind: req.Kind, SubjectID: req.SubjectID, Model: "scripted", PromptVersion: "test:v1"}
	if req.Kind == "rank_rationale" {
		var p struct {
			Order []string `json:"order"`
		}
		_ = json.Unmarshal(req.Payload, &p)
		for i := 0; i+1 < len(p.Order); i++ {
			resp.PairwiseReasons = append(resp.PairwiseReasons, workerclient.PairReason{RankedHigherID: p.Order[i], RankedLowerID: p.Order[i+1],
				Reason: "fits the state better", EvidenceRefs: req.EvidenceIDs[:1], KnowledgeRefs: []string{}})
		}
		return resp, nil
	}
	for _, d := range dimsByKind[req.Kind] {
		verdict := "pass"
		if v, ok := s.verdicts[req.Kind+"/"+d]; ok {
			verdict = v
		}
		resp.Dimensions = append(resp.Dimensions, workerclient.DimensionVerdict{Dimension: d, Verdict: verdict, Why: "judged " + d, EvidenceRefs: req.EvidenceIDs[:1]})
	}
	if req.Kind == "intent_fit" {
		r, q := "ACT", true
		resp.RightReaction, resp.QuietMoveConsidered = &r, &q
	}
	return resp, nil
}

type scriptedInferrer struct{ activity string }

func (s scriptedInferrer) InferJudgment(context.Context, workerclient.InferJudgmentRequest) (workerclient.InferJudgmentResponse, error) {
	return workerclient.InferJudgmentResponse{
		InferredSemanticDelta: json.RawMessage(`{"statement":"The person softened the ask.","semantic_labels":["smaller_ask"],"edit_class":["cta"],"signal_strength":"moderate","explicit_instructions":[],"unknown":false}`),
		Evidence:              json.RawMessage(`{"candidate_differences":["softer ask"],"evidence_refs":[{"activity_id":"` + s.activity + `"}],"eval_differences":[],"knowledge_refs":[],"no_applicable_knowledge":true}`),
		Model:                 "scripted-infer"}, nil
}

type fixture struct {
	t      *testing.T
	runner *bucket2run.Runner
	seed   strategytest.Seeded
	judge  *scriptedJudge
	svc    *strategystore.Service
}

func setup(t *testing.T, judge *scriptedJudge) *fixture { return setupWith(t, judge) }

func setupWith(t *testing.T, judge *scriptedJudge, opts ...bucket2run.Option) *fixture {
	t.Helper()
	world := ctxfixture.Get(t, env.DB)
	seed := strategytest.Seed(t, env.DB, world.AccountA)
	svc, err := strategystore.New(env.DB, nil, strategystore.WithInferrer(scriptedInferrer{activity: seed.ActivityID}))
	if err != nil {
		t.Fatal(err)
	}
	rec, err := recompute.New(env.DB, clock.Real{})
	if err != nil {
		t.Fatal(err)
	}
	var j bucket2run.Judge
	if judge != nil {
		j = judge
	}
	runner, err := bucket2run.New(env.DB, svc, rec, j, opts...)
	if err != nil {
		t.Fatal(err)
	}
	f := &fixture{t: t, runner: runner, seed: seed, judge: judge, svc: svc}
	f.humanSteps()
	return f
}

// humanSteps plays the choice, the edit and the send on the fixture's store.
func (f *fixture) humanSteps() {
	t, svc, seed := f.t, f.svc, f.seed
	ctx := context.Background()
	if _, _, err := svc.RecordDecision(ctx, seed.RunID, strategystore.DecisionRequest{SelectedCandidateID: seed.Candidates[1], Surface: "slack", ActorLabel: "alex"}); err != nil {
		t.Fatal(err)
	}
	subject := "Changed at send time"
	edited := strategystore.Artifact{Channel: "email", Subject: &subject, Body: "Hi Marco,\n\nLet us know what timing suits.\n\nBest,\nDana"}
	if _, _, err := svc.RecordDecision(ctx, seed.RunID, strategystore.DecisionRequest{SelectedCandidateID: seed.Candidates[1], Surface: "slack", ActorLabel: "alex", FinalArtifact: &edited}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Send(ctx, seed.RunID, strategystore.SendRequest{Decision: "send", Surface: "slack", ActorLabel: "alex"}); err != nil {
		t.Fatal(err)
	}
}

func gates(rs []bucket2.Result) map[string][]bucket2.Result {
	m := map[string][]bucket2.Result{}
	for _, r := range rs {
		m[r.Gate] = append(m[r.Gate], r)
	}
	return m
}

func TestRunProducesEveryGateWithEvidenceOnEveryPass(t *testing.T) {
	f := setup(t, &scriptedJudge{})
	results, err := f.runner.Run(context.Background(), f.seed.RunID)
	if err != nil {
		t.Fatal(err)
	}
	by := gates(results)
	for _, g := range bucket2.Gates {
		if len(by[g]) == 0 {
			t.Errorf("gate %s produced no result", g)
		}
	}
	for _, r := range results {
		if r.Verdict != bucket2.Unknown && len(r.EvidenceRefs) == 0 {
			t.Errorf("%s/%s says %s with no evidence", r.Gate, r.SubGate, r.Verdict)
		}
		if r.Calibrated || r.Question == "" || r.Improves == "" || r.SpanID == "" || r.JudgedID == "" {
			t.Errorf("%s/%s is incomplete: %+v", r.Gate, r.SubGate, r)
		}
		if err := r.Validate(); err != nil {
			t.Error(err)
		}
	}
	if got := len(f.judge.calls); got != 8 {
		t.Errorf("model judge calls = %d (%v), want 8", got, f.judge.calls)
	}
	var d5 bucket2.Result
	for _, r := range by["D5"] {
		d5 = r
	}
	if d5.Grader.Kind != "model" || !strings.Contains(d5.Observed, "cta") {
		t.Errorf("D5 must come from the real inference: %+v", d5)
	}
}

func TestRankingIsPersistedWithAReasonPerAdjacentPair(t *testing.T) {
	f := setup(t, &scriptedJudge{})
	if _, err := f.runner.Run(context.Background(), f.seed.RunID); err != nil {
		t.Fatal(err)
	}
	var n, reasons int
	if err := env.DB.QueryRow(`SELECT count(*), COALESCE(max(jsonb_array_length(pairwise_reasons)), 0) FROM decision_rankings WHERE strategy_set_id = $1::uuid`, f.seed.SetID).Scan(&n, &reasons); err != nil {
		t.Fatal(err)
	}
	if n != 1 || reasons != 2 {
		t.Fatalf("rankings = %d, reasons = %d, want 1 and 2", n, reasons)
	}
	if _, err := f.runner.Run(context.Background(), f.seed.RunID); err != nil { // idempotent
		t.Fatal(err)
	}
}

func TestResultsAreStoredAndReloaded(t *testing.T) {
	f := setup(t, &scriptedJudge{})
	results, err := f.runner.Run(context.Background(), f.seed.RunID)
	if err != nil {
		t.Fatal(err)
	}
	stored, err := bucket2.Load(context.Background(), env.DB, f.seed.EpisodeID)
	if err != nil {
		t.Fatal(err)
	}
	if len(stored) != len(results) {
		t.Fatalf("stored %d, ran %d", len(stored), len(results))
	}
	if stored[0].Gate != "D1" {
		t.Fatalf("results must load in flow order, first = %s", stored[0].Gate)
	}
}

func TestAJudgeFailureLeavesTheGateNotMeasuredAndIsReported(t *testing.T) {
	f := setup(t, &scriptedJudge{fail: map[string]bool{"intent_fit": true}})
	results, err := f.runner.Run(context.Background(), f.seed.RunID)
	if err == nil || !strings.Contains(err.Error(), "intent_fit") {
		t.Fatalf("the failure must be reported, got %v", err)
	}
	for _, r := range gates(results)["D1"] {
		if r.SubGate == "" {
			t.Fatal("a failed intent judge must not produce the D1 intent result")
		}
	}
	if len(gates(results)["D4"]) == 0 {
		t.Fatal("deterministic gates still run")
	}
}

func TestWithoutAJudgeOnlyDeterministicGatesAreMeasured(t *testing.T) {
	f := setup(t, nil)
	results, err := f.runner.Run(context.Background(), f.seed.RunID)
	if err != nil {
		t.Fatal(err)
	}
	by := gates(results)
	for _, g := range []string{"D1", "D2", "D3"} {
		if len(by[g]) != 0 {
			t.Errorf("%s was reported without a judge", g)
		}
	}
	for _, g := range []string{"D4", "D5", "D6", "D7", "D8", "D9", "D10"} {
		if len(by[g]) == 0 {
			t.Errorf("%s was not measured", g)
		}
	}
}

func TestKnowledgeUseIsReportedAndNotMeasuredWhenNothingWasApplicable(t *testing.T) {
	f := setup(t, &scriptedJudge{})
	results, err := f.runner.Run(context.Background(), f.seed.RunID)
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, r := range results {
		if r.Gate == "D1" && r.SubGate == "knowledge_use" {
			found = true
			if r.Verdict == bucket2.Pass {
				t.Fatalf("no applicable knowledge must never read as a pass: %+v", r)
			}
			if !strings.Contains(r.Observed, "retrieved") || strings.Contains(strings.ToLower(r.Observed), "influence") {
				t.Fatalf("observed = %q", r.Observed)
			}
		}
	}
	if !found {
		t.Fatal("D1 knowledge_use result missing")
	}
}

func TestUnresolvableAndPlaceholderRefsNeverCountAsEvidence(t *testing.T) {
	f := setup(t, nil)
	real := "candidate:" + f.seed.Candidates[0]
	var stateRef string
	if err := env.DB.QueryRow(`SELECT state_refs[1] FROM strategy_candidates WHERE id = $1::uuid`, f.seed.Candidates[0]).Scan(&stateRef); err != nil || stateRef == "" {
		t.Fatalf("the seeded candidate cites no state field: %q %v", stateRef, err)
	}
	refs := []string{real, "candidate:00000000-0000-4000-8000-00000000dead", "activity:todo", "knowledge:", "nonsense", "mystery:00000000-0000-4000-8000-000000000001",
		"state:" + stateRef, "state:invented_field", "artifact_field:body", "artifact_field:made_up", "executed_action:RUN:execute", "ranking_rationale:not-an-id"}
	got, err := bucket2.ResolveRefs(context.Background(), env.DB, refs)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[0] != real || got[1] != "state:"+stateRef || got[2] != "artifact_field:body" {
		t.Fatalf("resolved = %v", got)
	}
	// A pass that rests only on a made-up id is stored as unknown (rule R1 with resolution).
	r := bucket2.Result{Gate: "D4", JudgedType: "DecisionEpisode", JudgedID: f.seed.EpisodeID, SpanID: "human_interaction:x", Verdict: bucket2.Pass,
		Observed: "o", Why: "w", Grader: bucket2.Deterministic, EvidenceRefs: []string{"candidate:00000000-0000-4000-8000-00000000dead"}}
	if err := bucket2.Save(context.Background(), env.DB, f.seed.EpisodeID, []bucket2.Result{r}); err != nil {
		t.Fatal(err)
	}
	stored, err := bucket2.Load(context.Background(), env.DB, f.seed.EpisodeID)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range stored {
		if s.JudgedType == "DecisionEpisode" && s.Gate == "D4" && (s.Verdict != bucket2.Unknown || len(s.EvidenceRefs) != 0) {
			t.Fatalf("a pass on a made-up ref must be unknown with no evidence: %+v", s)
		}
	}
}

func TestNewRequiresItsDependencies(t *testing.T) {
	if _, err := bucket2run.New(nil, nil, nil, nil); err == nil {
		t.Fatal("missing dependencies must be an error")
	}
}

// build seeds an awaiting-choice episode and gives the store a gate runner, without playing any human step.
func build(t *testing.T, judge *scriptedJudge) *fixture {
	t.Helper()
	world := ctxfixture.Get(t, env.DB)
	seed := strategytest.Seed(t, env.DB, world.AccountA)
	svc, err := strategystore.New(env.DB, nil, strategystore.WithInferrer(scriptedInferrer{activity: seed.ActivityID}))
	if err != nil {
		t.Fatal(err)
	}
	rec, err := recompute.New(env.DB, clock.Real{})
	if err != nil {
		t.Fatal(err)
	}
	runner, err := bucket2run.New(env.DB, svc, rec, judge)
	if err != nil {
		t.Fatal(err)
	}
	svc.SetGates(runner)
	return &fixture{t: t, runner: runner, seed: seed, judge: judge, svc: svc}
}

func storedByGate(t *testing.T, episode string) map[string][]bucket2.Stored {
	t.Helper()
	rows, err := bucket2.Load(context.Background(), env.DB, episode)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string][]bucket2.Stored{}
	for _, r := range rows {
		out[r.Gate] = append(out[r.Gate], r)
	}
	return out
}

func TestPlayPathMeasuresD1ToD3AtPublishBeforeAnyChoice(t *testing.T) {
	f := build(t, &scriptedJudge{})
	// Play: the orchestrator's after-publish hook calls RunGatesAsync; no human step has happened yet.
	f.svc.RunGatesAsync(context.Background(), f.seed.RunID, "publish")
	f.svc.WaitGates()
	by := storedByGate(t, f.seed.EpisodeID)
	for _, g := range []string{"D1", "D2", "D3"} {
		if len(by[g]) == 0 {
			t.Errorf("%s was not measured at publish", g)
		}
	}
	for _, g := range []string{"D4", "D5", "D6", "D7", "D8", "D9", "D10"} {
		if len(by[g]) != 0 {
			t.Errorf("%s has a result before the person acted: %+v", g, by[g])
		}
	}
	if got := len(f.judge.calls); got != 7 {
		t.Errorf("publish made %d judge calls (%v), want 7", got, f.judge.calls)
	}
}

func TestWholePathStaysWithinTheModelCallBudgetAndNeverRejudgesTheSet(t *testing.T) {
	f := build(t, &scriptedJudge{})
	ctx := context.Background()
	f.svc.RunGatesAsync(ctx, f.seed.RunID, "publish")
	f.svc.WaitGates()
	f.humanSteps() // choice, edit and send each start a gate run through the store's hooks
	f.svc.WaitGates()
	if got := len(f.judge.calls); got > 10 {
		t.Fatalf("%d judge calls for one episode (%v), the budget is 10", got, f.judge.calls)
	}
	counts := map[string]int{}
	for _, k := range f.judge.calls {
		counts[k]++
	}
	for _, k := range []string{"intent_fit", "set_quality", "rank_rationale", "ranking"} {
		if counts[k] != 1 {
			t.Errorf("%s was asked %d times, want once per strategy set", k, counts[k])
		}
	}
	if counts["candidate_quality"] != 3 {
		t.Errorf("candidate_quality asked %d times, want 3 (the unedited intent is not re-judged)", counts["candidate_quality"])
	}
	by := storedByGate(t, f.seed.EpisodeID)
	for _, g := range bucket2.Gates {
		if len(by[g]) == 0 {
			t.Errorf("%s has no stored result after the whole path", g)
		}
	}
}

func TestJudgeWarnAndFailVerdictsSurviveToStorage(t *testing.T) {
	f := build(t, &scriptedJudge{verdicts: map[string]string{
		"candidate_quality/cta": "warn", "ranking/no_blocked_preferred": "fail", "set_quality/coverage": "unknown"}})
	f.svc.RunGatesAsync(context.Background(), f.seed.RunID, "publish")
	f.svc.WaitGates()
	by := storedByGate(t, f.seed.EpisodeID)
	var warn, fail, unknown int
	for _, r := range by["D2"] {
		switch r.Verdict {
		case bucket2.Warn:
			warn++
		case bucket2.Unknown:
			unknown++
		}
	}
	for _, r := range by["D3"] {
		if r.SubGate == "ranking" && r.Verdict == bucket2.Fail {
			fail++
		}
	}
	if warn != 3 || fail != 1 || unknown != 1 {
		t.Fatalf("D2 warns = %d (want 3), D3 ranking fails = %d (want 1), D2 set unknown = %d (want 1)", warn, fail, unknown)
	}
	for _, r := range by["D1"] {
		if r.SubGate == "" && r.Verdict != bucket2.Pass {
			t.Fatalf("D1 intent = %s, want pass", r.Verdict)
		}
	}
}

// The record run lost every gate after the first slow judges: one expired deadline was shared by all the judge calls and then by
// the database reads and writes that followed ("context deadline exceeded" on the stored ranking, the load, the save).
func TestAnExpiredGateDeadlineDoesNotFailLaterJudgesOrTheDatabase(t *testing.T) {
	f := setup(t, &scriptedJudge{delay: 40 * time.Millisecond})
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Millisecond)
	defer cancel()
	results, err := f.runner.Run(ctx, f.seed.RunID)
	if err != nil {
		t.Fatalf("a deadline on the caller's context must not fail the gates: %v", err)
	}
	if len(gates(results)["D1"]) == 0 || len(storedByGate(t, f.seed.EpisodeID)["D1"]) == 0 {
		t.Fatal("the gates must be measured and persisted")
	}
}

func TestEachJudgeCallHasItsOwnDeadlineAndAHungOneOnlyLosesItself(t *testing.T) {
	judge := &scriptedJudge{hang: map[string]bool{"intent_fit": true}}
	f := setupWith(t, judge, bucket2run.WithJudgeBudget(50*time.Millisecond))
	results, err := f.runner.Run(context.Background(), f.seed.RunID)
	if err == nil || !strings.Contains(err.Error(), "intent_fit") || !strings.Contains(err.Error(), "deadline") {
		t.Fatalf("the hung call must be reported as a deadline, got %v", err)
	}
	if strings.Contains(err.Error(), "stored ranking") || strings.Contains(err.Error(), "set_quality") {
		t.Fatalf("one hung judge must not take the later reads and judges with it: %v", err)
	}
	if len(gates(results)["D4"]) == 0 || len(gates(results)["D2"]) == 0 {
		t.Fatal("the other gates are still measured")
	}
}
