// The knowledge trace of one decision ("retrieval is not influence"): which company knowledge was retrieved, which of it
// was judged applicable, which the options used, whether the option the human chose was one of them, and whether gtm_ai's
// inference of the human's judgment cites it. Retrieval comes from the run's own record: the build_context step keeps
// what was retrieved, applicable, blocked by an exception and used in detail.knowledge_attribution. A run without that
// record says so, instead of implying a retrieval it cannot show.
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

/** What the run recorded about company knowledge before any option was written, as counts. */
export interface KnowledgeAttribution {
  /** The time the knowledge was read as of (the replay clock of the run). */
  asOf: string;
  retrieved: number;
  applicable: number;
  /** Retrieved knowledge whose conditions held but an exception overrode it. */
  exceptionBlocked: number;
  /** Knowledge the final options cite. */
  used: number;
}

export interface KnowledgeTrace {
  /** True when the run recorded its knowledge retrieval (an empty retrieval is a recorded one). */
  retrievedTraced: boolean;
  attribution: KnowledgeAttribution | null;
  used: KnowledgeUse[];
  /**
   * The human chose an option that cites company knowledge. This is citation in the chosen option, NOT a
   * measure of influence: nothing here shows the knowledge changed the decision.
   */
  inChosenOption: boolean;
}

const isIdList = (v: unknown): v is string[] => Array.isArray(v) && v.every((x) => typeof x === "string");

/**
 * Reads agent_run_steps[build_context].detail.knowledge_attribution. A record that is missing or does not have the
 * four id lists and the clock is treated as not recorded: half a record is not shown as a retrieval.
 */
export function readAttribution(run: EvalPageData["run"] | null): KnowledgeAttribution | null {
  const step = run?.steps?.find((s) => s.step === "build_context");
  const raw = step?.detail?.knowledge_attribution;
  if (raw === null || typeof raw !== "object") return null;
  const a = raw as Record<string, unknown>;
  if (typeof a.as_of !== "string" || !isIdList(a.retrieved) || !isIdList(a.applicable) || !isIdList(a.exception_blocked) || !isIdList(a.used)) return null;
  return { asOf: a.as_of, retrieved: a.retrieved.length, applicable: a.applicable.length, exceptionBlocked: a.exception_blocked.length, used: a.used.length };
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
  const attribution = readAttribution(data.run);
  return { retrievedTraced: attribution !== null, attribution, used, inChosenOption: used.some((u) => u.inChosen) };
}
