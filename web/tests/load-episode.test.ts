// The episode loader (HAR-145): the episode comes from GET /episodes/{id} (no run-list scan), absent pieces degrade to
// null with a notice instead of failing, Message 1 is tied to this episode's own account change, and a failed Cliff
// ref read is "unreadable", not "nothing posted".
import { describe, expect, it, vi } from "vitest";
import { CoreError } from "@/lib/api/core-client";
import { loadEpisodePage, type EpisodeApi } from "@/lib/load-episode";
import type { BusinessIntelligence, DependencyInvalidation, EpisodeSummary, EpisodeTrace, GraphDiff, HumanStrategyDecision, JudgmentInference, KnowledgeMutation, RunStrategies, SurfaceMessage } from "@/lib/api/types";
import { accountChangeId, sourceEventId } from "@/lib/view/episode";
import { loadExample, loadFixture } from "./contract-validator";

const summary = loadExample<EpisodeSummary>("episode_summary");
const trace = loadExample<EpisodeTrace>("episode_trace");
const mutation = loadExample<KnowledgeMutation>("knowledge_mutation");
const recomputation = loadExample<DependencyInvalidation>("dependency_invalidation");
const strategies = loadFixture<RunStrategies>("medtech.run-strategies.json");
const decision = loadFixture<HumanStrategyDecision>("medtech.strategy-decision.json");
const inference = loadFixture<JudgmentInference>("medtech.judgment-inference.json");
const graphDiff = loadFixture<GraphDiff>("medtech.graph-diff.json");
const ref = loadExample<SurfaceMessage>("surface_message");
const bi = { ...loadExample<BusinessIntelligence>("business_intelligence_update"), account_change_id: accountChangeId(trace)! } as BusinessIntelligence;
const EPISODE = summary.id;

function fakeApi(over: Partial<EpisodeApi> = {}): EpisodeApi {
  return {
    getEpisode: vi.fn(async () => summary),
    getEpisodeTrace: vi.fn(async () => trace),
    listEpisodeKnowledgeMutations: vi.fn(async () => [mutation]),
    getRunRecomputation: vi.fn(async () => recomputation),
    getRunStrategies: vi.fn(async () => strategies),
    getStrategyDecision: vi.fn(async () => decision),
    getJudgmentInference: vi.fn(async () => inference),
    getSurfaceMessage: vi.fn(async (_id: string, _s: string, kind: string) => ({ ...ref, kind }) as SurfaceMessage),
    getEventGraphDiff: vi.fn(async () => graphDiff),
    getLatestBusinessIntelligence: vi.fn(async () => bi),
    ...over,
  } as EpisodeApi;
}

const boom = async (): Promise<never> => {
  throw new CoreError(500, "internal", "boom");
};

