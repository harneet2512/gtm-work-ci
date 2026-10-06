// Loader for /control (HAR-145): the replay view drives the world card and trajectory; the account's latest EvalRun feeds
// the four health bands; the pipeline's progress seeds the Play strip; the episode summary and the surface-message refs
// feed the Decision & Learning and Cliff bands. A read a partial world cannot answer degrades to an honest band state
// ("no eval run yet"); a read that fails for any other reason reads "backend unavailable", never an empty result.
import type { CoreClient } from "@/lib/api/core-client";
import { CoreError } from "@/lib/api/core-client";
import type { AgentRun, EpisodeSummary, EvalRun, PipelineProgress } from "@/lib/api/types";
import { loadReplayPage, type ReplayPageData } from "@/lib/load-replay";
import { buildControlView, type ControlView } from "@/lib/view/control";

export type ControlApi = Pick<
  CoreClient,
  | "getReplayEpisodes"
  | "listRuns"
  | "listEvalRunsPage"
  | "getReplayProgress"
  | "getEpisode"
  | "getSurfaceMessage"
  | "getLatestBusinessIntelligence"
>;

export interface ControlPageData {
  replay: ReplayPageData;
  control: ControlView;
  /** Message kinds (bi/chooser/judgment) with a recorded post, for the Cliff band. */
  cliffPosted: string[] | null;
  /** A read failed (transport or server error): the bands say "backend unavailable", not "no run yet". */
  backendUnavailable: boolean;
}

const latestRun = (runs: AgentRun[]): AgentRun | null =>
  [...runs].sort((a, b) => String(b.created_at ?? "").localeCompare(String(a.created_at ?? "")))[0] ?? null;

/**
 * Posted Cliff kinds for the episode: chooser/judgment are keyed on the DecisionEpisode id, Message 1
 * on the BusinessIntelligenceUpdate (HAR-136) — found through the episode's account_change_id. Null
 * when the read itself fails (distinct from "nothing posted").
 */
async function cliffPosted(
  api: ControlApi,
  accountId: string,
  episodeId: string | null,
  accountChangeId: string | null,
): Promise<string[] | null> {
  if (!episodeId) return null;
  try {
    const posted: string[] = [];
    if (accountChangeId) {
      const bi = await api.getLatestBusinessIntelligence(accountId);
      if (bi?.account_change_id === accountChangeId) {
        const ref = await api.getSurfaceMessage(bi.id, "slack", "bi");
        if (ref?.ts != null) posted.push("bi");
      }
    }
    for (const k of ["chooser", "judgment"] as const) {
      const ref = await api.getSurfaceMessage(episodeId, "slack", k);
      if (ref?.ts != null) posted.push(k);
    }
    return posted;
  } catch (e) {
    if (e instanceof CoreError && (e.status === 404 || e.status === 501)) return [];
    return null;
  }
}

const accountName = (view: ReplayPageData["view"]): string | null => {
  const doc = view.state?.document;
  const name = doc && typeof doc["account_name"] === "string" ? (doc["account_name"] as string) : null;
  return name;
};

/** One read's value (or its fallback) together with whether it failed, so the failure travels with the read. */
interface Read<T> {
  value: T;
  failed: boolean;
}

export async function loadControlPage(api: ControlApi, manifestId: string, rawAt: string | undefined): Promise<ControlPageData> {
  const replay = await loadReplayPage(api, manifestId, rawAt);
  const accountId = replay.view.account_id;
  const latestDecision = [...replay.view.prior_episodes].reverse().find((e) => e.decision_episode_id) ?? null;
  const episodeId = latestDecision?.decision_episode_id ?? null;

  const read = <T,>(f: () => Promise<T>, fallback: T): Promise<Read<T>> =>
    f().then(
      (value) => ({ value, failed: false }),
      () => ({ value: fallback, failed: true }),
    );

  const [listedRead, evalRunsRead, progressRead, episodeRead, posted] = await Promise.all([
    read(() => api.listRuns({ accountId, limit: 20 }), [] as AgentRun[]),
    read(() => api.listEvalRunsPage({ accountId, limit: 1 }).then((p) => p.items), [] as EvalRun[]),
    read(() => api.getReplayProgress(manifestId), null as PipelineProgress | null),
    episodeId ? read(() => api.getEpisode(episodeId), null as EpisodeSummary | null) : Promise.resolve({ value: null as EpisodeSummary | null, failed: false }),
    cliffPosted(api, accountId, episodeId, latestDecision?.account_change_id ?? null),
  ]);
  const listed = listedRead.value;
  const evalRuns = evalRunsRead.value;
  const progress = progressRead.value;
  const episode = episodeRead.value;
  const backendUnavailable = [listedRead, evalRunsRead, progressRead, episodeRead].some((r) => r.failed);
  const run = latestRun(listed.filter((r) => r.account_id === accountId));

  const control = buildControlView({
    view: replay.view,
    accountName: accountName(replay.view),
    run,
    evalRun: evalRuns[0] ?? null,
    episode,
    cliffKinds: posted,
    backendUnavailable,
    progress,
  });
  return { replay, control, cliffPosted: posted, backendUnavailable };
}
