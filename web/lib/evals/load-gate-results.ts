// The gate results the evals overview shows: those of the episode named by ?episode=, else of the newest episode that has
// an eval run. A failed read is "not measured" (an empty list), never a pass; nothing is invented.
import type { CoreClient } from "@/lib/api/core-client";
import type { GateResultRow } from "./bucket2-results";
import { UUID } from "@/lib/uuid";

export interface LatestGateResults {
  results: GateResultRow[];
  episodeId: string | null;
}

type Api = Pick<CoreClient, "listEvalRunsPage" | "listEpisodeGateResults">;

export async function loadLatestGateResults(api: Api, requested: string | undefined): Promise<LatestGateResults> {
  try {
    let episodeId: string | null = requested && UUID.test(requested.trim().toLowerCase()) ? requested.trim().toLowerCase() : null;
    if (episodeId === null) {
      const page = await api.listEvalRunsPage({ limit: 20 });
      episodeId = page.items.find((r) => r.decision_episode_id)?.decision_episode_id ?? null;
    }
    if (episodeId === null) return { results: [], episodeId: null };
    const items = await api.listEpisodeGateResults(episodeId);
    return { results: (items ?? []) as unknown as GateResultRow[], episodeId };
  } catch {
    return { results: [], episodeId: null };
  }
}
