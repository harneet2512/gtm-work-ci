// The episode trace as a Braintrust-style span tree (HAR-145): the causal chain Event, Evidence, State, Precedents and
// knowledge, Candidates, Ranking, Cliff, Human action, Recompute, Knowledge mutation, each phase holding its spans, each span
// with the gate results attached to it. A span records when it happened, not how long it took, so the only duration shown is
// the gap since the previous span that has a time; with no time on either side it is "not measured".
import type { GateResult, TraceSpan } from "@/lib/api/types";

export const PHASES: readonly { id: string; label: string }[] = [
  { id: "event", label: "Event" },
  { id: "evidence", label: "Evidence" },
  { id: "state", label: "State" },
  { id: "knowledge", label: "Precedents and knowledge" },
  { id: "candidates", label: "Candidates" },
  { id: "ranking", label: "Ranking" },
  { id: "cliff", label: "Cliff" },
  { id: "human", label: "Human action" },
  { id: "recompute", label: "Recompute" },
  { id: "mutation", label: "Knowledge mutation" },
];

const PHASE_OF: Readonly<Record<TraceSpan["kind"], string>> = {
  source_event: "event",
  evidence: "evidence",
  resolution: "evidence",
  graph_mutation: "evidence",
  tool_call: "evidence",
  state: "state",
  precedents: "knowledge",
  knowledge_retrieved: "knowledge",
  knowledge_applicable: "knowledge",
  knowledge_used: "knowledge",
  candidates: "candidates",
  ranking: "ranking",
  cliff_message: "cliff",
  human_interaction: "human",
  execution: "human",
  recomputed_action: "recompute",
  knowledge_mutation: "mutation",
};

export type SpanVerdict = "pass" | "warn" | "fail" | "unknown";

/**
 * A trace mixes two clocks: replayed world time (the 2023 emails) and wall-clock time (when the platform recorded a step in
 * 2026). A gap across clocks is meaningless, so a gap is only computed between two timed spans less than this far apart;
 * a bigger jump means a different clock and starts a new one.
 */
export const SAME_CLOCK_MAX_MS = 7 * 86_400_000;

export interface TreeSpan {
  span: TraceSpan;
  /** Milliseconds since the previous timed span on the same clock; null when there is none (nothing is shown). */
  gapMs: number | null;
  gates: GateResult[];
  verdicts: SpanVerdict[];
}

export interface TreePhase {
  id: string;
  label: string;
  spans: TreeSpan[];
}

const asVerdict = (g: GateResult): SpanVerdict => (g.verdict === "pass" || g.verdict === "warn" || g.verdict === "fail" ? (g.verdict === "pass" && g.evidence_refs.length === 0 ? "unknown" : g.verdict) : "unknown");

/** Phases in chain order, empty ones left out; the input is never changed. */
export function buildTraceTree(spans: readonly TraceSpan[], gates: readonly GateResult[]): TreePhase[] {
  const ordered = [...spans].sort((a, b) => a.seq - b.seq);
  let lastTime: number | null = null;
  const entries: (TreeSpan & { phase: string })[] = ordered.map((span) => {
    const t = span.occurred_at ? Date.parse(span.occurred_at) : Number.NaN;
    const raw = lastTime !== null && Number.isFinite(t) ? t - lastTime : null;
    const gapMs = raw !== null && raw >= 0 && raw <= SAME_CLOCK_MAX_MS ? raw : null;
    if (Number.isFinite(t)) lastTime = t;
    const mine = gates.filter((g) => g.span_id === span.id);
    return { span, gapMs, gates: mine, verdicts: mine.map(asVerdict), phase: PHASE_OF[span.kind] ?? "evidence" };
  });
  return PHASES.map((p) => ({ id: p.id, label: p.label, spans: entries.filter((e) => e.phase === p.id).map(({ phase: _phase, ...rest }) => rest) })).filter((p) => p.spans.length > 0);
}

/** "+2 min", or "" when there is no same-clock gap (nothing is shown). */
export function gapText(ms: number | null): string {
  if (ms === null) return "";
  if (ms < 1000) return `+${ms} ms`;
  if (ms < 60_000) return `+${Math.round(ms / 1000)} s`;
  if (ms < 3_600_000) return `+${Math.round(ms / 60_000)} min`;
  if (ms < 86_400_000) return `+${(ms / 3_600_000).toFixed(1)} h`;
  return `+${(ms / 86_400_000).toFixed(1)} d`;
}

/** The span a ?span= value names, else the first span; null when there are none. */
export function pickSpan(spans: readonly TraceSpan[], wanted: string | null): TraceSpan | null {
  const ordered = [...spans].sort((a, b) => a.seq - b.seq);
  return ordered.find((s) => s.id === wanted) ?? ordered[0] ?? null;
}

const AGG_ORDER: readonly SpanVerdict[] = ["fail", "warn", "unknown", "pass"];

/** One verdict for a span: the worst of its gates, and a "1 warn · 1 pass" count line. Null when no gate is attached. */
export function aggregateVerdicts(verdicts: readonly SpanVerdict[]): { worst: SpanVerdict; text: string } | null {
  if (verdicts.length === 0) return null;
  const parts = AGG_ORDER.filter((v) => verdicts.includes(v)).map((v) => `${verdicts.filter((x) => x === v).length} ${v}`);
  return { worst: AGG_ORDER.find((v) => verdicts.includes(v))!, text: parts.join(" · ") };
}
