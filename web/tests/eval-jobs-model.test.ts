// The eval page's three jobs from the recorded run (HAR-129 demo-loop clarification): what gtm_ai understood from
// Event N (the object the intelligence-building evals judge), the knowledge trace of the decision (E7: retrieved,
// used, changed), and the system view (trace completeness, model, timings, judge cost). All of it read from the
// run's own trace, strategies, decision and inference; nothing that the recorded objects do not say.
import { describe, expect, it } from "vitest";
import type { AgentRun, HumanStrategyDecision, JudgmentInference, Knowledge, RunStrategies, RunTrace } from "@/lib/api/types";
import { evidenceContext } from "@/lib/evals/evidence";
import { buildKnowledgeTrace } from "@/lib/evals/knowledge-trace";
import { buildSystemView } from "@/lib/evals/system-view";
import { buildUnderstanding } from "@/lib/evals/understanding";
import type { EvalPageData } from "@/lib/load-eval-page";
import { loadExample, loadFixture } from "./contract-validator";

const medtech: EvalPageData = {
  run: loadFixture<AgentRun>("medtech.agent-run.json"),
  trace: loadFixture<RunTrace>("medtech.run-trace.json"),
  strategies: loadFixture<RunStrategies>("medtech.run-strategies.json"),
  decision: loadFixture<HumanStrategyDecision>("medtech.strategy-decision.json"),
  inference: loadFixture<JudgmentInference>("medtech.judgment-inference.json"),
  knowledge: {},
  notices: [],
  reevaluation: null,
};
const k17 = loadExample<Knowledge>("knowledge");
const acmeTrace = loadFixture<RunTrace>("acme.run-trace.json");
const acme: EvalPageData = {
  run: acmeTrace.run as AgentRun,
  trace: acmeTrace,
  strategies: loadFixture<RunStrategies>("acme.run-strategies.json"),
  decision: loadExample<HumanStrategyDecision>("human_strategy_decision"),
  inference: loadExample<JudgmentInference>("judgment_inference"),
  knowledge: { [k17.id]: k17 },
  notices: [],
  reevaluation: null,
};

describe("buildUnderstanding", () => {
  const u = buildUnderstanding(medtech.trace, evidenceContext(medtech.trace, medtech.run.id, {}))!;

  it("names Event N and what gtm_ai now understands, each change with the words it rests on", () => {
    expect(u.event).toMatchObject({ source: "Email", who: "Fatoumata Touré", when: "Nov 9, 2023", href: `/runs/${medtech.run.id}#activity-${medtech.trace!.trigger_activities[0]!.id}` });
    expect(u.changes.map((c) => c.label)).toEqual(["Objections", "Decision criteria", "Next meeting", "Next milestone", "Product use case"]);
    const objections = u.changes[0]!;
    // A list change names what it added or removed, not the whole list twice.
    expect(objections).toMatchObject({ added: ["Long-term cost of integrating EduTech Lab and SecureData Nexus"], removed: [], before: null, after: null });
    expect(objections.evidence[0]).toMatchObject({ who: "Fatoumata Touré", quote: expect.stringContaining("long-term cost implications") });
    expect(objections.standing).toBeNull();
    expect(u.changes[2]).toMatchObject({ before: "unknown", after: "Follow-up call requested; no time agreed", added: [], standing: "first_party_ai", confidence: 0.9 });
    expect(u.bookkeeping).toBe(2);
  });

  it("carries the signal and why the run was eligible, in plain words", () => {
    expect(u.signals).toEqual(["Customer replied"]);
    expect(u.trigger).toBe("Fatoumata replied with a new cost question and asked for a call; no open run.");
    expect(u.version).toEqual({ from: 107, to: 108 });
  });

  it("names a person added to the buying group instead of printing their record", () => {
    const a = buildUnderstanding(acme.trace, evidenceContext(acme.trace, acme.run.id, {}))!;
    expect(a.changes.find((c) => c.label === "Buying group")!.after).toBe("Marco Ruiz (security)");
    // A list that went from empty to one item reads as an addition.
    expect(a.changes.find((c) => c.label === "Blockers")).toMatchObject({ added: ["Security review: SOC2 Type II report and pen-test summary required"], removed: [] });
  });

  it("is null without a trace or a state diff", () => {
    expect(buildUnderstanding(null, evidenceContext(null, "r", {}))).toBeNull();
    expect(buildUnderstanding({ ...medtech.trace!, state_diff: null }, evidenceContext(null, "r", {}))!.changes).toEqual([]);
  });
});

describe("buildKnowledgeTrace", () => {
  it("says plainly when no company knowledge reached the decision", () => {
    const k = buildKnowledgeTrace(medtech);
    expect(k).toMatchObject({ retrievedTraced: false, used: [], inChosenOption: false });
  });

  it("follows a knowledge object from the options that used it to the human's choice and the inference", () => {
    const k = buildKnowledgeTrace(acme);
    expect(k.used).toEqual([{ id: k17.id, label: `K17: ${k17.title}`, status: k17.status, options: ["B"], inChosen: true, citedByInference: true }]);
    expect(k.inChosenOption).toBe(true);
  });

  it("labels knowledge the page could not load, and an option nobody chose", () => {
    const k = buildKnowledgeTrace({ ...acme, knowledge: {}, decision: null, inference: null });
    expect(k.used[0]).toMatchObject({ label: "Company knowledge (not loaded)", status: null, inChosen: false, citedByInference: false });
    expect(k.inChosenOption).toBe(false);
  });
});

describe("buildSystemView", () => {
  it("checks the trace is complete from the recorded objects, and times every step", () => {
    const s = buildSystemView(medtech);
    expect(s.trace).toEqual([
      { label: "Trigger activity", ok: true, detail: "1 recorded" },
      { label: "State diff", ok: true, detail: "v107 → v108, 7 changes" },
      { label: "Signals", ok: true, detail: "1" },
      { label: "Trigger evaluation", ok: true, detail: "eligible" },
      { label: "Context pulls", ok: true, detail: "2, read as of Nov 9, 2023" },
      { label: "Human decision", ok: true, detail: "1" },
    ]);
    expect(s.model).toBe("qwen/qwen3.8-flash");
    expect(s.steps.map((x) => [x.step, x.seconds])).toEqual([
      ["build_context", 2],
      ["draft", 54],
      ["crm_intent", 0],
      ["await_human", 1590],
      ["execute", 1],
    ]);
    expect(s.judges).toEqual({ verdicts: 14, rule: 2, ai: 12, costUsd: null, latencyMs: null, tokens: null });
  });

  it("sums recorded judge cost, latency and tokens when the bundles carry them, and flags a missing trace", () => {
    const s = buildSystemView({ ...acme, trace: null });
    expect(s.judges.costUsd).toBeGreaterThan(0);
    expect(s.judges.tokens).toBeGreaterThan(0);
    expect(s.trace.every((t) => !t.ok)).toBe(true);
    expect(s.steps.length).toBeGreaterThan(0);
  });
});
