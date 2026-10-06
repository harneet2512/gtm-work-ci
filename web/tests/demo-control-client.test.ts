import { describe, expect, it, vi } from "vitest";
import { controlConfigFromEnv, fetchStatus, parseStatus, postHandoff } from "@/lib/demo/control-client";
import { statusDoc, wire } from "./demo-fixtures";

const TOKEN = "control-token-0123456789";
const BASE = "http://127.0.0.1:8099";
const json = (body: unknown, status = 200) => new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
const opts = (fetchImpl: typeof fetch, extra: Record<string, unknown> = {}) => ({ baseUrl: BASE, token: TOKEN, fetchImpl, ...extra });

describe("controlConfigFromEnv", () => {
  it("reads the control URL and this boot's control token, with empty strings as absent", () => {
    expect(controlConfigFromEnv({ GHOST_DEMO_CONTROL_URL: BASE, GHOST_DEMO_CONTROL_TOKEN: TOKEN })).toEqual({ baseUrl: BASE, token: TOKEN });
    // the long-lived core API token is not the control token: a handoff must not be callable with it
    expect(controlConfigFromEnv({ GHOST_DEMO_CONTROL_URL: BASE, GHOST_API_TOKEN: "core-token" })).toEqual({ baseUrl: BASE, token: undefined });
    expect(controlConfigFromEnv({ GHOST_DEMO_CONTROL_URL: "", GHOST_DEMO_CONTROL_TOKEN: "" })).toEqual({ baseUrl: undefined, token: undefined });
    expect(controlConfigFromEnv({})).toEqual({ baseUrl: undefined, token: undefined });
  });
});

describe("parseStatus", () => {
  it("accepts the document the control service serves", () => {
    const status = parseStatus(wire(statusDoc()));
    expect(status?.ready).toBe(true);
    expect(status?.message).toBe("All systems ready");
    expect(status?.cases.map((c) => c.slot)).toEqual(["case1", "case2"]);
    expect(status?.cases[0]?.manifest_id).toBe("0d3a0000-0000-4000-8000-000000000501");
    expect(status?.services).toHaveLength(6);
  });

  it.each([null, undefined, "ready", 7, [], {}, { ready: true }, { ready: "yes", message: "x", phase: "ready" }, { ready: true, message: "x", phase: "exploded" }])(
    "rejects %j",
    (body) => {
      expect(parseStatus(body)).toBeNull();
    },
  );

  it("drops malformed list entries instead of failing the page", () => {
    const body = { ...wire(statusDoc()), services: [{ name: "core", healthy: true }, { name: 7 }, null, "x"], cases: [{ slot: "c", label: "C" }, { slot: 1 }] };
    const status = parseStatus(body);
    expect(status?.services).toEqual([{ name: "core", state: "unknown", healthy: true }]);
    expect(status?.cases).toEqual([
      { slot: "c", label: "C", seeded: false, active: false, manifest_id: undefined, account_id: undefined, opportunity_id: undefined, invisibility: undefined },
    ]);
  });

  it("treats missing lists and non-string secrets as empty", () => {
    const status = parseStatus({ ready: false, phase: "starting", message: "Starting core...", missing_secrets: ["A", 3] });
    expect(status).toMatchObject({ services: [], cases: [], missing_secrets: ["A"] });
    expect(parseStatus({ ready: false, phase: "starting", message: "m" })?.missing_secrets).toEqual([]);
  });
});

