// Edge cases of the HAR-145 view models: sparse traces, partial decisions and runs with missing pieces. Each assertion is
// about what the UI would say, so a sparse payload can never be read as more than it recorded (PR #66 LOW 12).
import { describe, expect, it } from "vitest";
import type { AdvanceResult, AgentRun, EpisodeReplayView, HumanStrategyDecision, JudgmentInference, RunStrategies, RunTrace } from "@/lib/api/types";
import { buildControlView, materialChanges, trajectory } from "@/lib/view/control";
import { buildEpisodeView } from "@/lib/view/episode";
import { pipelineFromAdvance } from "@/lib/view/pipeline";
import { buildLoop } from "@/lib/view/run-loop";
import { loadFixture } from "./contract-validator";

const run = loadFixture<AgentRun>("medtech.agent-run.json");
const trace = loadFixture<RunTrace>("medtech.run-trace.json");
const strategies = loadFixture<RunStrategies>("medtech.run-strategies.json");
const decision = loadFixture<HumanStrategyDecision>("medtech.strategy-decision.json");
const inference = loadFixture<JudgmentInference>("medtech.judgment-inference.json");

const cast = <T>(v: unknown) => v as T;
const withTrace = (over: Record<string, unknown>) => cast<RunTrace>({ ...trace, ...over });
const nodes = (t: RunTrace, s: RunStrategies | null = strategies, d: HumanStrategyDecision | null = decision, i: JudgmentInference | null = inference, posted: string[] | null = []) => {
  const v = buildEpisodeView(run, t, s, d, i, posted);
  return (id: string) => v.nodes.find((n) => n.id === id)!;
};
const setOf = (candidates: unknown[], extra: Record<string, unknown> = {}) => cast<RunStrategies>({ ...strategies, strategy_set: { ...strategies.strategy_set, candidates, ...extra } });

