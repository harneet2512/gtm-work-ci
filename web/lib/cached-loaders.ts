// Per-request loader cache (HAR-145 review MEDIUM 5). A page and its @inspector parallel slot render in the same
// request and used to each run the full loader (about 2 x 101 core calls). React cache() memoizes by argument list for
// the duration of one server render, so the slot reuses the page's result. Pages and slots call these, not the loaders.
import "server-only";
import { cache } from "react";
import { core } from "@/lib/api/server";
import { loadControlPage } from "@/lib/load-control";
import { loadEpisodePage } from "@/lib/load-episode";
import { loadEvalRuns, loadFamilies } from "@/lib/load-eval-runs";

export const getEpisodePage = cache((episodeId: string) => loadEpisodePage(core(), episodeId));
export const getEvalRuns = cache((cursor: string | undefined) => loadEvalRuns(core(), { cursor }));
export const getEvalFamilies = cache((evalRunId: string) => loadFamilies(core(), evalRunId));
export const getControlPage = cache((manifestId: string, at: string | undefined) => loadControlPage(core(), manifestId, at));
