// @vitest-environment jsdom
// The explorer table and run comparison (HAR-145): filters and sorts are client-local, selection writes
// ?result= to the URL, and a run that never produced a check shows "not checked", not a pass.
import { cleanup, fireEvent, render, screen, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { AgentRun, HumanStrategyDecision, RunStrategies } from "@/lib/api/types";
import type { ExplorerData } from "@/lib/load-eval-results";
import { flattenResults } from "@/lib/view/evals-explorer";
import { loadFixture } from "./contract-validator";

const nav = vi.hoisted(() => ({ replace: vi.fn(), search: "view=results" }));
vi.mock("next/navigation", () => ({
  useRouter: () => ({ replace: nav.replace }),
  usePathname: () => "/evals",
  useSearchParams: () => new URLSearchParams(nav.search),
}));

const { Explorer } = await import("@/components/evals/Explorer");
const { RunCompare } = await import("@/components/evals/RunCompare");

const run = loadFixture<AgentRun>("medtech.agent-run.json");
const strategies = loadFixture<RunStrategies>("medtech.run-strategies.json");
const decision = loadFixture<HumanStrategyDecision>("medtech.strategy-decision.json");
const acme = loadFixture<RunStrategies>("acme.run-strategies.json");
const acmeRun: AgentRun = { ...run, id: "0f0a0000-0000-4000-8000-000000000602" };
const rows = flattenResults([
  { run, strategies, decision },
  { run: acmeRun, strategies: acme, decision: null },
]);

beforeEach(() => {
  nav.replace.mockClear();
  nav.search = "view=results";
});
afterEach(cleanup);

const bodyRows = () => document.querySelectorAll("tbody tr");
const counter = () => document.querySelector(".count")!.textContent;

describe("Explorer", () => {
  it("lists every row and counts them", () => {
    render(<Explorer rows={rows} selected={null} />);
    expect(counter()).toBe(`${rows.length} of ${rows.length} results`);
    expect(bodyRows()).toHaveLength(rows.length);
  });

  it("starts worst-first: the first row is a worst-severity verdict", () => {
    render(<Explorer rows={rows} selected={null} />);
    const order = ["fail", "warn", "abstain", "pass", "not_relevant"];
    const verdicts = [...bodyRows()].map((tr) => order.findIndex((v) => tr.classList.contains(`st-${v}`)));
    expect([...verdicts].sort((a, b) => a - b)).toEqual(verdicts);
  });

  it("narrows by search text and says when nothing matches", () => {
    render(<Explorer rows={rows} selected={null} />);
    const box = screen.getByLabelText("Search eval results");
    fireEvent.change(box, { target: { value: "zzz-no-such-eval" } });
    expect(counter()).toBe(`0 of ${rows.length} results`);
    expect(document.querySelector("td.empty")!.textContent).toContain("No eval results match");
    fireEvent.change(box, { target: { value: "" } });
    expect(bodyRows()).toHaveLength(rows.length);
  });

  it("filters by verdict and toggles the filter off on a second click", () => {
    render(<Explorer rows={rows} selected={null} />);
    const pass = within(screen.getByRole("group", { name: "Verdict filter" })).getByRole("button", { name: /pass/ });
    fireEvent.click(pass);
    const passes = rows.filter((r) => r.verdict === "pass").length;
    expect(counter()).toBe(`${passes} of ${rows.length} results`);
    fireEvent.click(pass);
    expect(counter()).toBe(`${rows.length} of ${rows.length} results`);
  });

  it("filters by kind and by 'Blocks send'", () => {
    render(<Explorer rows={rows} selected={null} />);
    const kinds = screen.getByRole("group", { name: "Kind filter" });
    fireEvent.click(within(kinds).getByRole("button", { name: "semantic" }));
    expect(counter()).toBe(`${rows.filter((r) => r.kind === "semantic" || r.evidenceClass === "semantic").length} of ${rows.length} results`);
    fireEvent.click(within(kinds).getByRole("button", { name: "semantic" }));
    fireEvent.click(within(kinds).getByRole("button", { name: "Blocks send" }));
    expect(counter()).toBe(`${rows.filter((r) => r.blocking).length} of ${rows.length} results`);
  });

  it("sorts a column ascending, then descending, and reflects it in aria-sort", () => {
    render(<Explorer rows={rows} selected={null} />);
    const header = screen.getByRole("button", { name: "Eval" }).closest("th")!;
    fireEvent.click(within(header).getByRole("button"));
    expect(header.getAttribute("aria-sort")).toBe("ascending");
    const names = [...document.querySelectorAll("td.ename")].map((td) => td.firstChild!.textContent!);
    expect(names).toEqual([...names].sort((a, b) => a.localeCompare(b)));
    fireEvent.click(within(header).getByRole("button"));
    expect(header.getAttribute("aria-sort")).toBe("descending");
  });

  it("selecting a row writes ?result= alongside the existing params (click and Enter)", () => {
    render(<Explorer rows={rows} selected={null} />);
    const first = bodyRows()[0]!;
    fireEvent.click(first);
    expect(nav.replace).toHaveBeenCalledTimes(1);
    const url = new URL(nav.replace.mock.calls[0]![0] as string, "http://x");
    expect(url.pathname).toBe("/evals");
    expect(url.searchParams.get("view")).toBe("results");
    expect(url.searchParams.get("result")).toBeTruthy();
    fireEvent.keyDown(first, { key: "Tab" });
    expect(nav.replace).toHaveBeenCalledTimes(1);
    fireEvent.keyDown(first, { key: "Enter" });
    expect(nav.replace).toHaveBeenCalledTimes(2);
  });

  it("selects a row with the Space key and keeps the page from scrolling", () => {
    render(<Explorer rows={rows} selected={null} />);
    const first = bodyRows()[0]!;
    const notPrevented = fireEvent.keyDown(first, { key: " " });
    expect(nav.replace).toHaveBeenCalledTimes(1);
    expect(notPrevented).toBe(false);
  });

  it("exposes row selection (aria-selected) and gives every verdict glyph a text word", () => {
    render(<Explorer rows={rows} selected={rows[3]!.resultId} />);
    const trs = [...bodyRows()];
    expect(trs.filter((tr) => tr.getAttribute("aria-selected") === "true")).toHaveLength(1);
    expect(trs.every((tr) => tr.getAttribute("aria-selected") !== null)).toBe(true);
    for (const tr of trs) expect(tr.querySelector("td .sr-only")!.textContent).toMatch(/^(fail|warn|abstain|pass|not relevant|not checked)/);
    cleanup();
    render(<Explorer rows={[{ ...rows[0]!, verdict: "fail", blocking: true }]} selected={null} />);
    expect(bodyRows()[0]!.querySelector("td .sr-only")!.textContent).toBe("fail, blocks send");
  });

  it("reports filter chips as toggle buttons (aria-pressed)", () => {
    render(<Explorer rows={rows} selected={null} />);
    const chip = screen.getByRole("button", { name: /fail/ });
    expect(chip.getAttribute("aria-pressed")).toBe("false");
    fireEvent.click(chip);
    expect(chip.getAttribute("aria-pressed")).toBe("true");
    const blocks = screen.getByRole("button", { name: "Blocks send" });
    expect(blocks.getAttribute("aria-pressed")).toBe("false");
    fireEvent.click(blocks);
    expect(blocks.getAttribute("aria-pressed")).toBe("true");
  });

  it("marks the selected row", () => {
    render(<Explorer rows={rows} selected={rows[3]!.resultId} />);
    expect(document.querySelectorAll("tr.selected")).toHaveLength(1);
  });

  it("shows the Ghost pick and chosen markers and a dash for a missing kind or reason", () => {
    const bare = { ...rows[0]!, resultId: "bare", kind: null, evidenceClass: null, reason: null, createdAt: null, isGhostPick: true, isChosen: true, blocking: true, runId: "short" };
    render(<Explorer rows={[bare]} selected={null} />);
    const cells = [...bodyRows()[0]!.querySelectorAll("td")].map((td) => td.textContent);
    expect(cells[0]).toContain("⛔");
    expect(cells.slice(4)).toEqual(["—", "—", "—", "—"]);
    expect(bodyRows()[0]!.textContent).toContain("★");
  });
});

const data = (over: Partial<ExplorerData> = {}): ExplorerData => ({
  rows,
  byId: new Map(),
  unavailableRuns: 0,
  runs: [
    { id: run.id, episodeId: null, phase: "published", rows: 15 },
    { id: acmeRun.id, episodeId: null, phase: null, rows: 9 },
  ],
  ...over,
});

describe("RunCompare", () => {
  it("needs two runs and says how many it found", () => {
    render(<RunCompare data={data({ runs: [data().runs[0]!] })} a={null} b={null} />);
    expect(screen.getByText(/Two or more runs/).textContent).toContain("1 found");
  });

  it("compares the first two runs by default and counts the differences", () => {
    render(<RunCompare data={data()} a={null} b={null} />);
    expect(document.querySelectorAll(".cmp-side")).toHaveLength(2);
    const total = document.querySelectorAll("tbody tr").length;
    const n = (cls: string) => document.querySelectorAll(`tbody tr.${cls}`).length;
    const other = n("cmp-changed") + n("cmp-one-sided");
    expect(screen.getByText(/eval types/).textContent).toBe(
      `${total} eval types · ${n("cmp-improved")} improved · ${n("cmp-regressed")} regressed · ${other} otherwise differ · ${n("cmp-same")} unchanged`,
    );
  });

  it("labels an improvement and a regression with their own arrow and word", () => {
    const base = rows[0]!;
    const mk = (runId: string, verdict: string, evalType: string) => ({ ...base, runId, resultId: `${runId}-${evalType}`, evalType, name: evalType, verdict }) as (typeof rows)[number];
    const d = { rows: [mk(run.id, "fail", "e_up"), mk(acmeRun.id, "pass", "e_up"), mk(run.id, "pass", "e_down"), mk(acmeRun.id, "warn", "e_down")], byId: new Map(), unavailableRuns: 0, runs: data().runs } as ExplorerData;
    render(<RunCompare data={d} a={run.id} b={acmeRun.id} />);
    expect(document.querySelector("tr.cmp-improved [aria-label='improved']")!.textContent).toBe("↑");
    expect(document.querySelector("tr.cmp-regressed [aria-label='regressed']")!.textContent).toBe("↓");
  });

  it("shows a check only one run produced as 'not checked', never as a pass", () => {
    render(<RunCompare data={data()} a={run.id} b={acmeRun.id} />);
    const oneSided = document.querySelector("tr.cmp-one-sided");
    if (oneSided) {
      expect(oneSided.textContent).toContain("not checked");
      expect(oneSided.querySelector("[aria-label='one-sided']")).not.toBeNull();
    }
  });

  it("falls back to the defaults for ids that are not eligible, and offers pair links", () => {
    render(<RunCompare data={data()} a="not-a-run" b="also-not" />);
    const links = within(screen.getByRole("navigation", { name: "Compare pairs" })).getAllByRole("link");
    expect(links).toHaveLength(1);
    expect(links[0]!.getAttribute("href")).toBe(`/evals?view=compare&runs=${run.id},${acmeRun.id}`);
    expect(links[0]!.className).toContain("on");
  });
});
