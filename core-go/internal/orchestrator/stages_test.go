package orchestrator_test

import (
	"encoding/json"
	"sync/atomic"
	"testing"

	"github.com/harneet2512/gtm-work/core-go/internal/orchestrator"
	"github.com/harneet2512/gtm-work/core-go/internal/schemacheck"
	"github.com/harneet2512/gtm-work/core-go/internal/stageevents"
	"github.com/harneet2512/gtm-work/core-go/internal/workerclient"
)

// stagedService is the orchestrator with stage recording on (HAR-145).
func stagedService(t *testing.T, w orchestrator.Worker) (*orchestrator.Service, *stageevents.Reader) {
	t.Helper()
	svc := service(t, w)
	rec, err := stageevents.NewRecorder(env.DB, nil)
	if err != nil {
		t.Fatal(err)
	}
	reader, err := stageevents.NewReader(env.DB, nil)
	if err != nil {
		t.Fatal(err)
	}
	svc.SetStages(rec)
	return svc, reader
}

func runProgress(t *testing.T, r *stageevents.Reader, runID string) stageevents.Progress {
	t.Helper()
	p, err := r.Run(bg, runID)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(p)
	v, err := schemacheck.New()
	if err != nil {
		t.Fatal(err)
	}
	if err := v.Validate("pipeline_progress", raw); err != nil {
		t.Fatalf("progress violates its schema: %v\n%s", err, raw)
	}
	return p
}

func stageOf(p stageevents.Progress, s stageevents.Stage) stageevents.StageDoc {
	for _, d := range p.Stages {
		if d.Stage == s {
			return d
		}
	}
	return stageevents.StageDoc{}
}

func TestARunRecordsItsDecideAndEvalsStagesWithTheRowsItProduced(t *testing.T) {
	sc := newScene(t)
	svc, reader := stagedService(t, newFake(sc))
	if p := runProgress(t, reader, sc.RunID); stageOf(p, stageevents.Decide).Status != stageevents.Waiting {
		t.Fatalf("before the run: decide = %s, want waiting", stageOf(p, stageevents.Decide).Status)
	}
	out := mustRun(t, svc, sc.RunID)

	p := runProgress(t, reader, sc.RunID)
	decide, evals := stageOf(p, stageevents.Decide), stageOf(p, stageevents.Evals)
	if decide.Status != stageevents.Completed || decide.Refs.RunID != sc.RunID || decide.Refs.StrategySetID != out.SetID || decide.Refs.DecisionEpisodeID != out.EpisodeID {
		t.Fatalf("decide = %+v, want completed with the run, set %s and episode %s", decide, out.SetID, out.EpisodeID)
	}
	outbox := col(t, `SELECT id::text FROM outbox_events WHERE agent_run_id = $1::uuid AND topic = 'strategy_set.published'`, sc.RunID)
	if len(outbox) != 1 || len(decide.Refs.OutboxEventIDs) != 1 || outbox[0] != itoa(decide.Refs.OutboxEventIDs[0]) {
		t.Errorf("decide outbox refs = %v, want %v: the event Slack reacts to", decide.Refs.OutboxEventIDs, outbox)
	}
	if evals.Status != stageevents.Passed && evals.Status != stageevents.Warning {
		t.Fatalf("evals = %s, want passed or warning (the run was judged)", evals.Status)
	}
	bundles := col(t, `SELECT id::text FROM eval_bundles WHERE agent_run_id = $1::uuid ORDER BY draft_index`, sc.RunID)
	if len(evals.Refs.EvalBundleIDs) != 3 || len(bundles) != 3 {
		t.Fatalf("evals bundles = %v, want the run's 3: %v", evals.Refs.EvalBundleIDs, bundles)
	}
	results := col(t, `SELECT DISTINCT it -> 'result' ->> 'id' FROM eval_bundles b, jsonb_array_elements(b.items) it
 WHERE b.agent_run_id = $1::uuid AND jsonb_typeof(it -> 'result') = 'object' ORDER BY 1`, sc.RunID)
	if len(results) == 0 || len(evals.EvalResultIDs) != len(results) {
		t.Fatalf("evals result ids = %d, bundles hold %d judged results", len(evals.EvalResultIDs), len(results))
	}
	if evals.StartedAt == nil || decide.StartedAt == nil || evals.StartedAt.Before(*decide.StartedAt) {
		t.Errorf("evals started %v before decide %v: the evals run inside the decision", evals.StartedAt, decide.StartedAt)
	}
	if stageOf(p, stageevents.Cliff).Status != stageevents.Waiting {
		t.Errorf("cliff = %s: nothing was posted", stageOf(p, stageevents.Cliff).Status)
	}

	again := mustRun(t, svc, sc.RunID) // an idempotent repeat executes nothing
	if again.Published {
		t.Fatal("repeat published again")
	}
	if p2 := runProgress(t, reader, sc.RunID); stageOf(p2, stageevents.Decide).Attempt != 1 || stageOf(p2, stageevents.Evals).Attempt != 1 {
		t.Errorf("a repeat of a published run re-recorded its stages")
	}
}

