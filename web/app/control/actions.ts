"use server";

// /control server actions (HAR-145): Play releases exactly one held-out event through the real
// pipeline; episodeProgress re-reads the linked run and the episode's posted Cliff messages so the
// pipeline strip can follow real backend state — it is a read of what happened, never a timer.
import { errorCode } from "@/lib/api/core-client";
import { core } from "@/lib/api/server";

export type PlayOutcome =
  | { ok: true; result: import("@/lib/api/types").AdvanceResult }
  | { ok: false; code: string };

export interface EpisodeProgress {
  /** The run bound to the decision episode (generation.decision_episode_id), when core has created it. */
  run: import("@/lib/api/types").AgentRun | null;
  /** Cliff message kinds whose surface ref has a recorded post. */
  cliffPosted: string[];
  /** True when a read failed for a reason other than "not found": the strip then says "backend unavailable". */
  unavailable: boolean;
}

export async function playEvent(manifestId: string): Promise<PlayOutcome> {
  try {
    const result = await core().advanceReplayEpisode(manifestId);
    return { ok: true, result };
  } catch (e) {
    return { ok: false, code: errorCode(e, "unexpected") };
  }
}

/**
 * The strip's poll: the run attached to the episode (by generation.decision_episode_id) plus the posted
 * Cliff kinds. Message 1's subject is the BusinessIntelligenceUpdate, matched by the advance result's
 * account_change_id; chooser/judgment are keyed on the episode id (HAR-136).
 */
export async function episodeProgress(
  manifestId: string,
  accountId: string,
  decisionEpisodeId: string,
  accountChangeId: string | null,
): Promise<EpisodeProgress> {
  const c = core();
  let unavailable = false;
  // A failed read is "backend unavailable", never an empty result that would look like "still running".
  const attempt = async <T,>(f: () => Promise<T>, fallback: T): Promise<T> => {
    try {
      return await f();
    } catch {
      unavailable = true;
      return fallback;
    }
  };
  const runs = await attempt(() => c.listRuns({ accountId, limit: 20 }), []);
  const run = runs.find((r) => r.account_id === accountId && r.generation?.decision_episode_id === decisionEpisodeId) ?? null;
  const posted: string[] = [];
  if (accountChangeId) {
    const bi = await attempt(() => c.getLatestBusinessIntelligence(accountId), null);
    if (bi?.account_change_id === accountChangeId) {
      const ref = await attempt(() => c.getSurfaceMessage(bi.id, "slack", "bi"), null);
      if (ref?.ts != null) posted.push("bi");
    }
  }
  for (const k of ["chooser", "judgment"] as const) {
    const ref = await attempt(() => c.getSurfaceMessage(decisionEpisodeId, "slack", k), null);
    if (ref?.ts != null) posted.push(k);
  }
  return { run, cliffPosted: posted, unavailable };
}
