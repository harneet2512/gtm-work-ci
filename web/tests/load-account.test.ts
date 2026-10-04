import { describe, expect, it, vi } from "vitest";
import { CoreError } from "@/lib/api/core-client";
import { loadAccountPage, type AccountApi } from "@/lib/load-account";
import type { AccountState, Activity, Graph, GraphDiff } from "@/lib/api/types";
import { loadExample, loadFixture } from "./contract-validator";

const ACCOUNT = "0a0c0000-0000-4000-8000-000000000001";
const EVENT = "05e00000-0000-4000-8000-000000000101";
const EVENT_AT = "2026-09-29T15:42:00Z";
const AFTER_AT = "2026-09-29T15:42:00.000001Z";
const graph = loadFixture<Graph>("acme.graph.json");
const graphBefore = loadFixture<Graph>("acme.graph-before.json");
const diff = loadFixture<GraphDiff>("acme.graph-diff.json");
const state = loadExample<AccountState>("account_state");
const timeline = loadFixture<{ items: Activity[] }>("acme.timeline.json").items;

function fakeApi(over: Partial<AccountApi> = {}): AccountApi {
  return {
    getAccountGraph: vi.fn(async () => graph),
    getEventGraphDiff: vi.fn(async () => diff),
    getAccountState: vi.fn(async () => state),
    getTimeline: vi.fn(async () => timeline),
    ...over,
  };
}

const fail = (status: number, code: string) => async (): Promise<never> => {
  throw new CoreError(status, code, `${code}: boom`);
};

describe("loadAccountPage", () => {
  it("Before Play reads the world strictly before N (graph, state and timeline) and shows no highlights", async () => {
    const api = fakeApi({ getAccountGraph: vi.fn(async () => graphBefore) });
    const page = await loadAccountPage(api, ACCOUNT, { event: EVENT });
    expect(page.view.view).toBe("before");
    expect(page.view.cutoff).toBe(EVENT_AT);
    expect(api.getAccountGraph).toHaveBeenCalledWith(ACCOUNT, { limit: 20, worldAsOf: EVENT_AT });
    expect(api.getAccountState).toHaveBeenCalledWith(ACCOUNT, { worldAsOf: EVENT_AT });
    expect(api.getTimeline).toHaveBeenCalledWith(ACCOUNT, { before: EVENT_AT });
    expect(page.view.graph).toEqual(graphBefore);
    expect(page.view.timeline).toHaveLength(2);
    expect(page.accountName).toBe("Acme Corp");
    expect(page.notices).toEqual([]);
    expect(page.badge.kind).toBe("CANDIDATE");
  });

  it("After Play reads the world at N+1µs (graph and state inclusive of N) and highlights the diff", async () => {
    const api = fakeApi();
    const page = await loadAccountPage(api, ACCOUNT, { event: EVENT, view: "after" });
    expect(api.getAccountGraph).toHaveBeenCalledWith(ACCOUNT, { limit: 20, worldAsOf: AFTER_AT });
    expect(api.getAccountState).toHaveBeenCalledWith(ACCOUNT, { worldAsOf: AFTER_AT });
    expect(api.getTimeline).toHaveBeenCalledWith(ACCOUNT, { before: AFTER_AT });
    expect(page.view.showMarks).toBe(true);
    expect(page.eventId).toBe(EVENT);
  });

  it("without an event it never asks for a diff and reads the current world", async () => {
    const api = fakeApi();
    const page = await loadAccountPage(api, ACCOUNT, {});
    expect(api.getEventGraphDiff).not.toHaveBeenCalled();
    expect(page.eventId).toBeNull();
    expect(api.getAccountGraph).toHaveBeenCalledWith(ACCOUNT, { limit: 20, worldAsOf: undefined });
    expect(page.view.graph).toEqual(graph);
  });

  it("a graph failure is fatal: the page has nothing to draw", async () => {
    const api = fakeApi({ getAccountGraph: fail(503, "graph_unavailable") });
    await expect(loadAccountPage(api, ACCOUNT, {})).rejects.toMatchObject({ code: "graph_unavailable" });
  });

  it("a failing graph diff is fatal for an event (fail closed, never show the After graph as Before)", async () => {
    const api = fakeApi({ getEventGraphDiff: fail(404, "not_found") });
    await expect(loadAccountPage(api, ACCOUNT, { event: EVENT })).rejects.toMatchObject({ code: "not_found" });
  });

  it("fails closed when the event cannot be placed in world time", async () => {
    const unprojected: GraphDiff = { ...diff, projected: false, changes: [] };
    const api = fakeApi({ getEventGraphDiff: async () => unprojected, getTimeline: async () => [] });
    await expect(loadAccountPage(api, ACCOUNT, { event: EVENT })).rejects.toMatchObject({ code: "event_time_unknown" });
  });

  it("resolves the event instant from the newest timeline page when the diff carries no activity props", async () => {
    const withoutProps: GraphDiff = { ...diff, changes: diff.changes.map((c) => ({ ...c, props: undefined })) };
    const api = fakeApi({ getEventGraphDiff: async () => withoutProps });
    const page = await loadAccountPage(api, ACCOUNT, { event: EVENT });
    expect(page.view.cutoff).toBe(EVENT_AT);
  });

  it("degrades with a notice when the timeline or state cannot be read", async () => {
    const api = fakeApi({ getTimeline: fail(0, "unreachable"), getAccountState: fail(500, "internal") });
    const page = await loadAccountPage(api, ACCOUNT, { event: EVENT, view: "after" });
    expect(page.notices).toHaveLength(2);
    expect(page.notices.join(" ")).toMatch(/timeline/i);
    expect(page.notices.join(" ")).toMatch(/state/i);
    expect(page.state).toBeNull();
    expect(page.badge.kind).toBe("none");
    expect(page.view.timeline).toEqual([]);
    // The graph and its diff still rendered: only the timeline and state degraded.
    expect(page.view.showMarks).toBe(true);
  });

  it("shows only the error code, not the raw core message", async () => {
    const api = fakeApi({ getTimeline: fail(500, "internal") });
    const page = await loadAccountPage(api, ACCOUNT, { event: EVENT });
    expect(page.notices.join(" ")).toContain("internal");
    expect(page.notices.join(" ")).not.toContain("boom");
  });

  it("ignores a malformed event id and cutoff with a notice instead of calling the core", async () => {
    const api = fakeApi();
    const page = await loadAccountPage(api, ACCOUNT, { event: "../x", cutoff: "yesterday" });
    expect(api.getEventGraphDiff).not.toHaveBeenCalled();
    expect(page.eventId).toBeNull();
    expect(page.notices.join(" ")).toMatch(/event id/i);
    expect(page.notices.join(" ")).toMatch(/cutoff/i);
  });

  it("falls back to the account node label, then the id, when the state has no name", async () => {
    const noState = fakeApi({ getAccountState: async () => null });
    expect((await loadAccountPage(noState, ACCOUNT, {})).accountName).toBe("Acme Corp");
    const bare = fakeApi({ getAccountState: async () => null, getAccountGraph: async () => ({ ...graph, nodes: [] }) });
    expect((await loadAccountPage(bare, ACCOUNT, {})).accountName).toBe(ACCOUNT);
  });
});
