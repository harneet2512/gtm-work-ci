import { describe, expect, it } from "vitest";
import { controlEffectView, EFFECT_WORDS, effectForVerdict } from "@/lib/evals/inspector/effects";
import { def, result, RULES } from "./fixtures";

describe("control effect mapping (HAR-149 section 7)", () => {
  it("a monitoring-only gate only records, whatever its verdict: never implied to be a hard stop", () => {
    for (const verdict of ["pass", "warn", "fail"] as const) {
      const v = controlEffectView(result({ gate: "D3", verdict }), def("D3"), RULES);
      expect(v.effect).toBe("RECORD ONLY");
      expect(v.hardStop).toBe(false);
      expect(v.plain).toMatch(/nothing/i);
    }
  });

  it("an unknown verdict marks the result unknown", () => {
    expect(controlEffectView(result({ gate: "B5", verdict: "unknown", evidence_refs: [] }), def("B5"), RULES).effect).toBe("MARK UNKNOWN");
  });

  it("a stored BLOCK (a deterministic D8 blocker) blocks and cites the registry's code", () => {
    const v = controlEffectView(result({ gate: "D8", sub_gate: "recipients", verdict: "fail", control_effect: "BLOCK" }), def("D8", { impact: "blocks current action", impact_basis: "fallback basis" }), RULES);
    expect(v.effect).toBe("BLOCK");
    expect(v.hardStop).toBe(true);
    expect(v.basis).toBe("strategystore/presend_d8.go d8AsBlockingEvals");
  });

  it("falls back to the gate's impact_basis when the rules cite nothing for the gate", () => {
    const v = controlEffectView(result({ gate: "D3" }), def("D3"), RULES);
    expect(v.basis).toMatch(/No code reads/);
  });

  it("rules that hold only for some results are never applied by guessing: a D8 fail with no stored effect does not read as BLOCK", () => {
    const r = { ...result({ gate: "D8", sub_gate: "model", verdict: "fail" }), control_effect: undefined };
    expect(controlEffectView(r, def("D8"), RULES).effect).toBe("RECORD ONLY");
    expect(controlEffectView(r, def("D8"), RULES).hardStop).toBe(false);
  });

  it("B9 never reads as BLOCK from its verdict", () => {
    expect(controlEffectView({ ...result({ gate: "B9", verdict: "fail" }), control_effect: undefined }, def("B9"), RULES).effect).toBe("RECORD ONLY");
  });

  it("the stored effect wins over the rules (the backend decided it)", () => {
    const v = controlEffectView(result({ gate: "D3", verdict: "fail", control_effect: "BLOCK" }), def("D3"), RULES);
    expect(v.effect).toBe("BLOCK");
  });

  it("a recomputed result shows RECOMPUTE and what the earlier run said, beside its verdict effect", () => {
    const v = controlEffectView(result({ gate: "D8", verdict: "pass", control_effect: "CONTINUE", lineage: { recompute_of: "x", previous_verdict: "fail" } }), def("D8"), RULES);
    expect(v.lineage).toEqual({ kind: "RECOMPUTE", previousVerdict: "fail" });
    expect(v.headline).toBe("RECOMPUTE");
    expect(v.effect).toBe("CONTINUE");
  });

  it("a retried result shows RETRY", () => {
    const v = controlEffectView(result({ gate: "D2", lineage: { retry_of: "x" } }), def("D2"), RULES);
    expect(v.headline).toBe("RETRY");
  });

  it("with no stored effect and no rules, the effect is RECORD ONLY (or MARK UNKNOWN), never a guess of BLOCK", () => {
    const r = { ...result({ gate: "D8", verdict: "fail" }), control_effect: undefined };
    expect(controlEffectView(r, def("D8"), null).effect).toBe("RECORD ONLY");
    expect(effectForVerdict(null, "D8", "unknown")).toBe("MARK UNKNOWN");
  });

  it("every effect has a label and a plain sentence", () => {
    for (const e of RULES.vocabulary) {
      expect(EFFECT_WORDS[e as keyof typeof EFFECT_WORDS].label.length).toBeGreaterThan(2);
      expect(EFFECT_WORDS[e as keyof typeof EFFECT_WORDS].plain.length).toBeGreaterThan(10);
    }
  });
});
