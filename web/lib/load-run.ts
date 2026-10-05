// Loader for the run detail page (WP24, HAR-122): reads the run plus the chain around it — trace,
// strategies + eval bundles, the human's strategy decision, the judgment inference of the semantic
// delta, and every knowledge object the chain cites. Only the run itself is required; every other
// section degrades to a notice so the page renders whatever exists.
import type { CoreClient, CoreError } from "@/lib/api/core-client";
import { errorCode } from "@/lib/api/core-client";
import type { AgentRun, HumanStrategyDecision, JudgmentInference, Knowledge, RunStrategies, RunTrace } from "@/lib/api/types";
import { knowledgeIds } from "@/lib/view/run-chain";

export type RunApi = Pick<
  CoreClient,
  "getRun" | "getRunTrace" | "getRunStrategies" | "getStrategyDecision" | "getJudgmentInference" | "getKnowledge"
>;

export interface RunPageData {
  run: AgentRun;
  trace: RunTrace | null;
  strategies: RunStrategies | null;
  decision: HumanStrategyDecision | null;
  inference: JudgmentInference | null;
  /** Every knowledge object the chain cites, by id (null when that id could not be read). */
  knowledge: Record<string, Knowledge | null>;
  /** Sections that could not be read: the page still renders what it has. */
  notices: string[];
}

/** Only the core's error code is shown: the raw message can carry internal detail. */
const reason = (e: unknown): string => errorCode(e, "unreadable");

async function attempt<T>(notices: string[], what: string, fn: () => Promise<T>, fallback: T): Promise<T> {
  try {
    return await fn();
  } catch (e) {
    notices.push(`${what} could not be read (${reason(e)}).`);
    return fallback;
  }
}

/**
 * Reads the endpoints one run page renders. The run document is required — a failure propagates to
 * the error page (404 → notFound upstream). Trace, strategies, decision, inference and each cited
 * knowledge object degrade independently; their sections show an explicit empty/notice state.
 */
export async function loadRunPage(api: RunApi, runId: string): Promise<RunPageData> {
  const notices: string[] = [];
  const run = await api.getRun(runId);

  const [trace, strategies, decision] = await Promise.all([
    attempt<RunTrace | null>(notices, "The run trace", () => api.getRunTrace(runId), null),
    attempt<RunStrategies | null>(notices, "The strategy set", () => api.getRunStrategies(runId), null),
    attempt<HumanStrategyDecision | null>(notices, "The human decision", () => api.getStrategyDecision(runId), null),
  ]);

  const episodeId = strategies?.strategy_set.decision_episode_id ?? decision?.decision_episode_id ?? run.generation?.decision_episode_id ?? null;
  const inference = episodeId
    ? await attempt<JudgmentInference | null>(notices, "The judgment inference", () => api.getJudgmentInference(episodeId), null)
    : null;

  const knowledge: Record<string, Knowledge | null> = {};
  const ids = knowledgeIds(strategies, inference);
  await Promise.all(
    ids.map(async (id) => {
      knowledge[id] = await attempt<Knowledge | null>(notices, `Knowledge ${id.slice(0, 8)}`, () => api.getKnowledge(id), null);
    }),
  );

  return { run, trace, strategies, decision, inference, knowledge, notices };
}
