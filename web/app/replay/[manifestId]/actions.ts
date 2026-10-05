"use server";

// Server actions for the replay controls (HAR-129 section C): they run on the server, so the core token
// never reaches the browser. The outcome is the core's answer reduced to what the status line may show —
// the error code only, never internal detail.
import { errorCode } from "@/lib/api/core-client";
import { core } from "@/lib/api/server";

export type AdvanceOutcome =
  | {
      ok: true;
      episode: number;
      total: number;
      material: boolean;
      noActionReason: string | null;
      stateVersion: number | null;
      coalesced: boolean;
    }
  | { ok: false; code: string };

export type ResetOutcome = { ok: true; episode: number } | { ok: false; code: string };

export async function advanceEpisode(manifestId: string): Promise<AdvanceOutcome> {
  try {
    const res = await core().advanceReplayEpisode(manifestId);
    return {
      ok: true,
      episode: res.episode,
      total: res.total,
      material: res.material,
      noActionReason: res.no_action_reason,
      stateVersion: res.state_version,
      coalesced: res.coalesced,
    };
  } catch (e) {
    return { ok: false, code: errorCode(e, "unexpected") };
  }
}

export async function resetReplayCursor(manifestId: string, episode: number): Promise<ResetOutcome> {
  try {
    const res = await core().resetReplay(manifestId, episode);
    return { ok: true, episode: res.episode };
  } catch (e) {
    return { ok: false, code: errorCode(e, "unexpected") };
  }
}