describe("loadEpisodePage", () => {
  it("is null for an unknown episode (a real 404) and reads nothing else", async () => {
    const api = fakeApi({ getEpisode: vi.fn(async () => null) });
    expect(await loadEpisodePage(api, EPISODE)).toBeNull();
    expect(api.getEpisodeTrace).not.toHaveBeenCalled();
  });

  it("throws, never a 404, when the core cannot be reached for the episode", async () => {
    await expect(loadEpisodePage(fakeApi({ getEpisode: boom }), EPISODE)).rejects.toMatchObject({ status: 500 });
  });

  it("assembles the view, the mutations, the recomputation, the surface refs and the trigger's graph diff", async () => {
    const api = fakeApi();
    const page = (await loadEpisodePage(api, EPISODE))!;
    expect(page.episodeId).toBe(EPISODE);
    expect(page.view.nodes.length).toBe(trace.spans.length + 1);
    expect(page.mutations).toEqual([mutation]);
    expect(page.recomputation).toEqual(recomputation);
    expect(page.biStatus).toBe("matched");
    expect(page.bi).toEqual(bi);
    expect(Object.keys(page.surfaces).sort()).toEqual(["bi", "chooser", "judgment"]);
    expect(page.surfacesReadable).toBe(true);
    expect(page.notices).toEqual([]);
    expect(api.getSurfaceMessage).toHaveBeenCalledWith(bi.id, "slack", "bi");
    expect(api.getSurfaceMessage).toHaveBeenCalledWith(EPISODE, "slack", "chooser");
    expect(api.getEventGraphDiff).toHaveBeenCalledWith(sourceEventId(trace));
    expect(page.graphDiff).toEqual(graphDiff);
  });

  it("reads everything through the episode's own run and account, with no run-list scan", async () => {
    const api = fakeApi();
    await loadEpisodePage(api, EPISODE);
    expect(api.getRunStrategies).toHaveBeenCalledWith(summary.agent_run_id);
    expect(api.getRunRecomputation).toHaveBeenCalledWith(summary.agent_run_id);
    expect(api.getLatestBusinessIntelligence).toHaveBeenCalledWith(summary.account_id);
    expect(api.getJudgmentInference).toHaveBeenCalledWith(EPISODE);
    expect("listRuns" in api).toBe(false);
  });

  it("never shows another change's Message 1: a BI update for a different account_change_id is unresolvable", async () => {
    const other = { ...bi, account_change_id: "0acc0000-0000-4000-8000-0000000009ff" } as BusinessIntelligence;
    const api = fakeApi({ getLatestBusinessIntelligence: vi.fn(async () => other) });
    const page = (await loadEpisodePage(api, EPISODE))!;
    expect(page.bi).toBeNull();
    expect(page.biStatus).toBe("unresolvable");
    expect(api.getSurfaceMessage).not.toHaveBeenCalledWith(bi.id, "slack", "bi");
    expect("bi" in page.surfaces).toBe(false);
  });

  it("cannot resolve Message 1 when the trace is missing or its evidence names no account change", async () => {
    for (const getEpisodeTrace of [vi.fn(async () => null), vi.fn(async () => ({ ...trace, spans: trace.spans.map((s) => (s.kind === "evidence" ? { ...s, refs: [] } : s)) }))]) {
      const page = (await loadEpisodePage(fakeApi({ getEpisodeTrace }), EPISODE))!;
      expect(page.bi).toBeNull();
      expect(page.biStatus).toBe("unresolvable");
    }
  });

  it("never probes Message 1 when the account has no BI update", async () => {
    const api = fakeApi({ getLatestBusinessIntelligence: vi.fn(async () => null) });
    const page = (await loadEpisodePage(api, EPISODE))!;
    expect(page.biStatus).toBe("none");
    expect("bi" in page.surfaces).toBe(false);
    expect(page.surfacesReadable).toBe(true);
  });

  it("marks surfaces unreadable (not empty) when a ref read throws", async () => {
    const page = (await loadEpisodePage(fakeApi({ getSurfaceMessage: boom }), EPISODE))!;
    expect(page.surfacesReadable).toBe(false);
    expect(page.surfaces).toEqual({});
  });

  it("degrades every unreadable piece to null with a 'backend unavailable' notice, never an empty list", async () => {
    const page = (await loadEpisodePage(
      fakeApi({
        getEpisodeTrace: boom,
        listEpisodeKnowledgeMutations: boom,
        getRunRecomputation: boom,
        getRunStrategies: boom,
        getStrategyDecision: boom,
        getJudgmentInference: boom,
        getLatestBusinessIntelligence: boom,
        getEventGraphDiff: boom,
      }),
      EPISODE,
    ))!;
    expect([page.trace, page.mutations, page.recomputation, page.strategies, page.decision, page.inference, page.bi, page.graphDiff]).toEqual([null, null, null, null, null, null, null, null]);
    expect(page.notices).toHaveLength(7);
    expect(page.notices.every((n) => n.endsWith("backend unavailable."))).toBe(true);
    expect(page.view.nodes).toEqual([]);
  });

  it("keeps an episode that changed no knowledge as an empty list, not null", async () => {
    const page = (await loadEpisodePage(fakeApi({ listEpisodeKnowledgeMutations: vi.fn(async () => []) }), EPISODE))!;
    expect(page.mutations).toEqual([]);
  });

  it("reads a diff failure as no diff and skips it when the trace names no trigger event", async () => {
    const noEvent = fakeApi({ getEpisodeTrace: vi.fn(async () => ({ ...trace, spans: trace.spans.filter((s) => s.kind !== "source_event") })) });
    expect((await loadEpisodePage(noEvent, EPISODE))!.graphDiff).toBeNull();
    expect(noEvent.getEventGraphDiff).not.toHaveBeenCalled();
    expect((await loadEpisodePage(fakeApi({ getEventGraphDiff: boom }), EPISODE))!.graphDiff).toBeNull();
  });
});
