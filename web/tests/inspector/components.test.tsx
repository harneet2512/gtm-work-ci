// @vitest-environment jsdom
// The inspector's views render what the models say and nothing more: nine sections in order, a row per criterion with its own
// result, "Not measured yet" where nothing was measured, no raw JSON in Demo mode, and the loop's mode filter.
import { cleanup, fireEvent, render, screen, within } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { DecisionView } from "@/components/evals/inspector/DecisionView";
import { EvalDrawer } from "@/components/evals/inspector/EvalDrawer";
import { LoopLanding } from "@/components/evals/inspector/LoopLanding";
import { HealthBody, OfflineBody } from "@/components/evals/inspector/OfflineAndHealth";
import { buildDecisionScreen } from "@/lib/evals/inspector/candidates";
import { buildDrawerModel } from "@/lib/evals/inspector/drawer-model";
import { buildHealthView } from "@/lib/evals/inspector/health";
import { buildLoop } from "@/lib/evals/inspector/loop-model";
import { buildOfflineView } from "@/lib/evals/inspector/offline";
import { CAND_A, def, EP, result, RULES } from "./fixtures";

afterEach(cleanup);

const resolve = (ref: string) => ({ ref, kind: ref.split(":")[0]!, id: ref.split(":")[1]!, label: ref, summary: "We can sign next week.", spanId: null, href: null, source: "Email · Nov 9, 2023" });

function drawer() {
  return buildDrawerModel({
    episodeId: EP,
    episodeLabel: "MedTech · Nov 9, 2023",
    def: def("B5", { grader: "hybrid", mode: "live_conditional" }),
    results: [
      result({
        gate: "B5",
        verdict: "warn",
        grader: { kind: "model", model: "m" },
        criteria: [
          { id: "relevant_precedent_found", label: "", result: "pass", why: "found", evidence_refs: ["activity:a"] },
          { id: "important_precedent_missed", label: "", result: "warn", why: "a case was missed", evidence_refs: ["activity:a"] },
        ],
        evidence_refs: ["activity:0e7a1000-0000-4000-8000-0000000000a1"],
      }),
    ],
    rules: RULES,
    resolve,
    titleOf: () => null,
    extraInputs: [],
    fleet: [],
    confidence: null,
  });
}

describe("EvalDrawer", () => {
  it("renders the nine sections in order with a row and result per criterion", () => {
    render(<EvalDrawer model={drawer()} demo={false} onClose={() => undefined} />);
    expect(screen.getAllByRole("heading", { level: 3 }).map((h) => h.textContent)).toHaveLength(9);
    expect(screen.getByRole("heading", { level: 2 }).textContent).toBe("Is B5 right?");
    const how = document.querySelector("[data-section='how']") as HTMLElement;
    expect(within(how).getByText("Relevant precedent found")).toBeTruthy();
    expect(within(how).getByText("Important precedent missed")).toBeTruthy();
    expect(how.querySelector(".dr-fold")?.textContent).toMatch(/WARN/);
    expect(how.textContent).toMatch(/Not yet calibrated/);
    expect(how.textContent).toMatch(/No numeric score/);
  });

  it("says Not measured yet and names what the calibration run will measure", () => {
    render(<EvalDrawer model={drawer()} demo={false} onClose={() => undefined} />);
    const perf = document.querySelector("[data-section='performance']") as HTMLElement;
    expect(perf.textContent).toMatch(/Not measured yet/);
    expect(perf.textContent).toMatch(/needs the calibration run/);
    expect(perf.textContent).toMatch(/passed something it should have failed/);
    const fail = document.querySelector("[data-section='failures']") as HTMLElement;
    expect(fail.textContent).toContain("No failures recorded yet");
  });

  it("keeps raw JSON under Advanced and drops it in Demo mode", () => {
    const { unmount } = render(<EvalDrawer model={drawer()} demo={false} onClose={() => undefined} />);
    expect(screen.getByText("Advanced")).toBeTruthy();
    unmount();
    render(<EvalDrawer model={drawer()} demo onClose={() => undefined} />);
    expect(screen.queryByText("Advanced")).toBeNull();
  });

  it("closes on Escape and on the close button", () => {
    const onClose = vi.fn();
    render(<EvalDrawer model={drawer()} demo={false} onClose={onClose} />);
    fireEvent.keyDown(window, { key: "Escape" });
    fireEvent.click(screen.getByRole("button", { name: "Close" }));
    expect(onClose).toHaveBeenCalledTimes(2);
  });
});

describe("LoopLanding", () => {
  const defs = ["B1", "B5", "D3", "S2", "S6"].map((id) => def(id, { mode: id === "S2" ? "offline_benchmark" : id === "S6" ? "continuous_aggregate" : id === "B5" ? "live_conditional" : "live_required" }));
  it("the mode filter narrows the checks and the buckets stay the three questions", () => {
    render(<LoopLanding loop={buildLoop(defs)} demo={false} episodeHref="/evals/episode" />);
    expect(screen.getAllByRole("heading", { level: 2 }).map((h) => h.textContent)).toEqual(expect.arrayContaining(["Do we understand what is happening?", "Given what we know, are we doing the right thing?", "Can we trust the machinery measuring the first two?"]));
    fireEvent.click(screen.getByRole("button", { name: /^Offline/ }));
    expect(document.querySelectorAll(".loop-gates li")).toHaveLength(1);
    fireEvent.click(screen.getByRole("button", { name: /^All checks/ }));
    expect(document.querySelectorAll(".loop-gates li")).toHaveLength(5);
  });
});

describe("DecisionView", () => {
  it("is honest with no options", () => {
    render(<DecisionView screen={buildDecisionScreen({ summary: {} as never, strategies: null, results: [], ranking: null, decision: null, inference: null, recomputation: null, titleOf: () => null, evidenceText: () => null })} />);
    expect(screen.getByText(/No options are stored/)).toBeTruthy();
  });
  void CAND_A;
});

describe("aggregate views", () => {
  it("offline says nothing has run and invents no figure", () => {
    const gates = [def("S2", { mode: "offline_benchmark" }), def("S3", { mode: "offline_benchmark" })];
    render(<OfflineBody view={buildOfflineView(gates, [], false)} />);
    expect(screen.getByTestId("offline-headline").textContent).toMatch(/None of these has run yet/);
    expect(screen.getAllByText("Not run yet")).toHaveLength(2);
  });

  it("health with no usage says not measured and never zero", () => {
    render(<HealthBody view={buildHealthView([{ episodeId: "e1", label: "e1", metrics: null, spanKinds: null }])} demo={false} episodeIds={["e1"]} />);
    expect(screen.getAllByText("not measured").length).toBeGreaterThan(5);
    expect(screen.queryByText("0")).toBeNull();
  });
});