describe("episode nodes on sparse traces", () => {
  it("falls back to the source object id when the trigger has no summary, and shows no hint line", () => {
    const ev = { ...trace.trigger_activities![0]!, summary: null, account_hint: null };
    const n = nodes(withTrace({ trigger_activities: [ev] }))("source_event");
    expect(n.summary).toContain(ev.source_object_id);
    expect(nodes(withTrace({ trigger_activities: [ev] }))("resolution").detail).toBeNull();
  });

  it("an unresolved trigger is 'not recorded', and an opportunity is named only when present", () => {
    const base = trace.trigger_activities![0]!;
    const bare = nodes(withTrace({ trigger_activities: [{ ...base, account_id: null, opportunity_id: null }] }))("resolution");
    expect(bare.status).toBe("absent");
    expect(bare.summary).toBe("No account linkage recorded.");
    const noOpp = nodes(withTrace({ trigger_activities: [{ ...base, opportunity_id: null }] }))("resolution");
    expect(noOpp.summary).not.toContain("opportunity");
    expect(nodes(trace)("resolution").summary).toContain("opportunity");
  });

  it("evidence without a summary names the activity type; no trigger at all is 'not recorded'", () => {
    const acts = [{ ...trace.correlated_activities![0]!, summary: null }];
    expect(nodes(withTrace({ correlated_activities: acts }))("evidence").summary).toContain(acts[0]!.activity_type);
    const none = nodes(withTrace({ trigger_activities: [] }))("source_event");
    expect(none.status).toBe("absent");
    expect(none.data).toBeNull();
  });

  it("graph: no diff and an empty diff are both 'not recorded'; a diff without versions shows '?'", () => {
    expect(nodes(withTrace({ state_diff: null }))("graph").status).toBe("absent");
    const empty = nodes(withTrace({ state_diff: { id: "0d1ff000-0000-4000-8000-000000000001", changes: [], is_material: false } }))("graph");
    expect(empty.status).toBe("absent");
    expect(empty.detail).toContain("?→?");
    expect(empty.detail).toContain("material: no");
    const noChangesKey = nodes(withTrace({ state_diff: { id: "0d1ff000-0000-4000-8000-000000000001", is_material: true } }))("graph");
    expect(noChangesKey.status).toBe("absent");
    expect(noChangesKey.detail).toContain("material: yes");
    const noField = nodes(withTrace({ state_diff: { id: "0d1ff000-0000-4000-8000-000000000001", changes: [{}], from_version: 1, to_version: 2 } }))("graph");
    expect(noField.summary).toContain("state");
    expect(noField.summary).toContain("1 change");
  });

  it("state: absent without a run-time version; unchanged version reads 'read state vN'; no diff means no detail", () => {
    expect(nodes(withTrace({ state_at_run: null }))("state").status).toBe("absent");
    const same = nodes(withTrace({ state_before: { version: 5 }, state_at_run: { version: 5 }, state_diff: null }))("state");
    expect(same.summary).toBe("The run read account state v5.");
    expect(same.detail).toBeNull();
    const noBefore = nodes(withTrace({ state_before: null, state_at_run: { version: 6 } }))("state");
    expect(noBefore.summary).toBe("The run read account state v6.");
  });

  it("precedents: counts non-state tools, tolerates a tool with no items, and is absent when only state was read", () => {
    const only = nodes(withTrace({ context_accesses: [{ access_id: 1, tool: "state", items: [] }] }))("precedents");
    expect(only.status).toBe("absent");
    const two = nodes(withTrace({ context_accesses: [{ access_id: 1, tool: "evidence", items: [1, 2] }, { access_id: 2, tool: "precedents" }] }))("precedents");
    expect(two.summary).toContain("2 context sources");
    expect(two.detail).toContain("precedents: 0 items");
    expect(nodes(withTrace({ context_accesses: undefined }))("precedents").status).toBe("absent");
  });

  it("knowledge cited: a candidate with no refs adds nothing, repeated refs count once", () => {
    const k = "11110000-0000-4000-8000-000000000001";
    const cs = strategies.strategy_set.candidates;
    const both = setOf([{ ...cs[0]!, knowledge_refs: [k] }, { ...cs[1]!, knowledge_refs: [k] }, { ...cs[2]!, knowledge_refs: undefined }]);
    expect(nodes(trace, both)("knowledge_cited").summary).toContain("1 knowledge item");
    expect(nodes(trace, null)("knowledge_cited").status).toBe("absent");
  });

  it("candidates: an abstention is recorded, not a warning; a missing set is absent; untitled candidates use their strategy type", () => {
    const abstain = nodes(trace, setOf([], { no_acceptable_candidate: true }))("candidates");
    expect(abstain.status).toBe("recorded");
    expect(abstain.summary).toContain("abstained");
    expect(nodes(trace, setOf([]))("candidates").status).toBe("absent");
    const c0 = strategies.strategy_set.candidates[0]!;
    const untitled = nodes(trace, setOf([{ ...c0, title: null, action_class: null }]))("candidates");
    expect(untitled.summary).toContain(c0.strategy_type);
  });

  it("ranking: no pick is 'not recorded'; a pick without a rank omits it; no generation phase shows '?'", () => {
    const c0 = strategies.strategy_set.candidates[0]!;
    const noPick = nodes(trace, setOf([{ ...c0, preferred_by_agent: false }]))("ranking");
    expect(noPick.status).toBe("absent");
    const noRank = nodes(trace, setOf([{ ...c0, preferred_by_agent: true, ranking: null, title: null }]))("ranking");
    expect(noRank.summary).not.toContain("rank");
    expect(noRank.summary).toContain(c0.candidate_id);
    const v = buildEpisodeView({ ...run, generation: undefined } as AgentRun, trace, strategies, decision, inference, []);
    expect(v.nodes.find((n) => n.id === "ranking")!.detail).toBe("run ?");
  });

  it("cliff: an unknown message kind keeps its raw name", () => {
    expect(nodes(trace, strategies, decision, inference, ["weird"])("cliff").summary).toContain("weird");
  });

  it("human: reads the trace decision, the strategy decision, or says it is still open", () => {
    const d0 = trace.decisions![0]!;
    const noLabel = nodes(withTrace({ decisions: [{ ...d0, decision: "approve", actor_label: null }] }))("human");
    expect(noLabel.summary).toBe("A human chose approve.");
    const viaStrategy = nodes(withTrace({ decisions: [] }), strategies, cast<HumanStrategyDecision>({ ...decision, actor_label: null, selected_candidate_id: null }))("human");
    expect(viaStrategy.summary).toBe("A human chose a candidate.");
    const withPick = nodes(withTrace({ decisions: [] }))("human");
    expect(withPick.summary).toContain("chose a candidate (");
    expect(nodes(withTrace({ decisions: [] }), strategies, null)("human").status).toBe("waiting");
  });

  it("recompute: never inferred from the edit; without an edit there is no detail or data", () => {
    const d0 = trace.decisions![0]!;
    const plain = nodes(withTrace({ decisions: [{ ...d0, decision: "approve" }] }))("recompute");
    expect(plain.status).toBe("not_observable");
    expect(plain.detail).toBeNull();
    expect(plain.data).toBeNull();
    const edited = nodes(trace)("recompute");
    expect(edited.detail).toContain("edit is recorded");
    expect(edited.data).not.toBeNull();
  });

  it("knowledge mutation: recorded updates win over an unconfirmed inference", () => {
    const updated = nodes(withTrace({ placeholders: { knowledge_updates: [{ id: "k1" }, { id: "k2" }] } }))("knowledge_mutation");
    expect(updated.status).toBe("recorded");
    expect(updated.summary).toContain("2 knowledge updates");
    expect(nodes(withTrace({ placeholders: undefined }), strategies, decision, null)("knowledge_mutation").status).toBe("absent");
  });
});

