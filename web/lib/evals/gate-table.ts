// The gate results table model (HAR-145, Braintrust-style logs/experiment table). Pure: it turns the persisted gate results
// (B1-B9, D1-D10, S1-S5, GET /episodes/{id}/gate-results) into rows, and owns the filters, the severity sort, the saved views
// and the per-bucket summary. A gate with no stored result is a "not measured" row (verdict null): never a pass, never a
// guess. A pass that cites no evidence, and any verdict we do not recognise, is "unknown" (rule R1). No aggregate score exists.
import type { GateResult } from "@/lib/api/types";
import { displayStatus, type DisplayStatus } from "./naming";
import { humanizeObserved } from "./observed";

export type GateVerdict = "pass" | "warn" | "fail" | "unknown";
export type BucketKey = "context" | "decision" | "system";
export type GraderKind = "deterministic" | "model" | "hybrid";

export const BUCKETS: readonly { id: BucketKey; label: string }[] = [
  { id: "context", label: "Context" },
  { id: "decision", label: "Decision" },
  { id: "system", label: "System" },
];
const BUCKET_INDEX: Readonly<Record<BucketKey, number>> = { context: 0, decision: 1, system: 2 };
const PREFIX: Readonly<Record<string, BucketKey>> = { B: "context", D: "decision", S: "system" };
/** The registry grader names the gate's grader; the stored kind is only a fallback (it cannot say "hybrid"). */
export function effectiveGrader(registry: string | undefined, stored: GraderKind): GraderKind {
  const v = (registry ?? "").trim().toLowerCase();
  if (v.startsWith("hybrid")) return "hybrid";
  if (v.startsWith("model")) return "model";
  if (v.startsWith("deterministic")) return "deterministic";
  return stored;
}

const GATE = /^([BDS])([1-9]\d?)$/;

/** The "order by failures first" rank: lower is shown first; a not-measured row has nothing to show and goes last. */
const SEVERITY: Readonly<Record<GateVerdict, number>> = { fail: 0, warn: 1, unknown: 2, pass: 3 };
const NOT_MEASURED_RANK = 4;

export type GateMode = "live_required" | "live_conditional" | "offline_benchmark" | "continuous_aggregate";

export interface GateCatalogEntry {
  id: string;
  question: string;
  improves: string;
  /** HAR-97 execution mode; a catalog entry without one is read as live_required. */
  mode?: GateMode;
  /** The registry grader ("deterministic", "model", "hybrid"); the stored result kind knows only two of these. */
  grader?: string;
  /** For a live_conditional gate: the situation whose absence reads "Not triggered". */
  notTriggered?: string;
  /** The Cliff message and the failure impact, both registry data. */
  message?: string;
  impact?: string;
}

/** One piece of evidence behind a result, resolved against the episode's real records where it can be. */
export interface EvidenceItem {
  ref: string;
  kind: string;
  id: string;
  label: string;
  /** The real text (an email's summary, a claim's value) when the record could be read; otherwise null. */
  summary: string | null;
  /** The trace span this evidence sits in (the link target on the episode page), when there is one. */
  spanId: string | null;
  href: string | null;
  /** Where the summary comes from, in words ("Email · Nov 9, 2023"); null when unknown. */
  source?: string | null;
}

export interface GateRow {
  key: string;
  gate: string;
  /** The gate's position in flow order: bucket then number (B1..B9, D1..D10, S1..S5). */
  gateOrder: number;
  subGate: string;
  label: string;
  bucket: BucketKey;
  episodeId: string;
  episodeLabel: string;
  question: string;
  /** What was observed; null when the gate is not measured. */
  observed: string | null;
  /** Null: not measured (no stored result). */
  verdict: GateVerdict | null;
  measured: boolean;
  /** Set when a live_conditional gate has no result because its trigger did not occur ("no human edit"): not a pass, not "not measured". */
  notTriggered: string | null;
  mode: GateMode;
  /** M1, M2, M3, ecolite or system, from the registry; null when the catalog does not say. */
  message: string | null;
  /** What a failure changes, from the registry; null when the catalog does not say. Never derived. */
  impact: string | null;
  why: string;
  spanId: string | null;
  evidence: readonly EvidenceItem[];
  improves: string;
  /** The registry grader for the gate, else the stored kind: a hybrid gate stored as deterministic is still hybrid. */
  graderKind: GraderKind | null;
  /** True for model and hybrid gates: no model grader is human-calibrated yet. */
  uncalibrated: boolean;
  graderModel: string | null;
  promptVersion: string | null;
  /** Always false: no grader is human-calibrated yet. */
  calibrated: false;
  /** The judged span's time; null when the trace has none for it. Gate results carry no time of their own. */
  time: string | null;
}

