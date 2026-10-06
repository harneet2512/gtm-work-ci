// Judge quality on /evals comes from a recorded snapshot (not a live reading): per eval, verdict agreement with
// reference answers by a stronger model (not human), false pass and false block, with the date it was recorded and nothing
// of the benchmark's internals. A missing or malformed snapshot reads as "not measured", never as a crash or an invented
// number, and so does a snapshot measured on the retired invented-account gold: no agreement number is read from it.
// The committed legacy snapshot is that retired gold, so these tests read it with includeRetired as number mechanics only.
import { readFileSync } from "node:fs";
import path from "node:path";
import { describe, expect, it } from "vitest";
import { JUDGE_QUALITY_CAVEAT, loadQualityReport, parseQualityReport, qualityReportPathFromEnv, REFERENCE_ANSWERS } from "@/lib/evals/quality-report";
import { buildOverview, loadEvalContracts } from "@/lib/evals/registry";
import { CONTRACTS_DIR } from "./contract-validator";

const REPORT = path.resolve(CONTRACTS_DIR, "../bench/reports/judges-2026-10-03-goldv2-deepseek-v4-flash-t3.json");

describe("the recorded judge snapshot", () => {
  const report = loadQualityReport(REPORT, { includeRetired: true })!;

  it("gives each judged eval its agreement, false pass, false block and sample, labelled as recorded", () => {
    expect(report.byType.get("cta_calibration")).toEqual({ agreement: 0.988, falsePass: 0, falseBlock: 0.259, cases: 84, report: "Recorded Oct 3, 2026" });
    expect(report.byType.get("economic_buyer_coverage")).toMatchObject({ agreement: 0.786, falsePass: null, cases: 14 });
    expect(report.byType.size).toBe(25);
  });

  it("carries only the pooled numbers and the date: no benchmark internals", () => {
    expect(report.source).toEqual({ recordedAt: "2026-10-03T12:45:16+00:00", judged: 945, agreement: 0.908, falsePass: 0.092, falseBlock: 0.14 });
    const text = JSON.stringify([...report.byType.values()]) + JSON.stringify(report.source);
    expect(text).not.toMatch(/deepseek|gold|qwen|bench|\.json|trials/i);
  });

  it("feeds the overview: judged checks are measured, rule checks are not", () => {
    const overview = buildOverview(loadEvalContracts(CONTRACTS_DIR), report.byType, report.source);
    const rows = overview.groups.flatMap((g) => g.rows);
    expect(overview.measured).toBe(true);
    expect(overview.source?.recordedAt).toBe("2026-10-03T12:45:16+00:00");
    expect(rows.filter((r) => r.quality).length).toBe(25);
    expect(rows.find((r) => r.evalType === "pricing_integrity")!.quality).toBeNull();
  });
});

describe("a snapshot measured on the retired invented-account gold", () => {
  it("is not read: no agreement number comes from it, so the page says not measured", () => {
    expect(loadQualityReport(REPORT)).toBeNull();
    expect(parseQualityReport(JSON.parse(readFileSync(REPORT, "utf8")))).toBeNull();
    expect(loadQualityReport(qualityReportPathFromEnv({}, path.resolve(CONTRACTS_DIR, "../web")))).toBeNull();
  });

  it("is read only when asked, as number mechanics", () => {
    expect(parseQualityReport(JSON.parse(readFileSync(REPORT, "utf8")), { includeRetired: true })).not.toBeNull();
  });

  it("does not stop a snapshot whose reference set is real accounts", () => {
    const doc = { generated_at: "2026-10-10T00:00:00Z", gold: "CRMArena-Pro test-partition accounts; reference answers by a stronger model", overall: { n_judged: 2, verdict_agreement: 1 }, per_eval: { a: { n_judged: 2, verdict_agreement: 1 } } };
    expect(parseQualityReport(doc)!.byType.size).toBe(1);
  });

  it("says in the caveat what the figures are measured against: reference answers by a stronger model, not human", () => {
    expect(REFERENCE_ANSWERS).toBe("reference answers by a stronger model, not human");
    expect(JUDGE_QUALITY_CAVEAT).toContain(REFERENCE_ANSWERS);
    expect(JUDGE_QUALITY_CAVEAT).not.toMatch(/one author|human[- ]labell?ed/i);
  });
});

describe("reading a snapshot defensively", () => {
  it("returns null for a missing file and for a document that is not a judge snapshot", () => {
    expect(loadQualityReport("/nowhere/report.json")).toBeNull();
    expect(parseQualityReport({ hello: "world" })).toBeNull();
    expect(parseQualityReport(null)).toBeNull();
    expect(parseQualityReport({ per_eval: {}, generated_at: 5 })).toBeNull();
  });

  it("skips evals with insufficient data or no agreement", () => {
    const doc = {
      generated_at: "2026-10-01T00:00:00Z",
      overall: { n_judged: 3, verdict_agreement: 1, false_pass_rate: null, false_block_rate: null },
      per_eval: { a: { n_judged: 2, verdict_agreement: 1, false_pass_rate: 0, false_block_rate: 0 }, b: { n_judged: 1, insufficient_data: true, verdict_agreement: 1 }, c: { n_judged: 0, verdict_agreement: null } },
    };
    const r = parseQualityReport(doc)!;
    expect([...r.byType.keys()]).toEqual(["a"]);
    expect(r.byType.get("a")!.report).toBe("Recorded Oct 1, 2026");
    expect(r.source).toEqual({ recordedAt: "2026-10-01T00:00:00Z", judged: 3, agreement: 1, falsePass: null, falseBlock: null });
  });

  it("tolerates a snapshot with no pooled numbers", () => {
    const r = parseQualityReport({ generated_at: "2026-10-01T00:00:00Z", per_eval: {} })!;
    expect(r.source).toEqual({ recordedAt: "2026-10-01T00:00:00Z", judged: 0, agreement: null, falsePass: null, falseBlock: null });
  });

  it("finds the snapshot beside the web app unless the environment names another", () => {
    expect(qualityReportPathFromEnv({ GHOST_EVAL_QUALITY_REPORT: "/srv/r.json" }, "/app/web")).toBe("/srv/r.json");
    expect(qualityReportPathFromEnv({}, "/app/web").replaceAll("\\", "/")).toMatch(/\/app\/bench\/reports\/judges-2026-10-03-goldv2-deepseek-v4-flash-t3\.json$/);
  });
});
