// @vitest-environment jsdom
// The Intelligence job (the section that holds Bucket 1) says gtm_ai in its heading, its question and its description.
import { cleanup, render } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";
import { EvalOverview } from "@/components/evals/EvalOverview";
import { buildOverview, loadEvalContracts } from "@/lib/evals/registry";
import { CONTRACTS_DIR } from "./contract-validator";

afterEach(cleanup);
const overview = buildOverview(loadEvalContracts(CONTRACTS_DIR), null);

describe("the Intelligence job wording", () => {
  it("never says Ghost in the Intelligence section, the job navigation or the Bucket 1 table", () => {
    render(<EvalOverview overview={overview} />);
    const section = document.getElementById("job-intelligence")!;
    expect(section.textContent).toContain("gtm_ai");
    expect(section.textContent).not.toContain("Ghost");
    expect(document.querySelector(".job-nav")!.textContent).not.toContain("Ghost");
    expect(document.querySelector(".job-nav")!.textContent).toContain("gtm_ai");
  });
});
