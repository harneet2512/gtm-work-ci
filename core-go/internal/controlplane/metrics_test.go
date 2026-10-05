package controlplane_test

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"testing"

	"github.com/harneet2512/gtm-work/core-go/internal/readmodel"
	"github.com/harneet2512/gtm-work/core-go/internal/schemacheck"
	"github.com/harneet2512/gtm-work/core-go/internal/strategystore/strategytest"
)

const usageSQL = `INSERT INTO run_model_usage (agent_run_id, run_step, stage, models, model_calls, input_tokens, output_tokens, cached_input_tokens,
 reasoning_tokens, tool_calls, retries, cost_usd, model_ms, wall_ms, usage_source)
 VALUES ($1::uuid, $2, $3, string_to_array($4, ','), $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15)`

type usageRow struct {
	step, stage, models            string
	calls, in, out, tools, retries int
	cached, reasoning              any // nil: the provider did not report it
	cost                           any
	modelMs, wallMs                int
	source                         string // "" is live
}

func addUsage(t *testing.T, run string, r usageRow) {
	t.Helper()
	source := r.source
	if source == "" {
		source = "live"
	}
	exec(t, usageSQL, run, r.step, r.stage, r.models, r.calls, r.in, r.out, r.cached, r.reasoning, r.tools, r.retries, r.cost, r.modelMs, r.wallMs, source)
}

func metricsOf(t *testing.T, episode string) []byte {
	t.Helper()
	m, err := reader(t).Metrics(context.Background(), episode)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	v, err := schemacheck.New()
	if err != nil {
		t.Fatal(err)
	}
	if err := v.Validate("operational_metrics", raw); err != nil {
		t.Fatalf("the metrics violate operational_metrics.v1.json: %v\n%s", err, raw)
	}
	return raw
}

func TestMetricsOfARunWithNoRecordedUsageAreUnmeasuredZeros(t *testing.T) {
	seed := seedEpisode(t, seedAccount(t, "Metrics None"))
	m, err := reader(t).Metrics(context.Background(), seed.EpisodeID)
	if err != nil {
		t.Fatal(err)
	}
	metricsOf(t, seed.EpisodeID)
	if m.Classification != "metric" || m.Measured || m.ModelCalls != 0 || m.InputTokens != 0 || m.CostUSD != nil || m.Latency.WorkerCallMs != 0 {
		t.Fatalf("metrics = %+v", m)
	}
	if m.Models == nil || m.Stages == nil || len(m.Models) != 0 || len(m.Stages) != 0 {
		t.Fatalf("models and stages are empty lists, not null: %+v", m)
	}
	if m.AgentRunID != seed.RunID || m.DecisionEpisodeID == nil || *m.DecisionEpisodeID != seed.EpisodeID {
		t.Fatalf("ids = %s %v", m.AgentRunID, m.DecisionEpisodeID)
	}
}

func TestMetricsSumTheRecordedCallsAndGroupThemByStageInFirstRunOrder(t *testing.T) {
	seed := seedEpisode(t, seedAccount(t, "Metrics Sum"))
	addUsage(t, seed.RunID, usageRow{step: "draft", stage: "strategies", models: "deepseek-v4-flash", calls: 3, in: 12000, out: 1500, tools: 3, retries: 1, cached: 6000, reasoning: 400, cost: 0.008, modelMs: 13000, wallMs: 14000})
	addUsage(t, seed.RunID, usageRow{step: "draft", stage: "judge", models: "deepseek-v4-flash", calls: 2, in: 3000, out: 400, tools: 0, retries: 0, cached: 1000, reasoning: 100, cost: 0.002, modelMs: 2000, wallMs: 2500})
	addUsage(t, seed.RunID, usageRow{step: "draft", stage: "judge", models: "deepseek-v4-flash,other-model", calls: 2, in: 3420, out: 410, tools: 0, retries: 1, cached: 2000, reasoning: 140, cost: 0.0023, modelMs: 4800, wallMs: 4900})
	addUsage(t, seed.RunID, usageRow{step: "await_human", stage: "human_delta", models: "stub", calls: 1, in: 800, out: 60, tools: 0, retries: 0, cached: 0, reasoning: 0, cost: 0.0004, modelMs: 900, wallMs: 1000})
	exec(t, `INSERT INTO context_access_log (agent_run_id, tool, args, returned_ids, bytes) VALUES ($1::uuid, 'state', '{}', '[]', 10), ($1::uuid, 'people', '{}', '[]', 10)`, seed.RunID)

	m, err := reader(t).Metrics(context.Background(), seed.EpisodeID)
	if err != nil {
		t.Fatal(err)
	}
	metricsOf(t, seed.EpisodeID)
	if !m.Measured || m.ModelCalls != 8 || m.InputTokens != 19220 || m.OutputTokens != 2370 || m.CachedInputTokens == nil || *m.CachedInputTokens != 9000 || m.ReasoningTokens == nil || *m.ReasoningTokens != 640 {
		t.Fatalf("totals = %+v", m)
	}
	if m.ToolCalls != 3 || m.ContextPulls != 2 || m.Retries != 2 || m.Latency.ModelMs != 20700 || m.Latency.WorkerCallMs != 22400 {
		t.Fatalf("tool calls / pulls / retries / latency = %d %d %d %+v", m.ToolCalls, m.ContextPulls, m.Retries, m.Latency)
	}
	if m.CostUSD == nil || math.Abs(*m.CostUSD-0.0127) > 1e-9 {
		t.Fatalf("cost = %v", m.CostUSD)
	}
	if len(m.Models) != 3 || m.Models[0] != "deepseek-v4-flash" || m.Models[1] != "other-model" || m.Models[2] != "stub" {
		t.Fatalf("models = %v", m.Models)
	}
	if len(m.Stages) != 3 || m.Stages[0].Stage != "strategies" || m.Stages[1].Stage != "judge" || m.Stages[2].Stage != "human_delta" {
		t.Fatalf("stages = %+v", m.Stages)
	}
	judge := m.Stages[1]
	if judge.WorkerCalls != 2 || judge.ModelCalls != 4 || judge.InputTokens != 6420 || judge.WorkerCallMs != 7400 || judge.ModelMs != 6800 || judge.Retries != 1 {
		t.Fatalf("judge stage = %+v", judge)
	}
	if judge.CostUSD == nil || math.Abs(*judge.CostUSD-0.0043) > 1e-9 {
		t.Fatalf("judge cost = %v", judge.CostUSD)
	}
}

