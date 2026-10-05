// The /evals explorer model (HAR-145): every eval result across the listed runs, flattened to one row
// per (run × candidate × eval) with the words the matrix already uses — verdict, plain name, reason —
// plus sort/filter/search and a two-run comparison. Only bundle items a run actually produced become
// rows; "not checked" is a per-candidate matrix concept, not an explorer row.
import type { AgentRun, HumanStrategyDecision, RunStrategies } from "@/lib/api/types";
import { compareVerdicts, evalName, type CellVerdict } from "@/lib/evals/vocabulary";

export interface EvalRow {
  resultId: string;
  runId: string;
  episodeId: string | null;
  /** Candidate letter within its run ("A"), and its title. */
  candidate: string;
  candidateTitle: string;
  isGhostPick: boolean;
  isChosen: boolean;
  evalType: string;
  name: string;
  verdict: CellVerdict;
  blocking: boolean;
  kind: string | null;
  evidenceClass: string | null;
  reason: string | null;
  model: string | null;
  createdAt: string | null;
}

export interface RunEvalSlice {
  run: AgentRun;
  strategies: RunStrategies | null;
  decision: HumanStrategyDecision | null;
}

const letter = (i: number): string => String.fromCharCode(65 + (i % 26));

/** One row per produced eval result; bundles a run never generated contribute nothing. */
export function flattenResults(slices: readonly RunEvalSlice[]): EvalRow[] {
  const rows: EvalRow[] = [];
  for (const { run, strategies, decision } of slices) {
    const candidates = strategies?.strategy_set?.candidates ?? [];
    const byId = new Map(candidates.map((c, i) => [c.candidate_id, { letter: letter(i), title: c.title ?? c.strategy_type, pick: Boolean(c.preferred_by_agent) }]));
    for (const b of strategies?.eval_bundles ?? []) {
      const cand = b.strategy_candidate_id ? byId.get(b.strategy_candidate_id) : undefined;
      for (const item of b.items ?? []) {
        const r = item.result;
        rows.push({
          resultId: r?.id ?? `${b.id}:${item.eval_type}`,
          runId: run.id,
          episodeId: run.generation?.decision_episode_id ?? null,
          candidate: cand?.letter ?? "—",
          candidateTitle: cand?.title ?? "",
          isGhostPick: cand?.pick ?? false,
          isChosen: decision?.selected_candidate_id != null && decision.selected_candidate_id === b.strategy_candidate_id,
          evalType: item.eval_type,
          name: evalName(item.eval_type),
          verdict: item.verdict as CellVerdict,
          blocking: r?.blocking === true && item.verdict === "fail",
          kind: r?.kind ?? null,
          evidenceClass: r?.evidence_class ?? null,
          reason: r?.reason ?? item.relevance_reason ?? null,
          model: r?.model ?? null,
          createdAt: r?.created_at ?? null,
        });
      }
    }
  }
  return rows;
}

export type SortKey = "severity" | "name" | "run" | "candidate" | "created";
export interface ExplorerQuery {
  q: string;
  verdict: string | null;
  kind: string | null;
  blocking: boolean;
  sort: SortKey;
  dir: "asc" | "desc";
}

export const DEFAULT_QUERY: ExplorerQuery = { q: "", verdict: null, kind: null, blocking: false, sort: "severity", dir: "asc" };

export function filterRows(rows: readonly EvalRow[], q: ExplorerQuery): EvalRow[] {
  const needle = q.q.trim().toLowerCase();
  return rows.filter(
    (r) =>
      (!needle || `${r.name} ${r.evalType} ${r.reason ?? ""} ${r.candidateTitle} ${r.model ?? ""}`.toLowerCase().includes(needle)) &&
      (!q.verdict || r.verdict === q.verdict) &&
      (!q.kind || r.kind === q.kind || r.evidenceClass === q.kind) &&
      (!q.blocking || r.blocking),
  );
}

const by: Record<SortKey, (a: EvalRow, b: EvalRow) => number> = {
  severity: (a, b) => compareVerdicts(a.verdict, b.verdict),
  name: (a, b) => a.name.localeCompare(b.name),
  run: (a, b) => a.runId.localeCompare(b.runId),
  candidate: (a, b) => a.candidate.localeCompare(b.candidate),
  created: (a, b) => (a.createdAt ?? "").localeCompare(b.createdAt ?? ""),
};

export function sortRows(rows: readonly EvalRow[], q: ExplorerQuery): EvalRow[] {
  const sorted = [...rows].sort(by[q.sort]);
  return q.dir === "desc" ? sorted.reverse() : sorted;
}

/** eval_type × run → verdict, for the two-run comparison. Types only one run checked show "not_checked". */
export interface CompareRow {
  evalType: string;
  name: string;
  a: CellVerdict;
  b: CellVerdict;
  /**
   * Same verdict on both; checked on one side only; improved / regressed when run B is better / worse than run A on
   * the fail < warn < pass scale; otherwise a plain change. Unsure (abstain) and not-relevant are not ranked against
   * the others, so a move to or from them is only "changed".
   */
  delta: "same" | "improved" | "regressed" | "changed" | "one-sided";
}

export function compareRuns(rows: readonly EvalRow[], runA: string, runB: string): CompareRow[] {
  const types = new Map<string, string>();
  const a = new Map<string, CellVerdict>();
  const b = new Map<string, CellVerdict>();
  for (const r of rows) {
    types.set(r.evalType, r.name);
    if (r.runId === runA) a.set(r.evalType, worstOf(a.get(r.evalType), r.verdict));
    if (r.runId === runB) b.set(r.evalType, worstOf(b.get(r.evalType), r.verdict));
  }
  return [...types.keys()].sort().map((t) => {
    const va = a.get(t) ?? "not_checked";
    const vb = b.get(t) ?? "not_checked";
    return { evalType: t, name: types.get(t)!, a: va, b: vb, delta: deltaOf(va, vb) };
  });
}

/** Rank on the comparable scale only; abstain, not_relevant and not_checked have no rank. */
const RANK: Partial<Record<CellVerdict, number>> = { fail: 0, warn: 1, pass: 2 };

function deltaOf(a: CellVerdict, b: CellVerdict): CompareRow["delta"] {
  if (a === b) return "same";
  if (a === "not_checked" || b === "not_checked") return "one-sided";
  const ra = RANK[a];
  const rb = RANK[b];
  if (ra === undefined || rb === undefined) return "changed";
  return rb > ra ? "improved" : "regressed";
}

const worstOf = (cur: CellVerdict | undefined, next: CellVerdict): CellVerdict =>
  cur === undefined || compareVerdicts(next, cur) < 0 ? next : cur;
