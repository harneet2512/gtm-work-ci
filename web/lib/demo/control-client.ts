// Client for the demo's control service (core-go internal/codespace). SERVER-ONLY: it reads
// GHOST_DEMO_CONTROL_TOKEN, made for each boot (no NEXT_PUBLIC_ prefix), so the token never reaches a browser bundle; pages, route handlers and
// server actions call it. The control service listens on loopback only and is not a forwarded port.
import type { DemoCase, DemoPhase, DemoService, DemoStatus, StatusResult } from "./types";

export interface ControlConfig {
  baseUrl: string | undefined;
  token: string | undefined;
}

export interface ControlOptions extends ControlConfig {
  fetchImpl?: typeof fetch;
  timeoutMs?: number;
}

const DEFAULT_TIMEOUT_MS = 8_000;
const PHASES: readonly DemoPhase[] = ["ready", "starting", "busy", "setup", "needs-secrets", "attention"];

export function controlConfigFromEnv(env: Record<string, string | undefined>): ControlConfig {
  return { baseUrl: env.GHOST_DEMO_CONTROL_URL || undefined, token: env.GHOST_DEMO_CONTROL_TOKEN || undefined };
}

const isObject = (v: unknown): v is Record<string, unknown> => typeof v === "object" && v !== null && !Array.isArray(v);
const str = (v: unknown): string | undefined => (typeof v === "string" ? v : undefined);

function parseService(v: unknown): DemoService | null {
  if (!isObject(v) || typeof v.name !== "string" || typeof v.healthy !== "boolean") return null;
  return { name: v.name, state: str(v.state) ?? "unknown", healthy: v.healthy };
}

function parseCase(v: unknown): DemoCase | null {
  if (!isObject(v) || typeof v.slot !== "string" || typeof v.label !== "string") return null;
  return {
    slot: v.slot,
    label: v.label,
    seeded: v.seeded === true,
    active: v.active === true,
    manifest_id: str(v.manifest_id),
    account_id: str(v.account_id),
    opportunity_id: str(v.opportunity_id),
    invisibility: str(v.invisibility),
  };
}

/** The status document, or null when the body is not one (a proxy error page, an older control service). */
export function parseStatus(body: unknown): DemoStatus | null {
  if (!isObject(body) || typeof body.ready !== "boolean" || typeof body.message !== "string") return null;
  if (typeof body.phase !== "string" || !PHASES.includes(body.phase as DemoPhase)) return null;
  const list = <T>(raw: unknown, one: (x: unknown) => T | null): T[] =>
    Array.isArray(raw) ? raw.map(one).filter((x): x is T => x !== null) : [];
  return {
    ready: body.ready,
    phase: body.phase as DemoPhase,
    message: body.message,
    services: list(body.services, parseService),
    cases: list(body.cases, parseCase),
    active_case: str(body.active_case),
    missing_secrets: Array.isArray(body.missing_secrets) ? body.missing_secrets.filter((x): x is string => typeof x === "string") : [],
    checked_at: str(body.checked_at),
  };
}

async function request(o: ControlOptions, method: "GET" | "POST", path: string, body?: unknown): Promise<Response> {
  const f = o.fetchImpl ?? fetch;
  const ctl = new AbortController();
  const timer = setTimeout(() => ctl.abort(), o.timeoutMs ?? DEFAULT_TIMEOUT_MS);
  try {
    return await f(`${o.baseUrl}${path}`, {
      method,
      headers: { Authorization: `Bearer ${o.token ?? ""}`, ...(body === undefined ? {} : { "Content-Type": "application/json" }) },
      body: body === undefined ? undefined : JSON.stringify(body),
      cache: "no-store",
      signal: ctl.signal,
    });
  } finally {
    clearTimeout(timer);
  }
}

/** The readiness status, "unconfigured" outside a codespace, "unreachable" while the control service is still starting. */
export async function fetchStatus(o: ControlOptions): Promise<StatusResult> {
  if (!o.baseUrl) return { kind: "unconfigured" };
  try {
    const res = await request(o, "GET", "/status");
    if (!res.ok) return { kind: "unreachable" };
    const status = parseStatus(await res.json());
    return status ? { kind: "ok", status } : { kind: "unreachable" };
  } catch {
    return { kind: "unreachable" };
  }
}

/** How long a handoff may take: the knowledge carry, a graph rebuild and a core restart. */
const HANDOFF_TIMEOUT_MS = 12 * 60_000;

export type HandoffOutcome = { ok: true; manifestId: string; accountId: string } | { ok: false; code: string };

/**
 * The hidden step behind Play when the account on screen has no episode left: the control service moves the demo to the next
 * case (the knowledge it learned is carried over, the graph and core are switched) and names it. Failures are reduced to the
 * service's error code.
 */
export async function postHandoff(o: ControlOptions, fromManifestId: string): Promise<HandoffOutcome> {
  if (!o.baseUrl) return { ok: false, code: "not_configured" };
  try {
    const res = await request({ timeoutMs: HANDOFF_TIMEOUT_MS, ...o }, "POST", "/handoff", { manifest_id: fromManifestId });
    const parsed: unknown = await res.json().catch(() => null);
    if (!res.ok) {
      const code = isObject(parsed) && isObject(parsed.error) ? str(parsed.error.code) : undefined;
      return { ok: false, code: code ?? `http_${res.status}` };
    }
    const manifestId = isObject(parsed) ? str(parsed.manifest_id) : undefined;
    const accountId = isObject(parsed) ? str(parsed.account_id) : undefined;
    return manifestId && accountId ? { ok: true, manifestId, accountId } : { ok: false, code: "bad_response" };
  } catch {
    return { ok: false, code: "unreachable" };
  }
}
