// Client for the Go core. SERVER-ONLY: it reads GHOST_API_TOKEN (no NEXT_PUBLIC_ prefix), so the
// token is never inlined into a browser bundle; pages call it from server components and server
// actions. Reads are GETs; the replay controls POST the demo's advance/reset endpoints (HAR-129 §C).
import type { AdvanceResult, AgentRun, AccountState, AccountSummary, Activity, BusinessIntelligence, EpisodeReplayView, EvalDispute, EvalDisputeRequest, Graph, GraphDiff, HumanStrategyDecision, InvisibilityReport, JudgmentInference, Knowledge, OutboxEvent, ProviderBreakerStatus, ResetResult, RunStrategies, RunTrace, SurfaceMessage } from "./types";

export interface CoreConfig {
  baseUrl: string;
  token: string | undefined;
}

export interface ClientOptions extends CoreConfig {
  fetchImpl?: typeof fetch;
  timeoutMs?: number;
  /** Play runs the whole pipeline in one POST and may stay open far longer than a read; no other request uses this. */
  postTimeoutMs?: number;
}

const DEFAULT_BASE_URL = "http://127.0.0.1:8080";
const DEFAULT_TIMEOUT_MS = 10_000;
const DEFAULT_POST_TIMEOUT_MS = 120_000;
const TIMELINE_PAGE_SIZE = 200;
const TIMELINE_MAX_PAGES = 5;

export function coreConfigFromEnv(env: Record<string, string | undefined>): CoreConfig {
  return { baseUrl: env.CORE_URL || DEFAULT_BASE_URL, token: env.GHOST_API_TOKEN || undefined };
}

export * from "./core-support";
export type { CompareOutcome, PageOptions, RunPageOptions } from "./core-control";
import { createControlReads } from "./core-control";
import { CoreError, errorOf, expectShape, isObject, seg, type EpisodesOptions, type GraphOptions, type KnowledgeOptions, type Query, type RunsOptions, type TimelineOptions, type WorldAsOf } from "./core-support";

