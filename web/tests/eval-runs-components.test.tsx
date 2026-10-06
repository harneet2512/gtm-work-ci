// @vitest-environment jsdom
// The evals explorer and the operator comparison as rendered (HAR-145): real counts, "not measured" areas, deltas labelled
// "previous episode", outage vs. empty, and every comparison outcome in words (inconclusive and not comparable included).
import { cleanup, render, screen, within } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";
import { EvalRuns } from "@/components/evals/EvalRuns";
import { RunCompare } from "@/components/evals/RunCompare";
import type { EvalFamilySummary, EvalRun, EvalRunComparison } from "@/lib/api/types";
import type { ComparisonData, EvalRunsData } from "@/lib/load-eval-runs";
import { loadExample } from "./contract-validator";

afterEach(cleanup);

const run = loadExample<EvalRun>("eval_run");
const families = loadExample<EvalFamilySummary>("eval_family_summary");
const comparison = loadExample<EvalRunComparison>("eval_run_comparison");
const data = (over: Partial<EvalRunsData> = {}): EvalRunsData => ({ runs: [run], nextCursor: null, unavailable: false, badCursor: false, ...over });

describe("EvalRuns", () => {
  it("lists each run with the four areas' real counts and says not measured where nothing was recorded", () => {
    render(<EvalRuns data={data()} selectedId={null} families={null} />);
    const row = screen.getAllByRole("row")[1]!;
    expect(within(row).getByText("Acme Corp")).toBeTruthy();
    expect(within(row).getByText("2 results")).toBeTruthy();
    expect(within(row).getAllByText("not measured").length).toBeGreaterThan(0); // an area with no results
    expect(row.textContent).toContain("1 pass · 0 warn · 0 fail · 0 unknown");
    expect(within(row).getByRole("link").getAttribute("href")).toBe(`/evals?view=results&run=${run.id}`);
    expect(screen.getByText("Select a run to see its areas, families and eval types.")).toBeTruthy();
  });

  it("shows a check mark only for an area whose counts are all passes", () => {
    render(<EvalRuns data={data()} selectedId={null} families={null} />);
    const marks = [...document.querySelectorAll(".area-chip")].map((c) => c.textContent);
    expect(marks[0]).toBe("· no signal: not measured"); // intelligence: not measured, no mark of health
    expect(marks[1]).toContain("✓");
    expect(marks[2]).toContain("!"); // a warn
  });

  it("links older runs by cursor", () => {
    render(<EvalRuns data={data({ nextCursor: "a b" })} selectedId={null} families={null} />);
    expect(screen.getByRole("link", { name: "Older runs" }).getAttribute("href")).toBe("/evals?view=results&cursor=a%20b");
  });

  it("reads an unreadable list as backend unavailable, never as 'no eval run'", () => {
    render(<EvalRuns data={data({ runs: [], unavailable: true })} selectedId={null} families={null} />);
    expect(screen.getByText(/Backend unavailable: the eval runs could not be read/)).toBeTruthy();
    expect(screen.getByText(/not an eval failure/)).toBeTruthy();
    expect(screen.queryByText("No eval run has been recorded yet.")).toBeNull();
  });

  it("says plainly when there are no runs, and when the cursor was refused", () => {
    render(<EvalRuns data={data({ runs: [] })} selectedId={null} families={null} />);
    expect(screen.getByText("No eval run has been recorded yet.")).toBeTruthy();
    cleanup();
    render(<EvalRuns data={data({ badCursor: true })} selectedId={null} families={null} />);
    expect(screen.getByText(/page marker was not valid/)).toBeTruthy();
  });

  it("opens the selected run's areas, families and eval types, with deltas labelled 'previous episode'", () => {
    render(<EvalRuns data={data()} selectedId={run.id} families={{ summary: families, unavailable: false }} />);
    const decision = screen.getByRole("region", { name: "Decision & Learning results" });
    expect(decision.textContent).toContain("vs previous episode: +1 pass · -1 fail");
    expect(within(decision).getByText("Decision construction")).toBeTruthy();
    expect(within(decision).getByText("Relationship continuity")).toBeTruthy();
    const intelligence = screen.getByRole("region", { name: "Intelligence results" });
    expect(intelligence.textContent).toContain("not measured");
    expect(intelligence.textContent).toContain("It is not a pass.");
    expect(intelligence.textContent).not.toContain("vs previous episode");
    expect(screen.getByRole("link", { name: /This run's evals with their reasons/ }).getAttribute("href")).toBe(`/runs/${run.id}/evals`);
    expect(screen.getByRole("link", { name: "Its episode" }).getAttribute("href")).toBe(`/episodes/${run.decision_episode_id}`);
  });

  it("does not link an episode the run has none of, and handles an unknown or unreadable summary", () => {
    render(<EvalRuns data={data({ runs: [{ ...run, decision_episode_id: null }] })} selectedId={run.id} families={{ summary: null, unavailable: false }} />);
    expect(screen.queryByRole("link", { name: "Its episode" })).toBeNull();
    expect(screen.getByText("That run has no recorded eval results.")).toBeTruthy();
    cleanup();
    render(<EvalRuns data={data()} selectedId={run.id} families={{ summary: null, unavailable: true }} />);
    expect(screen.getByText(/this run's families could not be read/)).toBeTruthy();
  });

  it("never prints a percentage or a score", () => {
    render(<EvalRuns data={data()} selectedId={run.id} families={{ summary: families, unavailable: false }} />);
    expect(document.body.textContent).not.toMatch(/%|score/i);
  });
});

const runs = [run, { ...run, id: "0f0a0000-0000-4000-8000-000000000600" }];
const view = (state: ComparisonData) => render(<RunCompare data={state} runs={runs} a={null} b={null} />);

describe("RunCompare", () => {
  it("is labelled an operator view and offers a picker of two runs", () => {
    view({ state: "idle" });
    expect(screen.getByText(/Operator view/)).toBeTruthy();
    expect(screen.getByLabelText("Run A")).toBeTruthy();
    expect(screen.getByLabelText("Run B")).toBeTruthy();
    expect(screen.getAllByRole("option").filter((o) => (o as HTMLOptionElement).value !== "")).toHaveLength(4);
    expect(document.querySelector("form")!.getAttribute("action")).toBe("/evals");
    expect((document.querySelector("input[name=view]") as HTMLInputElement).value).toBe("compare");
  });

  it("asks for two runs when fewer than two exist", () => {
    render(<RunCompare data={{ state: "idle" }} runs={[run]} a={null} b={null} />);
    expect(screen.getByText("Two runs with eval results are needed to compare; 1 found.")).toBeTruthy();
    expect(screen.queryByLabelText("Run A")).toBeNull();
  });

  it("shows each side, the whole-episode change and one row per eval with the change in words", () => {
    view({ state: "compared", comparison });
    expect(screen.getByText("improved", { selector: "strong" })).toBeTruthy();
    const rows = screen.getAllByRole("row").slice(1);
    expect(rows[0]!.textContent).toContain("Relationship continuity");
    expect(rows[0]!.textContent).toContain("✗ fail · blocks send");
    expect(rows[0]!.textContent).toContain("✓ pass");
    expect(rows[1]!.textContent).toContain("unchanged");
    expect(screen.getAllByText("Run A").length).toBeGreaterThan(0);
  });

  it("renders an inconclusive change as inconclusive with its explanation, not as an improvement", () => {
    const inconclusive: EvalRunComparison = {
      ...comparison,
      rows: [{ ...comparison.rows[0]!, change: "inconclusive", a: "unknown", b: "pass", a_blocking: false }],
      overall: { ...comparison.overall, change: "inconclusive", improved: 0, inconclusive: 1, unchanged: 0 },
    };
    view({ state: "compared", comparison: inconclusive });
    expect(screen.getByText("inconclusive", { selector: "strong" })).toBeTruthy();
    expect(screen.getByText(/could not be judged either way/)).toBeTruthy();
    expect(screen.getAllByRole("row")[1]!.textContent).toContain("◌ unknown");
    expect(document.querySelector(".compare-overall")!.className).toContain("cmp-inconclusive");
  });

  it.each([
    ["not_comparable", /not triggered by the same event, so they cannot be compared/],
    ["not_found", /does not exist/],
    ["invalid", /Choose two runs to compare/],
    ["unavailable", /Backend unavailable: the comparison could not be read\. This is not an eval failure/],
  ] as const)("explains %s in words", (state, text) => {
    view({ state });
    expect(screen.getByRole("status").textContent).toMatch(text);
    expect(screen.queryByRole("table")).toBeNull();
  });
});
