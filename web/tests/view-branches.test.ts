// Edge cases of the HAR-145 view models: sparse traces, partial decisions and runs with missing pieces. Each assertion is
// about what the UI would say, so a sparse payload can never be read as more than it recorded (PR #66 LOW 12).
import { describe, expect, it } from "vitest";
import type { AgentRun, EpisodeReplayView, HumanStrategyDecision, JudgmentInference, RunStrategies, RunTrace } from "@/lib/api/types";
import { buildControlView, materialChanges, trajectory } from "@/lib/view/control";
import { buildLoop } from "@/lib/view/run-loop";
import { loadFixture } from "./contract-validator";

const run = loadFixture<AgentRun>("medtech.agent-run.json");
const trace = loadFixture<RunTrace>("medtech.run-trace.json");
const strategies = loadFixture<RunStrategies>("medtech.run-strategies.json");
const decision = loadFixture<HumanStrategyDecision>("medtech.strategy-decision.json");
const inference = loadFixture<JudgmentInference>("medtech.judgment-inference.json");

const cast = <T>(v: unknown) => v as T;
const withTrace = (over: Record<string, unknown>) => cast<RunTrace>({ ...trace, ...over });
const setOf = (candidates: unknown[], extra: Record<string, unknown> = {}) => cast<RunStrategies>({ ...strategies, strategy_set: { ...strategies.strategy_set, candidates, ...extra } });

const view = loadFixture<EpisodeReplayView>("replay.episodes.json");

describe("control view edge cases", () => {
  const inputs = (v: EpisodeReplayView) => ({ view: v, accountName: null, run: null, evalRun: null, episode: null, cliffKinds: [] as string[] | null, backendUnavailable: false, progress: null });

  it("intelligence facts: singular wording, no state and no knowledge adds no facts", () => {
    const sparse = { ...view, state: null, knowledge: { as_of: "x", items: [] }, prior_episodes: [] } as unknown as EpisodeReplayView;
    const facts = buildControlView(inputs(sparse)).bands[0]!.facts;
    expect(facts[0]).toBe("0 material events released of " + sparse.episode);
    expect(facts.some((f) => f.startsWith("account state"))).toBe(false);
    const one = { ...view, state: { ...view.state!, as_of: null }, knowledge: { as_of: "x", items: [{ id: "k" }] } } as unknown as EpisodeReplayView;
    const oneFacts = buildControlView(inputs(one)).bands[0]!.facts;
    expect(oneFacts.join(" ")).toContain("as of unknown");
    expect(oneFacts.join(" ")).toContain("1 knowledge item in scope");
  });

  it("material changes: a detail only from the artifacts that exist, and a view with no next event or state", () => {
    const e = view.prior_episodes[0]!;
    const sparse = { ...view, prior_episodes: [{ ...e, material: true, state_version: null, graph_diff_id: null }], next_event: null, state: null } as unknown as EpisodeReplayView;
    expect(materialChanges(sparse)[0]!.detail).toBeNull();
    const only = { ...sparse, prior_episodes: [{ ...e, material: true, state_version: 9, graph_diff_id: null }] } as unknown as EpisodeReplayView;
    expect(materialChanges(only)[0]!.detail).toBe("state v9");
    const v = buildControlView(inputs(sparse));
    expect(v.nextEvent).toBeNull();
    expect(v.stateVersion).toBeNull();
    expect(v.stateDigest).toBeNull();
    expect(v.stateAsOf).toBeNull();
    expect(trajectory(sparse).every((s) => s.heldOut === (s.released === false))).toBe(true);
  });
});

