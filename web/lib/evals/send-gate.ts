// Whether Send is open for the option in view (eval design spec 4b-3: "a blocking FAIL disables Send, and the
// button explains why"). For the chosen option it follows the decision and the latest verdicts (after-edit);
// for any other option it says what would happen if it were chosen. The web does not send; Slack does, through
// the same core gate (POST /runs/{id}/send refuses a blocking failure with 422 blocking_eval).
import type { AfterEditView } from "./after-edit";
import type { SelectedView } from "./selected";

export type SendGateState = "sent" | "discarded" | "blocked" | "open" | "would_block" | "none";

export interface SendGate {
  state: SendGateState;
  title: string | null;
  blockers: { name: string; reason: string }[];
}

const NONE: SendGate = { state: "none", title: null, blockers: [] };

export function sendGate(view: SelectedView, after: AfterEditView): SendGate {
  if (after.sent) return { state: "sent", title: null, blockers: [] };
  if (after.discarded) return { state: "discarded", title: null, blockers: [] };
  if (view.isChosen && (after.state === "unedited" || after.state === "not_reevaluated" || after.state === "reevaluated")) {
    return after.sendBlocked.length > 0
      ? { state: "blocked", title: after.sendBlockedTitle, blockers: after.sendBlocked }
      : { state: "open", title: "Send is open: no check blocks it.", blockers: [] };
  }
  const blockers = view.lines.filter((l) => l.verdict === "fail" && l.blocking).map((l) => ({ name: l.name, reason: l.reason }));
  if (blockers.length === 0) return NONE;
  return { state: "would_block", title: `If chosen, Send stays blocked: ${blockers[0]!.name} still fails.`, blockers };
}
