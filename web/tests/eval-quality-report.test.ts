// Measured eval quality on /evals comes from a committed judge report (bench/reports/judges-*.json): per eval,
// verdict agreement with gold, false pass and false block, with the report's provenance and caveats. A missing
// or malformed report reads as "not measured", never as a crash or an invented number.
import path from "node:path";
import { describe, expect, it } from "vitest";
import { loadQualityReport, parseQualityReport, qualityReportPathFromEnv } from "@/lib/evals/quality-report";
import { buildOverview, loadEvalContracts } from "@/lib/evals/registry";
import { CONTRACTS_DIR } from "./contract-validator";

const REPORT = path.resolve(CONTRACTS_DIR, "../bench/reports/judges-2026-10-03-goldv2-deepseek-v4-flash-t3.json");

describe("the committed judge report", () => {
  const report = loadQualityReport(REPORT)!;

  it("gives each judged eval its agreement, false pass, false block and sample", () => {
    expect(report.byType.get("cta_calibration")).toEqual({ agreement: 0.988, falsePass: 0, falseBlock: 0.259, cases: 84, report: "Gold v2 · deepseek-v4-flash · Oct 3, 2026" });
    expect(report.byType.get("economic_buyer_coverage")).toMatchObject({ agreement: 0.786, falsePass: null, cases: 14 });
    expect(report.byType.size).toBe(25);
  });

  it("carries the pooled numbers and the provenance a reader needs to weigh them", () => {
    expect(report.source).toEqual({
      model: "deepseek-v4-flash",
      gold: "legacy fixture gold (invented demo accounts; to be replaced by CRMArena-based gold)",
      cases: 87,
      trials: 3,
      judged: 945,
      generatedAt: "2026-10-03T12:45:16+00:00",
      agreement: 0.908,
      kappa: 0.825,
      falsePass: 0.092,
      falseBlock: 0.14,
      file: "judges-2026-10-03-goldv2-deepseek-v4-flash-t3.json",
    });
  });

  it("feeds the overview: judged checks are measured, rule checks are not", () => {
    const overview = buildOverview(loadEvalContracts(CONTRACTS_DIR), report.byType, report.source);
    const rows = overview.groups.flatMap((g) => g.rows);
    expect(overview.measured).toBe(true);
    expect(overview.source?.model).toBe("deepseek-v4-flash");
    expect(rows.filter((r) => r.quality).length).toBe(25);
    expect(rows.find((r) => r.evalType === "pricing_integrity")!.quality).toBeNull();
  });
});

describe("reading a report defensively", () => {
  it("returns null for a missing file and for a document that is not a judge report", () => {
    expect(loadQualityReport("/nowhere/report.json")).toBeNull();
    expect(parseQualityReport({ hello: "world" }, "x.json")).toBeNull();
    expect(parseQualityReport(null, "x.json")).toBeNull();
  });

  it("skips evals with insufficient data or no agreement", () => {
    const doc = {
      model: "openrouter/m",
      gold: "g",
      n_cases: 1,
      trials: 1,
      generated_at: "2026-10-01T00:00:00Z",
      overall: { n_judged: 3, verdict_agreement: 1, verdict_kappa: null, false_pass_rate: null, false_block_rate: null },
      per_eval: { a: { n_judged: 2, verdict_agreement: 1, false_pass_rate: 0, false_block_rate: 0 }, b: { n_judged: 1, insufficient_data: true, verdict_agreement: 1 }, c: { n_judged: 0, verdict_agreement: null } },
    };
    const r = parseQualityReport(doc, "y.json")!;
    expect([...r.byType.keys()]).toEqual(["a"]);
    expect(r.source.model).toBe("m");
    expect(r.byType.get("a")!.report).toBe("g · m · Oct 1, 2026");
  });

  it("finds the report beside the web app unless GHOST_EVAL_QUALITY_REPORT says otherwise", () => {
    expect(qualityReportPathFromEnv({ GHOST_EVAL_QUALITY_REPORT: "/srv/r.json" }, "/app/web")).toBe("/srv/r.json");
    expect(qualityReportPathFromEnv({}, "/app/web").replaceAll("\\", "/")).toMatch(/\/app\/bench\/reports\/judges-2026-10-03-goldv2-deepseek-v4-flash-t3\.json$/);
  });
});
