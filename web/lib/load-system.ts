// Loader for /system (HAR-145): the platform-health reads (the backend's health check, the model provider breaker, the Cliff
// delivery queue, the held-out leak check) plus the latest decision episode's operational metrics. Every read is
// independent: a failed one is "unreadable"/"unavailable", never green and never an empty result.
import type { CoreClient } from "@/lib/api/core-client";
import type { InvisibilityReport, OperationalMetrics, OutboxEvent, ProviderBreakerStatus } from "@/lib/api/types";

export type SystemApi = Pick<CoreClient, "getHealth" | "getProviderBreaker" | "listOutboxEvents" | "getInvisibility" | "getReplayEpisodes" | "getEpisodeMetrics">;

export type MetricsRead =
  | { state: "none_asked" }
  | { state: "no_episode" }
  | { state: "not_found" }
  | { state: "unavailable" }
  | { state: "ok"; metrics: OperationalMetrics };

export interface SystemPageData {
  coreOk: boolean;
  breaker: ProviderBreakerStatus | null;
  outbox: OutboxEvent[] | null;
  invisibility: InvisibilityReport | null;
  invisibilityAsked: boolean;
  metrics: MetricsRead;
}

const quietly = async <T,>(f: () => Promise<T | null>): Promise<T | null> => {
  try {
    return await f();
  } catch {
    return null;
  }
};

/** The latest episode that opened a decision, read from the replay view; its metrics are the run's operational cost. */
async function readMetrics(api: SystemApi, manifestId: string | null): Promise<MetricsRead> {
  if (!manifestId) return { state: "none_asked" };
  try {
    const view = await api.getReplayEpisodes(manifestId);
    const episodeId = [...view.prior_episodes].reverse().find((e) => e.decision_episode_id)?.decision_episode_id ?? null;
    if (!episodeId) return { state: "no_episode" };
    const metrics = await api.getEpisodeMetrics(episodeId);
    return metrics ? { state: "ok", metrics } : { state: "not_found" };
  } catch {
    return { state: "unavailable" };
  }
}

export async function loadSystemPage(api: SystemApi, manifestId: string | null): Promise<SystemPageData> {
  const [coreOk, breaker, outbox, invisibility, metrics] = await Promise.all([
    api.getHealth().then(
      (h) => h.status === "ok",
      () => false,
    ),
    quietly(() => api.getProviderBreaker()),
    quietly(() => api.listOutboxEvents("slack")),
    manifestId ? quietly(() => api.getInvisibility(manifestId)) : Promise.resolve(null),
    readMetrics(api, manifestId),
  ]);
  return { coreOk, breaker, outbox, invisibility, invisibilityAsked: manifestId !== null, metrics };
}
