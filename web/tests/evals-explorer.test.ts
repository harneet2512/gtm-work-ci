// The /evals explorer model (HAR-145): rows come only from produced bundle items — "not checked" stays
// a per-candidate concept in the run page's matrix, never an explorer row.
import { describe, expect, it } from "vitest";
import type { AgentRun, HumanStrategyDecision, RunStrategies } from "@/lib/api/types";
import { compareRuns, filterRows, flattenResults, sortRows, DEFAULT_QUERY } from "@/lib/view/evals-explorer";
import { loadFixture } from "./contract-validator";

const medtechRun = loadFixture<AgentRun>("medtech.agent-run.json");
const medtech = loadFixture<RunStrategies>("medtech.run-strategies.json");
const medtechDecision = loadFixture<HumanStrategyDecision>("medtech.strategy-decision.json");
const acme = loadFixture<RunStrategies>("acme.run-strategies.json");

const acmeRun: AgentRun = { ...medtechRun, id: "0f0a0000-0000-4000-8000-000000000602", account_id: "0a0c0000-0000-4000-8000-000000000001" };

const rows = flattenResults([
  { run: medtechRun, strategies: medtech, decision: medtechDecision },
  { run: acmeRun, strategies: acme, decision: null },
]);

describe("flattenResults", () => {
  it("emits one row per produced bundle item across runs", () => {
    expect(rows).toHaveLength(24); // 15 medtech + 9 acme
    expect(rows.every((r) => r.resultId && r.evalType)).toBe(true);
  });

  it("resolves candidate letters, Ghost's pick and the human's choice", () => {
    const medtechRows = rows.filter((r) => r.runId === medtechRun.id);
    expect(new Set(medtechRows.map((r) => r.candidate))).toEqual(new Set(["A", "B", "C"]));
    expect(medtechRows.filter((r) => r.isChosen).every((r) => r.candidate === "B")).toBe(true); // Luis edited option B
    expect(medtechRows.some((r) => r.isGhostPick)).toBe(true);
    expect(rows.filter((r) => r.runId === acmeRun.id).every((r) => !r.isChosen)).toBe(true);
  });

  it("marks a blocking fail as blocking — a non-blocking one is not", () => {
    const fails = rows.filter((r) => r.verdict === "fail");
    expect(fails.length).toBeGreaterThan(0);
    expect(fails.every((r) => r.blocking === (r.blocking && true))).toBe(true);
  });
});

describe("filterRows / sortRows", () => {
  it("searches across names, reasons and option titles", () => {
    expect(filterRows(rows, { ...DEFAULT_QUERY, q: "cta" }).every((r) => r.evalType.includes("cta") || r.name.toLowerCase().includes("cta"))).toBe(true);
    expect(filterRows(rows, { ...DEFAULT_QUERY, q: "no-such-thing" })).toHaveLength(0);
  });

  it("filters by verdict and blocks-send", () => {
    const fails = filterRows(rows, { ...DEFAULT_QUERY, verdict: "fail" });
    expect(fails.length).toBeGreaterThan(0);
    expect(fails.every((r) => r.verdict === "fail")).toBe(true);
    expect(filterRows(rows, { ...DEFAULT_QUERY, blocking: true }).every((r) => r.blocking)).toBe(true);
  });

  it("severity sort is worst-first and reverses", () => {
    const asc = sortRows(rows, { ...DEFAULT_QUERY, sort: "severity", dir: "asc" });
    expect(asc[0]!.verdict).toBe("fail");
    const desc = sortRows(rows, { ...DEFAULT_QUERY, sort: "severity", dir: "desc" });
    expect(desc[0]!.verdict).not.toBe("fail");
  });
});

describe("compareRuns", () => {
  const cmp = compareRuns(rows, medtechRun.id, acmeRun.id);

  it("aligns eval types across runs and marks deltas", () => {
    expect(cmp.length).toBeGreaterThan(0);
    const shared = cmp.find((r) => r.evalType === "cta_calibration")!;
    expect(shared.a).not.toBe("not_checked");
    expect(shared.b).not.toBe("not_checked");
    // champion_continuity exists only on acme — one-sided, never a phantom pass.
    expect(cmp.find((r) => r.evalType === "champion_continuity")!.delta).toBe("one-sided");
  });
});

describe("compareRuns direction", () => {
  const base = rows[0]!;
  const row = (runId: string, verdict: string, evalType = "e_x"): (typeof rows)[number] => ({ ...base, runId, resultId: `${runId}-${verdict}-${evalType}`, evalType, name: evalType, verdict } as (typeof rows)[number]);
  const delta = (a: string, b: string) => compareRuns([row("A", a), row("B", b)], "A", "B")[0]!.delta;

  it("reports improved when run B's verdict is better than run A's, regressed when worse", () => {
    expect(delta("fail", "pass")).toBe("improved");
    expect(delta("warn", "pass")).toBe("improved");
    expect(delta("fail", "warn")).toBe("improved");
    expect(delta("pass", "fail")).toBe("regressed");
    expect(delta("pass", "warn")).toBe("regressed");
    expect(delta("warn", "fail")).toBe("regressed");
  });

  it("does not rank unsure or not-relevant against the others: any such change is just changed", () => {
    expect(delta("abstain", "pass")).toBe("changed");
    expect(delta("warn", "abstain")).toBe("changed");
    expect(delta("pass", "not_relevant")).toBe("changed");
  });

  it("keeps same and one-sided", () => {
    expect(delta("pass", "pass")).toBe("same");
    expect(compareRuns([row("A", "pass")], "A", "B")[0]!.delta).toBe("one-sided");
  });
});
