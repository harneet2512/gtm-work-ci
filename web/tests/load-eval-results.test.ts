// The explorer loader (HAR-145): no eval-result list endpoint exists, so it flattens each run's bundles;
// a run whose artifacts fail contributes no rows instead of failing the page.
import { describe, expect, it, vi } from "vitest";
import { CoreError } from "@/lib/api/core-client";
import { loadExplorer, type ExplorerApi } from "@/lib/load-eval-results";
import type { AgentRun, HumanStrategyDecision, RunStrategies } from "@/lib/api/types";
import { loadFixture } from "./contract-validator";

const run = loadFixture<AgentRun>("medtech.agent-run.json");
const strategies = loadFixture<RunStrategies>("medtech.run-strategies.json");
const decision = loadFixture<HumanStrategyDecision>("medtech.strategy-decision.json");
const other: AgentRun = { ...run, id: "0f0a0000-0000-4000-8000-000000000602" };

const boom = async (): Promise<never> => {
  throw new CoreError(500, "internal", "boom");
};

function fakeApi(over: Partial<ExplorerApi> = {}): ExplorerApi {
  return {
    listRuns: vi.fn(async () => [run]),
    getRunStrategies: vi.fn(async () => strategies),
    getStrategyDecision: vi.fn(async () => decision),
    getRun: vi.fn(async () => run),
    ...over,
  } as ExplorerApi;
}

describe("loadExplorer", () => {
  it("indexes every produced result by id and lists the runs that produced rows", async () => {
    const data = await loadExplorer(fakeApi());
    expect(data.rows.length).toBeGreaterThan(0);
    // A bundle item with no result still gets a row (fallback id) but has nothing to inspect in byId.
    expect(data.byId.size).toBe(data.rows.length - 1);
    const rowIds = new Set(data.rows.map((r) => r.resultId));
    expect([...data.byId.keys()].every((id) => rowIds.has(id))).toBe(true);
    expect(data.runs).toEqual([{ id: run.id, episodeId: run.generation!.decision_episode_id, phase: "published", rows: data.rows.length }]);
  });

  it("passes the page limit to listRuns", async () => {
    const api = fakeApi();
    await loadExplorer(api, 7);
    expect(api.listRuns).toHaveBeenCalledWith({ limit: 7 });
  });

  it("a run whose strategies fail contributes no rows and is not listed", async () => {
    const api = fakeApi({
      listRuns: vi.fn(async () => [run, other]),
      getRunStrategies: vi.fn(async (id: string) => (id === other.id ? boom() : strategies)),
      getStrategyDecision: boom,
    });
    const data = await loadExplorer(api);
    expect(data.runs.map((r) => r.id)).toEqual([run.id]);
    expect(data.rows.every((r) => r.runId === run.id)).toBe(true);
  });

  it("counts runs whose artifacts could not be read instead of silently dropping them", async () => {
    const api = fakeApi({
      listRuns: vi.fn(async () => [run, other]),
      getRunStrategies: vi.fn(async (id: string) => (id === other.id ? boom() : strategies)),
    });
    expect((await loadExplorer(api)).unavailableRuns).toBe(1);
    expect((await loadExplorer(fakeApi())).unavailableRuns).toBe(0);
  });

  it("does not count a run that simply has no strategies yet (a 404 read as null) as unavailable", async () => {
    const api = fakeApi({ getRunStrategies: vi.fn(async () => null) });
    expect((await loadExplorer(api)).unavailableRuns).toBe(0);
  });

  it("reports a null episode and phase for a run with no generation record", async () => {
    const bare = { ...run, generation: undefined } as unknown as AgentRun;
    const data = await loadExplorer(fakeApi({ listRuns: vi.fn(async () => [bare]) }));
    expect(data.runs[0]).toMatchObject({ episodeId: null, phase: null });
  });

  it("is empty when there are no runs", async () => {
    const data = await loadExplorer(fakeApi({ listRuns: vi.fn(async () => []) }));
    expect(data).toEqual({ rows: [], byId: new Map(), runs: [], unavailableRuns: 0 });
  });
});
