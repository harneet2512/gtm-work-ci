// One row of the episode's knowledge-mutations panel (HAR-145): what the episode did to a company-knowledge object. The
// operation is shown as the core names it (DEMOTE included) beside a plain word; scope and the knowledge's version at
// that time come straight from the mutation. Nothing here is a verdict or a score.
import type { KnowledgeMutation } from "@/lib/api/types";

export const OPERATION_WORD: Record<KnowledgeMutation["operation"], string> = {
  CREATE: "the episode seeded this knowledge",
  SUPPORT: "supporting evidence added",
  COUNTEREXAMPLE: "a counterexample was recorded",
  PROMOTE: "moved up the confidence ladder",
  DEMOTE: "moved down the confidence ladder",
  DISPUTE: "moved to disputed",
  STALE: "moved to stale",
  REFINE: "wording rewritten",
  NARROW: "scope narrowed",
};

export const SCOPE_WORD: Record<KnowledgeMutation["scope"], string> = {
  undetermined: "scope not determined",
  account_specific: "this account only",
  reusable_candidate: "reusable candidate",
};

const HUMAN_WORD: Record<KnowledgeMutation["human_verdict"], string> = {
  none: "no human judgment",
  pending: "awaiting the human's confirmation",
  confirmed: "confirmed by the human",
  corrected: "corrected by the human",
};

const EVIDENCE_WORD: Record<KnowledgeMutation["evidence"]["kind"], string> = {
  decision_episode: "the decision episode",
  human_decision: "the human's decision",
  customer_reaction: "a customer reaction",
  business_outcome: "a business outcome",
  counterexample: "a counterexample",
};

export interface MutationRow {
  id: string;
  operation: KnowledgeMutation["operation"];
  operationWord: string;
  knowledgeId: string;
  key: string;
  title: string;
  scope: string;
  /** The knowledge's version in force when the mutation happened, with the time. */
  version: string;
  status: string;
  human: string;
  evidence: string;
}

function statusLine(before: KnowledgeMutation["status_before"], after: KnowledgeMutation["status"]): string {
  if (before === null) return `new → ${after}`;
  return before === after ? `${after} (unchanged)` : `${before} → ${after}`;
}

export function mutationRow(m: KnowledgeMutation): MutationRow {
  const note = m.evidence.note ? `: ${m.evidence.note}` : "";
  return {
    id: m.id,
    operation: m.operation,
    operationWord: OPERATION_WORD[m.operation],
    knowledgeId: m.knowledge_id,
    key: m.knowledge_key ?? "—",
    title: m.title,
    scope: SCOPE_WORD[m.scope],
    version: `v${m.version} as of ${m.occurred_at}`,
    status: statusLine(m.status_before, m.status),
    human: HUMAN_WORD[m.human_verdict],
    evidence: `${EVIDENCE_WORD[m.evidence.kind]}${note}`,
  };
}
