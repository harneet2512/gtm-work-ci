import { describe, expect, it, vi } from "vitest";
import { CoreError, createCoreClient, coreConfigFromEnv, InvalidIdError } from "@/lib/api/core-client";
import type { Activity } from "@/lib/api/types";
import { loadFixture } from "./contract-validator";

const ACCOUNT = "0a0c0000-0000-4000-8000-000000000001";
const EVENT = "05e00000-0000-4000-8000-000000000101";

function json(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), { status, headers: { "content-type": "application/json" } });
}

function client(handler: (url: URL, init: RequestInit) => Response | Promise<Response>, token: string | null = "t0k") {
  const calls: { url: URL; init: RequestInit }[] = [];
  const fetchImpl = vi.fn(async (input: string | URL | Request, init?: RequestInit) => {
    const url = new URL(String(input));
    calls.push({ url, init: init ?? {} });
    return handler(url, init ?? {});
  }) as unknown as typeof fetch;
  return { api: createCoreClient({ baseUrl: "http://core.test:8080", token: token ?? undefined, fetchImpl }), calls };
}

describe("core client requests", () => {
  it("GETs the graph with the bearer token and limit/include_closed/world_as_of", async () => {
    const graph = loadFixture("acme.graph.json");
    const { api, calls } = client(() => json(graph));
    await expect(api.getAccountGraph(ACCOUNT, { limit: 20, includeClosed: true, worldAsOf: "2026-09-29T15:42:00Z" })).resolves.toEqual(graph);
    expect(calls[0]!.url.pathname).toBe(`/accounts/${ACCOUNT}/graph`);
    expect(calls[0]!.url.searchParams.get("limit")).toBe("20");
    expect(calls[0]!.url.searchParams.get("include_closed")).toBe("true");
    expect(calls[0]!.url.searchParams.get("world_as_of")).toBe("2026-09-29T15:42:00Z");
    expect((calls[0]!.init.headers as Record<string, string>).Authorization).toBe("Bearer t0k");
  });

  it("omits Authorization when no token is configured", async () => {
    const { api, calls } = client(() => json({ items: [] }), null);
    await api.listAccounts();
    expect((calls[0]!.init.headers as Record<string, string>).Authorization).toBeUndefined();
  });

  it("GETs the graph diff of an event", async () => {
    const diff = loadFixture("acme.graph-diff.json");
    const { api, calls } = client(() => json(diff));
    await expect(api.getEventGraphDiff(EVENT)).resolves.toEqual(diff);
    expect(calls[0]!.url.pathname).toBe(`/events/${EVENT}/graph-diff`);
  });

  it("passes world_as_of to the state endpoint and returns null while no state is computed", async () => {
    const { api, calls } = client(() => json({ error: { code: "state_not_computed", message: "no state" } }, 404));
    await expect(api.getAccountState(ACCOUNT, { worldAsOf: "2026-09-29T15:42:00.000Z" })).resolves.toBeNull();
    expect(calls[0]!.url.searchParams.get("world_as_of")).toBe("2026-09-29T15:42:00.000Z");
  });

  it("treats state_not_computed_before as no state, never a crash (H1)", async () => {
    const { api } = client(() => json({ error: { code: "state_not_computed_before", message: "none before" } }, 404));
    await expect(api.getAccountState(ACCOUNT, { worldAsOf: "2026-01-01T00:00:00Z" })).resolves.toBeNull();
  });

  it("still throws for an unknown account on the state endpoint", async () => {
    const { api } = client(() => json({ error: { code: "not_found", message: "no such account" } }, 404));
    await expect(api.getAccountState(ACCOUNT)).rejects.toMatchObject({ status: 404, code: "not_found" });
  });

  it("lists accounts", async () => {
    const { api } = client(() => json(loadFixture("accounts.json")));
    expect((await api.listAccounts())[0]!.name).toBe("Acme Corp");
  });
});

