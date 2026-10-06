import { describe, expect, it } from "vitest";
import type { OperationalMetrics } from "@/lib/api/types";
import { buildHealthView, EXPECTED_SPAN_KINDS, type HealthEpisode } from "@/lib/evals/inspector/health";
import { buildOfflineView } from "@/lib/evals/inspector/offline";
import { def } from "./fixtures";

const stage = (name: string, calls: number, inTok: number, outTok: number, cost: number | null, ms: number) => ({
  stage: name,
  worker_calls: calls,
  model_calls: calls,
  input_tokens: inTok,
  output_tokens: outTok,
  cached_input_tokens: null,
  reasoning_tokens: null,
  tool_calls: 0,
  retries: 0,
  cost_usd: cost,
  worker_call_ms: ms,
  model_ms: ms / 2,
});

function metrics(over: Partial<OperationalMetrics> = {}): OperationalMetrics {
  return {
    classification: "metric",
    agent_run_id: "r",
    decision_episode_id: "e",
    measured: true,
    usage_source: "live",
    model_calls: 5,
    input_tokens: 1000,
    output_tokens: 400,
    cached_input_tokens: null,
    reasoning_tokens: null,
    tool_calls: 3,
    context_pulls: 2,
    retries: 0,
    cost_usd: 0.05,
    latency: { worker_call_ms: 8000, model_ms: 6000 },
    models: ["m"],
    stages: [stage("strategies", 2, 600, 200, 0.03, 5000), stage("judge", 3, 400, 200, 0.02, 3000)],
    ...over,
  } as unknown as OperationalMetrics;
}

const ep = (id: string, m: OperationalMetrics | null, kinds: string[] | null = [...EXPECTED_SPAN_KINDS]): HealthEpisode => ({ episodeId: id, label: id, metrics: m, spanKinds: kinds });

describe("continuous health view (HAR-149 section 6)", () => {
  it("sums real counts over the episodes that have live usage and counts the rest apart", () => {
    const v = buildHealthView([ep("e1", metrics()), ep("e2", metrics({ model_calls: 7, cost_usd: 0.07 })), ep("e3", metrics({ measured: false, usage_source: null })), ep("e4", null)]);
    expect(v.episodes).toBe(4);
    expect(v.measuredEpisodes).toBe(2);
    const fact = (label: string) => v.totals.find((t) => t.label === label)!.value;
    expect(fact("Model calls")).toBe("12");
    expect(fact("Cost")).toBe("$0.12");
    expect(v.note).toMatch(/2 of 4 episodes/);
  });

  it("splits agent cost from eval cost using the judge stage, and says not measured when a cost is unreported", () => {
    const v = buildHealthView([ep("e1", metrics())]);
    expect(v.split.evals).toMatchObject({ modelCalls: "3", tokens: "600", cost: "$0.02" });
    expect(v.split.agent).toMatchObject({ modelCalls: "2", tokens: "800", cost: "$0.03" });
    const partial = buildHealthView([ep("e1", metrics({ stages: [stage("strategies", 2, 600, 200, null, 5000), stage("judge", 3, 400, 200, 0.02, 3000)] as unknown as OperationalMetrics["stages"] }))]);
    expect(partial.split.agent.cost).toBe("not measured");
  });

  it("with no measured episode every figure reads not measured, never zero", () => {
    const v = buildHealthView([ep("e1", null)]);
    expect(v.measuredEpisodes).toBe(0);
    expect(v.totals.every((t) => t.value === "not measured")).toBe(true);
    expect(v.split.agent.cost).toBe("not measured");
  });

  it("trace completeness is the share of the standard steps recorded, and says it is not an integrity verdict", () => {
    const some = EXPECTED_SPAN_KINDS.slice(0, EXPECTED_SPAN_KINDS.length - 3);
    const v = buildHealthView([ep("e1", null, [...EXPECTED_SPAN_KINDS]), ep("e2", null, [...some])]);
    expect(v.trace.episodes).toBe(2);
    expect(v.trace.complete).toBe(1);
    expect(v.trace.average).toBe(`${EXPECTED_SPAN_KINDS.length - 1.5} of ${EXPECTED_SPAN_KINDS.length}`);
    expect(v.trace.note).toMatch(/not an integrity verdict/i);
    expect(buildHealthView([ep("e1", null, null)]).trace.episodes).toBe(0);
  });
});

