// @vitest-environment jsdom
// Bucket 1 (B1 to B9) on /evals comes from the shared gate-results route, like Bucket 2: the rows are the stored results of
// the episode, a gate with none reads "Not measured", abstain and unknown are one Unknown and never a pass, and a pass that
// cites nothing is Unknown (rule R1). No file is read.
import { cleanup, render, within } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";
import { DecisionLearningView } from "@/components/evals/DecisionLearningView";
import { buildBucket1, type GateResultRow } from "@/lib/evals/bucket2-results";
import { loadLatestGateResults } from "@/lib/evals/load-gate-results";
import { buildOverview, loadEvalContracts } from "@/lib/evals/registry";
import { CONTRACTS_DIR } from "./contract-validator";

afterEach(cleanup);
const overview = buildOverview(loadEvalContracts(CONTRACTS_DIR), null);
const EP = "0e7a1000-0000-4000-8000-0000000000e1";

const row = (gate: string, over: Partial<GateResultRow> = {}): GateResultRow => ({
  gate, judged_object: { type: "Episode", id: EP }, span_id: "evidence:" + EP, verdict: "pass", question: `Q of ${gate}`,
  observed: `observed ${gate}`, why: `why ${gate}`, evidence_refs: ["activity:0e7a1000-0000-4000-8000-0000000000a1"],
  improves: `improves ${gate}`, grader: { kind: "deterministic" }, calibrated: false, ...over,
});

describe("Bucket 1 rows from the gate-results route", () => {
  it("lists B1 to B9 in order and keeps only Bucket 1 results", () => {
    const gates = overview.buckets.find((b) => b.id === "context_intelligence")!.gates;
    const built = buildBucket1(gates, [row("B2"), row("D1"), row("S1")], EP);
    expect(built.map((g) => g.id)).toEqual(["B1", "B2", "B3", "B4", "B5", "B6", "B7", "B8", "B9"]);
    expect(built.find((g) => g.id === "B2")!.rows).toHaveLength(1);
    expect(built.flatMap((g) => g.rows.map((r) => r.gate))).toEqual(["B2"]);
  });

  it("renders the route's rows, and Not measured for a gate with none", () => {
    render(<DecisionLearningView buckets={overview.buckets} bucket2Results={[row("B1"), row("B4", { verdict: "fail" }), row("B7", { verdict: "abstain" })]} episodeId={EP} />);
    const b1 = document.getElementById("gate-B1")!;
    expect(b1.textContent).toContain("observed B1");
    expect(within(b1).getByText("Pass")).toBeTruthy();
    expect(within(document.getElementById("gate-B4")!).getByText("Fail")).toBeTruthy();
    expect(within(document.getElementById("gate-B7")!).getByText("Unknown")).toBeTruthy();
    expect(document.getElementById("gate-B2")!.textContent).toContain("Not measured");
    expect(document.getElementById("gate-B2")!.textContent).not.toContain("Pass");
    expect(document.getElementById("gate-B1")!.textContent).toContain("Not yet calibrated");
  });

  it("reads a pass that cites no evidence as Unknown", () => {
    render(<DecisionLearningView buckets={overview.buckets} bucket2Results={[row("B3", { evidence_refs: [] })]} episodeId={EP} />);
    expect(within(document.getElementById("gate-B3")!).getByText("Unknown")).toBeTruthy();
  });

  it("the loader serves the B rows the core route returns", async () => {
    const api = {
      listEvalRunsPage: async () => ({ items: [{ decision_episode_id: EP }] }),
      listEpisodeGateResults: async (id: string) => (id === EP ? [row("B1"), row("D1")] : []),
    };
    const got = await loadLatestGateResults(api as never, undefined);
    expect(got.episodeId).toBe(EP);
    expect(got.results.filter((r) => r.gate.startsWith("B")).map((r) => r.gate)).toEqual(["B1"]);
    const failed = await loadLatestGateResults({ listEvalRunsPage: async () => { throw new Error("down"); }, listEpisodeGateResults: async () => [] } as never, undefined);
    expect(failed.results).toEqual([]);
  });
});
