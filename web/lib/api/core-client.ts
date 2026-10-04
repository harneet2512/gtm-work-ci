// Read-only client for the Go core. SERVER-ONLY: it reads GHOST_API_TOKEN (no NEXT_PUBLIC_ prefix), so the
// token is never inlined into a browser bundle; pages call it from server components.
import type { AccountState, AccountSummary, Activity, Graph, GraphDiff } from "./types";

export interface CoreConfig {
  baseUrl: string;
  token: string | undefined;
}

export interface ClientOptions extends CoreConfig {
  fetchImpl?: typeof fetch;
  timeoutMs?: number;
}

const DEFAULT_BASE_URL = "http://127.0.0.1:8080";
const DEFAULT_TIMEOUT_MS = 10_000;
const TIMELINE_PAGE_SIZE = 200;
const TIMELINE_MAX_PAGES = 5;
const UUID = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/;

export function coreConfigFromEnv(env: Record<string, string | undefined>): CoreConfig {
  return { baseUrl: env.CORE_URL || DEFAULT_BASE_URL, token: env.GHOST_API_TOKEN || undefined };
}

export class CoreError extends Error {
  constructor(
    readonly status: number,
    readonly code: string,
    message: string,
  ) {
    super(message);
    this.name = "CoreError";
  }
}

export class InvalidIdError extends Error {
  constructor(id: string) {
    super(`not a uuid: ${JSON.stringify(id.slice(0, 64))}`);
    this.name = "InvalidIdError";
  }
}

/** Ids are contract uuids; anything else (e.g. "..") never reaches the core as a path segment. */
function seg(id: string): string {
  if (!UUID.test(id)) throw new InvalidIdError(id);
  return id;
}

type Query = Record<string, string | number | boolean | undefined>;

/** Options shared by the reads that can be answered as of a world-time cutoff (ADR-0019). */
export interface WorldAsOf {
  /** World time, strictly before: only data derived from activities with `occurred_at < worldAsOf` (ADR-0019). */
  worldAsOf?: string;
}

export interface GraphOptions extends WorldAsOf {
  limit?: number;
  includeClosed?: boolean;
}

export interface TimelineOptions {
  /** Strict world-time cursor: only activities with `occurred_at < before`. */
  before?: string;
  maxPages?: number;
}

async function errorOf(res: Response): Promise<CoreError> {
  const text = await res.text().catch(() => "");
  try {
    const { error } = JSON.parse(text) as { error?: { code?: string; message?: string } };
    if (error?.code) return new CoreError(res.status, error.code, `${error.code}: ${error.message ?? ""}`.trim());
  } catch {
    // not the core's error envelope (a proxy page, say): fall through to the generic code
  }
  return new CoreError(res.status, `http_${res.status}`, `core answered HTTP ${res.status}`);
}

const isObject = (v: unknown): v is Record<string, unknown> => typeof v === "object" && v !== null && !Array.isArray(v);

function expectShape<T>(path: string, body: unknown, ok: boolean): T {
  if (!ok) throw new CoreError(200, "bad_response", `core GET ${path} returned an unexpected body`);
  return body as T;
}

export function createCoreClient(options: ClientOptions) {
  const fetchImpl = options.fetchImpl ?? fetch;
  const timeoutMs = options.timeoutMs ?? DEFAULT_TIMEOUT_MS;
  const base = options.baseUrl.endsWith("/") ? options.baseUrl : `${options.baseUrl}/`;

  async function get(path: string, query: Query = {}): Promise<Response> {
    const url = new URL(path.replace(/^\//, ""), base);
    for (const [key, value] of Object.entries(query)) if (value !== undefined) url.searchParams.set(key, String(value));
    const headers: Record<string, string> = { Accept: "application/json" };
    if (options.token) headers.Authorization = `Bearer ${options.token}`;
    try {
      return await fetchImpl(url.toString(), { method: "GET", headers, cache: "no-store", signal: AbortSignal.timeout(timeoutMs) });
    } catch (cause) {
      throw new CoreError(0, "unreachable", `core unreachable: ${cause instanceof Error ? cause.message : String(cause)}`);
    }
  }

  async function getJson(path: string, query?: Query): Promise<unknown> {
    const res = await get(path, query);
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

  return { listAccounts, getAccountGraph, getEventGraphDiff, getAccountState, getTimeline };
}

export type CoreClient = ReturnType<typeof createCoreClient>;