func itoa(n int64) string {
	b, _ := json.Marshal(n)
	return string(b)
}

func TestATransportErrorWhileJudgingIsAFailedDecisionAndUnknownEvalsNeverAnEvalFail(t *testing.T) {
	sc := newScene(t)
	fw := newFake(sc)
	var failed atomic.Bool
	fw.judgeErr = func(workerclient.JudgeRequest) error {
		if failed.CompareAndSwap(false, true) {
			return &workerclient.Error{Message: "worker unreachable or timed out: connection refused", Retryable: true}
		}
		return nil
	}
	svc, reader := stagedService(t, fw)
	if _, err := svc.Run(bg, sc.RunID); !orchestrator.IsTransient(err) {
		t.Fatalf("first run = %v, want transient", err)
	}
	p := runProgress(t, reader, sc.RunID)
	decide, evals := stageOf(p, stageevents.Decide), stageOf(p, stageevents.Evals)
	if decide.Status != stageevents.Failed || decide.FailureKind == nil || *decide.FailureKind != stageevents.Transport {
		t.Fatalf("decide = %+v, want failed/transport", decide)
	}
	if evals.Status != stageevents.Unknown || evals.FailureKind == nil || *evals.FailureKind != stageevents.Transport {
		t.Fatalf("evals = %+v, want unknown/transport: a worker that could not be reached is not an eval FAIL", evals)
	}
	if len(evals.EvalResultIDs) != 0 {
		t.Errorf("evals names results %v though none were produced", evals.EvalResultIDs)
	}
	if p.Overall != stageevents.Broken {
		t.Errorf("overall = %s, want failed", p.Overall)
	}

	if _, err := svc.Resume(bg, sc.RunID); err != nil { // the worker is back
		t.Fatalf("resume: %v", err)
	}
	p = runProgress(t, reader, sc.RunID)
	decide, evals = stageOf(p, stageevents.Decide), stageOf(p, stageevents.Evals)
	if decide.Status != stageevents.Completed || decide.Attempt != 2 || decide.FailureKind != nil {
		t.Errorf("decide after the resume = %+v, want completed on attempt 2 with the failure cleared", decide)
	}
	if (evals.Status != stageevents.Passed && evals.Status != stageevents.Warning) || evals.Attempt != 2 || evals.FailureKind != nil {
		t.Errorf("evals after the resume = %+v", evals)
	}
}

func TestAPermanentFailureBeforeJudgingFailsDecideAndNeverStartsEvals(t *testing.T) {
	sc := newScene(t)
	fw := newFake(sc)
	fw.strErr = func(int) error {
		return &workerclient.Error{Status: 502, Code: "invalid_strategies", Message: "not three distinct candidates"}
	}
	svc, reader := stagedService(t, fw)
	if _, err := svc.Run(bg, sc.RunID); !orchestrator.IsPermanent(err) {
		t.Fatalf("run = %v, want permanent", err)
	}
	p := runProgress(t, reader, sc.RunID)
	decide := stageOf(p, stageevents.Decide)
	if decide.Status != stageevents.Failed || *decide.FailureKind != stageevents.Contract {
		t.Fatalf("decide = %+v, want failed/contract: unusable model output breaks the contract", decide)
	}
	if e := stageOf(p, stageevents.Evals); e.Status != stageevents.Waiting || e.Seq != nil {
		t.Errorf("evals = %+v: judging never started, so it never ran", e)
	}
}

func TestARunWithoutARecorderWritesNoStageEvents(t *testing.T) {
	sc := newScene(t)
	mustRun(t, service(t, newFake(sc)), sc.RunID)
	if n := count(t, `SELECT count(*) FROM pipeline_stage_events WHERE run_id = $1::uuid`, sc.RunID); n != 0 {
		t.Fatalf("%d stage events written without a recorder", n)
	}
}
