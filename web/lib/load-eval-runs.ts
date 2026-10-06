// Loaders for the evals explorer and the operator's run comparison (HAR-145). The explorer reads the eval-run list (cursor
// pages) and, for the selected run, its family summary. A failed read is "backend unavailable"; it is never rendered as an
// empty list or as an eval failure. The comparison keeps the core's refusals (different triggers, unknown run) apart.
import type { CoreClient } from "@/lib/api/core-client";
import { CoreError } from "@/lib/api/core-client";
import type { EvalFamilySummary, EvalRun, EvalRunComparison } from "@/lib/api/types";
import { UUID } from "@/lib/uuid";

export type EvalRunsApi = Pick<CoreClient, "listEvalRunsPage" | "getEvalRunFamilies" | "compareEvalRuns">;

export const EVAL_RUNS_PAGE_SIZE = 25;

export interface EvalRunsData {
  runs: EvalRun[];
  /** The cursor of the next (older) page, or null on the last page. */
  nextCursor: string | null;
  /** The list could not be read: the page says "backend unavailable", not "no runs". */
  unavailable: boolean;
  /** The page marker in the URL was refused (400), so the first page is shown instead. */
  badCursor: boolean;
}

export async function loadEvalRuns(api: EvalRunsApi, opts: { cursor?: string } = {}): Promise<EvalRunsData> {
  try {
    const page = await api.listEvalRunsPage({ limit: EVAL_RUNS_PAGE_SIZE, cursor: opts.cursor });
    return { runs: page.items, nextCursor: page.nextCursor, unavailable: false, badCursor: false };
  } catch (e) {
    if (opts.cursor && e instanceof CoreError && e.status === 400) {
      const first = await loadEvalRuns(api);
      return { ...first, badCursor: true };
    }
    return { runs: [], nextCursor: null, unavailable: true, badCursor: false };
  }
}

export interface FamiliesData {
  summary: EvalFamilySummary | null;
  /** True when the read failed; false for a run that has no summary (404). */
  unavailable: boolean;
}

export async function loadFamilies(api: Pick<EvalRunsApi, "getEvalRunFamilies">, evalRunId: string): Promise<FamiliesData> {
  if (!UUID.test(evalRunId)) return { summary: null, unavailable: false };
  try {
    return { summary: await api.getEvalRunFamilies(evalRunId), unavailable: false };
  } catch {
    return { summary: null, unavailable: true };
  }
}

export type ComparisonData =
  | { state: "idle" }
  | { state: "invalid" }
  | { state: "compared"; comparison: EvalRunComparison }
  | { state: "not_comparable" }
  | { state: "not_found" }
  | { state: "unavailable" };

/** Both ids must be present and well formed; a missing pair is idle (the picker is shown), a malformed one is invalid. */
export async function loadComparison(api: Pick<EvalRunsApi, "compareEvalRuns">, a: string | undefined, b: string | undefined): Promise<ComparisonData> {
  if (!a && !b) return { state: "idle" };
  if (!a || !b || !UUID.test(a) || !UUID.test(b)) return { state: "invalid" };
  try {
    const r = await api.compareEvalRuns(a, b);
    return r.outcome === "compared" ? { state: "compared", comparison: r.comparison } : { state: r.outcome };
  } catch {
    return { state: "unavailable" };
  }
}
