import { describe, expect, it, vi } from "vitest";
import { CoreError, createCoreClient, coreConfigFromEnv, InvalidIdError } from "@/lib/api/core-client";
import type { Activity } from "@/lib/api/types";
import { loadExample, loadFixture } from "./contract-validator";

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

describe("run chain reads (WP24)", () => {
  const RUN = "0f0a0000-0000-4000-8000-000000000601";
  const EPISODE = "0e9e0000-0000-4000-8000-000000000a01";

  it("GET /runs/{id} propagates the run, and 404s propagate to notFound", async () => {
    const run = loadFixture<{ run: unknown }>("acme.run-trace.json").run;
    const { api, calls } = client((url) => (url.pathname.endsWith("/trace") ? json({ no: true }) : json(run)));
    await expect(api.getRun(RUN)).resolves.toEqual(run);
    expect(calls[0]!.url.pathname).toBe(`/runs/${RUN}`);
    const missing = client(() => json({ error: { code: "not_found", message: "no such run" } }, 404));
    await expect(missing.api.getRun(RUN)).rejects.toMatchObject({ status: 404, code: "not_found" });
  });

  it("GET /runs/{id}/trace returns null on 404, and rejects a malformed body", async () => {
    const trace = loadFixture("acme.run-trace.json");
    const { api, calls } = client(() => json(trace));
    await expect(api.getRunTrace(RUN)).resolves.toEqual(trace);
    expect(calls[0]!.url.pathname).toBe(`/runs/${RUN}/trace`);
    const none = client(() => json({ error: { code: "not_found", message: "no trace" } }, 404));
    await expect(none.api.getRunTrace(RUN)).resolves.toBeNull();
    const bad = client(() => json({ run: null }));
    await expect(bad.api.getRunTrace(RUN)).rejects.toMatchObject({ code: "bad_response" });
    const down = client(() => json({ error: { code: "internal", message: "x" } }, 500));
    await expect(down.api.getRunTrace(RUN)).rejects.toMatchObject({ status: 500 });
  });

  it("GET /runs/{id}/strategies is null for strategies_not_ready and not_found", async () => {
    const strategies = loadFixture("acme.run-strategies.json");
    const { api } = client(() => json(strategies));
    await expect(api.getRunStrategies(RUN)).resolves.toEqual(strategies);
    for (const code of ["strategies_not_ready", "not_found"]) {
      const c = client(() => json({ error: { code, message: code } }, 404));
      await expect(c.api.getRunStrategies(RUN)).resolves.toBeNull();
    }
  });

  it("GET /runs/{id}/strategy-decision is null for no_decision", async () => {
    const c = client(() => json({ error: { code: "no_decision", message: "nobody chose" } }, 404));
    await expect(c.api.getStrategyDecision(RUN)).resolves.toBeNull();
  });

  it("GET /episodes/{id}/judgment-inference is null for inference_not_ready", async () => {
    const { api, calls } = client(() => json(loadExample("judgment_inference")));
    await expect(api.getJudgmentInference(EPISODE)).resolves.toMatchObject({ decision_episode_id: EPISODE });
    expect(calls[0]!.url.pathname).toBe(`/episodes/${EPISODE}/judgment-inference`);
    const none = client(() => json({ error: { code: "inference_not_ready", message: "still thinking" } }, 404));
    await expect(none.api.getJudgmentInference(EPISODE)).resolves.toBeNull();
  });
});

describe("knowledge reads (WP24)", () => {
  it("GET /knowledge passes status and limit and returns the items", async () => {
    const { api, calls } = client(() => json({ items: [loadExample("knowledge")] }));
    const items = await api.listKnowledge({ status: "confirmed", limit: 50 });
    expect(items).toHaveLength(1);
    expect(calls[0]!.url.pathname).toBe("/knowledge");
    expect(calls[0]!.url.searchParams.get("status")).toBe("confirmed");
    expect(calls[0]!.url.searchParams.get("limit")).toBe("50");
  });

  it("GET /knowledge/{id} returns the object and propagates 404", async () => {
    const knowledge = loadExample<{ id: string }>("knowledge");
    const { api, calls } = client(() => json(knowledge));
    await expect(api.getKnowledge(knowledge.id)).resolves.toEqual(knowledge);
    expect(calls[0]!.url.pathname).toBe(`/knowledge/${knowledge.id}`);
    const missing = client(() => json({ error: { code: "not_found", message: "none" } }, 404));
    await expect(missing.api.getKnowledge(knowledge.id)).rejects.toMatchObject({ status: 404 });
  });

  it("rejects non-uuid knowledge ids before any request", async () => {
    const { api, calls } = client(() => json({}));
    await expect(api.getKnowledge("K17")).rejects.toBeInstanceOf(InvalidIdError);
    expect(calls).toHaveLength(0);
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

describe("POST timeouts", () => {
  // A fetch that honours the abort signal and answers after `ms`.
  const slow = (ms: number) => (_input: string | URL | Request, init?: RequestInit) =>
    new Promise<Response>((resolve, reject) => {
      const t = setTimeout(() => resolve(json({ episode: 1, total: 2, released: {}, id: "r1", eval_result_id: "e1" })), ms);
      init?.signal?.addEventListener("abort", () => {
        clearTimeout(t);
        reject(new Error("aborted"));
      });
    });
  const make = () => createCoreClient({ baseUrl: "http://core.test:8080", token: undefined, timeoutMs: 20, postTimeoutMs: 1000, fetchImpl: slow(120) as unknown as typeof fetch });

  it("lets Play stay open for the long POST timeout", async () => {
    await expect(make().advanceReplayEpisode("0d3a0000-0000-4000-8000-000000000501")).resolves.toMatchObject({ episode: 1 });
  });

  it("holds an eval dispute to the ordinary request timeout", async () => {
    await expect(make().disputeEvalResult("0e0a0000-0000-4000-8000-000000000001", { expected: "pass" } as never)).rejects.toMatchObject({ code: "unreachable" });
  });

  it("holds a replay reset to the ordinary request timeout", async () => {
    await expect(make().resetReplay("0d3a0000-0000-4000-8000-000000000501", 0)).rejects.toMatchObject({ code: "unreachable" });
  });
});
