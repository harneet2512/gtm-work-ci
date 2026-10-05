// The eval page's new view models, on both fixture worlds: the trace strip (Event N -> ... -> inference, each
// step linking to its page), who chose what, the send gate, and the comparison's evidence peeks and tallies.
import { describe, expect, it } from "vitest";
import type { AgentRun, HumanStrategyDecision, JudgmentInference, RunStrategies, RunTrace } from "@/lib/api/types";
import { buildAfterEdit } from "@/lib/evals/after-edit";
import { choiceSummary } from "@/lib/evals/choice";
import { evidenceContext } from "@/lib/evals/evidence";
import { buildMatrix } from "@/lib/evals/matrix";
import { buildSelectedView } from "@/lib/evals/selected";
import { sendGate } from "@/lib/evals/send-gate";
import { buildTraceSteps } from "@/lib/evals/trace-strip";
import type { EvalPageData } from "@/lib/load-eval-page";
import { buildChain } from "@/lib/view/run-chain";
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
const RUN = medtech.run.id;
const chain = buildChain(medtech.strategies);
const [A, B, C] = chain;
const ctx = evidenceContext(medtech.trace, RUN, {});

describe("buildTraceSteps", () => {
  it("walks Event N to the inference, each step linking to its page", () => {
    const steps = buildTraceSteps(medtech);
    expect(steps.map((s) => s.key)).toEqual(["event", "state", "strategies", "evals", "choice", "send", "inference"]);
    const byKey = Object.fromEntries(steps.map((s) => [s.key, s]));
    expect(byKey.event).toMatchObject({ detail: "Email from Fatoumata Touré", status: "done", at: "2023-11-09T09:30:00Z" });
    expect(byKey.event!.href).toBe(`/accounts/${medtech.run.account_id}?event=${medtech.trace!.trigger_activities[0]!.source_event_id}&view=after`);
    expect(byKey.state).toMatchObject({ detail: "5 material changes", href: "#intelligence-h" });
    expect(byKey.strategies).toMatchObject({ detail: "3 options", href: `/runs/${RUN}#candidates-h` });
    expect(byKey.evals).toMatchObject({ detail: "14 verdicts", status: "current", href: "#compare-h" });
    expect(byKey.choice).toMatchObject({ detail: "Luis Rodriguez chose B", status: "done", href: `/runs/${RUN}#human-h` });
    expect(byKey.send).toMatchObject({ detail: "Sent", status: "done", at: medtech.decision!.send_decided_at });
    expect(byKey.inference).toMatchObject({ detail: "Awaiting confirmation", status: "waiting", href: `/runs/${RUN}#why-h` });
  });

  it("says what has not happened yet, and when Send is blocked", () => {
    const steps = buildTraceSteps({ ...medtech, trace: null, decision: null, inference: null });
    const byKey = Object.fromEntries(steps.map((s) => [s.key, s]));
    expect(byKey.event).toMatchObject({ status: "none", href: null, detail: "Not recorded" });
    expect(byKey.state).toMatchObject({ status: "none", detail: "Not recorded" });
    expect(byKey.choice).toMatchObject({ status: "waiting", detail: "Awaiting a choice" });
    expect(byKey.send).toMatchObject({ status: "waiting", detail: "After a choice" });
    expect(byKey.inference).toMatchObject({ status: "waiting", detail: "After the send" });

    const pendingC = { ...medtech.decision!, selected_candidate_id: C!.candidate.candidate_id, send_decision: "pending" as const, send_decided_at: null, human_decision_id: null };
    const blocked = Object.fromEntries(buildTraceSteps({ ...medtech, decision: pendingC, inference: null }).map((s) => [s.key, s]));
    expect(blocked.send).toMatchObject({ status: "blocked", detail: "Blocked: Pricing policy" });
    expect(blocked.choice!.detail).toBe("Luis Rodriguez chose C");
  });

  it("covers agreement, a discard, a no-material diff, an empty set and the inference verdicts", () => {
    const agreed = { ...medtech.decision!, selected_candidate_id: A!.candidate.candidate_id, send_decision: "discard" as const };
    const noDiff = { ...medtech.trace!, state_diff: { ...medtech.trace!.state_diff!, is_material: false } };
    const steps = Object.fromEntries(
      buildTraceSteps({ ...medtech, decision: agreed, trace: noDiff, strategies: null, inference: { ...medtech.inference!, human_verdict: "corrected" } }).map((s) => [s.key, s]),
    );
    expect(steps.choice!.detail).toBe("Luis Rodriguez chose an option"); // no strategy set to name it from
    expect(buildTraceSteps({ ...medtech, decision: agreed }).find((s) => s.key === "choice")!.detail).toBe("Luis Rodriguez chose Ghost's pick");
    expect(steps.send!.detail).toBe("Discarded");
    expect(steps.state!.detail).toBe("No material change");
    expect(steps.strategies).toMatchObject({ status: "waiting", detail: "Not drafted yet" });
    expect(steps.evals!.detail).toBe("No verdicts yet");
    expect(steps.inference).toMatchObject({ status: "done", detail: "Corrected by the human" });
    const confirmed = buildTraceSteps({ ...medtech, inference: { ...medtech.inference!, human_verdict: "confirmed" } }).find((s) => s.key === "inference");
    expect(confirmed!.detail).toBe("Confirmed by the human");
  });

  it("falls back to the activity summary when the trigger names no sender", () => {
    const a = { ...medtech.trace!.trigger_activities[0]!, participants: [] };
    const step = buildTraceSteps({ ...medtech, trace: { ...medtech.trace!, trigger_activities: [a] } })[0]!;
    expect(step.detail).toBe("Email");
  });
});

