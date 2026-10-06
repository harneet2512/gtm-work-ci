// View models of the episode eval page (eval design spec 4b-3 Level 2 + the comparison view): pure
// functions over contract types, exercised on the contract-valid run fixtures and on variants of them.
import { describe, expect, it } from "vitest";
import type { EvalBundle, EvalResult, HumanStrategyDecision, Knowledge, RunStrategies, RunTrace } from "@/lib/api/types";
import { evidenceContext, evidenceSnippets } from "@/lib/evals/evidence";
import { buildSelectedView, defaultCandidateId } from "@/lib/evals/selected";
import { buildMatrix } from "@/lib/evals/matrix";
import { buildChain } from "@/lib/view/run-chain";
import { formatDay } from "@/lib/format";
import { loadExample, loadFixture } from "./contract-validator";

const strategies = loadFixture<RunStrategies>("acme.run-strategies.json");
const trace = loadFixture<RunTrace>("acme.run-trace.json");
const decision = loadExample<HumanStrategyDecision>("human_strategy_decision");
const knowledge: Record<string, Knowledge | null> = {};
const RUN = trace.run.id;
const A = "0ca00000-0000-4000-8000-0000000000a1";
const B = "0ca00000-0000-4000-8000-0000000000a2";
const C = "0ca00000-0000-4000-8000-0000000000a3";
const MARCO_EMAIL = "0ac70000-0000-4000-8000-000000000101";
const ctx = evidenceContext(trace, RUN, knowledge);
const chain = buildChain(strategies);
const link = (id: string) => chain.find((c) => c.candidate.candidate_id === id)!;

describe("formatDay", () => {
  it("prints a stable UTC day and passes junk through", () => {
    expect(formatDay("2026-09-29T23:59:00Z")).toBe("Sep 29, 2026");
    expect(formatDay("not a date")).toBe("not a date");
  });
});

describe("evidence snippets: who, when, what was said", () => {
  it("resolves the speaker, the day, the source and a deep link into the run trace", () => {
    const [snippet] = evidenceSnippets(
      [{ activity_id: MARCO_EMAIL, quote: "I can't commit to a review date until our team has read them.", speaker_person_id: "0b0e0000-0000-4000-8000-000000000018", occurred_at: "2026-09-29T15:42:00Z" }],
      ctx,
    );
    expect(snippet).toEqual({
      activityId: MARCO_EMAIL,
      quote: "I can't commit to a review date until our team has read them.",
      who: "Marco Ruiz",
      whoTitle: "Head of Security",
      when: "Sep 29, 2026",
      source: "Email",
      summary: "Marco (Acme security) requires SOC2 Type II + pen-test summary before EU rollout sign-off.",
      href: `/runs/${RUN}#activity-${MARCO_EMAIL}`,
    });
  });

  it("is honest when the activity or person is not in the trace: no invented name, no dead link", () => {
    const [snippet] = evidenceSnippets([{ activity_id: "99999999-9999-4999-8999-999999999999", speaker_person_id: "99999999-9999-4999-8999-999999999998" }], ctx);
    expect(snippet).toMatchObject({ who: null, when: null, source: null, href: null, quote: null });
    expect(evidenceSnippets([], evidenceContext(null, RUN, knowledge))).toEqual([]);
  });

  it("falls back to the activity's own participant name and time when the state has no such person", () => {
    const [snippet] = evidenceSnippets([{ activity_id: MARCO_EMAIL, speaker_person_id: "0b0e0000-0000-4000-8000-000000000001" }], ctx);
    expect(snippet).toMatchObject({ who: "Dana Kim", whoTitle: null, when: "Sep 29, 2026" });
  });
});

