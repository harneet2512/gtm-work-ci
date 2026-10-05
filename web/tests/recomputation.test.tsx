// @vitest-environment jsdom
// The edit recomputation view (HAR-145): what each edit invalidated, re-evaluated, did not recompute and preserved,
// exactly as GET /runs/{id}/recomputation derives it; the web infers nothing from the edit text.
import { cleanup, render, screen, within } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";
import { Recomputation } from "@/components/run/Recomputation";
import type { DependencyInvalidation } from "@/lib/api/types";
import { buildRecomputationView } from "@/lib/view/recomputation";
import { loadExample } from "./contract-validator";

afterEach(cleanup);

const example = loadExample<DependencyInvalidation>("dependency_invalidation");
const doc = (over: Partial<DependencyInvalidation> = {}): DependencyInvalidation => ({ ...example, ...over });

describe("buildRecomputationView", () => {
  it("builds one entry per edit with the four buckets, in the contract's words", () => {
    const v = buildRecomputationView(doc());
    expect(v.entries).toHaveLength(1);
    const e = v.entries[0]!;
    expect(e.edit).toBe("recipients: recipient removed");
    expect(e.invalidated).toHaveLength(5);
    expect(e.reevaluated).toHaveLength(2);
    expect(e.notRecomputed).toHaveLength(3);
    expect(e.preserved).toHaveLength(2);
    expect(v.headline).toContain("re-evaluated at send time");
  });

  it("names an eval by its plain name and says whether its verdict is the old or the new one", () => {
    const e = buildRecomputationView(doc()).entries[0]!;
    expect(e.invalidated.find((r) => r.kind === "eval_result")!.verdict).toBe("was pass");
    expect(e.reevaluated.find((r) => r.kind === "eval_result")!.verdict).toBe("now pass");
    expect(e.preserved.find((r) => r.kind === "eval_result")!.verdict).toBe("still warn");
    expect(e.invalidated.find((r) => r.kind === "ranking_rationale")!.verdict).toBeNull();
    expect(e.invalidated.find((r) => r.kind === "eval_result")!.label).not.toMatch(/_/);
  });

  it("states account-state preservation as yes, no or unknown, never assumed", () => {
    expect(buildRecomputationView(doc()).state).toEqual({ tone: "yes", text: "Account state preserved (v7, unchanged)" });
    const no = buildRecomputationView(doc({ account_state: { ...example.account_state, version_after: 8, preserved: false } })).state;
    expect(no).toEqual({ tone: "no", text: "Account state was not preserved (v7 → v8)" });
    const unknown = buildRecomputationView(doc({ account_state: { ...example.account_state, version_after: null, preserved: null } })).state;
    expect(unknown).toEqual({ tone: "unknown", text: "Whether account state was preserved is not known" });
  });

  it.each([
    ["not_decided", "No decision has been made yet, so nothing was edited."],
    ["unedited", "The chosen action was not edited."],
    ["discarded", "The action was discarded."],
    ["edits_pending", "re-evaluated when the human sends"],
    ["recomputation_unavailable", "could not be established"],
  ] as const)("words the %s status", (status, text) => {
    const v = buildRecomputationView(doc({ status, entries: status === "edits_pending" || status === "recomputation_unavailable" ? example.entries : [], edited: status === "edits_pending" || status === "recomputation_unavailable" }));
    expect(v.headline).toContain(text);
  });
});

describe("Recomputation", () => {
  it("shows each edit with invalidated, re-evaluated, not recomputed and preserved side by side", () => {
    render(<Recomputation recomputation={example} />);
    const entry = screen.getByRole("group", { name: /Edit 1/ });
    for (const name of ["Invalidated", "Re-evaluated", "Not recomputed", "Preserved"]) expect(within(entry).getByRole("heading", { name })).toBeTruthy();
    expect(within(entry).getAllByText(/Why this candidate ranked 2/).length).toBeGreaterThan(0);
    expect(screen.getByText("Account state preserved (v7, unchanged)")).toBeTruthy();
  });

  it("shows a state that is not proven preserved as such", () => {
    render(<Recomputation recomputation={{ ...example, account_state: { ...example.account_state, preserved: null, version_after: null } }} />);
    expect(screen.getByText("Whether account state was preserved is not known")).toBeTruthy();
  });

  it("shows only the headline for a run with no edit", () => {
    render(<Recomputation recomputation={{ ...example, status: "unedited", edited: false, entries: [], semantic_labels: [], preserved_overall: [] }} />);
    expect(screen.getByText("The chosen action was not edited.")).toBeTruthy();
    expect(screen.queryByRole("group")).toBeNull();
  });

  it("says nothing is recomputed from an unreadable result: backend unavailable", () => {
    render(<Recomputation recomputation={null} unavailable />);
    expect(screen.getByText(/backend unavailable/)).toBeTruthy();
  });

  it("renders nothing for a run with no decision episode (null, not unavailable)", () => {
    const { container } = render(<Recomputation recomputation={null} />);
    expect(container.textContent).toBe("");
  });
});
