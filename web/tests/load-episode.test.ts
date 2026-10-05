// The episode loader (HAR-145): the run is found by generation.decision_episode_id, absent pieces degrade
// to null rather than failing, and a failed Cliff ref read is "unreadable" - not "nothing posted".
import { describe, expect, it, vi } from "vitest";
import { CoreError } from "@/lib/api/core-client";
import { loadEpisodePage, type EpisodeApi } from "@/lib/load-episode";
import type { AgentRun, BusinessIntelligence, EpisodeReplayView, GraphDiff, HumanStrategyDecision, JudgmentInference, RunStrategies, RunTrace, SurfaceMessage } from "@/lib/api/types";
import { loadExample, loadFixture } from "./contract-validator";

const run = loadFixture<AgentRun>("medtech.agent-run.json");
const trace = loadFixture<RunTrace>("medtech.run-trace.json");
const strategies = loadFixture<RunStrategies>("medtech.run-strategies.json");
const decision = loadFixture<HumanStrategyDecision>("medtech.strategy-decision.json");
const inference = loadFixture<JudgmentInference>("medtech.judgment-inference.json");
const graphDiff = loadFixture<GraphDiff>("medtech.graph-diff.json");
const bi = loadExample<BusinessIntelligence>("business_intelligence_update");
const ref = loadExample<SurfaceMessage>("surface_message");
const EPISODE = run.generation!.decision_episode_id!;
const MANIFEST = "0d3a0000-0000-4000-8000-000000000501";

/** A replay view whose released episode for EPISODE carries the given account_change_id. */
const replayView = (accountChangeId: string | null) =>
  ({ account_id: run.account_id, prior_episodes: [{ position: 1, decision_episode_id: EPISODE, account_change_id: accountChangeId }], next_event: null }) as unknown as EpisodeReplayView;

function fakeApi(over: Partial<EpisodeApi> = {}): EpisodeApi {
  return {
    listRuns: vi.fn(async () => [run]),
    getRunTrace: vi.fn(async () => trace),
    getRunStrategies: vi.fn(async () => strategies),
    getStrategyDecision: vi.fn(async () => decision),
    getJudgmentInference: vi.fn(async () => inference),
    getSurfaceMessage: vi.fn(async (_id: string, _s: string, kind: string) => ({ ...ref, kind }) as SurfaceMessage),
    getEventGraphDiff: vi.fn(async () => graphDiff),
    getLatestBusinessIntelligence: vi.fn(async () => bi),
    getReplayEpisodes: vi.fn(async () => replayView(bi.account_change_id)),
    ...over,
  } as EpisodeApi;
}

const boom = async (): Promise<never> => {
  throw new CoreError(500, "internal", "boom");
};