export interface EpisodeInput {
  episode: { id: string; label: string; accountId: string };
  results: readonly GateResult[];
  spanTimes: ReadonlyMap<string, string | null>;
  resolve: (ref: string) => EvidenceItem;
}

export const bucketOfGate = (gate: string): BucketKey | null => {
  const m = GATE.exec(gate);
  return m ? PREFIX[m[1]!]! : null;
};

export function gateOrderOf(gate: string): number {
  const m = GATE.exec(gate);
  return m ? BUCKET_INDEX[PREFIX[m[1]!]!] * 100 + Number(m[2]) : 9999;
}

function readVerdict(raw: string, hasEvidence: boolean): GateVerdict {
  const v = raw.trim().toLowerCase();
  if (v !== "pass" && v !== "warn" && v !== "fail") return "unknown";
  return v === "pass" && !hasEvidence ? "unknown" : v;
}

function measuredRow(input: EpisodeInput, r: GateResult, index: number, entry: GateCatalogEntry | undefined): GateRow | null {
  const bucket = bucketOfGate(r.gate);
  if (bucket === null) return null;
  const evidence = r.evidence_refs.map(input.resolve);
  const spanId = r.span_id || null;
  const kind = effectiveGrader(entry?.grader, r.grader.kind);
  const verdict = readVerdict(r.verdict, evidence.length > 0);
  // A conditional precedent gate that found no precedent is not "unknown": nothing applied. Presentation only; the stored result is unchanged.
  const noPrecedent = verdict === "unknown" && entry?.mode === "live_conditional" && (/^0 precedents$/i.test((r.observed ?? "").trim()) || /no precedent source/i.test(`${r.why} ${r.observed}`));
  return {
    key: `${input.episode.id}:${r.gate}:${r.sub_gate}:${r.judged_object.id}:${index}`,
    gate: r.gate,
    gateOrder: gateOrderOf(r.gate),
    subGate: r.sub_gate,
    label: r.label,
    bucket,
    episodeId: input.episode.id,
    episodeLabel: input.episode.label,
    question: r.question || entry?.question || "",
    observed: r.observed ? humanizeObserved(r.observed) : null,
    verdict: noPrecedent ? null : verdict,
    measured: !noPrecedent,
    notTriggered: noPrecedent ? "no precedents retrieved" : null,
    mode: entry?.mode ?? "live_required",
    message: entry?.message ?? null,
    impact: entry?.impact ?? null,
    why: r.why,
    spanId,
    evidence,
    improves: r.improves || entry?.improves || "",
    graderKind: kind,
    uncalibrated: kind !== "deterministic",
    graderModel: r.grader.model ?? null,
    promptVersion: r.grader.prompt_version ?? null,
    calibrated: false,
    time: spanId ? (input.spanTimes.get(spanId) ?? null) : null,
  };
}

export const isPerEpisode = (mode: GateMode | undefined): boolean => mode === undefined || mode === "live_required" || mode === "live_conditional";

function missingRow(input: EpisodeInput, entry: GateCatalogEntry): GateRow {
  return {
    key: `${input.episode.id}:${entry.id}:not-measured`,
    gate: entry.id,
    gateOrder: gateOrderOf(entry.id),
    subGate: "",
    label: "",
    bucket: bucketOfGate(entry.id)!,
    episodeId: input.episode.id,
    episodeLabel: input.episode.label,
    question: entry.question,
    observed: null,
    verdict: null,
    measured: false,
    notTriggered: entry.mode === "live_conditional" ? (entry.notTriggered ?? "its trigger did not occur") : null,
    mode: entry.mode ?? "live_required",
    message: entry.message ?? null,
    impact: entry.impact ?? null,
    why: "",
    spanId: null,
    evidence: [],
    improves: entry.improves,
    graderKind: null,
    uncalibrated: false,
    graderModel: null,
    promptVersion: null,
    calibrated: false,
    time: null,
  };
}

