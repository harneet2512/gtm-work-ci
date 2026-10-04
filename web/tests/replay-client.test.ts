import { describe, expect, it, vi } from "vitest";
import { CoreError, createCoreClient, InvalidIdError } from "@/lib/api/core-client";
import { loadExample, loadFixture } from "./contract-validator";

const MANIFEST = "0d3a0000-0000-4000-8000-000000000501";

function json(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), { status, headers: { "content-type": "application/json" } });
}

function client(handler: (url: URL, init: RequestInit) => Response | Promise<Response>) {
  const calls: { url: URL; init: RequestInit }[] = [];
  const fetchImpl = vi.fn(async (input: string | URL | Request, init?: RequestInit) => {
    const url = new URL(String(input));
    calls.push({ url, init: init ?? {} });
    return handler(url, init ?? {});
  }) as unknown as typeof fetch;
  return { api: createCoreClient({ baseUrl: "http://core.test:8080", token: "t0k", fetchImpl }), calls };
}

describe("replay endpoints", () => {
  it("GETs the episode view at the released cursor", async () => {
    const episodes = loadFixture("replay.episodes.json");
    const { api, calls } = client(() => json(episodes));
    await expect(api.getReplayEpisodes(MANIFEST)).resolves.toEqual(episodes);
    expect(calls[0]!.url.pathname).toBe(`/replay/manifests/${MANIFEST}/episodes`);
    expect(calls[0]!.url.searchParams.get("at")).toBeNull();
  });

  it("passes the at query to the episodes endpoint", async () => {
    const { api, calls } = client(() => json(loadFixture("replay.episodes.json")));
    await api.getReplayEpisodes(MANIFEST, { at: 1 });
    expect(calls[0]!.url.searchParams.get("at")).toBe("1");
  });

  it("POSTs the advance with an empty body", async () => {
    const result = { ...loadFixture<Record<string, unknown>>("replay.episodes.json"), episode: 3, released: {}, advanced_at: "2026-10-04T06:00:01Z" };
    const { api, calls } = client(() => json(result));
    await api.advanceReplayEpisode(MANIFEST);
    expect(calls[0]!.url.pathname).toBe(`/replay/manifests/${MANIFEST}/episodes/next`);
    expect(calls[0]!.init.method).toBe("POST");
    expect(calls[0]!.init.body).toBeUndefined();
    expect((calls[0]!.init.headers as Record<string, string>).Authorization).toBe("Bearer t0k");
  });

  it("POSTs the reset target episode as JSON", async () => {
    const result = { manifest_id: MANIFEST, account_id: "a", episode: 0, released_events: [], reset_at: "x", digest: "d" };
    const { api, calls } = client(() => json(result));
    await api.resetReplay(MANIFEST, 0);
    expect(calls[0]!.url.pathname).toBe(`/replay/manifests/${MANIFEST}/reset`);
    expect(calls[0]!.init.method).toBe("POST");
    expect(JSON.parse(String(calls[0]!.init.body))).toEqual({ episode: 0 });
    expect((calls[0]!.init.headers as Record<string, string>)["Content-Type"]).toBe("application/json");
  });

  it("maps a completed replay to the core's error envelope (409)", async () => {
    const { api } = client(() => json({ error: { code: "replay_complete", message: "done" } }, 409));
    await expect(api.advanceReplayEpisode(MANIFEST)).rejects.toMatchObject({ status: 409, code: "replay_complete" });
  });

  it("rejects a non-uuid manifest id before any request is made", async () => {
    const { api, calls } = client(() => json({}));
    await expect(api.getReplayEpisodes("../escape")).rejects.toBeInstanceOf(InvalidIdError);
    await expect(api.advanceReplayEpisode("nope")).rejects.toBeInstanceOf(InvalidIdError);
    await expect(api.resetReplay("nope", 0)).rejects.toBeInstanceOf(InvalidIdError);
    expect(calls).toHaveLength(0);
  });

  it("rejects a 200 whose body is not the expected shape", async () => {
    const { api } = client(() => json({ nope: true }));
    await expect(api.getReplayEpisodes(MANIFEST)).rejects.toMatchObject({ code: "bad_response" });
    await expect(api.resetReplay(MANIFEST, 0)).rejects.toMatchObject({ code: "bad_response" });
  });
});

describe("runs endpoint", () => {
  it("GETs /runs and unwraps items", async () => {
    const run = loadExample("agent_run");
    const { api, calls } = client(() => json({ items: [run] }));
    const runs = await api.listRuns({ limit: 50 });
    expect(runs).toHaveLength(1);
    expect(calls[0]!.url.pathname).toBe("/runs");
    expect(calls[0]!.url.searchParams.get("limit")).toBe("50");
  });

  it("passes account_id and status filters", async () => {
    const { api, calls } = client(() => json({ items: [] }));
    await api.listRuns({ accountId: "0a0c0000-0000-4000-8000-000000000001", status: "awaiting_human" });
    expect(calls[0]!.url.searchParams.get("account_id")).toBe("0a0c0000-0000-4000-8000-000000000001");
    expect(calls[0]!.url.searchParams.get("status")).toBe("awaiting_human");
  });
});
