import { describe, expect, it } from "vitest";
import { humanizeObserved } from "@/lib/evals/observed";

describe("humanizeObserved", () => {
  it("reads a dimension list in words, worst first", () => {
    expect(humanizeObserved("fit=pass, grounding=pass, cta=warn")).toBe("Next-step ask: warn. Fits the account state and grounded in evidence: pass.");
  });
  it("orders failures before warnings", () => {
    expect(humanizeObserved("cta=warn, fit=fail, grounding=pass")).toBe("Fits the account state: fail. Next-step ask: warn. Grounded in evidence: pass.");
  });
  it("handles an all-pass list and an unknown key", () => {
    expect(humanizeObserved("fit=pass")).toBe("Fits the account state: pass.");
    expect(humanizeObserved("brand_voice=unknown")).toBe("Brand voice: unknown.");
  });
  it("leaves ordinary sentences, empty text and look-alikes untouched", () => {
    expect(humanizeObserved("the person chose option B over the preferred option A")).toBe("the person chose option B over the preferred option A");
    expect(humanizeObserved("")).toBe("");
    expect(humanizeObserved("a=b, fit=pass")).toBe("a=b, fit=pass");
    expect(humanizeObserved("x=1; DROP TABLE")).toBe("x=1; DROP TABLE");
  });
});
