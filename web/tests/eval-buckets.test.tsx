// @vitest-environment jsdom
// The evals presented as three buckets and their gates (B1-B9, D1-D10 in decision-flow order, S1-S5), built from the
// registry's `buckets`, `gates` and per-eval `gate`. Each gate shows its question and what it improves; a result is
// "Not measured" (never a pass) and a gate with no eval yet reads "Not built yet". Metrics are not evals.
import { cleanup, render, screen, within } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";
import { DecisionLearningView } from "@/components/evals/DecisionLearningView";
import { EvalOverview } from "@/components/evals/EvalOverview";
import { buildOverview, loadEvalContracts } from "@/lib/evals/registry";
import { CONTRACTS_DIR } from "./contract-validator";

afterEach(cleanup);
const contracts = loadEvalContracts(CONTRACTS_DIR);
const overview = buildOverview(contracts, null);
const bucket = (id: string) => overview.buckets.find((b) => b.id === id)!;

describe("the bucket data", () => {
  it("has the three buckets in order with their gates", () => {
    expect(overview.buckets.map((b) => b.id)).toEqual(["context_intelligence", "decision_action", "system_health"]);
    expect(bucket("context_intelligence").gates.map((g) => g.id)).toEqual(["B1", "B2", "B3", "B4", "B5", "B6", "B7", "B8", "B9"]);
    expect(bucket("decision_action").gates.map((g) => g.id)).toEqual(["D1", "D2", "D3", "D4", "D5", "D6", "D7", "D8", "D9", "D10"]);
    expect(bucket("system_health").gates.map((g) => g.id)).toEqual(["S1", "S2", "S3", "S4", "S5"]);
  });

  it("puts every visible eval under exactly one gate, and counts no metric", () => {
    const listed = overview.buckets.flatMap((b) => b.gates.flatMap((g) => g.evals));
    expect(listed).toHaveLength(overview.totals.registryEvals);
    expect(contracts.registry.metrics.every((m) => m.gate === "S6")).toBe(true);
    expect(overview.buckets.flatMap((b) => b.gates.map((g) => g.id))).not.toContain("S6");
  });

  it("reads D6 and D10 as gates not built yet, and every other gate as built", () => {
    const notBuilt = overview.buckets.flatMap((b) => b.gates).filter((g) => !g.built).map((g) => g.id);
    expect(notBuilt).toEqual(["D6", "D10"]);
  });

  it("describes an eval in plain words under its gate", () => {
    const e = bucket("decision_action").gates.find((g) => g.id === "D3")!.evals.find((x) => x.name === "Ranking supported by evidence")!;
    expect(e).toMatchObject({ judges: "the order of the options", status: "Planned", grader: "Not built yet", result: "Not measured" });
    expect(e.when).toEqual(["When the three options appear"]);
  });
});

describe("DecisionLearningView", () => {
  it("shows each gate with its question, its result and what it improves, and never a pass", () => {
    render(<DecisionLearningView buckets={overview.buckets} />);
    const d1 = document.getElementById("gate-D1")!;
    expect(within(d1).getByText("Intent and action-policy fit")).toBeTruthy();
    expect(d1.textContent).toContain("what class of reaction is appropriate");
    expect(d1.textContent).toContain("Evaluates the decision before the wording of an email.");
    expect(document.getElementById("gate-D6")!.textContent).toContain("Not built yet");
    expect(document.getElementById("gate-D2")!.querySelector(".gate-result")!.textContent).toBe("Not measured");
    const text = screen.getByRole("region", { name: "The three buckets" }).textContent!;
    expect(text).not.toMatch(/\bPass\b|\bPASS\b|HAR-\d+|\bE\d+\.\d+\b|Ghost/);
  });

  it("lists Bucket 2 in decision-flow order", () => {
    render(<DecisionLearningView buckets={overview.buckets} />);
    const rows = [...document.querySelectorAll("#bucket-decision_action .gate-row")].map((r) => r.id);
    expect(rows).toEqual(["D1", "D2", "D3", "D4", "D5", "D6", "D7", "D8", "D9", "D10"].map((g) => `gate-${g}`));
  });
});

describe("EvalOverview", () => {
  it("carries the buckets inside the decision area and counts evals only", () => {
    render(<EvalOverview overview={overview} />);
    expect(document.getElementById("job-decision")!.contains(document.getElementById("decision-learning"))).toBe(true);
    expect(screen.getByText(new RegExp(`${overview.totals.registryEvals} evals in 22 families`))).toBeTruthy();
  });

  it("lists the metrics apart in the system area", () => {
    render(<EvalOverview overview={overview} />);
    const metrics = within(document.getElementById("job-system")!).getByRole("region", { name: "Metrics" });
    expect(within(metrics).getAllByRole("listitem").map((li) => li.textContent)).toEqual(["Model and token efficiency", "Tool efficiency", "Eval overhead", "Latency", "Cost"]);
  });
});
