package orchestrator_test

import (
	"context"
	"testing"

	"github.com/harneet2512/gtm-work/core-go/internal/workerclient"
)

// meteredFake is the fake worker behaving like a worker that reports what each call cost: it reports before it
// answers, so a call that then fails has still been paid for (HAR-145 operational metrics).
type meteredFake struct{ *fakeWorker }

func usageOf(stage string, calls int) workerclient.UsageRecord {
	return workerclient.UsageRecord{Stage: stage, WallMs: 20, Usage: workerclient.Usage{ModelCalls: calls, InputTokens: int64(100 * calls),
		OutputTokens: int64(10 * calls), ToolCalls: calls - 1, ModelMs: 15, Models: []string{"fake/model"}, UsageSource: "live"}}
}

func (m meteredFake) Strategies(ctx context.Context, req workerclient.StrategiesRequest) (workerclient.StrategiesResponse, error) {
	workerclient.ReportUsage(ctx, usageOf("strategies", 4))
	return m.fakeWorker.Strategies(ctx, req)
}

func (m meteredFake) Judge(ctx context.Context, req workerclient.JudgeRequest) (workerclient.JudgeResponse, error) {
	workerclient.ReportUsage(ctx, usageOf("judge", 2))
	return m.fakeWorker.Judge(ctx, req)
}

func (m meteredFake) Revise(ctx context.Context, req workerclient.ReviseRequest) (workerclient.ReviseResponse, error) {
	workerclient.ReportUsage(ctx, usageOf("revise", 1))
	return m.fakeWorker.Revise(ctx, req)
}

func TestARunStoresWhatItsWorkerCallsCostUnderTheDraftStep(t *testing.T) {
	sc := newScene(t)
	svc := service(t, meteredFake{newFake(sc)})
	mustRun(t, svc, sc.RunID)

	if got := scalar(t, `SELECT count(*)::text FROM run_model_usage WHERE agent_run_id = $1::uuid AND stage = 'strategies'`, sc.RunID); got != "1" {
		t.Fatalf("strategies rows = %s", got)
	}
	if got := scalar(t, `SELECT count(*)::text FROM run_model_usage WHERE agent_run_id = $1::uuid AND stage = 'judge'`, sc.RunID); got != "3" {
		t.Fatalf("one judge call per candidate, got %s rows", got)
	}
	if got := scalar(t, `SELECT count(*)::text FROM run_model_usage WHERE agent_run_id = $1::uuid AND run_step IS DISTINCT FROM 'draft'`, sc.RunID); got != "0" {
		t.Fatalf("%s rows are not under the draft step", got)
	}
	if got := scalar(t, `SELECT sum(model_calls)::text || '/' || sum(input_tokens) || '/' || sum(wall_ms) FROM run_model_usage WHERE agent_run_id = $1::uuid`, sc.RunID); got != "10/1000/80" {
		t.Fatalf("summed usage = %s, want 10 calls / 1000 input tokens / 80 ms", got)
	}
}

func TestAFailedAttemptStillRecordsItsSpendAndAResumeAddsToIt(t *testing.T) {
	sc := newScene(t)
	fw := newFake(sc)
	fw.strErr = func(call int) error {
		if call == 1 {
			return &workerclient.Error{Status: 502, Code: "model_error", Message: "the provider hiccuped", Retryable: true}
		}
		return nil
	}
	svc := service(t, meteredFake{fw})
	if _, err := svc.Run(bg, sc.RunID); err == nil {
		t.Fatal("the first attempt must fail transiently")
	}
	if got := scalar(t, `SELECT count(*)::text FROM run_model_usage WHERE agent_run_id = $1::uuid AND stage = 'strategies'`, sc.RunID); got != "1" {
		t.Fatalf("the failed attempt was paid for and must be recorded, got %s rows", got)
	}
	mustRun(t, svc, sc.RunID)
	if got := scalar(t, `SELECT count(*)::text FROM run_model_usage WHERE agent_run_id = $1::uuid AND stage = 'strategies'`, sc.RunID); got != "2" {
		t.Fatalf("the resume's own call is added, got %s rows", got)
	}
}

func TestAWorkerThatReportsNothingLeavesNoUsageRowsAndDoesNotFailTheRun(t *testing.T) {
	sc := newScene(t)
	mustRun(t, service(t, newFake(sc)), sc.RunID)
	if got := scalar(t, `SELECT count(*)::text FROM run_model_usage WHERE agent_run_id = $1::uuid`, sc.RunID); got != "0" {
		t.Fatalf("an older worker reports no usage, so none is stored: %s rows", got)
	}
}
