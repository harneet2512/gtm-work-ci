// Continuous health (HAR-149 section 6): real aggregates over the stored traces and the operational metrics of every episode.
// These are METRICS, visually and verbally apart from per-run verdicts: they describe how the system runs, never whether one
// decision was right. A figure nobody reported reads "not measured" (never 0, never "free"); an episode with no live usage is
// counted apart, not averaged in. Agent cost versus eval cost uses the worker's own stages: "judge" is the eval overhead.
import type { OperationalMetrics } from "@/lib/api/types";

/** The steps of the standard causal chain a complete trace records (tool calls and the delivery are not on every episode). */
export const EXPECTED_SPAN_KINDS: readonly string[] = [
  "source_event",
  "evidence",
  "resolution",
  "graph_mutation",
  "state",
  "precedents",
  "knowledge_retrieved",
  "knowledge_applicable",
  "knowledge_used",
  "candidates",
  "ranking",
  "cliff_message",
  "human_interaction",
  "recomputed_action",
  "knowledge_mutation",
];

export interface HealthEpisode {
  episodeId: string;
  label: string;
  metrics: OperationalMetrics | null;
  /** The span kinds the episode's trace holds; null when the trace could not be read. */
  spanKinds: readonly string[] | null;
}

export interface Fact {
  label: string;
  value: string;
}

export interface CostSide {
  modelCalls: string;
  tokens: string;
  cost: string;
}

export interface HealthView {
  episodes: number;
  measuredEpisodes: number;
  note: string;
  trace: { episodes: number; complete: number; average: string; note: string };
  totals: Fact[];
  split: { agent: CostSide; evals: CostSide };
}

const NM = "not measured";
const NUMBER = new Intl.NumberFormat("en-US");
const USD = new Intl.NumberFormat("en-US", { style: "currency", currency: "USD", maximumFractionDigits: 4 });
const count = (n: number | null): string => (n === null ? NM : NUMBER.format(n));
const money = (n: number | null): string => (n === null ? NM : USD.format(n));

export function duration(ms: number): string {
  if (ms < 1000) return `${Math.round(ms)} ms`;
  if (ms < 60_000) return `${(ms / 1000).toFixed(1)} s`;
  return `${Math.floor(ms / 60_000)} min ${Math.round((ms % 60_000) / 1000)} s`;
}

/** The sum, or null (not measured) when any term is unreported or there are none. */
const total = (xs: readonly (number | null)[]): number | null => (xs.length === 0 || xs.some((x) => x === null) ? null : (xs as number[]).reduce((a, b) => a + b, 0));

type Stage = OperationalMetrics["stages"][number];

function side(stages: readonly Stage[]): CostSide {
  if (stages.length === 0) return { modelCalls: NM, tokens: NM, cost: NM };
  return {
    modelCalls: count(total(stages.map((s) => s.model_calls))),
    tokens: count(total(stages.map((s) => s.input_tokens + s.output_tokens))),
    cost: money(total(stages.map((s) => s.cost_usd))),
  };
}

export function buildHealthView(episodes: readonly HealthEpisode[]): HealthView {
  const live = episodes.map((e) => e.metrics).filter((m): m is OperationalMetrics => m !== null && m.measured);
  const stages = live.flatMap((m) => m.stages);
  const traces = episodes.filter((e): e is HealthEpisode & { spanKinds: readonly string[] } => e.spanKinds !== null);
  const present = traces.map((e) => EXPECTED_SPAN_KINDS.filter((k) => e.spanKinds.includes(k)).length);
  const mean = present.length === 0 ? null : present.reduce((a, b) => a + b, 0) / present.length;
  const some = live.length > 0;
  const totals: Fact[] = [
    { label: "Model calls", value: some ? count(total(live.map((m) => m.model_calls))) : NM },
    { label: "Tool calls", value: some ? count(total(live.map((m) => m.tool_calls))) : NM },
    { label: "Input tokens", value: some ? count(total(live.map((m) => m.input_tokens))) : NM },
    { label: "Output tokens", value: some ? count(total(live.map((m) => m.output_tokens))) : NM },
    { label: "Model time (summed over calls)", value: some ? duration(live.reduce((a, m) => a + m.latency.model_ms, 0)) : NM },
    { label: "Worker call time (summed over calls)", value: some ? duration(live.reduce((a, m) => a + m.latency.worker_call_ms, 0)) : NM },
    { label: "Cost", value: some ? money(total(live.map((m) => m.cost_usd))) : NM },
  ];
  return {
    episodes: episodes.length,
    measuredEpisodes: live.length,
    note: `${live.length} of ${episodes.length} ${episodes.length === 1 ? "episode has" : "episodes have"} live model usage recorded. A replayed episode spent nothing and is not counted; an episode with no usage is not measured, not zero.`,
    trace: {
      episodes: traces.length,
      complete: present.filter((n) => n === EXPECTED_SPAN_KINDS.length).length,
      average: mean === null ? NM : `${Number.isInteger(mean) ? mean : mean.toFixed(1)} of ${EXPECTED_SPAN_KINDS.length}`,
      note: "This counts the steps of the standard chain that were recorded. It is not an integrity verdict: no automated trace check exists yet.",
    },
    totals,
    split: { agent: side(stages.filter((s) => s.stage !== "judge")), evals: side(stages.filter((s) => s.stage === "judge")) },
  };
}
