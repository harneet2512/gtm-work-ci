// Loader for /episodes/[id]: the decision episode's run is found by scanning runs for
// generation.decision_episode_id (core has no episode→run lookup); the trace, strategies, human
// decision, judgment inference and the episode's posted Cliff refs assemble the causal view.
import type { CoreClient } from "@/lib/api/core-client";
import type { AgentRun, BusinessIntelligence, EpisodeReplayView, GraphDiff, HumanStrategyDecision, JudgmentInference, RunStrategies, RunTrace, SurfaceMessage } from "@/lib/api/types";
import { buildEpisodeView, type EpisodeView } from "@/lib/view/episode";

export type EpisodeApi = Pick<
  CoreClient,
  | "listRuns"
  | "getRunTrace"
  | "getRunStrategies"
  | "getStrategyDecision"
  | "getJudgmentInference"
  | "getSurfaceMessage"
  | "getEventGraphDiff"
  | "getLatestBusinessIntelligence"
  | "getReplayEpisodes"
>;

/** Whether Message 1's subject could be tied to this very episode (never "the account's latest" by guess). */
export type BiStatus = "matched" | "none" | "unresolvable";

export interface EpisodePageData {
  episodeId: string;
  view: EpisodeView;
  /** The run's trace — the Trace and Raw mode bodies render it directly. */
  trace: RunTrace;
  /** The triggering event's projection diff (HAR-96), when core has one — the Graph diff mode's body. */
  graphDiff: GraphDiff | null;
  /** The Cliff surface refs keyed by kind (bi/chooser/judgment) — the receipt, posted or reserved. */
  surfaces: Partial<Record<"bi" | "chooser" | "judgment", SurfaceMessage | null>>;
  /** Message 1's subject payload, only when its account_change_id is this episode's — the Cliff mode renders it. */
  bi: BusinessIntelligence | null;
  biStatus: BiStatus;
  /** The payloads the Cliff cards and Raw mode render. */
  strategies: RunStrategies | null;
  decision: HumanStrategyDecision | null;
  inference: JudgmentInference | null;
  /** False when a surface-ref fetch threw — absent keys then mean "unreadable", not "not applicable". */
  surfacesReadable: boolean;
}

/**
 * The run bound to a decision episode, or null when none has been created for it. With the manifest's account the
 * scan is limited to that account's runs, so a busy core's newest 200 runs cannot hide an older episode.
 */
async function runForEpisode(api: EpisodeApi, episodeId: string, accountId: string | null): Promise<AgentRun | null> {
  const runs = await api.listRuns(accountId ? { accountId, limit: RUN_SCAN_LIMIT } : { limit: RUN_SCAN_LIMIT });
  return runs.find((r) => r.generation?.decision_episode_id === episodeId) ?? null;
}

const RUN_SCAN_LIMIT = 200;

/**
 * The replay manifest's own record of this episode: its account and the account change that opened it. Null when no
 * manifest was given, the read fails, or the manifest has no such episode — callers then fall back / report "not resolvable".
 */
async function replayContext(api: EpisodeApi, episodeId: string, manifestId: string | null): Promise<{ accountId: string; changeId: string | null } | null> {
  if (!manifestId) return null;
  const view: EpisodeReplayView | null = await api.getReplayEpisodes(manifestId).catch(() => null);
  if (!view) return null;
  const ep = view.prior_episodes.find((e) => e.decision_episode_id === episodeId);
  return { accountId: view.account_id, changeId: ep?.account_change_id ?? null };
}

async function surfaceRefs(
  api: EpisodeApi,
  episodeId: string,
  biId: string | null,
): Promise<{ posted: string[] | null; surfaces: EpisodePageData["surfaces"]; readable: boolean }> {
  const surfaces: EpisodePageData["surfaces"] = {};
  let readable = true;
  const probe = async (kind: "bi" | "chooser" | "judgment", subjectId: string | null) => {
    if (!readable || subjectId === null) return; // a throw stops the rest — remaining keys stay absent
    try {
      surfaces[kind] = await api.getSurfaceMessage(subjectId, "slack", kind);
    } catch {
      readable = false;
    }
  };
  await probe("bi", biId);
  await probe("chooser", episodeId);
  await probe("judgment", episodeId);
  return {
    posted: readable ? Object.values(surfaces).filter((r) => r?.ts != null).map((r) => r!.kind) : null,
    surfaces,
    readable,
  };
}

export async function loadEpisodePage(api: EpisodeApi, episodeId: string, manifestId: string | null = null): Promise<EpisodePageData | null> {
  const replay = await replayContext(api, episodeId, manifestId);
  const run = await runForEpisode(api, episodeId, replay?.accountId ?? null);
  if (!run) return null;
  const changeId = replay?.changeId ?? null;
  const [trace, strategies, decision, inference, latestBi] = await Promise.all([
    api.getRunTrace(run.id), // a transport error propagates (error boundary: backend unavailable); only a real 404 is null
    api.getRunStrategies(run.id).catch(() => null),
    api.getStrategyDecision(run.id).catch(() => null),
    api.getJudgmentInference(episodeId).catch(() => null),
    api.getLatestBusinessIntelligence(run.account_id).catch(() => null),
  ]);
  // Message 1 is the BI update of THIS episode's account change; the account's latest may be another episode's.
  const biStatus: BiStatus = changeId === null ? "unresolvable" : latestBi?.account_change_id === changeId ? "matched" : latestBi === null ? "none" : "unresolvable";
  const bi = biStatus === "matched" ? latestBi : null;
  if (!trace) return null; // the core answered 404: an episode with no trace can't be inspected
  const { posted, surfaces, readable } = await surfaceRefs(api, episodeId, bi?.id ?? null);
  const eventId = trace.trigger_activities?.[0]?.source_event_id ?? null;
  const graphDiff = eventId ? await api.getEventGraphDiff(eventId).catch(() => null) : null;
  return { episodeId, view: buildEpisodeView(run, trace, strategies, decision, inference, posted), trace, graphDiff, surfaces, bi, biStatus, strategies, decision, inference, surfacesReadable: readable };
}