describe("run loop edge cases", () => {
  const phase = (id: string, t: RunTrace | null, r: AgentRun = run, s: RunStrategies | null = strategies, d: HumanStrategyDecision | null = decision, i: JudgmentInference | null = inference) =>
    buildLoop(r, t, s, d, i).find((p) => p.id === id)!;

  it("retrieve: knowledge cited on the run counts even without a context pull", () => {
    const refs = ["11110000-0000-4000-8000-000000000001"];
    const p = phase("retrieve", withTrace({ context_accesses: [] }), { ...run, knowledge_refs_used: refs } as AgentRun);
    expect(p.status).toBe("recorded");
    expect(p.summary).toContain("1 knowledge item cited");
    expect(phase("retrieve", null, { ...run, knowledge_refs_used: undefined } as unknown as AgentRun).status).toBe("absent");
  });

  it("reason: an ineligible run is recorded with its explanation or its reason codes; no evaluation still counts signals", () => {
    const base = trace.trigger_evaluation!;
    expect(phase("reason", withTrace({ trigger_evaluation: { ...base, eligible: false, explanation: "cooling off" } })).summary).toContain("cooling off");
    expect(phase("reason", withTrace({ trigger_evaluation: { ...base, eligible: false, explanation: null, reason_codes: ["a", "b"] } })).summary).toContain("a, b");
    expect(phase("reason", withTrace({ trigger_evaluation: { ...base, eligible: false, explanation: null, reason_codes: [] } })).summary).toContain("no reason recorded");
    expect(phase("reason", withTrace({ trigger_evaluation: { ...base, eligible: false, explanation: null, reason_codes: undefined } })).summary).toContain("no reason recorded");
    const signalsOnly = phase("reason", withTrace({ trigger_evaluation: undefined, signals: [{ id: "s" }] }));
    expect(signalsOnly.summary).toBe("1 signal fired.");
    expect(signalsOnly.detail).toBeNull();
    const noReasons = phase("reason", withTrace({ trigger_evaluation: { ...base, eligible: true, explanation: null, reason_codes: undefined } }));
    expect(noReasons.summary).toMatch(/signals? fired\.$/);
  });

  it("rank: a blocked set is recorded; a pick without a title shows its id; no eval bundles says so", () => {
    const blocked = phase("rank", trace, run, setOf([], { no_acceptable_candidate: true }));
    expect(blocked.status).toBe("recorded");
    expect(blocked.summary).toContain("no acceptable option");
    const c0 = strategies.strategy_set.candidates[0]!;
    const bare = phase("rank", trace, run, cast<RunStrategies>({ ...setOf([{ ...c0, preferred_by_agent: true, title: null }]), eval_bundles: undefined }));
    expect(bare.summary).toContain(c0.candidate_id);
    expect(bare.detail).toBe("no eval bundles");
    const noItems = phase("rank", trace, run, cast<RunStrategies>({ ...setOf([{ ...c0, preferred_by_agent: true }]), eval_bundles: [{ items: undefined }] }));
    expect(noItems.detail).toBe("no eval bundles");
  });

  it("act: waiting with nothing, a draft that is not executed yet, and the channel and recipient count of a sent artifact", () => {
    const none = phase("act", withTrace({ decisions: [] }), cast<AgentRun>({ ...run, output: null, status: "proposed" }), strategies, null);
    expect(none.status).toBe("waiting");
    const pending = phase("act", withTrace({ decisions: [{ ...trace.decisions![0]!, decision: "approve" }] }), cast<AgentRun>({ ...run, status: "proposed", output: { proposed_action_type: "send_email", finished_artifact: { channel: "email" }, recipients: ["a"] } }));
    expect(pending.status).toBe("waiting");
    expect(pending.summary).toContain("awaiting execution evidence");
    expect(pending.summary).toContain("after the human's approve");
    expect(pending.detail).toBe("email · 1 recipients");
    const noChannel = phase("act", withTrace({ decisions: [] }), cast<AgentRun>({ ...run, status: "recorded", output: { proposed_action_type: "send_email", finished_artifact: {}, recipients: undefined } }), strategies, null);
    expect(noChannel.detail).toBe("draft · 0 recipients");
    const viaEffect = phase("act", null, cast<AgentRun>({ ...run, status: "proposed", output: { proposed_action_type: "x", external_effect_id: "e1" } }));
    expect(viaEffect.status).toBe("recorded");
    expect(viaEffect.detail).toBeNull();
  });

  it("learn: confirmed updates are recorded and counted", () => {
    const p = phase("learn", withTrace({ placeholders: { knowledge_updates: [{ id: "k" }] } }));
    expect(p.status).toBe("recorded");
    expect(p.summary).toBe("1 knowledge update written back.");
    expect(phase("learn", null, run, strategies, decision, null).status).toBe("absent");
  });
});
