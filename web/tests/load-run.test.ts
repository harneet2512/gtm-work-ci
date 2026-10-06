// The run loader (WP24): only the run document is required — every section around it degrades to a
// notice so the page renders what exists (a run still generating has no strategies yet; a pending
// run has no decision).
import { describe, expect, it, vi } from "vitest";
import { CoreError } from "@/lib/api/core-client";
import { loadRunPage, type RunApi } from "@/lib/load-run";
import type { AgentRun, HumanStrategyDecision, JudgmentInference, Knowledge, RunStrategies, RunTrace } from "@/lib/api/types";
import { loadExample, loadFixture } from "./contract-validator";

const RUN = "0f0a0000-0000-4000-8000-000000000601";
const EPISODE = "0e9e0000-0000-4000-8000-000000000a01";
const run = loadExample<AgentRun>("agent_run");
const trace = loadFixture<RunTrace>("acme.run-trace.json");
const strategies = loadFixture<RunStrategies>("acme.run-strategies.json");
const decision = loadExample<HumanStrategyDecision>("human_strategy_decision");
const inference = loadExample<JudgmentInference>("judgment_inference");
const knowledge = loadExample<Knowledge>("knowledge");

function fakeApi(over: Partial<RunApi> = {}): RunApi {
  return {
    getRun: vi.fn(async () => run),
    getRunTrace: vi.fn(async () => trace),
    getRunStrategies: vi.fn(async () => strategies),
    getStrategyDecision: vi.fn(async () => decision),
    getJudgmentInference: vi.fn(async () => inference),
    getKnowledge: vi.fn(async () => knowledge),
    ...over,
  };
}

const fail = (status: number, code: string) => async (): Promise<never> => {
  throw new CoreError(status, code, `${code}: secret internals`);
};

describe("loadRunPage", () => {
  it("loads the whole chain: run, trace, strategies, decision, inference and the cited knowledge", async () => {
    const api = fakeApi();
    const page = await loadRunPage(api, RUN);
    expect(api.getRun).toHaveBeenCalledWith(RUN);
    expect(api.getRunTrace).toHaveBeenCalledWith(RUN);
    expect(api.getRunStrategies).toHaveBeenCalledWith(RUN);
    expect(api.getStrategyDecision).toHaveBeenCalledWith(RUN);
    expect(api.getJudgmentInference).toHaveBeenCalledWith(EPISODE);
    expect(page.notices).toEqual([]);
    expect(page.trace).toEqual(trace);
    expect(page.inference).toEqual(inference);
    // Every knowledge id the chain cites was fetched by id.
    const cited = Object.keys(page.knowledge);
    expect(cited).toContain(knowledge.id);
    for (const id of cited) expect(api.getKnowledge).toHaveBeenCalledWith(id);
  });

  it("a missing run propagates: the page renders notFound (no half-rendered chain)", async () => {
    const api = fakeApi({ getRun: fail(404, "not_found") });
    await expect(loadRunPage(api, RUN)).rejects.toMatchObject({ status: 404, code: "not_found" });
  });

  it("a run without a materialized trace, strategies or decision still renders with notices", async () => {
    const api = fakeApi({
      getRunTrace: vi.fn(async () => null),
      getRunStrategies: vi.fn(async () => null),
      getStrategyDecision: vi.fn(async () => null),
    });
    const page = await loadRunPage(api, RUN);
    expect(page.trace).toBeNull();
    expect(page.strategies).toBeNull();
    expect(page.decision).toBeNull();
    // The run's own generation record names the decision episode, so its inference still loads.
    expect(api.getJudgmentInference).toHaveBeenCalledWith(EPISODE);
    expect(page.inference).toEqual(inference);
    for (const id of Object.keys(page.knowledge)) expect(api.getKnowledge).toHaveBeenCalledWith(id);
    expect(page.notices).toEqual([]);
  });

  it("a section failure becomes a notice carrying the code, never the raw message", async () => {
    const api = fakeApi({
      getRunTrace: fail(500, "internal"),
      getRunStrategies: fail(503, "strategies_not_ready"),
      getStrategyDecision: fail(0, "unreachable"),
    });
    const page = await loadRunPage(api, RUN);
    expect(page.trace).toBeNull();
    expect(page.strategies).toBeNull();
    expect(page.decision).toBeNull();
    expect(page.notices).toHaveLength(3);
    expect(page.notices.join(" ")).toMatch(/internal/);
    expect(page.notices.join(" ")).toMatch(/strategies_not_ready/);
    expect(page.notices.join(" ")).not.toMatch(/secret internals/);
  });

  it("the episode id comes from the strategy set, falling back to the decision", async () => {
    const noSet = fakeApi({ getRunStrategies: vi.fn(async () => null) });
    await loadRunPage(noSet, RUN);
    expect(noSet.getJudgmentInference).toHaveBeenCalledWith(decision.decision_episode_id);
  });

  it("a failed knowledge read degrades that citation to null without hiding the rest", async () => {
    const api = fakeApi({ getKnowledge: fail(404, "not_found") });
    const page = await loadRunPage(api, RUN);
    for (const k of Object.values(page.knowledge)) expect(k).toBeNull();
    expect(page.notices.join(" ")).toMatch(/Knowledge/);
    expect(page.inference).toEqual(inference);
  });
});
