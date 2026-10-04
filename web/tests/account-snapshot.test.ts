// The account snapshot (WP24): commitments, next milestone and the latest agent action — all read
// straight off contract fields, with unknown values rendered absent rather than fabricated.
import { describe, expect, it } from "vitest";
import { buildSnapshot, latestAgentAction } from "@/lib/view/account-snapshot";
import type { AccountState, Activity } from "@/lib/api/types";
import { loadExample, loadFixture } from "./contract-validator";

const state = loadExample<AccountState>("account_state");
const before = loadFixture<AccountState>("acme.state-before.json");
const activities = loadFixture<{ items: Activity[] }>("acme.timeline.json").items;

describe("latestAgentAction", () => {
  it("picks the newest AgentAction* — the executed send of the WP24 chain", () => {
    const action = latestAgentAction(activities);
    expect(action).not.toBeNull();
    expect(action!.activity_type).toBe("AgentActionExecuted");
    expect(action!.occurred_at).toBe("2026-09-29T16:05:10Z");
  });

  it("prefers an executed action over a later proposal, and the newest among peers", () => {
    const base = activities.find((a) => a.activity_type === "AgentActionExecuted")!;
    const older: Activity = { ...base, occurred_at: "2026-09-29T16:00:00Z", id: "0ac70000-0000-4000-8000-000000000110" };
    const proposedLater: Activity = { ...base, activity_type: "AgentActionProposed", occurred_at: "2026-09-29T17:00:00Z", id: "0ac70000-0000-4000-8000-000000000111" };
    // A later proposal does not beat an executed action.
    expect(latestAgentAction([proposedLater, older])!.id).toBe(older.id);
    expect(latestAgentAction([proposedLater])!.activity_type).toBe("AgentActionProposed");
  });

  it("is null when the window carries no agent action (an event-scoped, N-1 view)", () => {
    const beforeEvent = activities.filter((a) => a.occurred_at < "2026-09-29T15:42:00Z");
    expect(latestAgentAction(beforeEvent)).toBeNull();
    expect(latestAgentAction([])).toBeNull();
  });
});

describe("buildSnapshot", () => {
  it("reads commitments, next milestone and the latest agent action off the contract state", () => {
    const snap = buildSnapshot(state, activities);
    expect(snap.nextMilestone).toBe("Security review of SOC2 package");
    // The example's current_commitments is known=false: absent, not an empty list.
    expect(snap.commitments).toBeNull();
    expect(snap.latestAgentAction!.activity_type).toBe("AgentActionExecuted");
  });

  it("renders commitment items with status and due date when the field is known", () => {
    const withCommitments: AccountState = {
      ...state,
      fields: {
        ...state.fields,
        current_commitments: {
          ...state.fields.current_commitments!,
          known: true,
          value: [
            { text: "Send SOC2 Type II + pen-test summary", claim_id: "0c1a0000-0000-4000-8000-000000000202", status: "fulfilled", due_at: "2026-09-30T00:00:00Z" },
            { text: "Confirm EU rollout timeline", claim_id: "0c1a0000-0000-4000-8000-000000000203" },
          ],
        },
      },
    };
    const snap = buildSnapshot(withCommitments, activities);
    expect(snap.commitments).toHaveLength(2);
    expect(snap.commitments![0]).toEqual({ text: "Send SOC2 Type II + pen-test summary", status: "fulfilled", dueAt: "2026-09-30T00:00:00Z" });
    expect(snap.commitments![1]!.status).toBeNull();
  });

  it("reports everything unknown when there is no state and no agent activity", () => {
    const snap = buildSnapshot(null, []);
    expect(snap).toEqual({ commitments: null, nextMilestone: null, latestAgentAction: null });
  });

  it("reports unknown for fields the N-1 state has not learned yet", () => {
    const snap = buildSnapshot(before, []);
    expect(snap.nextMilestone).toBeNull();
    expect(snap.commitments).toBeNull();
  });
});