/**
 * Every stored result as a row, plus a row for each per-episode catalog gate with no result: "not measured" for a live_required
 * gate, "not triggered" for a live_conditional one. Offline and continuous gates are not per-episode: they get no missing row.
 */
export function buildGateRows(episodes: readonly EpisodeInput[], catalog: readonly GateCatalogEntry[]): GateRow[] {
  const byId = new Map(catalog.map((c) => [c.id, c]));
  const out: GateRow[] = [];
  for (const input of episodes) {
    const seen = new Set<string>();
    input.results.forEach((r, i) => {
      const row = measuredRow(input, r, i, byId.get(r.gate));
      if (row) {
        out.push(row);
        seen.add(r.gate);
      }
    });
    for (const entry of catalog) if (bucketOfGate(entry.id) && isPerEpisode(entry.mode) && !seen.has(entry.id)) out.push(missingRow(input, entry));
  }
  return out;
}

/** The row's display status: NOT RUN (no result), NOT APPLICABLE (trigger absent) or its verdict. */
export const rowStatus = (r: GateRow): DisplayStatus => displayStatus(r.verdict, r.notTriggered !== null);

export const severityOf = (v: GateVerdict | null): number => (v === null ? NOT_MEASURED_RANK : SEVERITY[v]);

/** Failures first, then warnings, unknown, passes, not measured; ties by flow order, then episode label, then sub-gate. */
export function compareRows(a: GateRow, b: GateRow): number {
  return (
    severityOf(a.verdict) - severityOf(b.verdict) ||
    a.gateOrder - b.gateOrder ||
    a.episodeLabel.localeCompare(b.episodeLabel) ||
    a.subGate.localeCompare(b.subGate)
  );
}

export interface GateFilters {
  buckets: readonly BucketKey[];
  gates: readonly string[];
  verdicts: readonly GateVerdict[];
  episodes: readonly string[];
  graders: readonly GraderKind[];
  modes: readonly GateMode[];
  impacts: readonly string[];
  /** The six display statuses (PASS WARN FAIL UNKNOWN NOT RUN NOT APPLICABLE). */
  statuses: readonly DisplayStatus[];
  notMeasured: boolean;
  notTriggered: boolean;
  query: string;
}

export const EMPTY_FILTERS: GateFilters = { buckets: [], gates: [], verdicts: [], episodes: [], graders: [], modes: [], impacts: [], statuses: [], notMeasured: false, notTriggered: false, query: "" };

/** Adds the value when absent, removes it when present; the input is never changed. */
export const toggleIn = <T>(list: readonly T[], value: T): T[] => (list.includes(value) ? list.filter((x) => x !== value) : [...list, value]);

const has = <T>(list: readonly T[], v: T | null): boolean => list.length === 0 || (v !== null && list.includes(v));

function haystack(r: GateRow): string {
  return [r.gate, r.subGate, r.label, r.episodeLabel, r.question, r.observed ?? "", r.why, r.improves, ...r.evidence.map((e) => `${e.label} ${e.summary ?? ""}`)].join("\n").toLowerCase();
}

/** AND across filter kinds, OR within one. The query is plain text (never a pattern). */
export function filterRows(rows: readonly GateRow[], f: GateFilters): GateRow[] {
  const q = f.query.trim().toLowerCase();
  return rows.filter(
    (r) =>
      has(f.buckets, r.bucket) &&
      has(f.gates, r.gate) &&
      has(f.verdicts, r.verdict) &&
      has(f.episodes, r.episodeId) &&
      has(f.graders, r.graderKind) &&
      has(f.modes, r.mode) &&
      has(f.impacts, r.impact) &&
      has(f.statuses, rowStatus(r)) &&
      (!f.notMeasured || (!r.measured && r.notTriggered === null)) &&
      (!f.notTriggered || r.notTriggered !== null) &&
      (q === "" || haystack(r).includes(q)),
  );
}

