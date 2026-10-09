// The stored results of every episode in the database (HAR-149 sections 8, 9 and 6): the drawer's performance and failure
// categories and the continuous health view aggregate over these. A read that fails drops that episode and says so; an
// unreadable backend is "unavailable", never an empty (and so healthy-looking) result.
import type { CoreClient } from "@/lib/api/core-client";
import type { EpisodeSummary } from "@/lib/api/types";
import { formatDay } from "@/lib/format";
import type { FleetEpisode } from "./fleet";
import { isTestData, TEST_DATA_TITLE } from "./test-data";
import type { HealthEpisode } from "./health";

type Api = Pick<CoreClient, "listEvalRunsPage" | "getEpisode" | "listEpisodeGateResults">;
type HealthApi = Api & Pick<CoreClient, "getEpisodeMetrics" | "getEpisodeTrace">;

const PAGE_SIZE = 50;
const MAX_PAGES = 2;
const MAX_EPISODES = 60;

export interface FleetData<T> {
  status: "ok" | "unavailable";
  episodes: T[];
  /** Episodes whose read failed. */
  skipped: number;
}

export const episodeLabel = (s: EpisodeSummary): string => {
  const name = isTestData() ? TEST_DATA_TITLE : s.account_name;
  return s.triggering_event ? `${name} · ${formatDay(s.triggering_event.occurred_at)}` : name;
};

/** Episode ids in the database that have an eval run, newest first, without repeats. */
export async function listEpisodeIds(api: Pick<CoreClient, "listEvalRunsPage">): Promise<string[]> {
  const ids: string[] = [];
  let cursor: string | undefined;
  for (let page = 0; page < MAX_PAGES; page += 1) {
    const res = await api.listEvalRunsPage({ limit: PAGE_SIZE, cursor });
    for (const run of res.items) if (run.decision_episode_id && !ids.includes(run.decision_episode_id)) ids.push(run.decision_episode_id);
    if (!res.nextCursor || ids.length >= MAX_EPISODES) break;
    cursor = res.nextCursor;
  }
  return ids.slice(0, MAX_EPISODES);
}

async function settle<T>(ids: readonly string[], one: (id: string) => Promise<T | null>): Promise<FleetData<T>> {
  const settled = await Promise.allSettled(ids.map(one));
  const episodes: T[] = [];
  let skipped = 0;
  for (const s of settled) {
    if (s.status === "fulfilled" && s.value) episodes.push(s.value);
    else skipped += 1;
  }
  return { status: ids.length > 0 && episodes.length === 0 && skipped > 0 ? "unavailable" : "ok", episodes, skipped };
}

export async function loadFleet(api: Api): Promise<FleetData<FleetEpisode & { summary: EpisodeSummary }>> {
  try {
    return await settle(await listEpisodeIds(api), async (id) => {
      const [summary, results] = await Promise.all([api.getEpisode(id), api.listEpisodeGateResults(id)]);
      return summary ? { episodeId: id, label: episodeLabel(summary), results: results ?? [], summary } : null;
    });
  } catch {
    return { status: "unavailable", episodes: [], skipped: 0 };
  }
}

export async function loadHealthEpisodes(api: HealthApi): Promise<FleetData<HealthEpisode>> {
  try {
    return await settle(await listEpisodeIds(api), async (id) => {
      const summary = await api.getEpisode(id);
      if (!summary) return null;
      const [metrics, trace] = await Promise.all([api.getEpisodeMetrics(id).catch(() => null), api.getEpisodeTrace(id).catch(() => null)]);
      return { episodeId: id, label: episodeLabel(summary), metrics, spanKinds: trace ? trace.spans.map((s) => s.kind) : null };
    });
  } catch {
    return { status: "unavailable", episodes: [], skipped: 0 };
  }
}
