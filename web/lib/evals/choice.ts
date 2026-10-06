// Who chose what (HAR-129 §8: gtm_ai's preference beside the human's choice): the two options by letter and
// title, for the eval page header.
import type { HumanStrategyDecision } from "@/lib/api/types";
import type { Matrix } from "./matrix";

export interface OptionRef {
  letter: string;
  title: string;
  candidateId: string;
}

export interface ChoiceSummary {
  ghostPick: OptionRef | null;
  chosen: OptionRef | null;
  actor: string | null;
  /** The human took Ghost's pick: the choice confirms rather than contradicts it. */
  agreed: boolean;
}

const ref = (c: Matrix["columns"][number] | undefined): OptionRef | null => (c ? { letter: c.letter, title: c.title, candidateId: c.candidateId } : null);

export function choiceSummary(matrix: Matrix, decision: HumanStrategyDecision | null): ChoiceSummary {
  const ghostPick = ref(matrix.columns.find((c) => c.isGhostPick));
  const chosen = ref(matrix.columns.find((c) => c.isChosen));
  return { ghostPick, chosen, actor: chosen ? (decision?.actor_label ?? null) : null, agreed: !!chosen && chosen.candidateId === ghostPick?.candidateId };
}
