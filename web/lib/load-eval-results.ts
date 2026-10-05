// Loader for the /evals explorer (HAR-145): core has no eval-result list endpoint — results live inside
// each run's eval_bundles — so the explorer reads listRuns then every run's strategies + decision in
// parallel. A run whose artifacts fail to load contributes no rows rather than failing the page, and is counted
// in `unavailableRuns` so the page can say results are missing instead of silently dropping them.
import type { CoreClient } from "@/lib/api/core-client";
import type { EvalResult } from "@/lib/api/types";
import { flattenResults, type EvalRow, type RunEvalSlice } from "@/lib/view/evals-explorer";

export type ExplorerApi = Pick<CoreClient, "listRuns" | "getRunStrategies" | "getStrategyDecision" | "getRun">;

export interface ExplorerData {
  rows: EvalRow[];
  /** Every result keyed by id — the `?result=` inspector reads it verbatim. */
  byId: Map<string, EvalResult>;
  /** Runs that produced rows, for the compare picker: "A"/"B" labels resolve here. */
  runs: { id: string; episodeId: string | null; phase: string | null; rows: number }[];
  /** Runs whose artifacts could not be read (not "no strategies yet"): their results are missing, and the page says so. */
  unavailableRuns: number;
}

export async function loadExplorer(api: ExplorerApi, limit = 50): Promise<ExplorerData> {
  const runs = await api.listRuns({ limit });
  let unavailableRuns = 0;
  const read = async <T,>(f: () => Promise<T | null>, failed: { v: boolean }): Promise<T | null> => {
    try {
      return await f();
    } catch {
      failed.v = true;
      return null;
    }
  };
  const slices = await Promise.all(
    runs.map(async (run): Promise<RunEvalSlice> => {
      const failed = { v: false };
      const [strategies, decision] = await Promise.all([read(() => api.getRunStrategies(run.id), failed), read(() => api.getStrategyDecision(run.id), failed)]);
      if (failed.v) unavailableRuns += 1;
      return { run, strategies, decision };
    }),
  );
  const rows = flattenResults(slices);
  const byId = new Map<string, EvalResult>();
  for (const s of slices)
    for (const b of s.strategies?.eval_bundles ?? [])
      for (const item of b.items ?? []) if (item.result?.id) byId.set(item.result.id, item.result);
  const seen = new Map<string, number>();
  for (const r of rows) seen.set(r.runId, (seen.get(r.runId) ?? 0) + 1);
  return {
    rows,
    byId,
    unavailableRuns,
    runs: [...seen.keys()].map((id) => {
      const run = runs.find((r) => r.id === id)!;
      return { id, episodeId: run.generation?.decision_episode_id ?? null, phase: run.generation?.phase ?? null, rows: seen.get(id)! };
    }),
  };
}
