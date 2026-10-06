// Bucket 2 (decision, human judgment, action) results on the evals page, in the order of the decision flow D1 to D10.
// Every row answers: what question we asked, what we observed, the verdict, why, the trace evidence, what it protects,
// and that it is not yet calibrated. A gate with no stored result reads "Not measured": a missing result is never shown
// as a pass, and a pass that cites no evidence is shown as unknown (rule R1). The older spelling "abstain" and "unknown"
// are the same verdict and are never styled as a pass.

export type RowVerdict = "pass" | "warn" | "fail" | "unknown";

/** One stored gate result as core serves it (gate_result.v1.json). */
export interface GateResultRow {
  gate: string;
  sub_gate?: string;
  label?: string;
  judged_object: { type: string; id: string };
  span_id: string;
  verdict: string;
  question: string;
  observed: string;
  why: string;
  evidence_refs: readonly string[];
  improves: string;
  grader: { kind: "deterministic" | "model"; model?: string; prompt_version?: string };
  calibrated: boolean;
}

export interface GateSourceLite {
  id: string;
  name: string;
  question: string;
  improves: string;
  /** The registry's flag: false when no eval is registered under the gate (it then reads "Not built yet"). */
  built?: boolean;
}

export interface Bucket2Row {
  key: string;
  gate: string;
  subGate: string;
  question: string;
  observed: string;
  verdict: RowVerdict;
  why: string;
  /** Trace evidence: where in the episode trace the row points (span) and the records it rests on. */
  evidence: readonly string[];
  traceHref: string | null;
  improves: string;
  grader: string;
  calibration: "Not yet calibrated" | "Calibrated";
}

export interface Bucket2Gate {
  id: string;
  name: string;
  question: string;
  improves: string;
  built?: boolean;
  /** Empty means "Not measured" (or "Not built yet" when the registry has no eval under the gate). */
  rows: readonly Bucket2Row[];
}

/** Reads a stored or model verdict. abstain and anything unrecognised are unknown, never a pass. */
export function readVerdict(raw: string, evidence: readonly string[]): RowVerdict {
  const v = raw.trim().toLowerCase();
  if (v !== "pass" && v !== "warn" && v !== "fail") return "unknown";
  return evidence.length === 0 ? "unknown" : v;
}

const FLOW = ["D1", "D2", "D3", "D4", "D5", "D6", "D7", "D8", "D9", "D10"] as const;
/** Bucket 1's gates, in order: the same stored results route serves them (gate_results, B1 to B9). */
export const BUCKET1_FLOW = ["B1", "B2", "B3", "B4", "B5", "B6", "B7", "B8", "B9"] as const;
const ORDER = new Map<string, number>(FLOW.map((g, i) => [g, i]));

function graderText(g: GateResultRow["grader"]): string {
  return g.kind === "model" ? `AI judge${g.model ? ` (${g.model})` : ""}` : "Checked from stored records";
}

/** The span id is "kind:ref"; the episode page anchors each stage by its span id. */
export function traceHref(episodeId: string | null, spanId: string): string | null {
  if (episodeId === null || !/^[a-z_]+:[A-Za-z0-9._-]+$/.test(spanId)) return null;
  return `/episodes/${episodeId}#span-${encodeURIComponent(spanId)}`;
}

function toRow(r: GateResultRow, episodeId: string | null, index: number): Bucket2Row {
  return {
    key: `${r.gate}:${r.sub_gate ?? ""}:${r.judged_object.id}:${index}`,
    gate: r.gate,
    subGate: r.sub_gate ?? "",
    question: r.question,
    observed: r.observed,
    verdict: readVerdict(r.verdict, r.evidence_refs),
    why: r.why,
    evidence: r.evidence_refs,
    traceHref: traceHref(episodeId, r.span_id),
    improves: r.improves,
    grader: graderText(r.grader),
    calibration: r.calibrated ? "Calibrated" : "Not yet calibrated",
  };
}

/**
 * Bucket 2's gates D1 to D10 in flow order, each with its stored results (none: not measured). Only the ten Bucket 2
 * gates are read; results of any other gate are ignored. The inputs are never mutated.
 */
export function buildBucket2(gates: readonly GateSourceLite[], results: readonly GateResultRow[], episodeId: string | null): Bucket2Gate[] {
  return buildGateRows(gates, results, episodeId, ORDER);
}

/** Bucket 1's gates B1 to B9 with their stored results from the shared gate-results route (none: not measured). */
export function buildBucket1(gates: readonly GateSourceLite[], results: readonly GateResultRow[], episodeId: string | null): Bucket2Gate[] {
  return buildGateRows(gates, results, episodeId, new Map<string, number>(BUCKET1_FLOW.map((g, i) => [g, i])));
}

function buildGateRows(gates: readonly GateSourceLite[], results: readonly GateResultRow[], episodeId: string | null, order: ReadonlyMap<string, number>): Bucket2Gate[] {
  return gates
    .filter((g) => order.has(g.id))
    .slice()
    .sort((a, b) => order.get(a.id)! - order.get(b.id)!)
    .map((g) => ({
      id: g.id,
      name: g.name,
      question: g.question,
      improves: g.improves,
      built: g.built,
      rows: results.filter((r) => r.gate === g.id).map((r, i) => toRow(r, episodeId, i)),
    }));
}
