// @vitest-environment jsdom
// Bucket 2 on the evals page: D1 to D10 in flow order, each row with the six answers, "not measured" for a gate with no
// stored result, and never a pass without evidence.
import { cleanup, render, screen, within } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";
import { DecisionLearningView } from "@/components/evals/DecisionLearningView";
import { buildBucket2, readVerdict, traceHref, type GateResultRow } from "@/lib/evals/bucket2-results";
import { buildOverview, loadEvalContracts } from "@/lib/evals/registry";
import { CONTRACTS_DIR } from "./contract-validator";

afterEach(cleanup);
const overview = buildOverview(loadEvalContracts(CONTRACTS_DIR), null);
const EP = "0e7a1000-0000-4000-8000-0000000000e1";

function row(over: Partial<GateResultRow> = {}): GateResultRow {
  return {
    gate: "D4",
    judged_object: { type: "HumanStrategyDecision", id: "d1" },
    span_id: "human_interaction:d1",
    verdict: "pass",
    question: "What does the person's choice tell us about gtm_ai's decision?",
    observed: "the person chose gtm_ai's preferred candidate",
    why: "agreement confirms the preference",
    evidence_refs: ["candidate:a"],
    improves: "Turns a click into structured supervision.",
    grader: { kind: "deterministic" },
    calibrated: false,
    ...over,
  };
}

describe("readVerdict", () => {
  it("reads abstain, unknown and anything unrecognised as unknown, never a pass", () => {
    for (const v of ["abstain", "unknown", "", "not_relevant", "PASSED", "excellent"]) expect(readVerdict(v, ["x"])).toBe("unknown");
  });
  it("a pass, warn or fail with no evidence is unknown (rule R1)", () => {
    for (const v of ["pass", "warn", "fail"]) expect(readVerdict(v, [])).toBe("unknown");
    expect(readVerdict("pass", ["x"])).toBe("pass");
    expect(readVerdict(" FAIL ", ["x"])).toBe("fail");
  });
});

describe("buildBucket2", () => {
  const gates = [
    { id: "D2", name: "two", question: "q2", improves: "i2" },
    { id: "D10", name: "ten", question: "q10", improves: "i10" },
    { id: "D1", name: "one", question: "q1", improves: "i1" },
    { id: "B1", name: "not bucket 2", question: "q", improves: "i" },
  ];
  it("orders D1 to D10 and drops other gates", () => {
    expect(buildBucket2(gates, [], null).map((g) => g.id)).toEqual(["D1", "D2", "D10"]);
  });
  it("leaves its inputs untouched and keeps unmeasured gates empty", () => {
    const results = [row({ gate: "D2" })];
    const snap = JSON.stringify([gates, results]);
    const out = buildBucket2(gates, results, EP);
    expect(JSON.stringify([gates, results])).toBe(snap);
    expect(out.find((g) => g.id === "D1")!.rows).toEqual([]);
    expect(out.find((g) => g.id === "D2")!.rows).toHaveLength(1);
  });
  it("links a row to its trace span only when the span id is well formed", () => {
    expect(traceHref(EP, "human_interaction:d1")).toBe(`/episodes/${EP}#span-human_interaction%3Ad1`);
    expect(traceHref(null, "human_interaction:d1")).toBeNull();
    expect(traceHref(EP, "bad span")).toBeNull();
  });
});

describe("Bucket 2 on the evals page", () => {
  it("shows every Bucket 2 gate in flow order as Not measured when nothing is stored", () => {
    render(<DecisionLearningView buckets={overview.buckets} />);
    const section = document.getElementById("bucket-decision_action")!;
    const ids = Array.from(section.querySelectorAll("li.gate-row")).map((e) => e.id);
    expect(ids).toEqual(["D1", "D2", "D3", "D4", "D5", "D6", "D7", "D8", "D9", "D10"].map((g) => `gate-${g}`));
    for (const li of Array.from(section.querySelectorAll("li.gate-row"))) {
      // D6 and D10 have no eval registered under them yet, which the registry flags; every other gate has stored-result rows to come.
      const text = li.id === "gate-D6" || li.id === "gate-D10" ? "Not built yet" : "Not measured";
      expect(within(li as HTMLElement).getByText(text)).toBeTruthy();
      expect(li.textContent).not.toMatch(/\bPass\b/);
    }
  });

  it("renders the six answers and the calibration note for a stored result", () => {
    render(<DecisionLearningView buckets={overview.buckets} bucket2Results={[row()]} episodeId={EP} />);
    const d4 = document.getElementById("gate-D4")!;
    for (const label of ["Question asked", "What we observed", "Verdict", "Why", "Trace evidence", "Improves or protects", "Calibration"]) {
      expect(within(d4).getByText(label)).toBeTruthy();
    }
    expect(within(d4).getByText("Pass")).toBeTruthy();
    expect(within(d4).getByText("Not yet calibrated")).toBeTruthy();
    expect(within(d4).getByRole("link", { name: "View in the episode trace" }).getAttribute("href")).toContain(`/episodes/${EP}#span-`);
    expect(screen.queryByText(/Ghost/)).toBeNull();
  });

  it("shows a pass that cites no evidence as unknown", () => {
    render(<DecisionLearningView buckets={overview.buckets} bucket2Results={[row({ evidence_refs: [] })]} episodeId={EP} />);
    const d4 = document.getElementById("gate-D4")!;
    expect(within(d4).getByText("Unknown")).toBeTruthy();
    expect(within(d4).queryByText("Pass")).toBeNull();
  });
});
