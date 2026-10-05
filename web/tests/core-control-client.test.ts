// The control-plane reads of the core client (HAR-145): paging, 404-to-null, and the honest 422 of a comparison.
import { describe, expect, it, vi } from "vitest";
import { CoreError, createCoreClient } from "@/lib/api/core-client";

const ID = "0a0c0000-0000-4000-8000-000000000001";
const OTHER = "0a0c0000-0000-4000-8000-000000000002";

const json = (body: unknown, status = 200) => new Response(JSON.stringify(body), { status, headers: { "content-type": "application/json" } });
const err = (status: number, code: string) => json({ error: { code, message: code } }, status);

function client(handler: (url: URL, init: RequestInit) => Response | Promise<Response>) {
  const calls: { url: URL; init: RequestInit }[] = [];
  const fetchImpl = vi.fn(async (input: string | URL | Request, init?: RequestInit) => {
    const url = new URL(String(input));
    calls.push({ url, init: init ?? {} });
    return handler(url, init ?? {});
  }) as unknown as typeof fetch;
  return { api: createCoreClient({ baseUrl: "http://core.test:8080", token: "t0k", fetchImpl }), calls };
}

describe("keyset pages", () => {
  it("reads one account page with its cursor", async () => {
    const { api, calls } = client(() => json({ items: [{ id: ID }], next_cursor: "abc" }));
    await expect(api.listAccountsPage({ limit: 25, cursor: "prev" })).resolves.toEqual({ items: [{ id: ID }], nextCursor: "abc" });
    expect(calls[0]!.url.pathname).toBe("/accounts");
    expect(calls[0]!.url.searchParams.get("limit")).toBe("25");
    expect(calls[0]!.url.searchParams.get("cursor")).toBe("prev");
  });

  it("reports the last page as a null cursor", async () => {
    const { api } = client(() => json({ items: [], next_cursor: null }));
    await expect(api.listAccountsPage()).resolves.toEqual({ items: [], nextCursor: null });
  });

  it("rejects a page body without items", async () => {
    const { api } = client(() => json({ nope: true }));
    await expect(api.listAccountsPage()).rejects.toMatchObject({ code: "bad_response" });
  });

  it("reads run pages with the account and status filters", async () => {
    const { api, calls } = client(() => json({ items: [], next_cursor: "n2" }));
    await expect(api.listRunsPage({ accountId: ID, status: "succeeded", cursor: "c1", limit: 10 })).resolves.toEqual({ items: [], nextCursor: "n2" });
    const q = calls[0]!.url.searchParams;
    expect(calls[0]!.url.pathname).toBe("/runs");
    expect([q.get("account_id"), q.get("status"), q.get("cursor"), q.get("limit")]).toEqual([ID, "succeeded", "c1", "10"]);
  });

  it("surfaces a malformed cursor (400) as a CoreError", async () => {
    const { api } = client(() => err(400, "bad_cursor"));
    await expect(api.listRunsPage({ cursor: "zzz" })).rejects.toMatchObject({ status: 400, code: "bad_cursor" });
  });

  it("pages eval runs by account", async () => {
    const { api, calls } = client(() => json({ items: [], next_cursor: null }));
    await api.listEvalRunsPage({ accountId: ID, limit: 5 });
    expect(calls[0]!.url.pathname).toBe("/eval-runs");
    expect(calls[0]!.url.searchParams.get("account_id")).toBe(ID);
  });
});

describe("play progress", () => {
  it("GETs the manifest progress", async () => {
    const body = { scope: "manifest", overall: "running", stages: [] };
    const { api, calls } = client(() => json(body));
    await expect(api.getReplayProgress(ID)).resolves.toEqual(body);
    expect(calls[0]!.url.pathname).toBe(`/replay/manifests/${ID}/progress`);
    expect(calls[0]!.init.method).toBe("GET");
  });

  it("treats an unknown manifest as null and a transport error as a throw", async () => {
    const missing = client(() => err(404, "manifest_not_found"));
    await expect(missing.api.getReplayProgress(ID)).resolves.toBeNull();
    const down = client(() => {
      throw new TypeError("fetch failed");
    });
    await expect(down.api.getReplayProgress(ID)).rejects.toMatchObject({ code: "unreachable" });
  });

  it("rejects a body that is not a progress document", async () => {
    const { api } = client(() => json({ overall: "running" }));
    await expect(api.getReplayProgress(ID)).rejects.toMatchObject({ code: "bad_response" });
  });

  it("refuses a path segment that is not a uuid", async () => {
    const { api } = client(() => json({}));
    await expect(api.getReplayProgress("../x")).rejects.toThrow(/not a uuid/);
  });
});

