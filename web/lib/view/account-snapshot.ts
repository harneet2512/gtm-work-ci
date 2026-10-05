// The account snapshot (WP24, HAR-122): commitments, the next milestone and the latest agent action,
// derived only from fields the contracts already expose — account_state.v1 list/scalar fields and the
// timeline's activity_type vocabulary. Nothing is inferred from outside the contract.
import type { AccountState, Activity } from "@/lib/api/types";

export interface Commitment {
  text: string;
  status: string | null;
  dueAt: string | null;
}

export interface AccountSnapshot {
  /** current_commitments items; null when the field is unknown (never an empty lie). */
  commitments: Commitment[] | null;
  /** next_milestone's value; null when unknown. */
  nextMilestone: string | null;
  /** The newest AgentActionExecuted/AgentActionProposed activity in the loaded window. */
  latestAgentAction: Activity | null;
}

const AGENT_ACTION_TYPES = new Set(["AgentActionExecuted", "AgentActionProposed"]);

/** The newest agent action the timeline carries (executed preferred; then proposed). */
export function latestAgentAction(activities: readonly Activity[]): Activity | null {
  const actions = activities.filter((a) => AGENT_ACTION_TYPES.has(a.activity_type));
  if (actions.length === 0) return null;
  const executed = actions.filter((a) => a.activity_type === "AgentActionExecuted");
  const pool = executed.length > 0 ? executed : actions;
  return pool.reduce((best, a) => (Date.parse(a.occurred_at) > Date.parse(best.occurred_at) ? a : best));
}

export function buildSnapshot(state: AccountState | null, activities: readonly Activity[]): AccountSnapshot {
  const field = state?.fields.current_commitments;
  const commitments: Commitment[] | null =
    field && field.known && Array.isArray(field.value)
      ? field.value.map((item) => ({ text: item.text, status: item.status ?? null, dueAt: item.due_at ?? null }))
      : null;
  const milestone = state?.fields.next_milestone;
  return {
    commitments,
    nextMilestone: milestone?.known === true && typeof milestone.value === "string" ? milestone.value : null,
    latestAgentAction: latestAgentAction(activities),
  };
}