describe("fetchStatus", () => {
  it("is unconfigured without a control URL and never calls the network", async () => {
    const f = vi.fn();
    expect(await fetchStatus({ baseUrl: undefined, token: TOKEN, fetchImpl: f as unknown as typeof fetch })).toEqual({ kind: "unconfigured" });
    expect(f).not.toHaveBeenCalled();
  });

  it("asks GET /status with the bearer token, uncached", async () => {
    const f = vi.fn(async () => json(wire(statusDoc())));
    const result = await fetchStatus(opts(f as unknown as typeof fetch));
    expect(result.kind).toBe("ok");
    const [url, init] = f.mock.calls[0] as unknown as [string, RequestInit];
    expect(url).toBe(`${BASE}/status`);
    expect(init.method).toBe("GET");
    expect((init.headers as Record<string, string>).Authorization).toBe(`Bearer ${TOKEN}`);
    expect(init.cache).toBe("no-store");
  });

  it.each([
    ["a 401", async () => json({ error: { code: "unauthorized" } }, 401)],
    ["a 500", async () => json({}, 500)],
    ["an HTML error page", async () => new Response("<html>bad gateway</html>", { status: 200 })],
    ["a document that is not a status", async () => json({ hello: "world" })],
    ["a refused connection", async () => { throw new TypeError("fetch failed"); }],
  ])("is unreachable on %s, and the result carries nothing sensitive", async (_name, impl) => {
    const result = await fetchStatus(opts(impl as unknown as typeof fetch));
    expect(result).toEqual({ kind: "unreachable" });
    expect(JSON.stringify(result)).not.toContain(TOKEN);
  });

  it("gives up on a control service that never answers", async () => {
    const hang: typeof fetch = (_url, init) =>
      new Promise((_resolve, reject) => {
        (init?.signal as AbortSignal).addEventListener("abort", () => reject(new DOMException("aborted", "AbortError")));
      });
    expect(await fetchStatus(opts(hang, { timeoutMs: 20 }))).toEqual({ kind: "unreachable" });
  });
});


describe("postHandoff", () => {
  const FROM = "0d3a0000-0000-4000-8000-000000000501";
  const NEXT = { slot: "case2", label: "EcoLite Innovations", manifest_id: "0d3a0000-0000-4000-8000-000000000502", account_id: "0d3a0000-0000-4000-8000-000000000102" };

  it("POSTs the manifest being left as JSON with the token and returns the next case", async () => {
    const f = vi.fn(async () => json(NEXT));
    const out = await postHandoff(opts(f as unknown as typeof fetch), FROM);
    expect(out).toEqual({ ok: true, manifestId: NEXT.manifest_id, accountId: NEXT.account_id });
    const [url, init] = f.mock.calls[0] as unknown as [string, RequestInit];
    expect(url).toBe(`${BASE}/handoff`);
    expect(init.method).toBe("POST");
    expect(JSON.parse(init.body as string)).toEqual({ manifest_id: FROM });
    const headers = init.headers as Record<string, string>;
    expect(headers["Content-Type"]).toBe("application/json");
    expect(headers.Authorization).toBe(`Bearer ${TOKEN}`);
  });

  it("is given as long as a graph rebuild and a core restart can take, not the status timeout", async () => {
    const f = vi.fn(async () => json(NEXT));
    const timers = vi.spyOn(globalThis, "setTimeout");
    await postHandoff(opts(f as unknown as typeof fetch), FROM);
    const delays = timers.mock.calls.map((c) => Number(c[1] ?? 0));
    timers.mockRestore();
    expect(Math.max(...delays)).toBeGreaterThanOrEqual(10 * 60_000);
  });

  it("returns the control service's error code, never its message", async () => {
    const none = async () => json({ error: { code: "no_next_case", message: "there is no later episode" } }, 409);
    expect(await postHandoff(opts(none as unknown as typeof fetch), FROM)).toEqual({ ok: false, code: "no_next_case" });
    const noEnvelope = async () => new Response("boom", { status: 502 });
    expect(await postHandoff(opts(noEnvelope as unknown as typeof fetch), FROM)).toEqual({ ok: false, code: "http_502" });
    const oddEnvelope = async () => json({ error: "plain" }, 400);
    expect(await postHandoff(opts(oddEnvelope as unknown as typeof fetch), FROM)).toEqual({ ok: false, code: "http_400" });
  });

  it("rejects a success that does not name the next case", async () => {
    const odd = async () => json({ slot: "case2" });
    expect(await postHandoff(opts(odd as unknown as typeof fetch), FROM)).toEqual({ ok: false, code: "bad_response" });
    const html = async () => new Response("<html></html>", { status: 200 });
    expect(await postHandoff(opts(html as unknown as typeof fetch), FROM)).toEqual({ ok: false, code: "bad_response" });
  });

  it("reports an unreachable service and a missing configuration without throwing", async () => {
    const down = async () => {
      throw new TypeError("fetch failed");
    };
    expect(await postHandoff(opts(down as unknown as typeof fetch), FROM)).toEqual({ ok: false, code: "unreachable" });
    expect(await postHandoff({ baseUrl: undefined, token: TOKEN }, FROM)).toEqual({ ok: false, code: "not_configured" });
  });
});
