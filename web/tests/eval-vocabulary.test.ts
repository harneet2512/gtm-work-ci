// The shared eval vocabulary (eval design spec 4b-3): web renders from lib/evals/vocabulary.ts, Slack from
// contracts/evals/eval_wording.json. The two must be identical, or Slack and web would say different things.
import path from "node:path";
import { describe, expect, it } from "vitest";
import {
  WORDING,
  compareVerdicts,
  diagnosticPhrase,
  evalName,
  evalQuestion,
  evidenceTag,
  fill,
  graderLabel,
  heldForReview,
  verdictWording,
  worstVerdict,
} from "@/lib/evals/vocabulary";
import { CONTRACTS_DIR, readJson, validateSchema } from "./contract-validator";

const contract = readJson<Record<string, unknown>>(path.join(CONTRACTS_DIR, "evals", "eval_wording.json"));

describe("the web vocabulary is the contract's wording table", () => {
  it("is identical to contracts/evals/eval_wording.json", () => {
    expect(WORDING).toEqual(contract);
  });

  it("validates against eval_wording.v1.json", () => {
    expect(validateSchema("eval_wording", WORDING).errors).toEqual([]);
  });
});

describe("verdict order and wording", () => {
  it("sorts worst first: fail, warn, abstain, pass, not relevant, not checked", () => {
    const shuffled = ["pass", "not_checked", "warn", "not_relevant", "fail", "abstain"] as const;
    expect([...shuffled].sort(compareVerdicts)).toEqual(["fail", "warn", "abstain", "pass", "not_relevant", "not_checked"]);
  });

  it("picks the worst verdict, or null for none", () => {
    expect(worstVerdict(["pass", "warn", "pass"])).toBe("warn");
    expect(worstVerdict(["not_relevant", "fail"])).toBe("fail");
    expect(worstVerdict([])).toBeNull();
  });

  it("words each verdict with its own label and icon", () => {
    expect(verdictWording("fail")).toMatchObject({ label: "Fail", icon: "x-circle" });
    expect(verdictWording("not_checked")).toMatchObject({ label: "Not checked", icon: "dashed-circle" });
  });
});

describe("plain-language names", () => {
  it("names eval types in seller language, never the code", () => {
    expect(evalName("stakeholder_selection")).toBe("Stakeholder fit");
    expect(evalName("cta_calibration")).toBe("CTA calibration");
    expect(evalQuestion("cta_calibration")).toMatch(/\?$/);
  });

  it("falls back to a readable phrase for an eval type the table does not know", () => {
    expect(evalName("brand_new_eval")).toBe("Brand new eval");
    expect(evalQuestion("brand_new_eval")).toBeNull();
  });

  it("tags evidence classes and names graders with their model", () => {
    expect(evidenceTag("deal_data").tag).toBe("Deal evidence");
    expect(graderLabel("semantic", "deepseek/deepseek-v4-flash")).toBe("AI judge · deepseek/deepseek-v4-flash");
    expect(graderLabel("deterministic", null)).toBe("Rule check");
    expect(graderLabel("semantic", null)).toBe("AI judge");
  });

  it("words diagnostics, keeping unknown ones readable", () => {
    expect(diagnosticPhrase("champion_bypassed")).toBe("Champion bypassed");
    expect(diagnosticPhrase("some_new_code")).toBe("Some new code");
  });
});

describe("phrases", () => {
  it("fills placeholders and leaves unknown ones visible", () => {
    expect(fill("after_edit", { eval: "CTA calibration", from: "WARN", to: "PASS" })).toBe("CTA calibration WARN → PASS after your edit");
    expect(fill("human_choice", {})).toBe("Chosen by {actor}");
  });

  it("explains a held candidate with every reason, joined plainly", () => {
    expect(heldForReview(["expansion_motion"])).toBe("Held for review: an expansion ask while the account change is unconfirmed.");
    expect(heldForReview(["expansion_motion", "pricing_push", "broad_outreach"])).toBe(
      "Held for review: an expansion ask, a pricing push and broad executive outreach while the account change is unconfirmed.",
    );
  });
});
