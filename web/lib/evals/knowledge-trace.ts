// The knowledge trace of one decision (HAR-97 E7, "retrieval is not influence"): which company knowledge the options
// used, whether the option the human chose was one of them, and whether gtm_ai's inference of the human's judgment
// cites it. Retrieval is reported only if the run traced it; the core's context pulls have no knowledge tool today,
// so the page says so instead of implying a retrieval it cannot show.
import type { EvalPageData } from "@/lib/load-eval-page";
import { buildChain, knowledgeLine } from "@/lib/view/run-chain";
import { buildMatrix } from "./matrix";

export interface KnowledgeUse {
  id: string;
  label: string;
  status: string | null;
  /** Letters of the options that used it. */
  options: string[];
  inChosen: boolean;
  citedByInference: boolean;
}

export interface KnowledgeTrace {
  retrievedTraced: boolean;
  used: KnowledgeUse[];
  /**
   * The human chose an option that cites company knowledge. This is citation in the chosen option, NOT a
   * measure of influence: nothing here shows the knowledge changed the decision (HAR-97 E7).
   */
  inChosenOption: boolean;
}

export function buildKnowledgeTrace(data: EvalPageData): KnowledgeTrace {
  const chain = buildChain(data.strategies);
  const letters = new Map(buildMatrix(chain, data.decision).columns.map((c) => [c.candidateId, c.letter]));
  const chosen = data.decision?.selected_candidate_id ?? null;
  const cited = new Set(data.inference?.evidence.knowledge_refs ?? []);
  const ids = [...new Set(chain.flatMap((c) => c.candidate.knowledge_refs))];
  const used = ids.map((id): KnowledgeUse => {
    const users = chain.filter((c) => c.candidate.knowledge_refs.includes(id));
    const line = knowledgeLine(id, data.knowledge[id]);
    return {
      id,
      label: data.knowledge[id] ? line.label : "Company knowledge (not loaded)",
      status: line.status,
      options: users.map((c) => letters.get(c.candidate.candidate_id) ?? "?"),
      inChosen: users.some((c) => c.candidate.candidate_id === chosen),
      citedByInference: cited.has(id),
    };
  });
  // The run's context pulls name their tool; none of the core's tools retrieves company knowledge today.
  const retrievedTraced = (data.trace?.context_accesses ?? []).some((c) => (c.tool as string) === "knowledge");
  return { retrievedTraced, used, inChosenOption: used.some((u) => u.inChosen) };
}
