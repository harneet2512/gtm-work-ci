// Job 3 of the HAR-129 demo loop, kept secondary: the run's engineering view. Is the trace complete enough to
// reconstruct the decision (E18), which model ran, how long each step took (M4), and what judging cost (M1, M5),
// summed only from what the eval bundles recorded; a figure nobody recorded stays null.
import type { EvalPageData } from "@/lib/load-eval-page";
import { formatDay } from "@/lib/format";

export interface TraceCheck {
  label: string;
  ok: boolean;
  detail: string;
}

export interface SystemView {
  trace: TraceCheck[];
  model: string | null;
  steps: { step: string; status: string; seconds: number | null }[];
  judges: { verdicts: number; rule: number; ai: number; costUsd: number | null; latencyMs: number | null; tokens: number | null };
}

const seconds = (from: string | null | undefined, to: string | null | undefined): number | null => {
  const a = Date.parse(from ?? "");
  const b = Date.parse(to ?? "");
  return Number.isNaN(a) || Number.isNaN(b) ? null : Math.round((b - a) / 1000);
};

/** The sum of a recorded figure, or null when no item recorded it. */
function sum(values: readonly (number | null | undefined)[]): number | null {
  const known = values.filter((v): v is number => typeof v === "number");
  return known.length === 0 ? null : known.reduce((a, b) => a + b, 0);
}

/** "2, read as of Nov 9, 2023": how many context pulls, and the world time they read (ADR-0019). */
function pullsDetail(pulls: readonly { world_as_of?: string | null }[]): string {
  const asOf = pulls[0]?.world_as_of;
  if (pulls.length === 0) return "0";
  return asOf ? `${pulls.length}, read as of ${formatDay(asOf)}` : String(pulls.length);
}

function traceChecks(data: EvalPageData): TraceCheck[] {
  const t = data.trace;
  const diff = t?.state_diff;
  const pulls = t?.context_accesses ?? [];
  return [
    { label: "Trigger activity", ok: (t?.trigger_activities.length ?? 0) > 0, detail: `${t?.trigger_activities.length ?? 0} recorded` },
    { label: "State diff", ok: !!diff, detail: diff ? `v${diff.from_version} → v${diff.to_version}, ${diff.changes.length} changes` : "not recorded" },
    { label: "Signals", ok: (t?.signals.length ?? 0) > 0, detail: String(t?.signals.length ?? 0) },
    { label: "Trigger evaluation", ok: !!t?.trigger_evaluation, detail: t?.trigger_evaluation ? (t.trigger_evaluation.eligible ? "eligible" : "not eligible") : "not recorded" },
    { label: "Context pulls", ok: pulls.length > 0, detail: pullsDetail(pulls) },
    { label: "Human decision", ok: (t?.decisions.length ?? 0) > 0, detail: String(t?.decisions.length ?? 0) },
  ];
}

export function buildSystemView(data: EvalPageData): SystemView {
  const items = (data.strategies?.eval_bundles ?? []).flatMap((b) => b.items);
  const results = items.flatMap((i) => (i.result ? [i.result] : []));
  const meta = items.map((i) => i.metadata);
  return {
    trace: traceChecks(data),
    model: data.run.model ?? null,
    steps: (data.run.steps ?? []).map((s) => ({ step: s.step, status: s.status, seconds: seconds(s.started_at, s.finished_at) })),
    judges: {
      verdicts: results.length,
      rule: results.filter((r) => r.kind === "deterministic").length,
      ai: results.filter((r) => r.kind !== "deterministic").length,
      costUsd: sum(meta.map((m) => m?.cost_usd)),
      latencyMs: sum(meta.map((m) => m?.latency_ms)),
      tokens: sum(meta.flatMap((m) => [m?.input_tokens, m?.output_tokens])),
    },
  };
}
