import type { AccountState } from "@/lib/api/types";

export type BadgeKind = "CANDIDATE" | "CONFIRMED" | "UNRESOLVED" | "none";

export interface TransitionBadge {
  kind: BadgeKind;
  detail: string;
  confidence?: number;
  missingRequired: string[];
}

const NONE: TransitionBadge = { kind: "none", detail: "No open or confirmed transition", missingRequired: [] };

/** Reads the badge from the account state endpoint: the open transition wins over the confirmed state. */
export function transitionBadge(state: AccountState | null): TransitionBadge {
  const open = state?.open_transition;
  if (open) {
    const target = open.to_state_candidate ?? "no target yet";
    return {
      kind: open.status === "CANDIDATE" ? "CANDIDATE" : "UNRESOLVED",
      detail: `${open.from_state} -> ${target}`,
      confidence: open.confidence,
      missingRequired: open.missing_facts.filter((f) => f.required).map((f) => f.key),
    };
  }
  const confirmed = state?.relationship_state;
  if (confirmed && confirmed.value !== "unknown") {
    return { kind: "CONFIRMED", detail: `Confirmed ${confirmed.value}`, missingRequired: [] };
  }
  return NONE;
}