describe("offline view (HAR-149 section 5)", () => {
  const gates = [
    def("S2", { name: "Grader validity", display_name: "Grader validity", display_question: "Can we trust the judges?", mode: "offline_benchmark", trigger: "calibration / evaluator change / periodic audit", plain_what: "We compare the model graders with reference answers." }),
    def("S3", { display_question: "Does it hold across repeated trials?", mode: "offline_benchmark" }),
    def("S4", { display_question: "Do we test what it can do and what it must never break?", mode: "offline_benchmark" }),
    def("S5", { display_question: "Can a grader look right without being right?", mode: "offline_benchmark" }),
    def("B1", { mode: "live_required" }),
  ];
  const deviations = [
    { id: "gold_model_reference", decision: "Reference answers come from a stronger model, not human labels.", applies_to: ["S2"] },
    { id: "single_trial", decision: "One trial per measurement for now.", applies_to: ["S3"] },
  ];

  it("lists only the offline gates, each as not run yet with what it measures", () => {
    const v = buildOfflineView(gates, deviations, false);
    expect(v.items.map((i) => i.id)).toEqual(["S2", "S3", "S4", "S5"]);
    for (const i of v.items) {
      expect(i.status).toBe("not_run");
      expect(i.statusLabel).toBe("Not run yet");
      expect(i.measures.length).toBeGreaterThan(10);
    }
    expect(v.items[0]!.question).toBe("Can we trust the judges?");
  });

  it("carries the deviation notes on the gates they apply to", () => {
    const v = buildOfflineView(gates, deviations, false);
    expect(v.items[0]!.deviation).toMatch(/not human labels/);
    expect(v.items[1]!.deviation).toMatch(/One trial/);
    expect(v.items[2]!.deviation).toBeNull();
    expect(v.headline).toMatch(/none of these has run/i);
  });

  it("contains no numbers it did not get: nothing is invented", () => {
    const v = buildOfflineView(gates, deviations, false);
    expect(JSON.stringify(v)).not.toMatch(/\d+(\.\d+)?%/);
  });

  it("when a judge-quality snapshot is recorded, S2 says so instead of not run", () => {
    const v = buildOfflineView(gates, deviations, true);
    expect(v.items[0]!.status).toBe("recorded");
    expect(v.items[1]!.status).toBe("not_run");
  });
});

describe("offline view: one statement, once", () => {
  it("does not repeat an owner deviation the registry status note already says", () => {
    const s2 = def("S2", { mode: "offline_benchmark", status_note: "Not run. Owner deviation: the reference answers are model-made (gpt-5.6-sol-pro), not human. The calibration run is pending." });
    const v = buildOfflineView([s2], [{ id: "d1", decision: "Reference answers are model-made, not human labels.", applies_to: ["S2"] }], false);
    expect(v.items[0]!.deviation).toBe("Reference answers are model-made, not human labels.");
    expect(v.items[0]!.statusNote).toBe("Not run. The calibration run is pending.");
    const only = buildOfflineView([def("S3", { mode: "offline_benchmark", status_note: "Owner deviation: single trial." })], [{ id: "d2", decision: "Single trial.", applies_to: ["S3"] }], false);
    expect(only.items[0]!.statusNote).toBeNull();
    const plain = buildOfflineView([def("S4", { mode: "offline_benchmark", status_note: "Not built for the demo." })], [], false);
    expect(plain.items[0]!.statusNote).toBe("Not built for the demo.");
  });
});
