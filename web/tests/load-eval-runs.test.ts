// The evals explorer loaders (HAR-145): cursor pages, the selected run's families, and the operator comparison. Every
// failure keeps its meaning: backend unavailable, run unknown, not comparable; none is an eval failure.
import { describe, expect, it, vi } from "vitest";
import { CoreError } from "@/lib/api/core-client";
import { EVAL_RUNS_PAGE_SIZE, loadComparison, loadEvalRuns, loadFamilies, type EvalRunsApi } from "@/lib/load-eval-runs";
import type { EvalFamilySummary, EvalRun, EvalRunComparison } from "@/lib/api/types";
import { loadExample } from "./contract-validator";

const run = loadExample<EvalRun>("eval_run");
const families = loadExample<EvalFamilySummary>("eval_family_summary");
const comparison = loadExample<EvalRunComparison>("eval_run_comparison");
const A = "0f0a0000-0000-4000-8000-000000000600";
const B = "0f0a0000-0000-4000-8000-000000000601";

const boom = (status = 500) => async (): Promise<never> => {
  throw new CoreError(status, "x", "x");
};

describe("loadEvalRuns", () => {
  it("reads one page of runs with its next cursor", async () => {
    const api = { listEvalRunsPage: vi.fn(async () => ({ items: [run], nextCursor: "n2" })) } as unknown as EvalRunsApi;
    await expect(loadEvalRuns(api, { cursor: "c1" })).resolves.toEqual({ runs: [run], nextCursor: "n2", unavailable: false, badCursor: false });
    expect(api.listEvalRunsPage).toHaveBeenCalledWith({ limit: EVAL_RUNS_PAGE_SIZE, cursor: "c1" });
  });

  it("falls back to the first page, and says so, when the cursor is refused", async () => {
    const list = vi.fn(async (o?: { cursor?: string }) => {
      if (o?.cursor) throw new CoreError(400, "bad_cursor", "x");
      return { items: [run], nextCursor: null };
    });
    const data = await loadEvalRuns({ listEvalRunsPage: list } as unknown as EvalRunsApi, { cursor: "junk" });
    expect(data).toMatchObject({ runs: [run], badCursor: true, unavailable: false });
  });

  it("reads a failed list as backend unavailable, not as no runs", async () => {
    const data = await loadEvalRuns({ listEvalRunsPage: boom() } as unknown as EvalRunsApi);
    expect(data).toEqual({ runs: [], nextCursor: null, unavailable: true, badCursor: false });
    const transport = await loadEvalRuns({ listEvalRunsPage: boom(400) } as unknown as EvalRunsApi);
    expect(transport.unavailable).toBe(true); // a 400 with no cursor sent is not a cursor problem
  });
});

describe("loadFamilies", () => {
  it("returns the family summary", async () => {
    const api = { getEvalRunFamilies: vi.fn(async () => families) };
    await expect(loadFamilies(api, B)).resolves.toEqual({ summary: families, unavailable: false });
    expect(api.getEvalRunFamilies).toHaveBeenCalledWith(B);
  });

  it("treats an unknown run (null) as nothing to show and a failure as unavailable", async () => {
    await expect(loadFamilies({ getEvalRunFamilies: async () => null }, B)).resolves.toEqual({ summary: null, unavailable: false });
    await expect(loadFamilies({ getEvalRunFamilies: boom() }, B)).resolves.toEqual({ summary: null, unavailable: true });
  });

  it("never sends a malformed id to the core", async () => {
    const api = { getEvalRunFamilies: vi.fn() };
    await expect(loadFamilies(api, "../x")).resolves.toEqual({ summary: null, unavailable: false });
    expect(api.getEvalRunFamilies).not.toHaveBeenCalled();
  });
});

describe("loadComparison", () => {
  it("is idle with no pair, and invalid with half a pair or a malformed id", async () => {
    const api = { compareEvalRuns: vi.fn() };
    await expect(loadComparison(api, undefined, undefined)).resolves.toEqual({ state: "idle" });
    await expect(loadComparison(api, A, undefined)).resolves.toEqual({ state: "invalid" });
    await expect(loadComparison(api, A, "nope")).resolves.toEqual({ state: "invalid" });
    expect(api.compareEvalRuns).not.toHaveBeenCalled();
  });

  it("returns the comparison", async () => {
    const api = { compareEvalRuns: vi.fn(async () => ({ outcome: "compared" as const, comparison })) };
    await expect(loadComparison(api, A, B)).resolves.toEqual({ state: "compared", comparison });
    expect(api.compareEvalRuns).toHaveBeenCalledWith(A, B);
  });

  it("keeps the refusals apart: not comparable, unknown run, and a failed read", async () => {
    await expect(loadComparison({ compareEvalRuns: async () => ({ outcome: "not_comparable" as const }) }, A, B)).resolves.toEqual({ state: "not_comparable" });
    await expect(loadComparison({ compareEvalRuns: async () => ({ outcome: "not_found" as const }) }, A, B)).resolves.toEqual({ state: "not_found" });
    await expect(loadComparison({ compareEvalRuns: boom() }, A, B)).resolves.toEqual({ state: "unavailable" });
  });
});