describe("timeline paging", () => {
  const items = loadFixture<{ items: Activity[] }>("acme.timeline.json").items;

  it("follows next_before until exhausted, newest page first", async () => {
    const { api, calls } = client((url) =>
      url.searchParams.has("before") ? json({ items: [items[2]], next_before: null }) : json({ items: [items[0], items[1]], next_before: "2026-09-20T10:00:00Z" }),
    );
    const all = await api.getTimeline(ACCOUNT);
    expect(all.map((a) => a.id)).toEqual([items[0]!.id, items[1]!.id, items[2]!.id]);
    expect(calls).toHaveLength(2);
    expect(calls[1]!.url.searchParams.get("before")).toBe("2026-09-20T10:00:00Z");
  });

  it("starts at a before cutoff and pages the tie-break cursor (next_before_id)", async () => {
    const { api, calls } = client((url) =>
      url.searchParams.has("before_id")
        ? json({ items: [items[1], items[2]], next_before: null, next_before_id: null })
        : json({ items: [items[0]], next_before: "2026-09-20T10:00:00Z", next_before_id: "0ac70000-0000-4000-8000-000000000100" }),
    );
    const all = await api.getTimeline(ACCOUNT, { before: "2026-09-29T15:42:00Z" });
    expect(all).toHaveLength(3);
    expect(calls[0]!.url.searchParams.get("before")).toBe("2026-09-29T15:42:00Z");
    expect(calls[0]!.url.searchParams.get("before_id")).toBeNull();
    expect(calls[1]!.url.searchParams.get("before")).toBe("2026-09-20T10:00:00Z");
    expect(calls[1]!.url.searchParams.get("before_id")).toBe("0ac70000-0000-4000-8000-000000000100");
  });

  it("stops at the page cap instead of looping forever", async () => {
    const { api, calls } = client(() => json({ items: [items[0]], next_before: "2026-01-01T00:00:00Z" }));
    const all = await api.getTimeline(ACCOUNT, { maxPages: 3 });
    expect(calls).toHaveLength(3);
    expect(all).toHaveLength(3);
  });
});

describe("core client failures", () => {
  it("raises CoreError with the core's error envelope", async () => {
    const { api } = client(() => json({ error: { code: "graph_unavailable", message: "neo4j is down" } }, 503));
    const err = await api.getAccountGraph(ACCOUNT).catch((e: unknown) => e);
    expect(err).toBeInstanceOf(CoreError);
    expect(err).toMatchObject({ status: 503, code: "graph_unavailable", message: expect.stringContaining("neo4j is down") });
  });

  it("raises CoreError for a non-JSON error body", async () => {
    const { api } = client(() => new Response("<html>bad gateway</html>", { status: 502 }));
    await expect(api.listAccounts()).rejects.toMatchObject({ status: 502, code: "http_502" });
  });

  it("raises CoreError when the network fails", async () => {
    const { api } = client(() => {
      throw new TypeError("fetch failed");
    });
    await expect(api.listAccounts()).rejects.toMatchObject({ status: 0, code: "unreachable" });
  });

  it("rejects ids that are not uuids before any request is made", async () => {
    const { api, calls } = client(() => json({}));
    await expect(api.getAccountGraph("../etc/passwd")).rejects.toBeInstanceOf(InvalidIdError);
    await expect(api.getEventGraphDiff("nope")).rejects.toBeInstanceOf(InvalidIdError);
    expect(calls).toHaveLength(0);
  });

  it("rejects a 200 whose body is not the expected shape", async () => {
    const { api } = client(() => json({ nope: true }));
    await expect(api.listAccounts()).rejects.toMatchObject({ code: "bad_response" });
    await expect(api.getAccountGraph(ACCOUNT)).rejects.toMatchObject({ code: "bad_response" });
  });
});

describe("coreConfigFromEnv", () => {
  it("prefers CORE_URL and reads the token from the server-only variable", () => {
    expect(coreConfigFromEnv({ CORE_URL: "http://a:1", GHOST_API_TOKEN: "x" })).toEqual({ baseUrl: "http://a:1", token: "x" });
  });

  it("defaults to the local core", () => {
    expect(coreConfigFromEnv({})).toEqual({ baseUrl: "http://127.0.0.1:8080", token: undefined });
  });

  it("treats an empty token as absent", () => {
    expect(coreConfigFromEnv({ GHOST_API_TOKEN: "" }).token).toBeUndefined();
  });
});