describe("selected-action view (Level 2)", () => {
  it("defaults to the human's choice, else gtm_ai's pick", () => {
    expect(defaultCandidateId(strategies, decision)).toBe(B);
    expect(defaultCandidateId(strategies, null)).toBe(A);
    expect(defaultCandidateId(null, null)).toBeNull();
  });

  it("orders Fail, Warn, Pass with the result's own reason, and collapses Not relevant", () => {
    const view = buildSelectedView(link(A), decision, ctx);
    expect(view.lines.map((l) => [l.name, l.verdict])).toEqual([
      ["CTA calibration", "fail"],
      ["Relationship continuity", "fail"],
      ["Enough evidence", "pass"],
    ]);
    expect(view.lines[0]!.reason).toBe("Marco explicitly said he cannot commit to a review date until his team reads the documents; a dated Tuesday ask ignores that.");
    expect(view.isGhostPick).toBe(true);
    expect(view.isChosen).toBe(false);

    const b = buildSelectedView(link(B), decision, ctx);
    expect(b.lines.map((l) => l.verdict)).toEqual(["warn", "pass"]);
    expect(b.notRelevant).toEqual([{ name: "Economic buyer", why: "No economic buyer is part of this thread and no commercial step is proposed." }]);
    expect(b.chosenBy).toBe("Dana Kim");
  });

  it("never puts the routing relevance reason on a verdict line; it lives in the detail", () => {
    const b = buildSelectedView(link(B), decision, ctx);
    const cta = b.lines.find((l) => l.evalType === "cta_calibration")!;
    expect(cta.reason).toMatch(/^The draft offers a call/);
    expect(cta.whyApplies).toBe("The draft makes an ask while the buyer has said he cannot commit to a date.");
    expect(cta.tag?.tag).toBe("Sales methodology");
    expect(cta.grader).toBe("AI judge · deepseek/deepseek-v4-flash");
    expect(cta.diagnostics).toEqual(["Ask too strong"]);
    expect(cta.evidence[0]?.who).toBe("Marco Ruiz");
    expect(cta.resultId).toBe("0e1a0000-0000-4000-8000-000000009201");
  });

  it("leads with the judgment: the worst line, in one sentence", () => {
    expect(buildSelectedView(link(A), decision, ctx).headline).toEqual({
      verdict: "fail",
      title: "Fail: CTA calibration",
      detail: "Marco explicitly said he cannot commit to a review date until his team reads the documents; a dated Tuesday ask ignores that.",
      more: 1,
    });
    expect(buildSelectedView(link(C), decision, ctx).headline).toMatchObject({ verdict: "warn", title: "Warn: Clear next step", more: 0 });
  });

  it("says plainly when everything passed, when nothing was routed, and when a blocking fail stops the send", () => {
    const allPass = { ...link(B), bundle: { ...link(B).bundle!, items: link(B).bundle!.items.filter((i) => i.verdict === "pass") } };
    expect(buildSelectedView(allPass, decision, ctx).headline).toEqual({ verdict: "pass", title: "Every relevant check passed", detail: "1 check, all pass.", more: 0 });
    expect(buildSelectedView({ ...link(B), bundle: null }, decision, ctx).headline).toEqual({ verdict: null, title: "No evals recorded for this option", detail: null, more: 0 });

    const blocking = blockingVariant(link(A).bundle!);
    const view = buildSelectedView({ ...link(A), bundle: blocking }, decision, ctx);
    expect(view.lines[0]).toMatchObject({ evalType: "champion_continuity", blocking: true });
    expect(view.headline.title).toBe("Send is blocked: Relationship continuity still fails.");
  });

  it("explains a candidate the transition policy held for review", () => {
    const held = { ...link(A).bundle!, candidate_policy: { transition_status: "CANDIDATE" as const, status: "restricted" as const, reasons: ["expansion_motion" as const], requires_human_review: true } };
    expect(buildSelectedView({ ...link(A), bundle: held }, decision, ctx).held).toBe("Held for review: an expansion ask while the account change is unconfirmed.");
    expect(buildSelectedView(link(A), decision, ctx).held).toBeNull();
  });
});

describe("comparison matrix: eval x candidate", () => {
  const matrix = buildMatrix(chain, decision);

  it("marks gtm_ai's pick and the human's choice on the columns", () => {
    expect(matrix.columns.map((c) => [c.letter, c.title, c.isGhostPick, c.isChosen])).toEqual([
      ["A", "Propose a security call", true, false],
      ["B", "Send package, buyer sets timing", false, true],
      ["C", "Check the gate with Priya", false, false],
    ]);
    expect(matrix.columns.map((c) => c.worst)).toEqual(["fail", "warn", "warn"]);
  });

  it("orders rows by severity across candidates", () => {
    expect(matrix.rows.map((r) => r.name)).toEqual(["CTA calibration", "Relationship continuity", "Clear next step", "Enough evidence", "Economic buyer"]);
  });

  it("gives every cell a verdict and a one-line reason, and tells not-relevant from not-checked", () => {
    const cta = matrix.rows[0]!;
    expect(cta.cells.map((c) => c.verdict)).toEqual(["fail", "warn", "not_relevant"]);
    expect(cta.cells[2]!.reason).toBe("No call-to-action is proposed, so calibration does not apply.");
    const continuity = matrix.rows[1]!;
    expect(continuity.cells.map((c) => c.verdict)).toEqual(["fail", "pass", "not_checked"]);
    expect(continuity.cells[2]!.reason).toBeNull();
    expect(continuity.tag?.tag).toBe("Sales methodology");
  });

  it("handles no strategy set", () => {
    expect(buildMatrix([], null)).toEqual({ columns: [], rows: [] });
  });
});

function blockingVariant(bundle: EvalBundle): EvalBundle {
  return {
    ...bundle,
    items: bundle.items.map((i) =>
      i.eval_type === "champion_continuity" && i.result ? { ...i, result: { ...i.result, blocking: true } as EvalResult } : i,
    ),
  };
}
