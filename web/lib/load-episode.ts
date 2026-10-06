// Loader for /episodes/[id] (HAR-145): the episode summary (GET /episodes/{id}, which replaces scanning the run list) and
// its trace give the page; the knowledge mutations, the run's recomputation, the strategy set, the human decision, the
// judgment inference, Message 1's update and the Cliff surface refs are read beside them. Only the summary is required
// (a 404 is "no such episode"); every other read degrades to a notice so the page renders what exists, and a failed read
// is "backend unavailable", never an empty result.
import type { CoreClient } from "@/lib/api/core-client";
import type {
  Activity,
  BusinessIntelligence,
  DependencyInvalidation,
  EpisodeTrace,
  GateResult,
  GraphDiff,
  HumanStrategyDecision,
  JudgmentInference,
  KnowledgeMutation,
  RunStrategies,
  SurfaceMessage,
} from "@/lib/api/types";
import { accountChangeId, buildEpisodeView, sourceEventId, type EpisodeView } from "@/lib/view/episode";

export type EpisodeApi = Partial<Pick<CoreClient, "listEpisodeGateResults" | "getTimeline">> &
  Pick<
  CoreClient,
  | "getEpisode"
  | "getEpisodeTrace"
  | "listEpisodeKnowledgeMutations"
  | "getRunRecomputation"
  | "getRunStrategies"
  | "getStrategyDecision"
  | "getJudgmentInference"
  | "getSurfaceMessage"
  | "getEventGraphDiff"
  | "getLatestBusinessIntelligence"
>;

/** Whether Message 1's subject could be tied to this very episode (never "the account's latest" by guess). */
export type BiStatus = "matched" | "none" | "unresolvable";

export interface EpisodePageData {
  episodeId: string;
  view: EpisodeView;
  /** The served trace; null when the core has none for this episode. */
  trace: EpisodeTrace | null;
  /** The triggering event's projection diff (HAR-96), when core has one: the Graph diff mode's body. */
  graphDiff: GraphDiff | null;
  /** What the episode did to company knowledge; null when that read failed (distinct from an empty list). */
  mutations: KnowledgeMutation[] | null;
  /** What the human's edits invalidated and re-evaluated; null when it could not be read. */
  recomputation: DependencyInvalidation | null;
  /** The stored gate results (Bucket 2 D1-D10 and the other buckets); null when the read failed or the client has no such read. */
  gateResults: GateResult[] | null;
  /** The Cliff surface refs keyed by kind (bi/chooser/judgment): the receipt, posted or reserved. */
  surfaces: Partial<Record<"bi" | "chooser" | "judgment", SurfaceMessage | null>>;
  /** Message 1's subject payload, only when its account_change_id is this episode's. */
  bi: BusinessIntelligence | null;
  biStatus: BiStatus;
  strategies: RunStrategies | null;
  decision: HumanStrategyDecision | null;
  inference: JudgmentInference | null;
  /** The triggering activity (for the human title); null when the timeline could not be read or does not hold it. */
  triggerActivity: Activity | null;
  /** False when a surface-ref fetch threw: absent keys then mean "unreadable", not "not applicable". */
  surfacesReadable: boolean;
  /** Reads that failed, in words; the page shows them instead of an empty section. */
  notices: string[];
}

async function surfaceRefs(api: EpisodeApi, episodeId: string, biId: string | null): Promise<{ surfaces: EpisodePageData["surfaces"]; readable: boolean }> {
  const surfaces: EpisodePageData["surfaces"] = {};
  let readable = true;
  const probe = async (kind: "bi" | "chooser" | "judgment", subjectId: string | null) => {
    if (!readable || subjectId === null) return; // a throw stops the rest: remaining keys stay absent
    try {
      surfaces[kind] = await api.getSurfaceMessage(subjectId, "slack", kind);
    } catch {
      readable = false;
    }
  };
  await probe("bi", biId);
  await probe("chooser", episodeId);
  await probe("judgment", episodeId);
  return { surfaces, readable };
}

export async function loadEpisodePage(api: EpisodeApi, episodeId: string): Promise<EpisodePageData | null> {
  const summary = await api.getEpisode(episodeId); // a transport error propagates (error boundary); only a real 404 is null
  if (!summary) return null;
  const runId = summary.agent_run_id;
  const notices: string[] = [];
  const attempt = async <T,>(what: string, f: () => Promise<T | null>): Promise<T | null> => {
    try {
      return await f();
    } catch {
      notices.push(`${what}: backend unavailable.`);
      return null;
    }
  };

  const [trace, mutations, recomputation, strategies, decision, inference, latestBi, gateResults] = await Promise.all([
    attempt("The episode trace", () => api.getEpisodeTrace(episodeId)),
    attempt("Knowledge mutations", () => api.listEpisodeKnowledgeMutations(episodeId)),
    attempt("The edit recomputation", () => api.getRunRecomputation(runId)),
    attempt("The strategy set", () => api.getRunStrategies(runId)),
    attempt("The human decision", () => api.getStrategyDecision(runId)),
    attempt("The judgment inference", () => api.getJudgmentInference(episodeId)),
    attempt("Message 1", () => api.getLatestBusinessIntelligence(summary.account_id)),
    api.listEpisodeGateResults ? attempt("The gate results", () => api.listEpisodeGateResults!(episodeId)) : Promise.resolve(null),
  ]);

  // Message 1 is the update of THIS episode's account change (the evidence span's ref); the account's latest may be another episode's.
  const changeId = accountChangeId(trace);
  const biStatus: BiStatus = changeId === null ? "unresolvable" : latestBi?.account_change_id === changeId ? "matched" : latestBi === null ? "none" : "unresolvable";
  const bi = biStatus === "matched" ? latestBi : null;
  const { surfaces, readable } = await surfaceRefs(api, episodeId, bi?.id ?? null);
  const triggerId = summary.triggering_event?.activity_id;
  const triggerActivity = triggerId && api.getTimeline ? ((await api.getTimeline(summary.account_id).catch(() => [])).find((a) => a.id === triggerId) ?? null) : null;
  const eventId = sourceEventId(trace);
  const graphDiff = eventId ? await api.getEventGraphDiff(eventId).catch(() => null) : null;

  return {
    episodeId,
    view: buildEpisodeView(summary, trace),
    trace,
    graphDiff,
    mutations,
    recomputation,
    gateResults,
    surfaces,
    bi,
    biStatus,
    strategies,
    decision,
    inference,
    triggerActivity,
    surfacesReadable: readable,
    notices,
  };
}
