// The episode's operational metrics (HAR-145): what producing the decision cost, as a METRIC. Counts are exact sums of what
// the model worker reported; a figure nobody reported (cached or reasoning tokens, cost) reads "not measured", never 0 and
// never "free". A run with no recorded usage, or one only replayed from recorded answers, reports no measurement at all:
// its zeros are not shown as figures, and a replay reports no spend. Worker call time is labelled for what it is, a sum
// over calls, not the run's elapsed time.
import type { OperationalMetrics } from "@/lib/api/types";

const NOT_MEASURED = "not measured";
const NUMBER = new Intl.NumberFormat("en-US");
const USD = new Intl.NumberFormat("en-US", { style: "currency", currency: "USD", maximumFractionDigits: 4 });

export interface MetricFact {
  label: string;
  value: string;
}

export interface StageMetric {
  name: string;
  modelCalls: string;
  tokens: string;
  cost: string;
  workerTime: string;
}

export interface MetricsView {
  status: string;
  note: string | null;
  facts: MetricFact[];
  models: string[];
  stages: StageMetric[];
}

const STAGE_NAME: Record<OperationalMetrics["stages"][number]["stage"], string> = {
  draft: "Draft",
  strategies: "Strategies",
  judge: "Judging",
  revise: "Revision",
  human_delta: "Human change analysis",
  judgment_inference: "Judgment inference",
};

export function duration(ms: number): string {
  if (ms < 1000) return `${ms} ms`;
  if (ms < 60_000) return `${(ms / 1000).toFixed(1)} s`;
  return `${Math.floor(ms / 60_000)} min ${Math.round((ms % 60_000) / 1000)} s`;
}

const count = (n: number | null): string => (n === null ? NOT_MEASURED : NUMBER.format(n));
const money = (n: number | null): string => (n === null ? NOT_MEASURED : USD.format(n));

const WORKER_TIME = "Worker call time (summed over calls, not elapsed time)";
const MODEL_TIME = "Model time (summed over calls)";

const FACT_LABELS = ["Model calls", "Input tokens", "Output tokens", "Cached input tokens", "Reasoning tokens", "Tool calls", "Context pulls", "Retries", "Cost", WORKER_TIME, MODEL_TIME] as const;

function unmeasured(status: string, note: string): MetricsView {
  return { status, note, facts: FACT_LABELS.map((label) => ({ label, value: NOT_MEASURED })), models: [], stages: [] };
}

export function metricsView(m: OperationalMetrics): MetricsView {
  if (!m.measured) {
    return m.usage_source === "replay"
      ? unmeasured("replayed from recorded answers: nothing was spent", "This run was replayed from recorded answers, so there is no spend and no measurement to show.")
      : unmeasured(NOT_MEASURED, "No model usage was recorded for this run, so nothing is shown as measured. It is not zero and not free.");
  }
  return {
    status: m.usage_source === "mixed" ? "measured from live and replayed model calls (replayed calls are not counted)" : "measured from live model calls",
    note: m.retries > 0 ? "Tokens of an answer that was discarded and retried are not reported, so token counts are a lower bound." : null,
    facts: [
      { label: "Model calls", value: count(m.model_calls) },
      { label: "Input tokens", value: count(m.input_tokens) },
      { label: "Output tokens", value: count(m.output_tokens) },
      { label: "Cached input tokens", value: count(m.cached_input_tokens) },
      { label: "Reasoning tokens", value: count(m.reasoning_tokens) },
      { label: "Tool calls", value: count(m.tool_calls) },
      { label: "Context pulls", value: count(m.context_pulls) },
      { label: "Retries", value: count(m.retries) },
      { label: "Cost", value: money(m.cost_usd) },
      { label: WORKER_TIME, value: duration(m.latency.worker_call_ms) },
      { label: MODEL_TIME, value: duration(m.latency.model_ms) },
    ],
    models: m.models,
    stages: m.stages.map((s) => ({
      name: STAGE_NAME[s.stage],
      modelCalls: count(s.model_calls),
      tokens: `${count(s.input_tokens)} in · ${count(s.output_tokens)} out`,
      cost: money(s.cost_usd),
      workerTime: duration(s.worker_call_ms),
    })),
  };
}
