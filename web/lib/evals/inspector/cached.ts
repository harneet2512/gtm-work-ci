// Per-request loaders for the inspector pages (the same React cache() pattern as lib/cached-loaders.ts): a page that needs the
// registry, the fleet and one episode reads each once per render. Pages call these, never the loaders directly.
import "server-only";
import { cache } from "react";
import { core } from "@/lib/api/server";
import { type EpisodeInspector, loadEpisodeInspector } from "./load-episode-inspector";
import { loadFleet, loadHealthEpisodes } from "./load-fleet";
import { type InspectorRegistry, loadInspectorRegistry } from "./load-registry";

export const getRegistry = cache((): InspectorRegistry => loadInspectorRegistry());
export const getFleet = cache(() => loadFleet(core()));
export const getHealthEpisodes = cache(() => loadHealthEpisodes(core()));
export const getEpisodeInspector = cache(async (episodeId: string): Promise<EpisodeInspector | null> => {
  const fleet = await getFleet();
  return loadEpisodeInspector(core(), episodeId, getRegistry(), fleet.episodes);
});