describe("choiceSummary", () => {
  const matrix = buildMatrix(chain, medtech.decision);

  it("names Ghost's pick and the human's choice with their letters", () => {
    expect(choiceSummary(matrix, medtech.decision)).toEqual({
      ghostPick: { letter: "A", title: "Book the call, bring the cost model", candidateId: A!.candidate.candidate_id },
      chosen: { letter: "B", title: "Put the costs in writing first", candidateId: B!.candidate.candidate_id },
      actor: "Luis Rodriguez",
      agreed: false,
    });
  });

  it("is waiting when nobody chose, and agreed when the human took Ghost's pick", () => {
    expect(choiceSummary(buildMatrix(chain, null), null)).toMatchObject({ chosen: null, actor: null, agreed: false });
    const same = { ...medtech.decision!, selected_candidate_id: A!.candidate.candidate_id };
    expect(choiceSummary(buildMatrix(chain, same), same).agreed).toBe(true);
  });
});

describe("sendGate", () => {
  const gate = (cand: (typeof chain)[number], decision: HumanStrategyDecision | null) =>
    sendGate(buildSelectedView(cand, decision, ctx), buildAfterEdit(cand, decision, null));

  it("is sent for the chosen, sent option", () => {
    expect(gate(B!, medtech.decision)).toMatchObject({ state: "sent", blockers: [] });
  });

  it("would block an option whose blocking failure stands, naming each blocker", () => {
    const g = gate(C!, medtech.decision);
    expect(g.state).toBe("would_block");
    expect(g.title).toBe("If chosen, Send stays blocked: Pricing policy still fails.");
    expect(g.blockers.map((b) => b.name)).toEqual(["Pricing policy", "CTA calibration"]);
  });

  it("is blocked for the chosen option while it is pending, open when nothing blocks, and none for an unblocked option", () => {
    const pending = (id: string) => ({ ...medtech.decision!, selected_candidate_id: id, send_decision: "pending" as const, send_decided_at: null, human_decision_id: null });
    expect(gate(C!, pending(C!.candidate.candidate_id))).toMatchObject({ state: "blocked", title: "Send is blocked: Pricing policy still fails." });
    expect(gate(A!, pending(A!.candidate.candidate_id))).toMatchObject({ state: "open", title: "Send is open: no check blocks it." });
    expect(gate(A!, medtech.decision).state).toBe("none");
    expect(gate(B!, { ...medtech.decision!, send_decision: "discard" }).state).toBe("discarded");
  });
});

describe("buildMatrix with evidence", () => {
  it("gives every judged cell a deep link and its first evidence, and every column its tally", () => {
    const m = buildMatrix(chain, medtech.decision, ctx);
    const pricing = m.rows.find((r) => r.evalType === "pricing_integrity")!;
    const c = pricing.cells[2]!;
    expect(c.href).toBe(`/runs/${RUN}/evals?candidate=${C!.candidate.candidate_id}#result-${c.resultId}`);
    expect(c.evidence).toMatchObject({ who: "Luis Rodriguez", when: "Nov 8, 2023" });
    expect(pricing.cells[0]).toMatchObject({ verdict: "not_relevant", href: null, evidence: null });
    expect(m.columns.map((col) => col.counts)).toEqual([
      { pass: 4, warn: 1, fail: 0, abstain: 0, notRelevant: 1 },
      { pass: 2, warn: 3, fail: 0, abstain: 0, notRelevant: 0 },
      { pass: 1, warn: 1, fail: 2, abstain: 0, notRelevant: 0 },
    ]);
  });

  it("orders the rows worst first across options", () => {
    expect(buildMatrix(chain, medtech.decision).rows.map((r) => r.name)).toEqual([
      "CTA calibration",
      "Pricing policy",
      "Responds to the change",
      "Stakeholder coverage",
      "Clear next step",
      "Grounding",
    ]);
  });

  it("works without an evidence context (no peeks) on the contract example world", () => {
    const acme = buildChain(loadFixture<RunStrategies>("acme.run-strategies.json"));
    const m = buildMatrix(acme, loadExample<HumanStrategyDecision>("human_strategy_decision"));
    expect(m.rows[0]!.cells.every((c) => c.evidence === null && c.href === null)).toBe(true);
  });
});