func TestACostIsOnlyReportedWhenEveryModelCallReportedOne(t *testing.T) {
	seed := seedEpisode(t, seedAccount(t, "Metrics Cost"))
	addUsage(t, seed.RunID, usageRow{step: "draft", stage: "strategies", models: "m", calls: 3, in: 10, out: 5, tools: 0, retries: 0, cached: 0, reasoning: 0, cost: 0.008, modelMs: 1, wallMs: 1})
	addUsage(t, seed.RunID, usageRow{step: "draft", stage: "judge", models: "m", calls: 2, in: 10, out: 5, tools: 0, retries: 0, cached: 0, reasoning: 0, cost: nil, modelMs: 1, wallMs: 1}) // the provider reported no cost for these calls
	m, err := reader(t).Metrics(context.Background(), seed.EpisodeID)
	if err != nil {
		t.Fatal(err)
	}
	if m.CostUSD != nil {
		t.Fatalf("a partial sum would understate the cost: %v", *m.CostUSD)
	}
	if m.Stages[0].CostUSD == nil || m.Stages[1].CostUSD != nil {
		t.Fatalf("per-stage costs = %v / %v", m.Stages[0].CostUSD, m.Stages[1].CostUSD)
	}
	metricsOf(t, seed.EpisodeID)
}

func TestACallThatMadeNoModelCallDoesNotMakeTheCostUnknown(t *testing.T) {
	seed := seedEpisode(t, seedAccount(t, "Metrics Zero Call"))
	addUsage(t, seed.RunID, usageRow{step: "draft", stage: "revise", models: "m", calls: 1, in: 10, out: 5, tools: 0, retries: 0, cached: 0, reasoning: 0, cost: 0.001, modelMs: 1, wallMs: 1})
	addUsage(t, seed.RunID, usageRow{step: "draft", stage: "revise", models: "", calls: 0, in: 0, out: 0, tools: 0, retries: 2, cached: 0, reasoning: 0, cost: nil, modelMs: 30, wallMs: 40}) // spent only retries and time
	m, err := reader(t).Metrics(context.Background(), seed.EpisodeID)
	if err != nil {
		t.Fatal(err)
	}
	if m.CostUSD == nil || *m.CostUSD != 0.001 || m.Retries != 2 || m.Latency.WorkerCallMs != 41 {
		t.Fatalf("metrics = %+v", m)
	}
}

func TestMetricsOfAnotherRunsEpisodeAreNotMixedIn(t *testing.T) {
	a := seedEpisode(t, seedAccount(t, "Metrics A"))
	b := seedEpisode(t, seedAccount(t, "Metrics B"))
	addUsage(t, a.RunID, usageRow{step: "draft", stage: "strategies", models: "m", calls: 1, in: 10, out: 5, tools: 0, retries: 0, cached: 0, reasoning: 0, cost: nil, modelMs: 1, wallMs: 1})
	m, err := reader(t).Metrics(context.Background(), b.EpisodeID)
	if err != nil || m.Measured || m.ModelCalls != 0 {
		t.Fatalf("B sees %+v %v", m, err)
	}
}

func TestMetricsOfAnUnknownEpisodeAreNotFound(t *testing.T) {
	for _, id := range []string{missing, "nope", strategytest.NewID()} {
		if _, err := reader(t).Metrics(context.Background(), id); !errors.Is(err, readmodel.ErrNotFound) {
			t.Errorf("%q: %v", id, err)
		}
	}
}