describe("loadEpisodePage", () => {
  it("is null when no run is bound to the episode", async () => {
    expect(await loadEpisodePage(fakeApi({ listRuns: vi.fn(async () => []) }), EPISODE)).toBeNull();
    expect(await loadEpisodePage(fakeApi(), "0e9e0000-0000-4000-8000-0000000000ff")).toBeNull();
  });

  it("is null when the core has no trace for the run (a real 404: nothing to inspect)", async () => {
    expect(await loadEpisodePage(fakeApi({ getRunTrace: vi.fn(async () => null) }), EPISODE)).toBeNull();
  });

  it("throws - never a 404 - when the core cannot be reached for the run list or the trace", async () => {
    await expect(loadEpisodePage(fakeApi({ getRunTrace: boom }), EPISODE)).rejects.toMatchObject({ status: 500 });
    await expect(loadEpisodePage(fakeApi({ listRuns: boom }), EPISODE)).rejects.toMatchObject({ status: 500 });
  });

  it("assembles the view, surface refs for all three kinds and the trigger's graph diff", async () => {
    const api = fakeApi();
    const page = (await loadEpisodePage(api, EPISODE, MANIFEST))!;
    expect(page.episodeId).toBe(EPISODE);
    expect(page.biStatus).toBe("matched");
    expect(Object.keys(page.surfaces).sort()).toEqual(["bi", "chooser", "judgment"]);
    expect(page.surfacesReadable).toBe(true);
    expect(api.getSurfaceMessage).toHaveBeenCalledWith(bi.id, "slack", "bi");
    expect(api.getSurfaceMessage).toHaveBeenCalledWith(EPISODE, "slack", "chooser");
    expect(page.graphDiff).toEqual(graphDiff);
    expect(page.bi).toEqual(bi);
    expect(page.decision).toEqual(decision);
    expect(page.inference).toEqual(inference);
  });

  it("looks the run up among the manifest account's runs, so another account's runs cannot crowd it out", async () => {
    const api = fakeApi();
    await loadEpisodePage(api, EPISODE, MANIFEST);
    expect(api.listRuns).toHaveBeenCalledWith({ accountId: run.account_id, limit: 200 });
    expect(api.getReplayEpisodes).toHaveBeenCalledTimes(1);
  });

  it("falls back to the global run scan when no manifest names the account", async () => {
    const api = fakeApi();
    await loadEpisodePage(api, EPISODE);
    expect(api.listRuns).toHaveBeenCalledWith({ limit: 200 });
    expect(api.getReplayEpisodes).not.toHaveBeenCalled();
  });

  it("never shows another change's Message 1: a BI update for a different account_change_id is unresolvable", async () => {
    const api = fakeApi({ getReplayEpisodes: vi.fn(async () => replayView("0acc0000-0000-4000-8000-0000000009ff")) });
    const page = (await loadEpisodePage(api, EPISODE, MANIFEST))!;
    expect(page.bi).toBeNull();
    expect(page.biStatus).toBe("unresolvable");
    expect(api.getSurfaceMessage).not.toHaveBeenCalledWith(bi.id, "slack", "bi");
    expect("bi" in page.surfaces).toBe(false);
  });

  it("cannot resolve Message 1 without a manifest, or when the replay read fails or lacks the episode", async () => {
    for (const [manifest, over] of [
      [null, {}],
      [MANIFEST, { getReplayEpisodes: boom }],
      [MANIFEST, { getReplayEpisodes: vi.fn(async () => replayView(null)) }],
      [MANIFEST, { getReplayEpisodes: vi.fn(async () => ({ prior_episodes: [], next_event: null }) as unknown as EpisodeReplayView) }],
    ] as const) {
      const api = fakeApi(over as Partial<EpisodeApi>);
      const page = (await loadEpisodePage(api, EPISODE, manifest))!;
      expect(page.bi).toBeNull();
      expect(page.biStatus).toBe("unresolvable");
      expect(api.getSurfaceMessage).not.toHaveBeenCalledWith(bi.id, "slack", "bi");
    }
  });

  it("never probes Message 1 when the account has no BI update", async () => {
    const api = fakeApi({ getLatestBusinessIntelligence: vi.fn(async () => null) });
    const page = (await loadEpisodePage(api, EPISODE, MANIFEST))!;
    expect(page.bi).toBeNull();
    expect(page.biStatus).toBe("none");
    expect("bi" in page.surfaces).toBe(false);
    expect(page.surfacesReadable).toBe(true);
  });

  it("marks surfaces unreadable (not empty) when a ref read throws", async () => {
    const page = (await loadEpisodePage(fakeApi({ getSurfaceMessage: boom }), EPISODE, MANIFEST))!;
    expect(page.surfacesReadable).toBe(false);
    expect(page.surfaces).toEqual({});
  });

  it("degrades missing strategies, decision, inference, BI and diff to null", async () => {
    const page = (await loadEpisodePage(
      fakeApi({
        getRunStrategies: boom,
        getStrategyDecision: boom,
        getJudgmentInference: boom,
        getLatestBusinessIntelligence: boom,
        getEventGraphDiff: boom,
      }),
      EPISODE,
    ))!;
    expect([page.strategies, page.decision, page.inference, page.bi, page.graphDiff]).toEqual([null, null, null, null, null]);
  });

  it("skips the graph diff when the trace names no trigger event", async () => {
    const api = fakeApi({ getRunTrace: vi.fn(async () => ({ ...trace, trigger_activities: [] })) });
    const page = (await loadEpisodePage(api, EPISODE))!;
    expect(page.graphDiff).toBeNull();
    expect(api.getEventGraphDiff).not.toHaveBeenCalled();
  });
});
