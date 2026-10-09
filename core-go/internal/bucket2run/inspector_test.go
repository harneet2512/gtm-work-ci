package bucket2run_test

import (
	"context"
	"testing"

	"github.com/harneet2512/gtm-work/core-go/internal/bucket2"
	"github.com/harneet2512/gtm-work/core-go/internal/workerclient"
)

func d3Of(t *testing.T, judge *scriptedJudge) bucket2.Result {
	t.Helper()
	f := setup(t, judge)
	results, err := f.runner.Run(context.Background(), f.seed.RunID)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range results {
		if r.Gate == "D3" && r.SubGate == "ranking" {
			return r
		}
	}
	t.Fatalf("no D3 ranking result in %d results", len(results))
	return bucket2.Result{}
}

// HAR-149: a model gate persists the dimensions it already folds as per-criterion results and the effect its verdict has.
func TestModelGatesPersistCriteriaAndEffect(t *testing.T) {
	d3 := d3Of(t, &scriptedJudge{})
	if len(d3.Criteria) != 2 || d3.Criteria[0].ID != "supported_by_evidence_state" || d3.Criteria[0].Result != "pass" {
		t.Fatalf("D3 criteria = %+v", d3.Criteria)
	}
	if d3.ControlEffect != "RECORD ONLY" || d3.EvaluatorVersion != "test:v1" {
		t.Fatalf("D3 effect %q version %q", d3.ControlEffect, d3.EvaluatorVersion)
	}
}

// Nothing is measured that the worker did not report: no usage is "not recorded" (never 1 call, never 0 ms).
func TestNoReportedUsageIsNotRecorded(t *testing.T) {
	d3 := d3Of(t, &scriptedJudge{})
	if d3.ModelCalls != nil || d3.LatencyMs != nil || d3.Tokens != nil || d3.CostUSD != nil {
		t.Fatalf("an unreported call must stay null: %v %v %v %v", d3.ModelCalls, d3.LatencyMs, d3.Tokens, d3.CostUSD)
	}
}

// A replayed answer made no model call and spent nothing: 0 calls, and no latency, tokens or cost to show.
func TestReplayedCallIsZeroModelCalls(t *testing.T) {
	d3 := d3Of(t, &scriptedJudge{usage: &workerclient.Usage{UsageSource: workerclient.UsageReplay, ModelCalls: 1, InputTokens: 900}})
	if d3.ModelCalls == nil || *d3.ModelCalls != 0 {
		t.Fatalf("a replayed call is 0 live model calls, got %v", d3.ModelCalls)
	}
	if d3.LatencyMs != nil || d3.Tokens != nil || d3.CostUSD != nil {
		t.Fatalf("a replay has no latency, tokens or cost: %v %v %v", d3.LatencyMs, d3.Tokens, d3.CostUSD)
	}
}

// A live call carries what the worker reported, and cost only when it reported one.
func TestLiveCallCarriesTheReportedUsage(t *testing.T) {
	cost := 0.004
	d3 := d3Of(t, &scriptedJudge{usage: &workerclient.Usage{UsageSource: workerclient.UsageLive, ModelCalls: 2, InputTokens: 700, OutputTokens: 100, ModelMs: 1500, CostUSD: &cost}})
	if d3.ModelCalls == nil || *d3.ModelCalls != 2 || d3.Tokens == nil || *d3.Tokens != 800 || d3.LatencyMs == nil || *d3.LatencyMs != 1500 || d3.CostUSD == nil || *d3.CostUSD != 0.004 {
		t.Fatalf("live usage: calls %v tokens %v ms %v cost %v", d3.ModelCalls, d3.Tokens, d3.LatencyMs, d3.CostUSD)
	}
	none := d3Of(t, &scriptedJudge{usage: &workerclient.Usage{UsageSource: workerclient.UsageLive, ModelCalls: 1, InputTokens: 10, OutputTokens: 5, ModelMs: 20}})
	if none.CostUSD != nil {
		t.Fatalf("an unreported cost stays null, got %v", *none.CostUSD)
	}
}
