"use server";

// /control server action (HAR-145): Play releases exactly one held-out event through the real pipeline. The strip does
// not read progress through a server action (Next runs a client's actions one at a time, so a poll would wait behind
// the open Play); it polls the web app's progress route while this request is open.
import { errorCode } from "@/lib/api/core-client";
import { core } from "@/lib/api/server";

export type PlayOutcome =
  | { ok: true; result: import("@/lib/api/types").AdvanceResult }
  | { ok: false; code: string };

export async function playEvent(manifestId: string): Promise<PlayOutcome> {
  try {
    const result = await core().advanceReplayEpisode(manifestId);
    return { ok: true, result };
  } catch (e) {
    return { ok: false, code: errorCode(e, "unexpected") };
  }
}
