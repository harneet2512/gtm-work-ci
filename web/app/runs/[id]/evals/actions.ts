"use server";

// The "This eval is wrong" server action (HAR-97 E19). It runs on the server, so the core token never reaches the
// browser; the form only ever sees a plain-language outcome.
import { core } from "@/lib/api/server";
import { actorLabelFromEnv, recordDispute, type DisputeOutcome } from "@/lib/evals/dispute";

export async function disputeEval(resultId: string, reason: string, expected: string | null): Promise<DisputeOutcome> {
  return recordDispute(core(), { resultId, reason, expected }, actorLabelFromEnv(process.env));
}
