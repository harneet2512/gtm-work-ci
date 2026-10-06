import { describe, expect, it } from "vitest";
import { criterionLabel } from "@/lib/evals/inspector/criterion-words";
import { buildCriteriaView, FOLDING_RULE, worstFirst } from "@/lib/evals/inspector/criteria";
import { CAND_A, CAND_B, result } from "./fixtures";

const ref = ["candidate:x"];

describe("criterion labels", () => {
  it("uses human wording for known criteria and a clean fallback for unknown ones", () => {
    expect(criterionLabel("no_future_leakage")).toBe("No case is dated after this event");
    expect(criterionLabel("cta")).toBe("Call to action");
    expect(criterionLabel("some_new_check")).toBe("Some new check");
    expect(criterionLabel("cta_timing_correct")).toBe("Call to action and timing are right");
  });
});

describe("criteria rendering (HAR-149 section 5)", () => {
  it("one row per stored criterion with its own result, then the folded result", () => {
    const r = result({
      gate: "B5",
      verdict: "warn",
      criteria: [
        { id: "relevant_precedent_found", label: "x", result: "pass", why: "found one", evidence_refs: ref },
        { id: "important_precedent_missed", label: "x", result: "warn", why: "a case was missed", evidence_refs: ref },
      ],
    });
    const v = buildCriteriaView([r], () => null);
    expect(v.storedCriteria).toBe(true);
    expect(v.groups).toHaveLength(1);
    expect(v.groups[0]!.rows.map((x) => [x.id, x.result])).toEqual([
      ["relevant_precedent_found", "pass"],
      ["important_precedent_missed", "warn"],
    ]);
    expect(v.result).toBe("warn");
    expect(v.folding).toBe(FOLDING_RULE);
    expect(v.score).toBeNull();
  });

  it("a failing criterion folds the result to FAIL even when the stored verdict is generous", () => {
    const r = result({ gate: "D2", verdict: "pass", criteria: [{ id: "fit", label: "", result: "fail", why: "w", evidence_refs: ref }] });
    expect(buildCriteriaView([r], () => null).result).toBe("fail");
  });

  it("with no stored criteria the sub-gate results are the rows; a single result gets one honest row", () => {
    const a = result({ gate: "D3", sub_gate: "no_blocked_preferred", verdict: "pass" });
    const b = result({ gate: "D3", sub_gate: "ranking", verdict: "warn" });
    const v = buildCriteriaView([a, b], () => null);
    expect(v.storedCriteria).toBe(false);
    expect(v.groups[0]!.rows.map((x) => x.id)).toEqual(["no_blocked_preferred", "ranking"]);
    expect(v.groups[0]!.rows.every((x) => x.origin === "sub_gate")).toBe(true);
    const single = buildCriteriaView([result({ gate: "D7", verdict: "fail" })], () => null);
    expect(single.groups[0]!.rows).toHaveLength(1);
    expect(single.groups[0]!.rows[0]!.origin).toBe("result");
    expect(single.note).toMatch(/one verdict/i);
  });

  it("groups by judged object and titles the group (one per candidate for D2)", () => {
    const mk = (id: string, v: "pass" | "fail") =>
      result({ gate: "D2", sub_gate: "candidate", judged_object: { type: "StrategyCandidate", id }, verdict: v, criteria: [{ id: "fit", label: "", result: v, why: "w", evidence_refs: ref }] });
    const v = buildCriteriaView([mk(CAND_A, "pass"), mk(CAND_B, "fail")], (r) => (r.judged_object.id === CAND_A ? "Option A" : "Option B"));
    expect(v.groups.map((g) => g.title)).toEqual(["Option A", "Option B"]);
    expect(v.groups.map((g) => g.result)).toEqual(["pass", "fail"]);
    expect(v.result).toBe("fail");
  });

  it("not applicable criteria never lift or lower the fold", () => {
    const r = result({
      gate: "B5",
      criteria: [
        { id: "a", label: "", result: "pass", why: "", evidence_refs: ref },
        { id: "b", label: "", result: "not_applicable", why: "no precedents", evidence_refs: [] },
      ],
    });
    expect(buildCriteriaView([r], () => null).result).toBe("pass");
  });

  it("no results is no view", () => {
    const v = buildCriteriaView([], () => null);
    expect(v.groups).toEqual([]);
    expect(v.result).toBeNull();
  });
});

describe("worstFirst", () => {
  it("orders fail, warn, unknown, pass, not applicable and keeps ties in judge order", () => {
    const rows = [
      { id: "a", result: "pass" },
      { id: "b", result: "fail" },
      { id: "c", result: "warn" },
      { id: "d", result: "fail" },
      { id: "e", result: "not_applicable" },
      { id: "f", result: "unknown" },
    ] as never[];
    expect(worstFirst(rows).map((r: { id: string }) => r.id)).toEqual(["b", "d", "c", "f", "a", "e"]);
  });
});
