// The evals overview's gate results: ?episode= when given, else the newest episode with an eval run. A failed or empty
// read is "not measured" (an empty list), never a pass.
import { describe, expect, it, vi } from "vitest";
import { loadLatestGateResults } from "@/lib/evals/load-gate-results";

const EP = "0e9e0000-0000-4000-8000-000000000a01";
const OTHER = "0de50000-0000-4000-8000-000000000a02";
const row = { gate: "D4" };

function api(over: Record<string, unknown> = {}) {
  return {
    listEvalRunsPage: vi.fn(async () => ({ items: [{ decision_episode_id: null }, { decision_episode_id: EP }], nextCursor: null })),
    listEpisodeGateResults: vi.fn(async () => [row]),
    ...over,
  } as unknown as Parameters<typeof loadLatestGateResults>[0];
}

describe("loadLatestGateResults", () => {
  it("reads the episode named by the query, validated as a uuid", async () => {
    const a = api();
    const out = await loadLatestGateResults(a, OTHER.toUpperCase());
    expect(out.episodeId).toBe(OTHER);
    expect(a.listEpisodeGateResults).toHaveBeenCalledWith(OTHER);
    expect(a.listEvalRunsPage).not.toHaveBeenCalled();
  });

  it("falls back to the newest eval run that has an episode, and ignores a malformed query", async () => {
    const out = await loadLatestGateResults(api(), "not-a-uuid");
    expect(out).toEqual({ results: [row], episodeId: EP });
  });

  it("is empty when no run has an episode", async () => {
    const a = api({ listEvalRunsPage: vi.fn(async () => ({ items: [{ decision_episode_id: null }], nextCursor: null })) });
    expect(await loadLatestGateResults(a, undefined)).toEqual({ results: [], episodeId: null });
  });

  it("is empty for an unknown episode (404 reads as null) and when the read fails", async () => {
    expect(await loadLatestGateResults(api({ listEpisodeGateResults: vi.fn(async () => null) }), EP)).toEqual({ results: [], episodeId: EP });
    expect(await loadLatestGateResults(api({ listEpisodeGateResults: vi.fn(async () => { throw new Error("down"); }) }), EP)).toEqual({ results: [], episodeId: null });
  });
});