func TestAReplayOnlyRunIsNotMeasuredAndReportsNoSpend(t *testing.T) {
	seed := seedEpisode(t, seedAccount(t, "Metrics Replay"))
	addUsage(t, seed.RunID, usageRow{step: "draft", stage: "strategies", source: "replay", cached: nil, reasoning: nil, cost: nil, wallMs: 40})
	addUsage(t, seed.RunID, usageRow{step: "draft", stage: "judge", source: "replay", cached: nil, reasoning: nil, cost: nil, wallMs: 25})
	m, err := reader(t).Metrics(context.Background(), seed.EpisodeID)
	if err != nil {
		t.Fatal(err)
	}
	metricsOf(t, seed.EpisodeID)
	if m.Measured || m.UsageSource == nil || *m.UsageSource != "replay" {
		t.Fatalf("a replayed run is not measured and says it was replayed: %+v", m)
	}
	if m.ModelCalls != 0 || m.InputTokens != 0 || m.CostUSD != nil || m.CachedInputTokens != nil || m.ReasoningTokens != nil ||
		m.Latency.WorkerCallMs != 0 || len(m.Stages) != 0 || len(m.Models) != 0 {
		t.Fatalf("a replayed run reports no spend, no stages and no time: %+v", m)
	}
}

func TestMixedRunsSumOnlyTheLiveCalls(t *testing.T) {
	seed := seedEpisode(t, seedAccount(t, "Metrics Mixed"))
	addUsage(t, seed.RunID, usageRow{step: "draft", stage: "strategies", models: "m", calls: 1, in: 100, out: 10, cached: 0, reasoning: 0, cost: 0.01, modelMs: 5, wallMs: 6})
	addUsage(t, seed.RunID, usageRow{step: "draft", stage: "judge", source: "replay", wallMs: 90})
	m, err := reader(t).Metrics(context.Background(), seed.EpisodeID)
	if err != nil {
		t.Fatal(err)
	}
	metricsOf(t, seed.EpisodeID)
	if !m.Measured || m.UsageSource == nil || *m.UsageSource != "mixed" || m.ModelCalls != 1 || m.Latency.WorkerCallMs != 6 || len(m.Stages) != 1 {
		t.Fatalf("metrics = %+v", m)
	}
	if m.CostUSD == nil || *m.CostUSD != 0.01 {
		t.Fatalf("replayed calls must not make the live cost unknown: %v", m.CostUSD)
	}
}

func TestALiveRunSaysLiveAndAnUnrecordedRunSaysNothing(t *testing.T) {
	seed := seedEpisode(t, seedAccount(t, "Metrics Live"))
	none, err := reader(t).Metrics(context.Background(), seed.EpisodeID)
	if err != nil || none.UsageSource != nil {
		t.Fatalf("no usage, no source: %+v %v", none.UsageSource, err)
	}
	addUsage(t, seed.RunID, usageRow{step: "draft", stage: "strategies", models: "m", calls: 1, in: 10, out: 5, cached: 0, reasoning: 0, cost: 0.001, wallMs: 1})
	m, err := reader(t).Metrics(context.Background(), seed.EpisodeID)
	if err != nil || m.UsageSource == nil || *m.UsageSource != "live" {
		t.Fatalf("source = %v %v", m.UsageSource, err)
	}
}

func TestCachedAndReasoningTokensAreNullUnlessEveryModelCallReportedThem(t *testing.T) {
	seed := seedEpisode(t, seedAccount(t, "Metrics Null Tokens"))
	addUsage(t, seed.RunID, usageRow{step: "draft", stage: "strategies", models: "m", calls: 1, in: 10, out: 5, cached: 4, reasoning: 1, cost: 0.001, wallMs: 1})
	addUsage(t, seed.RunID, usageRow{step: "draft", stage: "judge", models: "m", calls: 1, in: 10, out: 5, cached: nil, reasoning: nil, cost: 0.001, wallMs: 1})
	m, err := reader(t).Metrics(context.Background(), seed.EpisodeID)
	if err != nil {
		t.Fatal(err)
	}
	metricsOf(t, seed.EpisodeID)
	if m.CachedInputTokens != nil || m.ReasoningTokens != nil {
		t.Fatalf("a floor presented as a figure: %v %v", m.CachedInputTokens, m.ReasoningTokens)
	}
	if m.InputTokens != 20 {
		t.Fatalf("what was reported still counts: %d", m.InputTokens)
	}
	if s := m.Stages[0]; s.CachedInputTokens == nil || *s.CachedInputTokens != 4 {
		t.Fatalf("the stage that reported keeps its figure: %+v", s)
	}
	if s := m.Stages[1]; s.CachedInputTokens != nil || s.ReasoningTokens != nil {
		t.Fatalf("the stage that did not report has none: %+v", s)
	}
}
