// The /control loader (HAR-145): the replay view drives the page; reads a partial world cannot answer
// degrade to an honest band state, and "the surface read failed" (null) is never "nothing posted" ([]).
import { describe, expect, it, vi } from "vitest";
import { CoreError } from "@/lib/api/core-client";
import { loadControlPage, type ControlApi } from "@/lib/load-control";
import type { AgentRun, BusinessIntelligence, EpisodeReplayView, HumanStrategyDecision, RunStrategies, SurfaceMessage } from "@/lib/api/types";
import { loadExample, loadFixture } from "./contract-validator";

const view = loadFixture<EpisodeReplayView>("replay.episodes.json");
const strategies = loadFixture<RunStrategies>("medtech.run-strategies.json");
const decision = loadFixture<HumanStrategyDecision>("medtech.strategy-decision.json");
const medtechRun = loadFixture<AgentRun>("medtech.agent-run.json");
const bi = loadExample<BusinessIntelligence>("business_intelligence_update");
const ref = loadExample<SurfaceMessage>("surface_message");
const MANIFEST = view.manifest_id;

const withEpisode = (changeId: string | null): EpisodeReplayView => ({
  ...view,
  prior_episodes: view.prior_episodes.map((e, i, all) => (i === all.length - 1 ? { ...e, decision_episode_id: "0de50000-0000-4000-8000-000000000202", account_change_id: changeId } : e)),
});
const run: AgentRun = { ...medtechRun, account_id: view.account_id, created_at: "2026-10-04T10:00:00Z" } as AgentRun;
const older: AgentRun = { ...run, id: "0f0a0000-0000-4000-8000-0000000000aa", created_at: "2026-10-01T10:00:00Z" };

const boom = (status = 500, code = "internal") => async (): Promise<never> => {
  throw new CoreError(status, code, "x");
};

function fakeApi(over: Partial<ControlApi> = {}): ControlApi {
  return {
    getReplayEpisodes: vi.fn(async () => withEpisode("0a1c0000-0000-4000-8000-000000000302")),
    listRuns: vi.fn(async () => [older, run]),
    getRunStrategies: vi.fn(async () => strategies),
    getStrategyDecision: vi.fn(async () => decision),
    getSurfaceMessage: vi.fn(async (_id: string, _s: string, kind: string) => ({ ...ref, kind }) as SurfaceMessage),
    getLatestBusinessIntelligence: vi.fn(async () => ({ ...bi, account_change_id: "0a1c0000-0000-4000-8000-000000000302" })),
    ...over,
  } as ControlApi;
}

describe("loadControlPage", () => {
  it("reads the replay, the newest run for the account and the posted Cliff kinds", async () => {
    const api = fakeApi();
    const page = await loadControlPage(api, MANIFEST, undefined);
    expect(api.getRunStrategies).toHaveBeenCalledWith(run.id);
    expect(page.cliffPosted).toEqual(["bi", "chooser", "judgment"]);
    expect(page.control.bands).toHaveLength(4);
  });

  it("ignores runs that belong to another account", async () => {
    const stranger = { ...run, account_id: "0a0c0000-0000-4000-8000-0000000000ff" } as AgentRun;
    const api = fakeApi({ listRuns: vi.fn(async () => [stranger]) });
    const page = await loadControlPage(api, MANIFEST, undefined);
    expect(api.getRunStrategies).not.toHaveBeenCalled();
    expect(page.control.bands.find((b) => b.id === "decision_learning")!.status).toBe("no agent run yet");
  });

  it("reads a failed run listing as 'backend unavailable', never as 'no agent run yet'", async () => {
    const page = await loadControlPage(fakeApi({ listRuns: boom() }), MANIFEST, undefined);
    expect(page.control.bands.find((b) => b.id === "decision_learning")!.status).toBe("backend unavailable");
    expect(page.backendUnavailable).toBe(true);
    const ok = await loadControlPage(fakeApi(), MANIFEST, undefined);
    expect(ok.backendUnavailable).toBe(false);
  });

  it("tolerates missing strategies and decision for the run", async () => {
    const page = await loadControlPage(fakeApi({ getRunStrategies: boom(), getStrategyDecision: boom() }), MANIFEST, undefined);
    expect(page.control.bands).toHaveLength(4);
  });

  it("skips Message 1 when the latest BI belongs to a different change", async () => {
    const api = fakeApi({ getLatestBusinessIntelligence: vi.fn(async () => ({ ...bi, account_change_id: "0a1c0000-0000-4000-8000-0000000000ee" })) });
    const page = await loadControlPage(api, MANIFEST, undefined);
    expect(page.cliffPosted).toEqual(["chooser", "judgment"]);
  });

  it("skips Message 1 entirely when the episode has no account change", async () => {
    const api = fakeApi({ getReplayEpisodes: vi.fn(async () => withEpisode(null)) });
    const page = await loadControlPage(api, MANIFEST, undefined);
    expect(api.getLatestBusinessIntelligence).not.toHaveBeenCalled();
    expect(page.cliffPosted).toEqual(["chooser", "judgment"]);
  });

  it("counts a reserved-but-unposted ref (ts null) as not posted", async () => {
    const api = fakeApi({ getSurfaceMessage: vi.fn(async () => ({ ...ref, ts: null }) as SurfaceMessage) });
    expect((await loadControlPage(api, MANIFEST, undefined)).cliffPosted).toEqual([]);
  });

  it("a missing surface endpoint (404/501) means nothing posted; any other failure means unreadable", async () => {
    expect((await loadControlPage(fakeApi({ getSurfaceMessage: boom(404, "not_found") }), MANIFEST, undefined)).cliffPosted).toEqual([]);
    expect((await loadControlPage(fakeApi({ getSurfaceMessage: boom(501, "nope") }), MANIFEST, undefined)).cliffPosted).toEqual([]);
    expect((await loadControlPage(fakeApi({ getSurfaceMessage: boom(500) }), MANIFEST, undefined)).cliffPosted).toBeNull();
    expect((await loadControlPage(fakeApi({ getSurfaceMessage: async () => { throw new Error("network"); } }), MANIFEST, undefined)).cliffPosted).toBeNull();
  });

  it("has no Cliff signal (null) when no episode has opened a decision", async () => {
    const api = fakeApi({ getReplayEpisodes: vi.fn(async () => ({ ...view, prior_episodes: view.prior_episodes.map((e) => ({ ...e, decision_episode_id: null })) })) });
    const page = await loadControlPage(api, MANIFEST, undefined);
    expect(page.cliffPosted).toBeNull();
    expect(api.getSurfaceMessage).not.toHaveBeenCalled();
  });

  it("propagates a missing manifest", async () => {
    await expect(loadControlPage(fakeApi({ getReplayEpisodes: boom(404, "manifest_not_found") }), MANIFEST, undefined)).rejects.toMatchObject({ status: 404 });
  });
});
