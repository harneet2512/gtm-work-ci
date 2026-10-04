// The comparison view of the episode eval page: every routed eval x the three candidates. Each cell is a
// verdict and a one-line reason; Ghost's pick and the human's choice are marked on the columns; rows are
// ordered by severity across candidates so the evals that separate the options come first. An eval the
// router never selected for a candidate is "not checked", which is not the same as "not relevant" (the
// router decided, and said why).
import type { EvalBundleItem, HumanStrategyDecision } from "@/lib/api/types";
import type { CandidateChain } from "@/lib/view/run-chain";
import { compareVerdicts, evalName, evalQuestion, evidenceTag, heldForReview, worstVerdict, type CellVerdict, type Tag } from "./vocabulary";

export interface MatrixColumn {
  candidateId: string;
  letter: string;
  title: string;
  isGhostPick: boolean;
  isChosen: boolean;
  worst: CellVerdict | null;
  held: string | null;
}

export interface MatrixCell {
  candidateId: string;
  verdict: CellVerdict;
  /** The result's reason; for not relevant, the router's reason; null when not checked. */
  reason: string | null;
  blocking: boolean;
  resultId: string | null;
}

export interface MatrixRow {
  evalType: string;
  name: string;
  question: string | null;
  tag: Tag | null;
  cells: MatrixCell[];
  worst: CellVerdict;
}

export interface Matrix {
  columns: MatrixColumn[];
  rows: MatrixRow[];
}

const letter = (i: number): string => String.fromCharCode(65 + (i % 26));

function cellOf(candidateId: string, item: EvalBundleItem | undefined): MatrixCell {
  if (!item) return { candidateId, verdict: "not_checked", reason: null, blocking: false, resultId: null };
  return {
    candidateId,
    verdict: item.verdict,
    reason: item.result?.reason ?? item.relevance_reason,
    blocking: item.result?.blocking ?? false,
    resultId: item.result?.id ?? null,
  };
}

const count = (cells: readonly MatrixCell[], v: CellVerdict) => cells.filter((c) => c.verdict === v).length;

/** Worst verdict across candidates, then more failures, then more warnings, then name. */
function bySeverity(a: MatrixRow, b: MatrixRow): number {
  return (
    compareVerdicts(a.worst, b.worst) ||
    count(b.cells, "fail") - count(a.cells, "fail") ||
    count(b.cells, "warn") - count(a.cells, "warn") ||
    a.name.localeCompare(b.name)
  );
}

export function buildMatrix(chain: readonly CandidateChain[], decision: HumanStrategyDecision | null): Matrix {
  const ordered = [...chain].sort((a, b) => a.candidate.ranking - b.candidate.ranking);
  const columns = ordered.map((c, i): MatrixColumn => {
    const policy = c.bundle?.candidate_policy ?? null;
    return {
      candidateId: c.candidate.candidate_id,
      letter: letter(i),
      title: c.candidate.title,
      isGhostPick: c.candidate.preferred_by_agent,
      isChosen: decision?.selected_candidate_id === c.candidate.candidate_id,
      worst: worstVerdict((c.bundle?.items ?? []).map((item) => item.verdict)),
      held: policy?.status === "restricted" ? heldForReview(policy.reasons) : null,
    };
  });

  const evalTypes: string[] = [];
  for (const c of ordered) for (const item of c.bundle?.items ?? []) if (!evalTypes.includes(item.eval_type)) evalTypes.push(item.eval_type);

  const rows = evalTypes.map((evalType): MatrixRow => {
    const items = ordered.map((c) => c.bundle?.items.find((i) => i.eval_type === evalType));
    const cells = ordered.map((c, i) => cellOf(c.candidate.candidate_id, items[i]));
    const evidenceClass = items.find((i) => i?.result)?.result?.evidence_class ?? null;
    return {
      evalType,
      name: evalName(evalType),
      question: evalQuestion(evalType),
      tag: evidenceClass ? evidenceTag(evidenceClass) : null,
      cells,
      worst: worstVerdict(cells.map((c) => c.verdict)) ?? "not_checked",
    };
  });
  return { columns, rows: rows.sort(bySeverity) };
}
