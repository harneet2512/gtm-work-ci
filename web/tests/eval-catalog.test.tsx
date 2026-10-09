// @vitest-environment jsdom
// The eval catalog with the recorded judge snapshot: judge quality sits in the System job with its date, never as a
// live reading and never with the benchmark's internals; the evidence-class filter narrows the drafted-action checks.
import path from "node:path";
import { cleanup, render, screen, within } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";
import { EvalOverview } from "@/components/evals/EvalOverview";
import { loadQualityReport } from "@/lib/evals/quality-report";
import { buildOverview, loadEvalContracts } from "@/lib/evals/registry";
import { CONTRACTS_DIR } from "./contract-validator";

afterEach(cleanup);
const contracts = loadEvalContracts(CONTRACTS_DIR);
// The committed snapshot was measured on the retired invented-account gold; it is read here as number mechanics only.
const report = loadQualityReport(path.resolve(CONTRACTS_DIR, "../bench/reports/judges-2026-10-03-goldv2-deepseek-v4-flash-t3.json"), { includeRetired: true })!;
const measured = buildOverview(contracts, report.byType, report.source);

describe("the catalog with the recorded judge snapshot", () => {
  it("keeps the three eval jobs visibly separate, each a link to its section", () => {
    render(<EvalOverview overview={measured} />);
    const nav = screen.getByRole("navigation", { name: "The three eval jobs" });
    const tiles = within(nav).getAllByRole("link");
    expect(tiles.map((a) => a.getAttribute("href"))).toEqual(["#job-intelligence", "#job-decision", "#job-system"]);
    expect(tiles[0]!.textContent).toMatch(/^1Intelligence-building/);
    expect(tiles[1]!.textContent).toContain("plus 35 checks on every drafted action");
    expect(tiles[2]!.textContent).toMatch(/^3System/);
    const intelligence = document.getElementById("job-intelligence")!;
    expect([...intelligence.querySelectorAll("details.family")].map((d) => d.id)).toEqual(["family-E1", "family-E2", "family-E3", "family-E4", "family-E5", "family-E6", "family-E7"]);
    expect(document.getElementById("job-decision")!.querySelectorAll("details.family")).toHaveLength(10);
  });

  it("keeps system metrics secondary: collapsed, with judge quality inside", () => {
    const { container } = render(<EvalOverview overview={measured} />);
    const system = document.getElementById("job-system") as HTMLDetailsElement;
    expect(system.tagName).toBe("DETAILS");
    expect(system.open).toBe(false);
    expect(system.querySelectorAll("details.family")).toHaveLength(5);
    expect(system.querySelector("#judge-quality-h")).not.toBeNull();
    const stats = [...container.querySelectorAll("#job-system .stat")].map((s) => s.textContent);
    expect(stats[0]).toContain("90.8%");
    expect(stats[0]).toContain("945 judgments");
    expect(stats[1]).toContain("9.2% · 14%");
  });

  it("says the numbers are a recorded measurement with its date, and carries none of the benchmark's internals", () => {
    render(<EvalOverview overview={measured} />);
    const quality = screen.getByRole("region", { name: "Judge quality" });
    expect(quality.textContent).toMatch(/Recorded Oct 3, 2026/);
    expect(quality.textContent).toMatch(/recorded measurement, not a live reading/);
    expect(quality.textContent).not.toMatch(/deepseek|qwen|gold|trials|bench|\.json|single-author|invented/i);
  });

  it("shows each judged check's agreement and what a wrong verdict costs in the System job; rule checks are absent from it", () => {
    render(<EvalOverview overview={measured} />);
    const cta = document.getElementById("judge-cta_calibration")!;
    expect(within(cta).getAllByRole("cell").map((c) => c.textContent)).toEqual(["99%", "0%", "26%", "84"]);
    expect(within(cta).getByText("84").getAttribute("title")).toBe("Recorded Oct 3, 2026");
    const eb = document.getElementById("judge-economic_buyer_coverage")!;
    expect(within(eb).getAllByRole("cell").map((c) => c.textContent)).toEqual(["79%", "n/a", "0%", "14"]);
    expect(document.getElementById("judge-pricing_integrity")).toBeNull();
    expect(document.querySelectorAll(".judge-table tbody tr")).toHaveLength(25);
  });

  it("narrows the checks to one evidence class, the filter saying which", () => {
    render(<EvalOverview overview={measured} activeClass="deal_data" />);
    const tables = screen.getAllByRole("table").filter((t) => t.classList.contains("check-table"));
    expect(tables).toHaveLength(1);
    expect(tables[0]!.querySelector("caption .evidence-tag")!.textContent).toBe("Deal evidence");
    const filter = screen.getByRole("navigation", { name: "Filter checks by how proven they are" });
    const current = within(filter).getAllByRole("link").filter((a) => a.getAttribute("aria-current") === "page");
    expect(current.map((a) => a.textContent)).toEqual(["Deal evidence 5"]);
    expect(within(filter).getByRole("link", { name: "All 35" }).getAttribute("href")).toBe("/evals?view=catalog#checks-h");
    expect(document.querySelectorAll("details.family")).toHaveLength(22);
  });

  it("reads Not measured in the judge stats when nothing was recorded", () => {
    const { container } = render(<EvalOverview overview={buildOverview(contracts, null)} />);
    expect([...container.querySelectorAll("#job-system .stat-value")].map((s) => s.textContent)).toEqual(["Not measured", "Not measured"]);
    expect(container.querySelector(".judge-table")).toBeNull();
  });

  it("never names a ticket, an arm comparison or the hidden evals", () => {
    render(<EvalOverview overview={measured} />);
    expect(document.body.textContent).not.toMatch(/HAR-\d+|B over A|C equals A|negative transfer|E16\.5|E22\.4/i);
  });
});
