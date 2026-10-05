// Per-request loader cache (HAR-145 review MEDIUM 5). A page and its @inspector parallel slot render in the same
// request and used to each run the full loader (about 2 x 101 core calls). React cache() memoizes by argument list for
// the duration of one server render, so the slot reuses the page's result. Pages and slots call these, not the loaders.
import "server-only";
import { cache } from "react";
import { core } from "@/lib/api/server";
import { loadControlPage } from "@/lib/load-control";
import { loadEpisodePage } from "@/lib/load-episode";
import { loadExplorer } from "@/lib/load-eval-results";

export const getEpisodePage = cache((episodeId: string, manifestId: string | null) => loadEpisodePage(core(), episodeId, manifestId));
export const getExplorer = cache((limit?: number) => loadExplorer(core(), limit));
export const getControlPage = cache((manifestId: string, at: string | undefined) => loadControlPage(core(), manifestId, at));