/** The Default view leads with measured results: unless expanded, not-measured rows fold into a count. */
export function collapseNotMeasured(rows: readonly GateRow[], expanded: boolean): { rows: GateRow[]; hidden: number } {
  if (expanded) return { rows: [...rows], hidden: 0 };
  const measured = rows.filter((r) => r.measured);
  return { rows: measured, hidden: rows.length - measured.length };
}

export interface GateView {
  id: string;
  label: string;
  filters: GateFilters;
}

export const VIEWS: readonly GateView[] = [
  { id: "default", label: "Default", filters: EMPTY_FILTERS },
  { id: "failures", label: "Failures & warnings", filters: { ...EMPTY_FILTERS, verdicts: ["fail", "warn"] } },
  { id: "unknown", label: "Unknown", filters: { ...EMPTY_FILTERS, verdicts: ["unknown"] } },
  { id: "model", label: "Model-graded", filters: { ...EMPTY_FILTERS, graders: ["model", "hybrid"] } },
];

export const viewById = (id: string): GateView => VIEWS.find((v) => v.id === id) ?? VIEWS[0]!;

/** The rows of a saved view, failures first. An unrecognised view id is Default. */
export const applyView = (rows: readonly GateRow[], id: string): GateRow[] => filterRows(rows, viewById(id).filters).sort(compareRows);

export interface VerdictCounts {
  pass: number;
  warn: number;
  fail: number;
  unknown: number;
  notMeasured: number;
  notTriggered: number;
}

export interface BucketSummary {
  bucket: BucketKey;
  label: string;
  counts: VerdictCounts;
  /** Current minus the previous run, for runs of the SAME episode only; null means "first run". */
  delta: VerdictCounts | null;
}

const zero = (): VerdictCounts => ({ pass: 0, warn: 0, fail: 0, unknown: 0, notMeasured: 0, notTriggered: 0 });

export function countRows(rows: readonly GateRow[]): VerdictCounts {
  const c = zero();
  for (const r of rows) {
    if (r.verdict === null) c[r.notTriggered !== null ? "notTriggered" : "notMeasured"] += 1;
    else c[r.verdict] += 1;
  }
  return c;
}

const minus = (a: VerdictCounts, b: VerdictCounts): VerdictCounts => ({
  pass: a.pass - b.pass,
  warn: a.warn - b.warn,
  fail: a.fail - b.fail,
  unknown: a.unknown - b.unknown,
  notMeasured: a.notMeasured - b.notMeasured,
  notTriggered: a.notTriggered - b.notTriggered,
});

const sum = (list: readonly VerdictCounts[]): VerdictCounts =>
  list.reduce((t, c) => ({ pass: t.pass + c.pass, warn: t.warn + c.warn, fail: t.fail + c.fail, unknown: t.unknown + c.unknown, notMeasured: t.notMeasured + c.notMeasured, notTriggered: t.notTriggered + c.notTriggered }), zero());

/**
 * One summary per bucket. `previous` maps a bucket to the counts of an earlier run per episode id; the delta covers only the
 * episodes that have one, so two different episodes are never compared as if one were a regression of the other.
 */
export function bucketSummaries(rows: readonly GateRow[], previous: ReadonlyMap<string, ReadonlyMap<string, VerdictCounts>>): BucketSummary[] {
  return BUCKETS.map(({ id, label }) => {
    const mine = rows.filter((r) => r.bucket === id);
    const before = previous.get(id);
    let delta: VerdictCounts | null = null;
    if (before && before.size > 0) {
      const now = sum([...before.keys()].map((ep) => countRows(mine.filter((r) => r.episodeId === ep))));
      delta = minus(now, sum([...before.values()]));
    }
    return { bucket: id, label, counts: countRows(mine), delta };
  });
}

/** The distinct episodes of a row set, in first-seen order. */
export function episodesOf(rows: readonly GateRow[]): { id: string; label: string }[] {
  const seen = new Map<string, string>();
  for (const r of rows) if (!seen.has(r.episodeId)) seen.set(r.episodeId, r.episodeLabel);
  return [...seen].map(([id, label]) => ({ id, label }));
}
