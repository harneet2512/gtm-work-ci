// The evals explorer and the operator's run comparison (HAR-145): real counts straight from the eval-run reads,
// "not measured" for an area with no results, deltas labelled "previous episode", and an honest reading of every change a
// comparison can report (inconclusive included). The three views never offer the operator comparison in Demo mode.
import { describe, expect, it } from "vitest";
import type { EvalFamilySummary, EvalRun, EvalRunComparison } from "@/lib/api/types";
import { areaViews, compareView, evalRunRow, evalsTabs, resolveEvalsView } from "@/lib/view/eval-runs";
import { loadExample } from "./contract-validator";

const run = loadExample<EvalRun>("eval_run");
const families = loadExample<EvalFamilySummary>("eval_family_summary");
const comparison = loadExample<EvalRunComparison>("eval_run_comparison");

describe("evalRunRow", () => {
  it("carries the account, the time, the total and the four areas with their tone and counts", () => {
    const row = evalRunRow(run);
    expect(row).toMatchObject({ id: run.id, account: "Acme Corp", episodeId: run.decision_episode_id, evaluatedAt: run.evaluated_at });
    expect(row.total).toBe("2 results");
    expect(row.areas.map((a) => a.id)).toEqual(["intelligence", "decision_learning", "cliff_experience", "system"]);
    expect(row.areas.find((a) => a.id === "intelligence")).toMatchObject({ measured: false, text: "not measured", tone: "none" });
    expect(row.areas.find((a) => a.id === "decision_learning")).toMatchObject({ measured: true, tone: "ok" });
    expect(row.areas.find((a) => a.id === "cliff_experience")!.tone).toBe("warn");
  });

  it("says a single result in the singular and links the previous episode's run", () => {
    expect(evalRunRow({ ...run, result_count: 1 }).total).toBe("1 result");
    expect(evalRunRow({ ...run, previous_eval_run_id: "0f0a0000-0000-4000-8000-000000000600" }).previousId).toBe("0f0a0000-0000-4000-8000-000000000600");
    expect(evalRunRow({ ...run, previous_eval_run_id: null }).previousId).toBeNull();
  });
});

describe("areaViews", () => {
  const views = areaViews(families);

  it("lists the four areas in order with measured, counts and the previous-episode delta", () => {
    expect(views.map((v) => v.id)).toEqual(["intelligence", "decision_learning", "cliff_experience", "system"]);
    const decision = views.find((v) => v.id === "decision_learning")!;
    expect(decision.measured).toBe(true);
    expect(decision.counts).toBe("1 pass · 0 warn · 0 fail · 0 unknown");
    expect(decision.delta).toBe("vs previous episode: +1 pass · -1 fail");
  });

  it("reads an area with no results as not measured, with no delta and no families shown as healthy", () => {
    const intelligence = views.find((v) => v.id === "intelligence")!;
    expect(intelligence.measured).toBe(false);
    expect(intelligence.status).toBe("not measured");
    expect(intelligence.counts).toBeNull();
    expect(intelligence.delta).toBeNull();
    expect(intelligence.families).toEqual([]);
  });

  it("lists the families and eval types in each measured area with real counts and the number of results", () => {
    const decision = views.find((v) => v.id === "decision_learning")!;
    const family = decision.families[0]!;
    expect(family).toMatchObject({ id: "E8", name: "Decision construction" });
    const type = family.evalTypes[0]!;
    expect(type.name).toBe("Relationship continuity");
    expect(type.results).toBe(1);
    expect(type.counts).toBe("1 pass · 0 warn · 0 fail · 0 unknown");
    expect(type.delta).toBe("vs previous episode: +1 pass · -1 fail");
  });

  it("says there is no previous episode when a delta is absent, and shows blocking failures", () => {
    const noPrev: EvalFamilySummary = {
      ...families,
      previous_eval_run_id: null,
      areas: families.areas.map((a) => (a.area === "decision_learning" ? { ...a, delta: null, counts: { ...a.counts, fail: 1, blocking_fail: 1, total: 2 } } : a)),
    };
    const decision = areaViews(noPrev).find((v) => v.id === "decision_learning")!;
    expect(decision.delta).toBe("no previous episode to compare");
    expect(decision.blocking).toBe("1 blocking");
    expect(areaViews(families).find((v) => v.id === "decision_learning")!.blocking).toBeNull();
  });
});

describe("compareView", () => {
  it("shows each side's run, account and counts, and one row per eval type with its change", () => {
    const v = compareView(comparison);
    expect(v.a.account).toBe("Acme Corp");
    expect(v.rows).toHaveLength(2);
    const first = v.rows[0]!;
    expect(first).toMatchObject({ name: "Relationship continuity", a: "fail", b: "pass", change: "improved", changeWord: "improved", aBlocking: true, bBlocking: false });
    expect(v.rows[1]).toMatchObject({ a: "warn", b: "warn", change: "unchanged" });
    expect(v.overall).toMatchObject({ change: "improved", word: "improved" });
    expect(v.overall.counts).toBe("1 improved · 0 regressed · 1 unchanged · 0 added · 0 removed · 0 inconclusive");
  });

  it("words every change a comparison can report, inconclusive included", () => {
    const row = (change: EvalRunComparison["rows"][number]["change"], a: "pass" | "warn" | "fail" | "unknown" | null, b: "pass" | "warn" | "fail" | "unknown" | null) => ({ ...comparison.rows[0]!, change, a, b });
    const v = compareView({
      ...comparison,
      rows: [row("regressed", "pass", "fail"), row("added", null, "warn"), row("removed", "pass", null), row("inconclusive", "unknown", "pass")],
      overall: { ...comparison.overall, change: "inconclusive", inconclusive: 1, regressed: 1, added: 1, removed: 1, improved: 0, unchanged: 0 },
    });
    expect(v.rows.map((r) => r.changeWord)).toEqual(["regressed", "checked only in run B", "checked only in run A", "inconclusive"]);
    expect(v.rows[1]!.a).toBe("not checked");
    expect(v.rows[2]!.b).toBe("not checked");
    expect(v.rows[3]!.a).toBe("unknown");
    expect(v.overall.word).toBe("inconclusive");
    expect(v.overall.explanation).toContain("could not be judged");
  });

  it("notes an added failure the overall change counts", () => {
    const v = compareView({ ...comparison, overall: { ...comparison.overall, added_fail: 2 } });
    expect(v.overall.counts).toContain("2 added failures");
  });
});

describe("evals views", () => {
  it("offers Checks, Results and the operator-only run comparison", () => {
    expect(evalsTabs(false).map((t) => t.id)).toEqual(["gates", "cards", "catalog", "results", "compare"]);
    expect(evalsTabs(false).find((t) => t.id === "compare")!.label).toBe("Run comparison (operator)");
  });

  it("never offers the run comparison in Demo mode", () => {
    expect(evalsTabs(true).map((t) => t.id)).toEqual(["gates", "cards", "catalog", "results"]);
  });

  it("resolves the view, and falls back to Results rather than showing the comparison in Demo mode", () => {
    expect(resolveEvalsView(undefined, false)).toBe("gates");
    expect(resolveEvalsView("catalog", false)).toBe("catalog");
    expect(resolveEvalsView("cards", false)).toBe("cards");
    expect(resolveEvalsView("results", false)).toBe("results");
    expect(resolveEvalsView("compare", false)).toBe("compare");
    expect(resolveEvalsView("compare", true)).toBe("results");
    expect(resolveEvalsView("nonsense", true)).toBe("gates");
  });
});
