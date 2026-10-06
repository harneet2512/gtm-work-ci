"use server";

// /control server action (HAR-145): Play releases exactly one held-out event through the real pipeline. The strip does
// not read progress through a server action (Next runs a client's actions one at a time, so a poll would wait behind
// the open Play); it polls the web app's progress route while this request is open.
import { errorCode } from "@/lib/api/core-client";
import { core } from "@/lib/api/server";
import { postHandoff } from "@/lib/demo/control-client";
import { demoControl } from "@/lib/demo/server";

export type PlayOutcome =
  | { ok: true; result: import("@/lib/api/types").AdvanceResult }
  | { ok: false; code: string };

/** The core's answer when every event of a manifest is already released. */
const REPLAY_COMPLETE = "replay_complete";

/**
 * Play. It releases the next held-out event of the account on screen. When that account has none left, the same Play
 * continues into the next chronological episode: the hidden handoff (the control service) carries what was learned,
 * switches the graph and core to the next account, and that account's Event N is released through the same core endpoint.
 */
export async function playEvent(manifestId: string): Promise<PlayOutcome> {
  try {
    return { ok: true, result: await core().advanceReplayEpisode(manifestId) };
  } catch (e) {
    const code = errorCode(e, "unexpected");
    if (code !== REPLAY_COMPLETE) return { ok: false, code };
    return continueIntoNextEpisode(manifestId);
  }
}

async function continueIntoNextEpisode(manifestId: string): Promise<PlayOutcome> {
  const next = await postHandoff(demoControl(), manifestId);
  if (!next.ok) {
    // No later episode (or no demo service at all) is the end of the timeline: the core's own refusal stands.
    return { ok: false, code: next.code === "no_next_case" || next.code === "not_configured" ? REPLAY_COMPLETE : next.code };
  }
  try {
    return { ok: true, result: await core().advanceReplayEpisode(next.manifestId) };
  } catch (e) {
    return { ok: false, code: errorCode(e, "unexpected") };
  }
}
