// Error types, id/path helpers, response-shape guards and read options shared by the core client (split from
// core-client.ts to keep both files small; core-client re-exports everything public from here).
import { UUID } from "../uuid";

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

/** The display-safe error code: the core's code when it has one, else the caller's fallback. */
export const errorCode = (e: unknown, fallback: string): string => (e instanceof CoreError ? e.code : fallback);

/** Ids are contract uuids; anything else (e.g. "..") never reaches the core as a path segment. */
export function seg(id: string): string {
  if (!UUID.test(id)) throw new InvalidIdError(id);
  return id;
}

export type Query = Record<string, string | number | boolean | undefined>;

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

export interface EpisodesOptions {
  /** The released episode to view (0..released); defaults to the released cursor. */
  at?: number;
}

export interface RunsOptions {
  limit?: number;
  accountId?: string;
  status?: string;
}

export interface KnowledgeOptions {
  /** Lifecycle status filter (candidate | provisional | supported | confirmed | disputed | stale). */
  status?: string;
  limit?: number;
}

export async function errorOf(res: Response): Promise<CoreError> {
  const text = await res.text().catch(() => "");
  try {
    const { error } = JSON.parse(text) as { error?: { code?: string; message?: string } };
    if (error?.code) return new CoreError(res.status, error.code, `${error.code}: ${error.message ?? ""}`.trim());
  } catch {
    // not the core's error envelope (a proxy page, say): fall through to the generic code
  }
  return new CoreError(res.status, `http_${res.status}`, `core answered HTTP ${res.status}`);
}

export const isObject = (v: unknown): v is Record<string, unknown> => typeof v === "object" && v !== null && !Array.isArray(v);

export function expectShape<T>(path: string, body: unknown, ok: boolean): T {
  if (!ok) throw new CoreError(200, "bad_response", `core GET ${path} returned an unexpected body`);
  return body as T;
}
