// "How was the result produced?" (HAR-149 section 5): one row per criterion with its own result, then the folded result.
// Rows come from the persisted per-criterion results; for a result stored without them the sub-gate results are the rows,
// and a gate that stored one verdict gets one honest row, never invented ones. No numeric score is shown: the backend
// computes none, so `score` is always null.
import type { GateResult } from "@/lib/api/types";
import { criterionLabel } from "./criterion-words";

export type CriterionResult = "pass" | "warn" | "fail" | "unknown" | "not_applicable";
export type FoldedVerdict = "pass" | "warn" | "fail" | "unknown";

export const FOLDING_RULE = "Any FAIL makes the result FAIL. Otherwise any WARN makes it WARN. A PASS needs every required check to pass, with evidence; a check that cannot be judged keeps the result UNKNOWN.";

export interface CriterionRow {
  id: string;
  label: string;
  result: CriterionResult;
  why: string;
  evidenceCount: number;
  /** stored: a persisted criterion; sub_gate: a separate stored result of the gate; result: the gate's single verdict. */
  origin: "stored" | "sub_gate" | "result";
}

export interface CriteriaGroup {
  key: string;
  /** The judged object in words (an option's title), or null when the gate judges the episode as a whole. */
  title: string | null;
  rows: CriterionRow[];
  result: FoldedVerdict;
}

export interface CriteriaView {
  groups: CriteriaGroup[];
  result: FoldedVerdict | null;
  folding: string;
  storedCriteria: boolean;
  /** Said when the rows are not stored per-criterion results. */
  note: string | null;
  score: null;
}

const RANK: Readonly<Record<CriterionResult, number>> = { fail: 0, warn: 1, unknown: 2, pass: 3, not_applicable: 4 };
const FOLD_RANK: Readonly<Record<FoldedVerdict, number>> = { pass: 0, unknown: 1, warn: 2, fail: 3 };

const asVerdict = (v: string): FoldedVerdict => (v === "pass" || v === "warn" || v === "fail" ? v : "unknown");
const asResult = (v: string): CriterionResult => (v === "not_applicable" ? v : asVerdict(v));

/** Worst result of a list (fail > warn > unknown > pass); not applicable counts for nothing; empty is null. */
export function foldResults(results: readonly CriterionResult[]): FoldedVerdict | null {
  let out: FoldedVerdict | null = null;
  for (const r of results) {
    if (r === "not_applicable") continue;
    if (out === null || FOLD_RANK[r] > FOLD_RANK[out]) out = r;
  }
  return out;
}

/** Failures first, then warnings, unknown, passes, not applicable; ties keep the judge's order. */
export function worstFirst<T extends { result: CriterionResult }>(rows: readonly T[]): T[] {
  return rows.map((r, i) => ({ r, i })).sort((a, b) => RANK[a.r.result] - RANK[b.r.result] || a.i - b.i).map((x) => x.r);
}

function rowsOf(r: GateResult): CriterionRow[] {
  const stored = r.criteria ?? [];
  if (stored.length > 0) {
    return stored.map((c) => ({ id: c.id, label: criterionLabel(c.id), result: asResult(c.result), why: c.why, evidenceCount: c.evidence_refs.length, origin: "stored" as const }));
  }
  const id = r.sub_gate || r.gate;
  return [{ id, label: r.sub_gate ? criterionLabel(r.sub_gate) : "Overall result", result: asVerdict(r.verdict), why: r.why, evidenceCount: r.evidence_refs.length, origin: r.sub_gate ? ("sub_gate" as const) : ("result" as const) }];
}

/**
 * The criteria view of a gate in one episode. `results` are all stored results of the gate; they are grouped by the object
 * they judged (the three options of D2) and `titleOf` names each group.
 */
export function buildCriteriaView(results: readonly GateResult[], titleOf: (r: GateResult) => string | null): CriteriaView {
  const byObject = new Map<string, GateResult[]>();
  for (const r of results) {
    const key = `${r.judged_object.type}:${r.judged_object.id}`;
    byObject.set(key, [...(byObject.get(key) ?? []), r]);
  }
  const groups: CriteriaGroup[] = [...byObject.entries()].map(([key, rs]) => {
    const all = rs.flatMap(rowsOf);
    // A deterministic sub-gate that repeats a stored criterion (D3's "no blocked option") is shown once.
    const stored = new Set(all.filter((x) => x.origin === "stored").map((x) => x.id));
    const rows = all.filter((x) => !(x.origin === "sub_gate" && stored.has(x.id)));
    const verdicts = rs.map((r) => asVerdict(r.verdict));
    const fold = foldResults([...rows.map((x) => x.result), ...verdicts]) ?? "unknown";
    return { key, title: titleOf(rs[0]!), rows, result: fold };
  });
  const storedCriteria = results.some((r) => (r.criteria ?? []).length > 0);
  const single = results.length > 0 && !storedCriteria && groups.every((g) => g.rows.every((x) => x.origin === "result"));
  return {
    groups,
    result: groups.length === 0 ? null : (foldResults(groups.map((g) => g.result)) ?? "unknown"),
    folding: FOLDING_RULE,
    storedCriteria,
    note: groups.length === 0 ? null : single ? "This result was stored as one verdict; the individual checks were not recorded separately for this run." : storedCriteria ? null : "The checks shown are the gate's separately stored results; per-criterion detail was not recorded for this run.",
    score: null,
  };
}
