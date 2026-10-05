import { describe, expect, it } from "vitest";
import { withDemo } from "@/lib/view/demo-link";

describe("withDemo", () => {
  it("leaves the link alone when demo mode is off", () => {
    expect(withDemo("/evals#checks-h", false)).toBe("/evals#checks-h");
  });
  it("adds the flag to a bare path, keeping the fragment last", () => {
    expect(withDemo("/evals", true)).toBe("/evals?demo=1");
    expect(withDemo("/evals#job-intelligence", true)).toBe("/evals?demo=1#job-intelligence");
  });
  it("appends to an existing query and never duplicates the flag", () => {
    expect(withDemo("/evals?class=deal_data#checks-h", true)).toBe("/evals?class=deal_data&demo=1#checks-h");
    expect(withDemo("/evals?demo=1", true)).toBe("/evals?demo=1");
  });
});