const view = loadFixture<EpisodeReplayView>("replay.episodes.json");
const ctrlRun = { ...run, account_id: view.account_id } as AgentRun;
const band = (v: ReturnType<typeof buildControlView>, id: string) => v.bands.find((b) => b.id === id)!;

describe("control view edge cases", () => {
  it("decision band: no phase reads 'run recorded'; no decision or strategies adds no facts; the human decision is named", () => {
    const noPhase = band(buildControlView(view, null, { ...ctrlRun, generation: undefined } as AgentRun, null, null, []), "decision_learning");
    expect(noPhase.status).toBe("run recorded");
    expect(noPhase.facts).toEqual([]);
    expect(noPhase.tone).toBe("none");
    const chose = band(buildControlView(view, null, ctrlRun, strategies, decision, []), "decision_learning");
    expect(chose.facts.join(" ")).toContain("human decision: chose an option");
    const sent = band(buildControlView(view, null, ctrlRun, strategies, cast<HumanStrategyDecision>({ ...decision, selected_candidate_id: null, send_decision: "send" }), []), "decision_learning");
    expect(sent.facts.join(" ")).toContain("human decision: send");
    const recorded = band(buildControlView(view, null, ctrlRun, strategies, cast<HumanStrategyDecision>({ ...decision, selected_candidate_id: null, send_decision: null }), []), "decision_learning");
    expect(recorded.facts.join(" ")).toContain("human decision: recorded");
  });

  it("verdict tally counts every verdict kind and ignores bundles with no items", () => {
    const item = (verdict: string) => ({ verdict, result: null });
    const s = cast<RunStrategies>({ ...strategies, eval_bundles: [{ items: [item("pass"), item("warn"), item("fail"), item("abstain"), item("not_relevant")] }, { items: [] }] });
    const b = band(buildControlView(view, null, ctrlRun, s, null, []), "decision_learning");
    expect(b.facts[0]).toBe("1 fail · 1 warn · 1 pass across 2 options");
    expect(b.tone).toBe("fail");
  });

  it("intelligence band: singular wording, no state and no knowledge adds no facts", () => {
    const sparse = { ...view, state: null, knowledge: { as_of: "x", items: [] }, prior_episodes: [] } as unknown as EpisodeReplayView;
    const facts = band(buildControlView(sparse, null, null, null, null, []), "intelligence").facts;
    expect(facts[0]).toBe("0 material events released of " + sparse.episode);
    expect(facts.some((f) => f.startsWith("account state"))).toBe(false);
    const one = { ...view, state: { ...view.state!, as_of: null }, knowledge: { as_of: "x", items: [{ id: "k" }] } } as unknown as EpisodeReplayView;
    const oneFacts = band(buildControlView(one, null, null, null, null, []), "intelligence").facts;
    expect(oneFacts.join(" ")).toContain("as of ?");
    expect(oneFacts.join(" ")).toContain("1 knowledge item in scope");
  });

  it("cliff band: names unknown kinds raw and the singular message count; waits on a decision when none opened", () => {
    const one = band(buildControlView(view, null, null, null, null, ["chooser"]), "cliff_experience");
    expect(one.status).toBe("1 message posted");
    expect(band(buildControlView(view, null, null, null, null, ["x"]), "cliff_experience").facts).toEqual(["x"]);
    const noDecision = { ...view, prior_episodes: view.prior_episodes.map((e) => ({ ...e, decision_episode_id: null })) };
    expect(band(buildControlView(noDecision, null, null, null, null, []), "cliff_experience").status).toBe("waiting on a decision");
  });

  it("system band: counts done and failed steps and handles a run with no steps", () => {
    const steps = [{ seq: 1, step: "a", status: "recorded" }, { seq: 2, step: "b", status: "failed" }, { seq: 3, step: "c", status: "running" }];
    const b = band(buildControlView(view, null, { ...ctrlRun, steps } as unknown as AgentRun, null, null, []), "system");
    expect(b.facts).toEqual(["1/3 steps done", "1 failed"]);
    expect(b.status).toBe("a step failed");
    const none = band(buildControlView(view, null, { ...ctrlRun, steps: undefined } as unknown as AgentRun, null, null, []), "system");
    expect(none.facts).toEqual(["0/0 steps done", "no failed steps"]);
  });

  it("material changes: a detail only from the artifacts that exist, and a view with no next event or state", () => {
    const e = view.prior_episodes[0]!;
    const sparse = { ...view, prior_episodes: [{ ...e, material: true, state_version: null, graph_diff_id: null }], next_event: null, state: null } as unknown as EpisodeReplayView;
    expect(materialChanges(sparse)[0]!.detail).toBeNull();
    const only = { ...sparse, prior_episodes: [{ ...e, material: true, state_version: 9, graph_diff_id: null }] } as unknown as EpisodeReplayView;
    expect(materialChanges(only)[0]!.detail).toBe("state v9");
    const v = buildControlView(sparse, null, null, null, null, []);
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

const advance = (over: Partial<AdvanceResult> = {}): AdvanceResult =>
  cast<AdvanceResult>({
    manifest_id: "m",
    account_id: "a",
    episode: 3,
    total: 4,
    released: { position: 3, source_system: "email" },
    material: true,
    no_action_reason: null,
    state_version: 8,
    graph_diff_id: 302,
    account_change_id: "c1",
    decision_episode_id: "e1",
    ...over,
  });
const stage = (stages: ReturnType<typeof pipelineFromAdvance>, id: string) => stages.find((s) => s.id === id)!;
const runIn = (phase: string | undefined, steps: unknown[] = []) => cast<AgentRun>({ id: "r", account_id: "a", generation: phase ? { phase } : undefined, steps });

describe("pipeline edge cases", () => {
  it.each([
    ["queued", "running"],
    ["generating", "running"],
    ["evaluating", "running"],
    ["published", "passed"],
    ["paused", "warning"],
    ["failed", "failed"],
    ["not_orchestrated", "unknown"],
    [undefined, "unknown"],
  ])("run phase %s is stage %s", (phase, status) => {
    expect(stage(pipelineFromAdvance(advance(), runIn(phase), []), "decide").status).toBe(status);
  });

  it("decide detail: names the running step, a failed step, or the finished count; none means the phase", () => {
    const d = (steps: unknown[]) => stage(pipelineFromAdvance(advance(), runIn("generating", steps), []), "decide").detail;
    expect(d([{ seq: 1, step: "retrieve", status: "running" }])).toBe("step 1: retrieve running");
    expect(d([{ seq: 2, step: "judge", status: "failed" }])).toBe("step 2: judge failed");
    expect(d([{ seq: 1, step: "a", status: "succeeded" }, { seq: 2, step: "b", status: "recorded" }, { seq: 3, step: "c", status: "queued" }])).toBe("2/3 steps done");
    expect(d([])).toBe("generating");
    expect(stage(pipelineFromAdvance(advance(), runIn(undefined, []), []), "decide").detail).toBeNull();
    expect(stage(pipelineFromAdvance(advance(), cast<AgentRun>({ id: "r", account_id: "a", generation: { phase: "queued" } }), []), "decide").detail).toBe("queued");
  });

  it("a non-material event without an account change links nothing; a material one without artifacts warns on each", () => {
    expect(stage(pipelineFromAdvance(advance({ material: false, account_change_id: null }), null, []), "resolve").detail).toBeNull();
    const bare = pipelineFromAdvance(advance({ account_change_id: null, graph_diff_id: null, state_version: null, decision_episode_id: null }), null, []);
    expect(["resolve", "graph", "state"].map((id) => stage(bare, id).status)).toEqual(["warning", "warning", "warning"]);
    expect(stage(bare, "decide").detail).toBe("no decision was owed");
  });

  it("cliff after a settled run with unreadable refs is not observed; on a still-running run it is just waiting", () => {
    expect(stage(pipelineFromAdvance(advance(), runIn("published"), null), "cliff").status).toBe("unknown");
    expect(stage(pipelineFromAdvance(advance(), runIn("generating"), null), "cliff").status).toBe("waiting");
  });
});
