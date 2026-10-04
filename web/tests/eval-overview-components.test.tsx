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
  it("says honestly that quality is not measured yet, and why", () => {
    render(<EvalOverview overview={buildOverview(contracts, null)} />);
    const callout = screen.getByRole("region", { name: "Measured quality" });
    expect(within(callout).getByText(/the core does not serve yet/)).toBeTruthy();
    expect(screen.getAllByText("Not measured yet")).toHaveLength(35 + 1); // every row, plus the callout's own words
  });

  it("lists the 35 checks by how proven they are, each with its question and definition", () => {
    render(<EvalOverview overview={buildOverview(contracts, null)} />);
    const tables = screen.getAllByRole("table").filter((t) => t.classList.contains("check-table"));
    expect(tables.map((t) => t.querySelector("caption .evidence-tag")?.textContent)).toEqual(["Policy rule", "Deal evidence", "Sales methodology", "CS practice"]);
    const row = document.getElementById("eval-cta_calibration")!;
    expect(within(row).getByRole("rowheader").textContent).toMatch(/^CTA calibrationIs the ask the right size/);
    expect(within(row).getAllByRole("cell").map((c) => c.textContent)).toEqual(["AI judge", "Yes", "Action/output generation", "Not measured yet"]);
    expect(within(row).getByText(/When it blocks:/)).toBeTruthy();
    expect(within(document.getElementById("eval-buyer_readiness")!).getByText("Not mapped in the registry yet")).toBeTruthy();
    // Main lines carry no eval codes.
    expect(screen.queryByText("cta_calibration")).toBeNull();
  });

  it("shows measured numbers when a report covers a check", () => {
    const quality = new Map([["grounding", { agreement: 0.9, falsePass: 0.05, falseBlock: 0.02, cases: 40, report: "eval-of-evals 2026-10-10" }]]);
    render(<EvalOverview overview={buildOverview(contracts, quality)} />);
    expect(within(document.getElementById("eval-grounding")!).getByText(/90% agree · 5% false pass · 2% false block/)).toBeTruthy();
    expect(screen.getByRole("region", { name: "Measured quality" }).textContent).toMatch(/Checks it does not cover say so/);
  });

  it("lists the whole HAR-97 strategy by family, collapsed, with status in words", () => {
    render(<EvalOverview overview={buildOverview(contracts, null)} />);
    const families = document.querySelectorAll("details.family");
    expect(families).toHaveLength(27);
    const e7 = document.getElementById("family-E7")!;
    expect(e7.querySelector("summary")!.textContent).toMatch(/^Knowledge influence attribution/);
    expect(within(e7).getAllByRole("row").length).toBeGreaterThan(1);
    expect(screen.getByText(/213 evals in 27 families/)).toBeTruthy();
  });
});
