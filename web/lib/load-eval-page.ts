// Loader for the episode eval page (/runs/[id]/evals): the run chain the run page reads (run, trace, strategies
// + eval bundles, the human's decision, the inference, the cited knowledge), plus the send-time
// re-evaluation of the human's edited artifact.
//
// The re-evaluation is always null today. HAR-139 (PR #55) re-runs the deterministic evals on the final
// artifact at send and persists them to eval_runs, but no core endpoint serves them yet. The page therefore
// says "Not re-evaluated yet" rather than guessing a delta. When a read exists (for example
// GET /runs/{run_id}/send-evals), this loader is the one place to fetch it.
import { loadRunPage, type RunApi, type RunPageData } from "./load-run";
import type { ReEvaluation } from "./evals/after-edit";

export interface EvalPageData extends RunPageData {
  reevaluation: ReEvaluation | null;
}

export async function loadEvalPage(api: RunApi, runId: string): Promise<EvalPageData> {
  const data = await loadRunPage(api, runId);
  return { ...data, reevaluation: null };
}
