// @vitest-environment jsdom
// The eval catalog with the committed judge report: headline numbers, the provenance and its caveats, each
// judged check's agreement, rule checks still "Not measured yet", and the evidence-class filter.
import path from "node:path";
import { cleanup, render, screen, within } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";
import { EvalOverview } from "@/components/evals/EvalOverview";
import { loadQualityReport } from "@/lib/evals/quality-report";
import { buildOverview, loadEvalContracts } from "@/lib/evals/registry";
import { CONTRACTS_DIR } from "./contract-validator";

afterEach(cleanup);
const contracts = loadEvalContracts(CONTRACTS_DIR);
const report = loadQualityReport(path.resolve(CONTRACTS_DIR, "../bench/reports/judges-2026-10-03-goldv2-deepseek-v4-flash-t3.json"))!;
const measured = buildOverview(contracts, report.byType, report.source);

describe("the catalog with the judge report", () => {
  it("keeps the three eval jobs visibly separate, each a link to its section", () => {
    render(<EvalOverview overview={measured} />);
    const nav = screen.getByRole("navigation", { name: "The three eval jobs" });
    const tiles = within(nav).getAllByRole("link");
    expect(tiles.map((a) => a.getAttribute("href"))).toEqual(["#job-intelligence", "#job-decision", "#job-system"]);
    expect(tiles[0]!.textContent).toMatch(/^1Intelligence-building/);
    expect(tiles[1]!.textContent).toContain("plus 35 checks on every drafted action");
    expect(tiles[2]!.textContent).toMatch(/^3System/);
    const intelligence = document.getElementById("job-intelligence")!;
    expect([...intelligence.querySelectorAll("details.family")].map((d) => d.id)).toEqual(["family-E1", "family-E2", "family-E3", "family-E4", "family-E5", "family-E6"]);
    expect(document.getElementById("job-decision")!.querySelectorAll("details.family")).toHaveLength(11);
  });

  it("keeps system metrics secondary: collapsed, with grader quality inside", () => {
    const { container } = render(<EvalOverview overview={measured} />);
    const system = document.getElementById("job-system") as HTMLDetailsElement;
    expect(system.tagName).toBe("DETAILS");
    expect(system.open).toBe(false);
    expect(system.querySelectorAll("details.family")).toHaveLength(10);
    const stats = [...container.querySelectorAll("#job-system .stat")].map((s) => s.textContent);
    expect(stats[0]).toContain("90.8%");
    expect(stats[0]).toContain("κ 0.82 · 945 judgments");
    expect(stats[1]).toContain("9.2% · 14%");
  });

  it("says where the numbers come from and how far to trust them", () => {
    render(<EvalOverview overview={measured} />);
    const callout = screen.getByRole("region", { name: "Measured quality" });
    expect(callout.textContent).toMatch(/the deepseek-v4-flash judge, 3 trials over 87 gold cases \(945 judgments\)/);
    expect(callout.textContent).toMatch(/legacy single-author set/);
    expect(callout.textContent).toMatch(/runtime judge is now qwen3\.8-flash, which has not been measured per eval yet/);
    expect(callout.textContent).toMatch(/Not measured yet/);
  });

  it("shows each judged check's agreement and what a wrong verdict costs; rule checks stay unmeasured", () => {
    render(<EvalOverview overview={measured} />);
    const cta = document.getElementById("eval-cta_calibration")!;
    expect(within(cta).getByText("99% agree · 0% false pass · 26% false block")).toBeTruthy();
    const judged = within(cta).getByText("84 judged");
    expect(judged.getAttribute("title")).toBe("Gold v2 · deepseek-v4-flash · Oct 3, 2026");
    expect(cta.querySelector(".agree-meter")!.className).toContain("is-high");
    const eb = document.getElementById("eval-economic_buyer_coverage")!;
    expect(within(eb).getByText("79% agree · 0% false block")).toBeTruthy();
    expect(eb.querySelector(".agree-meter")!.className).toContain("is-low");
    expect(document.getElementById("eval-stakeholder_coverage")!.querySelector(".agree-meter")!.className).toContain("is-mid");
    expect(within(document.getElementById("eval-pricing_integrity")!).getByText("Not measured yet")).toBeTruthy();
  });

  it("narrows the checks to one evidence class, the filter saying which", () => {
    render(<EvalOverview overview={measured} activeClass="deal_data" />);
    const tables = screen.getAllByRole("table").filter((t) => t.classList.contains("check-table"));
    expect(tables).toHaveLength(1);
    expect(tables[0]!.querySelector("caption .evidence-tag")!.textContent).toBe("Deal evidence");
    const filter = screen.getByRole("navigation", { name: "Filter checks by how proven they are" });
    const current = within(filter).getAllByRole("link").filter((a) => a.getAttribute("aria-current") === "page");
    expect(current.map((a) => a.textContent)).toEqual(["Deal evidence 5"]);
    expect(within(filter).getByRole("link", { name: "All 35" }).getAttribute("href")).toBe("/evals#checks-h");
    expect(document.querySelectorAll("details.family")).toHaveLength(27);
  });

  it("reads Not measured in the grader stats when no report exists", () => {
    const { container } = render(<EvalOverview overview={buildOverview(contracts, null)} />);
    expect([...container.querySelectorAll("#job-system .stat-value")].map((s) => s.textContent)).toEqual(["Not measured", "Not measured"]);
  });
});