export function createCoreClient(options: ClientOptions) {
  const fetchImpl = options.fetchImpl ?? fetch;
  const timeoutMs = options.timeoutMs ?? DEFAULT_TIMEOUT_MS;
  const postTimeoutMs = options.postTimeoutMs ?? DEFAULT_POST_TIMEOUT_MS;
  const base = options.baseUrl.endsWith("/") ? options.baseUrl : `${options.baseUrl}/`;

  function headers(): Record<string, string> {
    const h: Record<string, string> = { Accept: "application/json" };
    if (options.token) h.Authorization = `Bearer ${options.token}`;
    return h;
  }

  function urlFor(path: string, query: Query = {}): string {
    const url = new URL(path.replace(/^\//, ""), base);
    for (const [key, value] of Object.entries(query)) if (value !== undefined) url.searchParams.set(key, String(value));
    return url.toString();
  }

  async function get(path: string, query: Query = {}): Promise<Response> {
    try {
      return await fetchImpl(urlFor(path, query), { method: "GET", headers: headers(), cache: "no-store", signal: AbortSignal.timeout(timeoutMs) });
    } catch (cause) {
      throw new CoreError(0, "unreachable", `core unreachable: ${cause instanceof Error ? cause.message : String(cause)}`);
    }
  }

  async function getJson(path: string, query?: Query): Promise<unknown> {
    const res = await get(path, query);
    if (!res.ok) throw await errorOf(res);
    return res.json();
  }

  /** POSTs get the ordinary request timeout; only Play (which runs the whole pipeline) passes the long one. */
  async function postJson(path: string, body?: unknown, timeout: number = timeoutMs): Promise<unknown> {
    const init: RequestInit = { method: "POST", headers: headers(), cache: "no-store", signal: AbortSignal.timeout(timeout) };
    if (body !== undefined) {
      init.body = JSON.stringify(body);
      (init.headers as Record<string, string>)["Content-Type"] = "application/json";
    }
    let res: Response;
    try {
      res = await fetchImpl(urlFor(path), init);
    } catch (cause) {
      throw new CoreError(0, "unreachable", `core unreachable: ${cause instanceof Error ? cause.message : String(cause)}`);
    }
    if (!res.ok) throw await errorOf(res);
    return res.json();
  }

  async function listAccounts(): Promise<AccountSummary[]> {
    const body = await getJson("/accounts");
    return expectShape<{ items: AccountSummary[] }>("/accounts", body, isObject(body) && Array.isArray(body.items)).items;
  }

  async function getAccountGraph(accountId: string, opts: GraphOptions = {}): Promise<Graph> {
    const path = `/accounts/${seg(accountId)}/graph`;
    const body = await getJson(path, {
      limit: opts.limit,
      include_closed: opts.includeClosed,
      world_as_of: opts.worldAsOf,
    });
    return expectShape<Graph>(path, body, isObject(body) && Array.isArray(body.nodes) && Array.isArray(body.edges));
  }

  async function getEventGraphDiff(eventId: string): Promise<GraphDiff> {
    const path = `/events/${seg(eventId)}/graph-diff`;
    const body = await getJson(path);
    return expectShape<GraphDiff>(path, body, isObject(body) && Array.isArray(body.changes));
  }

  /**
   * The state as of world time `worldAsOf` (strictly before, ADR-0019; default the current state).
   * Null when no state has been computed: current (`state_not_computed`) or before that world time
   * (`state_not_computed_before`) — both are "no state existed there", never an error page.
   */
  async function getAccountState(accountId: string, opts: WorldAsOf = {}): Promise<AccountState | null> {
    const path = `/accounts/${seg(accountId)}/state`;
    const res = await get(path, { world_as_of: opts.worldAsOf });
    if (res.ok) return (await res.json()) as AccountState;
    const err = await errorOf(res);
    if (err.status === 404 && (err.code === "state_not_computed" || err.code === "state_not_computed_before")) return null;
    throw err;
  }

  /**
   * Every activity of the account the core will give (newest first), following the keyset cursor. With
   * `before` only activities strictly before that world time are returned; the cursor is the pair
   * `(next_before, next_before_id)` so a page that ends inside a run of ties loses nothing (ADR-0019).
   */
  async function getTimeline(accountId: string, opts: TimelineOptions = {}): Promise<Activity[]> {
    const path = `/accounts/${seg(accountId)}/timeline`;
    const maxPages = opts.maxPages ?? TIMELINE_MAX_PAGES;
    const all: Activity[] = [];
    let before = opts.before;
    let beforeId: string | undefined;
    for (let page = 0; page < maxPages; page += 1) {
      const body = await getJson(path, { limit: TIMELINE_PAGE_SIZE, before, before_id: beforeId });
      const { items, next_before, next_before_id } = expectShape<{ items: Activity[]; next_before?: string | null; next_before_id?: string | null }>(
        path,
        body,
        isObject(body) && Array.isArray(body.items),
      );
      all.push(...items);
      if (!next_before) break;
      before = next_before;
      beforeId = next_before_id ?? undefined;
    }
    return all;
  }

  /**
   * The episode replay view of a demo manifest (HAR-129 §B): prior released episodes, the withheld
   * next event, the world-timed account state and applicable knowledge at the episode bound. With
   * `at` the view is the one at released position `at` (Previous semantics, read-only; the core
   * answers 422 `invalid_episode` for an unreleased position).
   */
  async function getReplayEpisodes(manifestId: string, opts: EpisodesOptions = {}): Promise<EpisodeReplayView> {
    const path = `/replay/manifests/${seg(manifestId)}/episodes`;
    const body = await getJson(path, { at: opts.at });
    return expectShape<EpisodeReplayView>(
      path,
      body,
      isObject(body) && typeof body.episode === "number" && typeof body.total === "number" && Array.isArray(body.prior_episodes),
    );
  }

  /** POST .../episodes/next: release/play exactly the next manifest event through the real pipeline. */
  async function advanceReplayEpisode(manifestId: string): Promise<AdvanceResult> {
    const path = `/replay/manifests/${seg(manifestId)}/episodes/next`;
    const body = await postJson(path, undefined, postTimeoutMs);
    return expectShape<AdvanceResult>(path, body, isObject(body) && typeof body.episode === "number" && isObject(body.released));
  }

  /**
   * POST .../reset: move the released cursor back to `episode` (0..released-so-far). Bookkeeping is
   * kept; two resets to the same episode answer the same released events and digest (HAR-129 §C).
   */
  async function resetReplay(manifestId: string, episode: number): Promise<ResetResult> {
    const path = `/replay/manifests/${seg(manifestId)}/reset`;
    const body = await postJson(path, { episode });
    return expectShape<ResetResult>(path, body, isObject(body) && typeof body.episode === "number" && Array.isArray(body.released_events));
  }

  /** Agent runs, newest first (the demo's decision runs; HAR-96 §12 runs surface). */
  async function listRuns(opts: RunsOptions = {}): Promise<AgentRun[]> {
    const body = await getJson("/runs", { limit: opts.limit, account_id: opts.accountId, status: opts.status });
    return expectShape<{ items: AgentRun[] }>("/runs", body, isObject(body) && Array.isArray(body.items)).items;
  }

  /** One agent run (404 propagates: the page renders notFound). */
  async function getRun(runId: string): Promise<AgentRun> {
    const path = `/runs/${seg(runId)}`;
    const body = await getJson(path);
    return expectShape<AgentRun>(path, body, isObject(body) && typeof body.id === "string");
  }

  /**
   * The run's trace (HAR-97 §3 chain reconstruction). Null on 404: a run can exist without a
   * materialized trace (e.g. generation still running) — the page shows the section with a notice.
   */
  async function getRunTrace(runId: string): Promise<RunTrace | null> {
    const path = `/runs/${seg(runId)}/trace`;
    const res = await get(path);
    if (res.ok) {
      const body = await res.json();
      return expectShape<RunTrace>(path, body, isObject(body) && isObject(body.run) && isObject(body.placeholders));
    }
    const err = await errorOf(res);
    if (err.status === 404) return null;
    throw err;
  }

  /**
   * The strategy set (exactly 3 candidates) plus one eval bundle each (HAR-129 §6/§9). Null on 404
   * (`not_found`, `strategies_not_ready`): the run may predate the demo path or still be generating.
   */
  async function getRunStrategies(runId: string): Promise<RunStrategies | null> {
    const path = `/runs/${seg(runId)}/strategies`;
    const res = await get(path);
    if (res.ok) {
      const body = await res.json();
      return expectShape<RunStrategies>(path, body, isObject(body) && isObject(body.strategy_set) && Array.isArray(body.eval_bundles));
    }
    const err = await errorOf(res);
    if (err.status === 404) return null;
    throw err;
  }

  /**
   * The human's choice among the candidates (HAR-129 §8). Null on 404 (`not_found`, `no_decision`):
   * "nobody has chosen yet" is a normal state, not an error.
   */
  async function getStrategyDecision(runId: string): Promise<HumanStrategyDecision | null> {
    const path = `/runs/${seg(runId)}/strategy-decision`;
    const res = await get(path);
    if (res.ok) {
      const body = await res.json();
      return expectShape<HumanStrategyDecision>(path, body, isObject(body) && typeof body.id === "string");
    }
    const err = await errorOf(res);
    if (err.status === 404) return null;
    throw err;
  }

  /**
   * Ghost's inference of why the human chose what they chose (HAR-129 §11), with the human's verdict
   * on it — the semantic-delta record. Null on 404 (`not_found`, `inference_not_ready`).
   */
  async function getJudgmentInference(episodeId: string): Promise<JudgmentInference | null> {
    const path = `/episodes/${seg(episodeId)}/judgment-inference`;
    const res = await get(path);
    if (res.ok) {
      const body = await res.json();
      return expectShape<JudgmentInference>(path, body, isObject(body) && typeof body.decision_episode_id === "string");
    }
    const err = await errorOf(res);
    if (err.status === 404) return null;
    throw err;
  }

  /** The organization's learned knowledge (HAR-97 §18), newest human keys first by number. */
  async function listKnowledge(opts: KnowledgeOptions = {}): Promise<Knowledge[]> {
    const body = await getJson("/knowledge", { status: opts.status, limit: opts.limit });
    return expectShape<{ items: Knowledge[] }>("/knowledge", body, isObject(body) && Array.isArray(body.items)).items;
  }

  /** One knowledge object (404 propagates: the page renders notFound). */
  async function getKnowledge(id: string): Promise<Knowledge> {
    const path = `/knowledge/${seg(id)}`;
    const body = await getJson(path);
    return expectShape<Knowledge>(path, body, isObject(body) && typeof body.id === "string");
  }

  /**
   * "This eval is wrong" (HAR-97 E19): records a dispute of one EvalResult. 201 new / 200 identical repeat both
   * return the stored EvalDispute; refusals (422 invalid_request, expected_equals_verdict, unknown_person; 404)
   * propagate as CoreError for the form to explain.
   */
  async function disputeEvalResult(resultId: string, body: EvalDisputeRequest): Promise<EvalDispute> {
    const path = `/eval-results/${seg(resultId)}/disputes`;
    const res = await postJson(path, body);
    return expectShape<EvalDispute>(path, res, isObject(res) && typeof res.id === "string" && res.eval_result_id === resultId);
  }

  /**
   * The surface-message ref for a subject (HAR-136): null on 404 (nothing reserved). `ts` null means
   * reserved but not provably posted; non-null means the surface recorded the post.
   */
  async function getSurfaceMessage(subjectId: string, surface: string, kind: string): Promise<SurfaceMessage | null> {
    const path = `/surface-messages/${seg(subjectId)}/${encodeURIComponent(surface)}/${encodeURIComponent(kind)}`;
    const res = await get(path);
    if (res.ok) {
      const body = await res.json();
      return expectShape<SurfaceMessage>(path, body, isObject(body) && typeof body.subject_id === "string");
    }
    const err = await errorOf(res);
    if (err.status === 404) return null;
    throw err;
  }

  /**
   * The account's newest business-intelligence update (HAR-129 §5 — the object Slack Message 1 rests
   * on). Null on 404: the account has none yet.
   */
  async function getLatestBusinessIntelligence(accountId: string): Promise<BusinessIntelligence | null> {
    const path = `/accounts/${seg(accountId)}/business-intelligence/latest`;
    const res = await get(path);
    if (res.ok) {
      const body = await res.json();
      return expectShape<BusinessIntelligence>(path, body, isObject(body) && typeof body.id === "string" && typeof body.account_change_id === "string");
    }
    const err = await errorOf(res);
    if (err.status === 404) return null;
    throw err;
  }

  /** Liveness (HAR-145 System view): the core's unauthenticated /healthz. Throws CoreError("unreachable") on failure. */
  async function getHealth(): Promise<{ status: string }> {
    const body = await getJson("/healthz");
    return expectShape<{ status: string }>("/healthz", body, isObject(body) && typeof body.status === "string");
  }

  /**
   * Unacknowledged outbox events for a consumer (HAR-117, newest consumer-visible queue state).
   * `consumer` is required by the contract ("slack" is the demo's surface consumer).
   */
  async function listOutboxEvents(consumer: string): Promise<OutboxEvent[]> {
    const body = await getJson("/outbox/events", { consumer });
    return expectShape<{ items: OutboxEvent[] }>("/outbox/events", body, isObject(body) && Array.isArray(body.items)).items;
  }

  /** The provider circuit breaker (HAR-135): open = every model call paused. */
  async function getProviderBreaker(): Promise<ProviderBreakerStatus> {
    const body = await getJson("/provider-breaker");
    return expectShape<ProviderBreakerStatus>("/provider-breaker", body, isObject(body) && typeof body.state === "string");
  }

  /**
   * The held-out event's leak check (HAR-129 §B): withheld = nothing about it leaked into the world
   * the agent sees; leaked lists each escape by store+kind+id. Null on 404 when the manifest is unknown.
   */
  async function getInvisibility(manifestId: string): Promise<InvisibilityReport | null> {
    const path = `/replay/manifests/${seg(manifestId)}/invisibility`;
    const res = await get(path);
    if (res.ok) {
      const body = await res.json();
      return expectShape<InvisibilityReport>(path, body, isObject(body) && typeof body.status === "string" && Array.isArray(body.leaks));
    }
    const err = await errorOf(res);
    if (err.status === 404) return null;
    throw err;
  }

  return {
    ...createControlReads({ get, getJson }),
    listAccounts,
    getAccountGraph,
    getEventGraphDiff,
    getAccountState,
    getTimeline,
    getReplayEpisodes,
    advanceReplayEpisode,
    resetReplay,
    listRuns,
    getRun,
    getRunTrace,
    getRunStrategies,
    getStrategyDecision,
    getJudgmentInference,
    listKnowledge,
    getKnowledge,
    disputeEvalResult,
    getSurfaceMessage,
    getLatestBusinessIntelligence,
    getHealth,
    listOutboxEvents,
    getProviderBreaker,
    getInvisibility,
  };
}

export type CoreClient = ReturnType<typeof createCoreClient>;
