import { describe, expect, it } from "vitest";
import { plainText } from "@/lib/evals/inspector/criterion-words";

describe("plainText: criterion ids never reach the copy", () => {
  it("swaps a leading criterion id for its display label and capitalises the sentence", () => {
    expect(plainText("uncertainty_reflected: the ranking is weak")).toBe("Uncertainty is reflected: the ranking is weak");
  });
  it("handles every id in a joined list and key=verdict pairs", () => {
    expect(plainText("grounding: no source; cta: too soft")).toBe("Grounded in evidence: no source; Call to action: too soft");
    expect(plainText("intent_preserved=pass, claims_grounded=fail")).toBe("The intent is preserved: pass, Claims are grounded in evidence: fail");
  });
  it("words an unknown id only where it is used as a key (before a colon)", () => {
    expect(plainText("some_new_check: failed")).toBe("Some new check: failed");
  });
  it("leaves ordinary snake_case words, addresses and field names alone", () => {
    expect(plainText("the buyer wrote to first_last@acme.com about deal_size")).toBe("The buyer wrote to first_last@acme.com about deal_size");
  });
  it("capitalises ordinary prose and leaves empty text empty", () => {
    expect(plainText("the body changed")).toBe("The body changed");
    expect(plainText("")).toBe("");
    expect(plainText("  ")).toBe("");
  });
});
