// @vitest-environment jsdom
// The /evals overview rendered from the real contracts: plain names and questions, how proven, whether a check
// can block, where it applies, and "Not measured yet" until an eval-of-evals report exists.
import { cleanup, render, screen, within } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";
import { EvalOverview } from "@/components/evals/EvalOverview";
import { buildOverview, loadEvalContracts } from "@/lib/evals/registry";
import { CONTRACTS_DIR } from "./contract-validator";

afterEach(cleanup);
const contracts = loadEvalContracts(CONTRACTS_DIR);

describe("EvalOverview", () => {
  it("says honestly, in the System job, that judge quality is not measured yet", () => {
    render(<EvalOverview overview={buildOverview(contracts, null)} />);
    const quality = screen.getByRole("region", { name: "Judge quality" });
    expect(document.getElementById("job-system")!.contains(quality)).toBe(true);
    expect(within(quality).getByText("Not measured yet")).toBeTruthy();
    expect(quality.textContent).not.toMatch(/core|serve|report/i);
    // Judge quality is no longer a column of the drafted-action checks in Job 2.
    expect(screen.queryByRole("columnheader", { name: "Measured quality" })).toBeNull();
  });

  it("lists the 35 checks by how proven they are, each with its question and definition", () => {
    render(<EvalOverview overview={buildOverview(contracts, null)} />);
    const tables = screen.getAllByRole("table").filter((t) => t.classList.contains("check-table"));
    expect(tables.map((t) => t.querySelector("caption .evidence-tag")?.textContent)).toEqual(["Policy rule", "Deal evidence", "Sales methodology", "CS practice"]);
    const row = document.getElementById("eval-cta_calibration")!;
    expect(within(row).getByRole("rowheader").textContent).toMatch(/^CTA calibrationIs the ask the right size/);
    expect(within(row).getAllByRole("cell").map((c) => c.textContent)).toEqual(["AI judge", "Yes", "Action/output generation"]);
    expect(within(row).getByText(/When it blocks:/)).toBeTruthy();
    expect(within(document.getElementById("eval-buyer_readiness")!).getByText("Not mapped yet")).toBeTruthy();
    // Main lines carry no eval codes.
    expect(screen.queryByText("cta_calibration")).toBeNull();
  });

  it("shows recorded numbers in the System job when a snapshot covers a check", () => {
    const quality = new Map([["grounding", { agreement: 0.9, falsePass: 0.05, falseBlock: 0.02, cases: 40, report: "Recorded Oct 10, 2026" }]]);
    const source = { recordedAt: "2026-10-10T00:00:00Z", judged: 40, agreement: 0.9, falsePass: 0.05, falseBlock: 0.02 };
    render(<EvalOverview overview={buildOverview(contracts, quality, source)} />);
    const row = document.getElementById("judge-grounding")!;
    expect(within(row).getAllByRole("cell").map((c) => c.textContent)).toEqual(["90%", "5%", "2%", "40"]);
    expect(document.getElementById("eval-grounding")!.textContent).not.toMatch(/agree/);
  });

  it("lists the whole HAR-97 strategy by family, collapsed, with status in words", () => {
    render(<EvalOverview overview={buildOverview(contracts, null)} />);
    const families = document.querySelectorAll("details.family");
    expect(families).toHaveLength(27);
    const e7 = document.getElementById("family-E7")!;
    expect(e7.querySelector("summary")!.textContent).toMatch(/^Knowledge influence attribution/);
    expect(within(e7).getAllByRole("row").length).toBeGreaterThan(1);
    expect(screen.getByText(/211 evals in 27 families/)).toBeTruthy();
  });
});
