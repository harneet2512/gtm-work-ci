// The control-plane reads of the core client (HAR-145): keyset pages, live Play progress, the episode summary and
// trace, what an edit recomputed, knowledge mutations, operational metrics and the eval-run roll-ups. Split from
// core-client.ts; the client hands this module its own transport so the token handling stays in one place.
import { errorOf, expectShape, isObject, seg, type CoreError, type Query } from "./core-support";
import type {
  AccountSummary,
  AgentRun,
  DependencyInvalidation,
  EpisodeSummary,
  EpisodeTrace,
  EvalFamilySummary,
  EvalRun,
  EvalRunComparison,
  KnowledgeMutation,
  OperationalMetrics,
  Page,
  PipelineProgress,
} from "./types";

export interface Transport {
  get(path: string, query?: Query): Promise<Response>;
  getJson(path: string, query?: Query): Promise<unknown>;
}

export interface PageOptions {
  limit?: number;
  /** The previous page's `next_cursor`; opaque to the web. */
  cursor?: string;
}

export interface RunPageOptions extends PageOptions {
  accountId?: string;
  status?: string;
}

/** A run comparison either happened or was refused for an honest reason; only a transport error throws. */
export type CompareOutcome =
  | { outcome: "compared"; comparison: EvalRunComparison }
  | { outcome: "not_comparable" }
  | { outcome: "not_found" };

export function createControlReads({ get, getJson }: Transport) {
  async function page<T>(path: string, query: Query): Promise<Page<T>> {
    const body = await getJson(path, query);
    const ok = isObject(body) && Array.isArray(body.items) && (typeof body.next_cursor === "string" || body.next_cursor === null);
    const checked = expectShape<{ items: T[]; next_cursor: string | null }>(path, body, ok);
    return { items: checked.items, nextCursor: checked.next_cursor };
  }

  /** A read whose 404 means "nothing there": null. Any other refusal or transport error propagates. */
  async function nullable<T>(path: string, valid: (body: Record<string, unknown>) => boolean, query?: Query): Promise<T | null> {
    const res = await get(path, query);
    if (res.ok) {
      const body = await res.json();
      return expectShape<T>(path, body, isObject(body) && valid(body));
    }
    const err: CoreError = await errorOf(res);
    if (err.status === 404) return null;
    throw err;
  }

  const listAccountsPage = (opts: PageOptions = {}) => page<AccountSummary>("/accounts", { limit: opts.limit, cursor: opts.cursor });

  const listRunsPage = (opts: RunPageOptions = {}) =>
    page<AgentRun>("/runs", { limit: opts.limit, cursor: opts.cursor, account_id: opts.accountId, status: opts.status });

  const listEvalRunsPage = (opts: PageOptions & { accountId?: string } = {}) =>
    page<EvalRun>("/eval-runs", { limit: opts.limit, cursor: opts.cursor, account_id: opts.accountId });

  /** The stages the real pipeline executed for the manifest's held-out event. Null when the manifest is unknown. */
  const getReplayProgress = async (manifestId: string) =>
    nullable<PipelineProgress>(`/replay/manifests/${seg(manifestId)}/progress`, (b) => typeof b.overall === "string" && Array.isArray(b.stages));

  const getEpisode = async (episodeId: string) => nullable<EpisodeSummary>(`/episodes/${seg(episodeId)}`, (b) => typeof b.id === "string" && typeof b.agent_run_id === "string");

  const getEpisodeTrace = async (episodeId: string) => nullable<EpisodeTrace>(`/episodes/${seg(episodeId)}/trace`, (b) => Array.isArray(b.spans));

  const getEpisodeMetrics = async (episodeId: string) => nullable<OperationalMetrics>(`/episodes/${seg(episodeId)}/metrics`, (b) => b.classification === "metric");

  async function listEpisodeKnowledgeMutations(episodeId: string): Promise<KnowledgeMutation[] | null> {
    const body = await nullable<{ items: KnowledgeMutation[] }>(`/episodes/${seg(episodeId)}/knowledge-mutations`, (b) => Array.isArray(b.items));
    return body?.items ?? null;
  }

  const getRunRecomputation = async (runId: string) => nullable<DependencyInvalidation>(`/runs/${seg(runId)}/recomputation`, (b) => Array.isArray(b.entries));

  const getEvalRunFamilies = async (evalRunId: string) => nullable<EvalFamilySummary>(`/eval-runs/${seg(evalRunId)}/families`, (b) => Array.isArray(b.areas));

  /**
   * Run A against run B of ONE trigger (operator view). 422 `not_comparable` (different events or accounts) and a
   * 404 on either side are outcomes the page explains; they are never rendered as an eval FAIL.
   */
  async function compareEvalRuns(a: string, b: string): Promise<CompareOutcome> {
    const path = "/eval-runs/compare";
    const res = await get(path, { a: seg(a), b: seg(b) });
    if (res.ok) {
      const body = await res.json();
      return { outcome: "compared", comparison: expectShape<EvalRunComparison>(path, body, isObject(body) && Array.isArray(body.rows) && isObject(body.overall)) };
    }
    const err = await errorOf(res);
    if (err.status === 422 && err.code === "not_comparable") return { outcome: "not_comparable" };
    if (err.status === 404) return { outcome: "not_found" };
    throw err;
  }

  return {
    listAccountsPage,
    listRunsPage,
    listEvalRunsPage,
    getReplayProgress,
    getEpisode,
    getEpisodeTrace,
    getEpisodeMetrics,
    listEpisodeKnowledgeMutations,
    getRunRecomputation,
    getEvalRunFamilies,
    compareEvalRuns,
  };
}