describe("episode reads", () => {
  it("GETs the episode summary and maps 404 to null", async () => {
    const ok = client(() => json({ id: ID, agent_run_id: OTHER }));
    await expect(ok.api.getEpisode(ID)).resolves.toMatchObject({ id: ID });
    expect(ok.calls[0]!.url.pathname).toBe(`/episodes/${ID}`);
    const missing = client(() => err(404, "not_found"));
    await expect(missing.api.getEpisode(ID)).resolves.toBeNull();
  });

  it("GETs the trace, the knowledge mutations and the metrics", async () => {
    const { api, calls } = client((url) => {
      if (url.pathname.endsWith("/trace")) return json({ episode_id: ID, spans: [] });
      if (url.pathname.endsWith("/knowledge-mutations")) return json({ episode_id: ID, items: [{ id: OTHER }] });
      return json({ classification: "metric", measured: false });
    });
    await expect(api.getEpisodeTrace(ID)).resolves.toMatchObject({ episode_id: ID });
    await expect(api.listEpisodeKnowledgeMutations(ID)).resolves.toEqual([{ id: OTHER }]);
    await expect(api.getEpisodeMetrics(ID)).resolves.toMatchObject({ classification: "metric" });
    expect(calls.map((c) => c.url.pathname)).toEqual([`/episodes/${ID}/trace`, `/episodes/${ID}/knowledge-mutations`, `/episodes/${ID}/metrics`]);
  });

  it("maps 404 to null on trace, mutations and metrics, but throws on a 500", async () => {
    const missing = client(() => err(404, "not_found"));
    await expect(missing.api.getEpisodeTrace(ID)).resolves.toBeNull();
    await expect(missing.api.listEpisodeKnowledgeMutations(ID)).resolves.toBeNull();
    await expect(missing.api.getEpisodeMetrics(ID)).resolves.toBeNull();
    const broken = client(() => err(500, "internal"));
    await expect(broken.api.getEpisodeTrace(ID)).rejects.toBeInstanceOf(CoreError);
  });

  it("GETs the run recomputation and maps 404 to null", async () => {
    const ok = client(() => json({ run_id: ID, entries: [] }));
    await expect(ok.api.getRunRecomputation(ID)).resolves.toMatchObject({ run_id: ID });
    expect(ok.calls[0]!.url.pathname).toBe(`/runs/${ID}/recomputation`);
    const missing = client(() => err(404, "not_found"));
    await expect(missing.api.getRunRecomputation(ID)).resolves.toBeNull();
  });
});

describe("eval run reads", () => {
  it("GETs the families of an eval run, null on 404", async () => {
    const ok = client(() => json({ eval_run_id: ID, areas: [] }));
    await expect(ok.api.getEvalRunFamilies(ID)).resolves.toMatchObject({ eval_run_id: ID });
    expect(ok.calls[0]!.url.pathname).toBe(`/eval-runs/${ID}/families`);
    const missing = client(() => err(404, "not_found"));
    await expect(missing.api.getEvalRunFamilies(ID)).resolves.toBeNull();
  });

  it("compares two runs by id", async () => {
    const { api, calls } = client(() => json({ a: {}, b: {}, rows: [], overall: { change: "unchanged" } }));
    await expect(api.compareEvalRuns(ID, OTHER)).resolves.toMatchObject({ outcome: "compared", comparison: { overall: { change: "unchanged" } } });
    expect(calls[0]!.url.pathname).toBe("/eval-runs/compare");
    expect(calls[0]!.url.searchParams.get("a")).toBe(ID);
    expect(calls[0]!.url.searchParams.get("b")).toBe(OTHER);
  });

  it("reports not_comparable (422) as an outcome, never as a failure", async () => {
    const { api } = client(() => err(422, "not_comparable"));
    await expect(api.compareEvalRuns(ID, OTHER)).resolves.toEqual({ outcome: "not_comparable" });
  });

  it("reports an unknown side (404) and lets other errors throw", async () => {
    const missing = client(() => err(404, "not_found"));
    await expect(missing.api.compareEvalRuns(ID, OTHER)).resolves.toEqual({ outcome: "not_found" });
    const broken = client(() => err(500, "internal"));
    await expect(broken.api.compareEvalRuns(ID, OTHER)).rejects.toMatchObject({ status: 500 });
  });
});
