// "This eval is wrong" (eval design spec 4b-3, HAR-97 E19): the web side of POST /eval-results/{id}/disputes.
// The outcome the form shows is plain language with the fix, never the core's raw message.
import type { CoreClient } from "@/lib/api/core-client";
import { errorCode } from "@/lib/api/core-client";
import type { EvalDisputeRequest } from "@/lib/api/types";

export type ExpectedVerdict = NonNullable<EvalDisputeRequest["expected_verdict"]>;
export const EXPECTED_VERDICTS: readonly ExpectedVerdict[] = ["pass", "warn", "fail", "abstain", "not_relevant"];

export interface DisputeInput {
  resultId: string;
  reason: string;
  expected: string | null;
}

export type DisputeOutcome = { ok: true; expected: ExpectedVerdict | null } | { ok: false; message: string };

const MESSAGES: Readonly<Record<string, string>> = {
  invalid_request: "Say what the eval got wrong, in up to 2,000 characters.",
  expected_equals_verdict: "That is the verdict it already has. Pick a different one, or choose “Not sure”.",
  not_found: "This eval result is no longer in the core, so it can't be disputed.",
  unknown_person: "The core doesn't know who you are, so the dispute was not saved.",
  unreachable: "The core can't be reached. Nothing was saved; try again in a moment.",
};

export function disputeMessage(code: string): string {
  return MESSAGES[code] ?? `The dispute could not be saved (${code}). Try again.`;
}

const isExpected = (v: string): v is ExpectedVerdict => (EXPECTED_VERDICTS as readonly string[]).includes(v);

/** The web's actor label: the operator running this console (there is no web sign-in yet). */
export const actorLabelFromEnv = (env: Readonly<Record<string, string | undefined>>): string => env.GHOST_WEB_ACTOR_LABEL?.trim() || "Web reviewer";

export async function recordDispute(api: Pick<CoreClient, "disputeEvalResult">, input: DisputeInput, actorLabel: string): Promise<DisputeOutcome> {
  const reason = input.reason.trim();
  if (reason === "" || [...reason].length > 2000) return { ok: false, message: disputeMessage("invalid_request") };
  const expected = input.expected && isExpected(input.expected) ? input.expected : null;
  if (input.expected && !expected) return { ok: false, message: disputeMessage("invalid_request") };
  try {
    const dispute = await api.disputeEvalResult(input.resultId, { reason, expected_verdict: expected, surface: "web", actor_label: actorLabel });
    return { ok: true, expected: dispute.expected_verdict ?? null };
  } catch (e) {
    return { ok: false, message: disputeMessage(errorCode(e, e instanceof Error && e.name === "InvalidIdError" ? "not_found" : "unexpected")) };
  }
}
