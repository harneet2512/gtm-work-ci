// "How is it performing over time?" and "Common failures" (HAR-149 sections 8 and 9): real aggregates over every episode in the
// database that has stored results. Only the metrics that apply to the gate's grader are listed; a metric the backend has not
// measured says "Not measured yet" and names what it will measure. Nothing here is estimated.
import type { GateResult } from "@/lib/api/types";
import { criterionLabel } from "./criterion-words";

export interface FleetEpisode {
  episodeId: string;
  label: string;
  results: readonly GateResult[];
}

export interface Distribution {
  pass: number;
  warn: number;
  fail: number;
  unknown: number;
  total: number;
}

export interface MetricView {
  id: "distribution" | "false_pass" | "agreement" | "stability" | "regression" | "latency";
  label: string;
  state: "measured" | "not_measured";
  value: string | null;
  /** What the metric will measure (always said). */
  measures: string;
  /** Why it is not measured, or how the value was computed. */
  note: string;
}

export interface PerformanceView {
  /** Episodes in the database that stored a result for this gate. */
  episodes: number;
  distribution: Distribution;
  metrics: MetricView[];
}

export interface FailureExample {
  episodeId: string;
  episodeLabel: string;
  why: string;
}

export interface FailureCategory {
  id: string;
  label: string;
  count: number;
  /** fail or warn: the worst severity seen in the category. */
  severity: "fail" | "warn";
  examples: FailureExample[];
}

export interface FailuresView {
  categories: FailureCategory[];
  /** Said when there is nothing to list. */
  empty: string | null;
}

const MAX_EXAMPLES = 3;
type Verdict = "pass" | "warn" | "fail" | "unknown";
const asVerdict = (v: string): Verdict => (v === "pass" || v === "warn" || v === "fail" ? v : "unknown");
const RANK: Readonly<Record<Verdict, number>> = { fail: 3, warn: 2, unknown: 1, pass: 0 };

const forGate = (fleet: readonly FleetEpisode[], gate: string) =>
  fleet.map((e) => ({ e, rs: e.results.filter((r) => r.gate === gate) })).filter((x) => x.rs.length > 0);

/** One verdict per episode: the worst of its results for the gate. */
function distributionOf(rows: ReturnType<typeof forGate>): Distribution {
  const d: Distribution = { pass: 0, warn: 0, fail: 0, unknown: 0, total: 0 };
  for (const { rs } of rows) {
    const worst = rs.map((r) => asVerdict(r.verdict)).sort((a, b) => RANK[b] - RANK[a])[0]!;
    d[worst] += 1;
    d.total += 1;
  }
  return d;
}

const seconds = (ms: number): string => `${(ms / 1000).toFixed(1)} s`;

function median(xs: readonly number[]): number {
  const s = [...xs].sort((a, b) => a - b);
  const mid = Math.floor(s.length / 2);
  return s.length % 2 ? s[mid]! : (s[mid - 1]! + s[mid]!) / 2;
}

export function buildPerformance(fleet: readonly FleetEpisode[], gate: string, grader: string | undefined): PerformanceView {
  const rows = forGate(fleet, gate);
  const distribution = distributionOf(rows);
  const graded = grader === "model" || grader === "hybrid";
  const metrics: MetricView[] = [
    {
      id: "distribution",
      label: "Results across episodes",
      state: distribution.total > 0 ? "measured" : "not_measured",
      value: distribution.total > 0 ? `${distribution.pass} pass · ${distribution.warn} warn · ${distribution.fail} fail · ${distribution.unknown} unknown` : null,
      measures: "How the verdicts of this check are spread over every episode that has run it.",
      note: distribution.total > 0 ? `Counted over ${distribution.total} ${distribution.total === 1 ? "episode" : "episodes"} in the database, one verdict per episode (the worst).` : "No episode has stored a result for this check yet.",
    },
  ];
  if (graded) {
    metrics.push(
      { id: "false_pass", label: "False-pass rate", state: "not_measured", value: null, measures: "How often this check passed something it should have failed.", note: "Not measured yet: needs the calibration run." },
      {
        id: "agreement",
        label: "Agreement with reference answers",
        state: "not_measured",
        value: null,
        measures: "How often this check agrees with a reference answer for the same case.",
        note: "Not measured yet: needs the calibration run. The reference answers come from a stronger model (not human labels).",
      },
      { id: "stability", label: "Same answer on repeated runs", state: "not_measured", value: null, measures: "Whether the check gives the same verdict when run several times on the same case.", note: "Not measured yet: needs repeated trials (one trial is run today)." },
    );
  } else {
    metrics.push({ id: "regression", label: "Regression coverage", state: "not_measured", value: null, measures: "Whether a fixed set of cases proves this check still catches what it must.", note: "Not measured yet: the regression suite is not built." });
  }
  const latencies = rows.flatMap((x) => x.rs.flatMap((r) => (typeof r.latency_ms === "number" ? [r.latency_ms] : [])));
  if (latencies.length > 0) {
    metrics.push({ id: "latency", label: "Model time to judge", state: "measured", value: seconds(median(latencies)), measures: "The model time the worker reported for this check's live judge call.", note: `Median of ${latencies.length} recorded ${latencies.length === 1 ? "run" : "runs"}.` });
  }
  return { episodes: distribution.total, distribution, metrics };
}

/** Real failure categories from stored results: the failed criterion where one is stored, else the sub-gate, else the gate's own verdict. */
export function buildFailures(fleet: readonly FleetEpisode[], gate: string): FailuresView {
  const cats = new Map<string, FailureCategory>();
  for (const { e, rs } of forGate(fleet, gate)) {
    for (const r of rs) {
      const v = asVerdict(r.verdict);
      if (v !== "fail" && v !== "warn") continue;
      const bad = (r.criteria ?? []).filter((c) => c.result === "fail" || c.result === "warn");
      const entries = bad.length > 0 ? bad.map((c) => ({ id: c.id, label: criterionLabel(c.id), why: c.why, sev: c.result as "fail" | "warn" })) : [{ id: r.sub_gate || "overall", label: r.sub_gate ? criterionLabel(r.sub_gate) : "Overall result", why: r.why, sev: v }];
      for (const en of entries) {
        const cur = cats.get(en.id) ?? { id: en.id, label: en.label, count: 0, severity: en.sev, examples: [] as FailureExample[] };
        cur.count += 1;
        if (en.sev === "fail") cur.severity = "fail";
        if (cur.examples.length < MAX_EXAMPLES && !cur.examples.some((x) => x.episodeId === e.episodeId)) cur.examples.push({ episodeId: e.episodeId, episodeLabel: e.label, why: en.why });
        cats.set(en.id, cur);
      }
    }
  }
  const categories = [...cats.values()].sort((a, b) => (a.severity === b.severity ? b.count - a.count : a.severity === "fail" ? -1 : 1));
  return { categories, empty: categories.length === 0 ? "No failures recorded yet" : null };
}
