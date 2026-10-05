import { describe, expect, it, vi } from "vitest";
import { CoreError } from "@/lib/api/core-client";
import { loadReplayPage, type ReplayApi } from "@/lib/load-replay";
import type { EpisodeReplayView } from "@/lib/api/types";
import { loadFixture } from "./contract-validator";

const MANIFEST = "0d3a0000-0000-4000-8000-000000000501";
const view = loadFixture<EpisodeReplayView>("replay.episodes.json");

function fakeApi(over: Partial<ReplayApi> = {}): ReplayApi {
  return { getReplayEpisodes: vi.fn(async () => view), ...over };
}

const fail = (status: number, code: string) => async (): Promise<never> => {
  throw new CoreError(status, code, `${code}: internal detail must not leak`);
};

describe("loadReplayPage", () => {
  it("reads the view at the released cursor when no at is given", async () => {
    const api = fakeApi();
    const page = await loadReplayPage(api, MANIFEST, undefined);
    expect(page.at).toBeNull();
    expect(page.view.episode).toBe(2);
    expect(page.released).toBe(2);
    expect(page.notices).toEqual([]);
    expect(api.getReplayEpisodes).toHaveBeenCalledWith(MANIFEST);
  });

  it("passes a numeric at through to the core and reads the live cursor alongside", async () => {
    const api = fakeApi();
    const page = await loadReplayPage(api, MANIFEST, "1");
    expect(page.at).toBe(1);
    expect(page.released).toBe(2);
    expect(api.getReplayEpisodes).toHaveBeenCalledWith(MANIFEST, { at: 1 });
    expect(api.getReplayEpisodes).toHaveBeenCalledWith(MANIFEST);
    expect(api.getReplayEpisodes).toHaveBeenCalledTimes(2);
  });

  it("reads the boundary view at position 0", async () => {
    const api = fakeApi({ getReplayEpisodes: vi.fn(async () => ({ ...view, episode: 0, prior_episodes: [] })) });
    const page = await loadReplayPage(api, MANIFEST, "0");
    expect(page.at).toBe(0);
    expect(api.getReplayEpisodes).toHaveBeenCalledWith(MANIFEST, { at: 0 });
  });

  it("ignores a malformed at with a notice instead of asking the core for it", async () => {
    const api = fakeApi();
    const page = await loadReplayPage(api, MANIFEST, "yesterday");
    expect(page.at).toBeNull();
    expect(page.notices.join(" ")).toMatch(/not a number/i);
    expect(api.getReplayEpisodes).toHaveBeenCalledWith(MANIFEST);
    expect(api.getReplayEpisodes).toHaveBeenCalledTimes(1);
  });

  it("falls back to the cursor with a notice when the core refuses the position (422 invalid_episode)", async () => {
    const api = fakeApi({
      getReplayEpisodes: vi.fn(async (_id: string, opts?: { at?: number }) => {
        if (opts?.at !== undefined) throw new CoreError(422, "invalid_episode", "invalid_episode: not released");
        return view;
      }),
    });
    const page = await loadReplayPage(api, MANIFEST, "9");
    expect(page.at).toBeNull();
    expect(page.notices.join(" ")).toContain("invalid_episode");
    expect(page.notices.join(" ")).toContain("Episode 9");
    expect(api.getReplayEpisodes).toHaveBeenCalledTimes(2);
  });

  it("propagates a missing manifest (404) and any other failure", async () => {
    await expect(loadReplayPage(fakeApi({ getReplayEpisodes: fail(404, "manifest_not_found") }), MANIFEST, undefined)).rejects.toMatchObject({
      status: 404,
      code: "manifest_not_found",
    });
    await expect(loadReplayPage(fakeApi({ getReplayEpisodes: fail(503, "graph_unavailable") }), MANIFEST, "1")).rejects.toMatchObject({ status: 503 });
  });

  it("propagates a 422 that is not about the episode position", async () => {
    await expect(loadReplayPage(fakeApi({ getReplayEpisodes: fail(422, "episode_not_materialized") }), MANIFEST, "2")).rejects.toMatchObject({
      code: "episode_not_materialized",
    });
  });
});
