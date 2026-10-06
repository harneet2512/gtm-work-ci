// @vitest-environment jsdom
// The gate results page body: unavailable is not a FAIL, rows render with icon + word verdicts, a row opens the inspector,
// chips and views narrow the table, Escape closes the pane, and a missing result reads "not measured".
import { cleanup, fireEvent, render, screen, within } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

vi.mock("next/link", () => ({ default: ({ href, children, ...p }: { href: string; children: React.ReactNode }) => <a href={href} {...p}>{children}</a> }));
import { GatesView } from "@/components/evals/gates/GatesView";
import { buildGateRows, type GateCatalogEntry } from "@/lib/evals/gate-table";
import type { GateTableData } from "@/lib/evals/load-gate-table";

afterEach(cleanup);

const CATALOG: GateCatalogEntry[] = [
  { id: "B1", question: "Did gtm_ai understand the event?", improves: "Protects state." },
  { id: "D2", question: "Are the actions plausible?", improves: "Real options." },
];
const result = (gate: string, verdict: string, why: string) =>
  ({ id: `0e9e0000-0000-4000-8000-0000000d00${gate.length}${verdict.length}`, gate, sub_gate: "", label: "", judged_object: { type: "X", id: gate }, span_id: "candidates:s1", verdict, question: `${gate}?`, observed: `${gate} observed`, why, evidence_refs: ["candidate:c1"], improves: "p", grader: { kind: gate === "D2" ? "model" : "deterministic", model: "m1" }, calibrated: false }) as never;

function data(): GateTableData {
  const rows = buildGateRows(
    [{ episode: { id: "e1", label: "MedTech", accountId: "a" }, results: [result("D2", "warn", "ask too strong")], spanTimes: new Map(), resolve: (ref) => ({ ref, kind: "candidate", id: "c1", label: "option c1", summary: null, spanId: "candidates:s1", href: "/episodes/e1?mode=trace&span=candidates%3As1" }) }],
    CATALOG,
  );
  return { status: "ok", rows, paths: { e1: [{ id: "candidates:s1", seq: 1, title: "Candidates", time: null }] }, unreadable: [] };
}

describe("GatesView", () => {
  it("says the backend is unavailable without showing any verdict", () => {
    render(<GatesView data={{ status: "unavailable", rows: [], paths: {}, unreadable: [] }} gates={["B1"]} />);
    expect(screen.getByRole("alert").textContent).toMatch(/unavailable/);
    expect(screen.queryByText("FAIL")).toBeNull();
  });

  it("renders measured rows with word verdicts and missing gates as not measured", () => {
    render(<GatesView data={data()} gates={["B1", "D2"]} />);
    expect(screen.getAllByRole("row").slice(1).map((r) => r.textContent).join(" ")).not.toContain("NOT MEASURED");
    fireEvent.click(screen.getByRole("button", { name: /gates with no result yet/ }));
    const rows = screen.getAllByRole("row").slice(1);
    expect(rows[0]!.textContent).toContain("WARN");
    expect(rows[1]!.textContent).toContain("NOT MEASURED");
    expect(screen.getByRole("list", { name: "Score summary by bucket" }).textContent).not.toContain("first run"); // no run history is stored, so no claim about it
  });

  it("opens the inspector on Enter, shows evidence with its trace link, and closes on Escape", () => {
    render(<GatesView data={data()} gates={["B1", "D2"]} />);
    const row = screen.getAllByRole("row")[1]!;
    fireEvent.keyDown(row, { key: "Enter" });
    const pane = screen.getByRole("complementary", { name: "Inspector for D2" });
    fireEvent.click(within(pane).getByRole("tab", { name: /Evidence/ }));
    expect(within(pane).getByRole("link", { name: /in the trace/ }).getAttribute("href")).toContain("mode=trace&span=");
    fireEvent.click(within(pane).getByRole("tab", { name: "Grader" }));
    expect(pane.textContent).toContain("not yet calibrated");
    fireEvent.keyDown(window, { key: "Escape" });
    expect(screen.queryByRole("complementary", { name: /Inspector for/ })).toBeNull();
  });

  it("narrows by the Not measured chip and by saved view", () => {
    render(<GatesView data={data()} gates={["B1", "D2"]} />);
    fireEvent.click(screen.getByRole("button", { name: "Not measured" }));
    expect(screen.getAllByRole("row").slice(1).every((r) => r.textContent!.includes("NOT MEASURED"))).toBe(true);
    fireEvent.click(screen.getByRole("button", { name: "Failures & warnings" }));
    expect(screen.getAllByRole("row")[1]!.textContent).toContain("WARN");
  });

  it("hides a column from the Display menu and says when nothing matches", () => {
    render(<GatesView data={data()} gates={["B1", "D2"]} />);
    fireEvent.click(screen.getByLabelText("Why"));
    expect(screen.queryByRole("columnheader", { name: "Why" })).toBeNull();
    fireEvent.change(screen.getByRole("searchbox", { name: "Search gate results" }), { target: { value: "zzzz" } });
    expect(screen.getByText("No result matches these filters.")).toBeTruthy();
  });

  it("reports episodes that could not be read as a notice, not a failure", () => {
    render(<GatesView data={{ ...data(), unreadable: ["x"] }} gates={["B1"]} />);
    expect(screen.getAllByRole("status").map((n) => n.textContent).join(" ")).toMatch(/not shown as failures/);
  });
});
