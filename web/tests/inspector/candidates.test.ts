import { describe, expect, it } from "vitest";
import type { DependencyInvalidation, EpisodeRanking, EpisodeSummary, HumanStrategyDecision, JudgmentInference, RunStrategies } from "@/lib/api/types";
import { buildDecisionScreen, type DecisionInput } from "@/lib/evals/inspector/candidates";
import { CAND_A, CAND_B, CAND_C, EP, result } from "./fixtures";

const ref = ["activity:0e7a1000-0000-4000-8000-0000000000a1"];

function cand(id: string, rank: number, title: string, over: Record<string, unknown> = {}) {
  return {
    candidate_id: id,
    strategy_type: rank === 1 ? "stronger_cta" : rank === 2 ? "clarify_first" : "add_stakeholder",
    title,
    description: `${title}: one-line intent`,
    ranking: rank,
    preferred_by_agent: rank === 1,
    rationale: "plausible",
    state_refs: ["blockers", "next_step"],
    evidence_refs: [{ activity_id: "0e7a1000-0000-4000-8000-0000000000a1" }],
    knowledge_refs: ["0e7a1000-0000-4000-8000-0000000000b1"],
    action_type: "send_email",
    action_class: "REPLY",
    to: [],
    cc: [],
    subject: "Re: DPA",
    five_questions: { what_changed: "", why_state_changed: "", what_remains_unknown: "", prior_knowledge_applies: "", why_next_action: "Because the DPA is the gate." },
    ...over,
  };
}

const strategies = {
  strategy_set: { id: "s1", candidates: [cand(CAND_B, 2, "Wait for legal"), cand(CAND_A, 1, "Ask for the DPA review date"), cand(CAND_C, 3, "Loop in the CFO")], no_acceptable_candidate: false },
  eval_bundles: [],
} as unknown as RunStrategies;

const summary = (over: Record<string, unknown> = {}) =>
  ({
    id: EP,
    recommended_action: { candidate_id: CAND_A, ranking: 1 },
    selected_action: { candidate_id: CAND_B, ranking: 2 },
    human_outcome: { agreement: "overrode", human_action: "APPROVE_WITH_EDIT", send_decision: "send", edited: true },
    ...over,
  }) as unknown as EpisodeSummary;

const d2 = (id: string, rows: [string, "pass" | "warn" | "fail"][]) =>
  result({
    gate: "D2",
    sub_gate: "candidate",
    judged_object: { type: "StrategyCandidate", id },
    verdict: rows.some((r) => r[1] === "fail") ? "fail" : rows.some((r) => r[1] === "warn") ? "warn" : "pass",
    criteria: rows.map(([cid, r]) => ({ id: cid, label: "", result: r, why: `${cid} ${r}`, evidence_refs: ref })),
  });

const ranking = {
  episode_id: EP,
  strategy_set_id: "s1",
  order: [CAND_A, CAND_B, CAND_C],
  preferred_candidate_id: CAND_A,
  tier_inputs: [
    { candidate_id: CAND_A, blocking: false, restricted: false, worker_rank: 1 },
    { candidate_id: CAND_B, blocking: false, restricted: false, worker_rank: 2 },
    { candidate_id: CAND_C, blocking: true, restricted: false, worker_rank: 3 },
  ],
  reasons: [
    { ranked_higher_id: CAND_A, ranked_lower_id: CAND_B, reason: "A asks for the date legal already owes us", evidence_refs: [], knowledge_refs: [] },
    { ranked_higher_id: CAND_B, ranked_lower_id: CAND_C, reason: "C is blocked", evidence_refs: [], knowledge_refs: [] },
  ],
  abstained: false,
  model: "m",
} as unknown as EpisodeRanking;

function input(over: Partial<DecisionInput> = {}): DecisionInput {
  return {
    summary: summary(),
    strategies,
    results: [
      d2(CAND_A, [["grounding", "pass"], ["cta", "warn"], ["recipients", "pass"]]),
      d2(CAND_B, [["grounding", "pass"], ["cta", "pass"]]),
      d2(CAND_C, [["grounding", "fail"], ["cta", "warn"]]),
      result({ gate: "D3", sub_gate: "ranking", verdict: "pass", why: "the rationale matches the evidence", criteria: [{ id: "rationale_matches_basis", label: "", result: "pass", why: "ok", evidence_refs: ref }] }),
      result({ gate: "D4", verdict: "pass", label: "CORRECTS_STRATEGY", observed: "the human chose the second option", why: "the chosen option was not worse evaluated" }),
      result({ gate: "D5", verdict: "pass", observed: "edit class cta; signal moderate" }),
      result({ gate: "D7", verdict: "pass", why: "all dependents recomputed" }),
      result({ gate: "D8", verdict: "pass", lineage: { recompute_of: "x", previous_verdict: "fail" }, why: "intent preserved" }),
    ],
    ranking,
    decision: null,
    inference: { inferred_semantic_delta: { statement: "The human wanted a softer ask.", edit_class: ["cta"], signal_strength: "moderate", confidence: 0.7 }, agreement: "overrode" } as unknown as JudgmentInference,
    recomputation: null,
    titleOf: (id) => (id === "0e7a1000-0000-4000-8000-0000000000b1" ? "Legal gates the close" : null),
    evidenceText: () => "We can sign next week if legal clears the DPA.",
    ...over,
  };
}

describe("three-candidate decision screen (HAR-149 section 4)", () => {
  it("shows the three options side by side in rank order, lettered, with strategy and intent", () => {
    const s = buildDecisionScreen(input());
    expect(s.cards.map((c) => [c.letter, c.title, c.rank])).toEqual([
      ["A", "Ask for the DPA review date", 1],
      ["B", "Wait for legal", 2],
      ["C", "Loop in the CFO", 3],
    ]);
    expect(s.cards[0]!.strategy).toBe("Stronger CTA");
    expect(s.cards[0]!.intent).toMatch(/one-line intent/);
  });

  it("marks recommended on gtm_ai's pick and chosen on the human's", () => {
    const s = buildDecisionScreen(input());
    expect(s.cards.map((c) => [c.letter, c.recommended, c.chosen])).toEqual([
      ["A", true, false],
      ["B", false, true],
      ["C", false, false],
    ]);
  });

  it("gives each option its per-criterion D2 verdicts, worst first, and no invented score", () => {
    const s = buildDecisionScreen(input());
    const c = s.cards[2]!;
    expect(c.verdicts.rows.map((r) => [r.label, r.result])).toEqual([
      ["Grounded in evidence", "fail"],
      ["Call to action", "warn"],
    ]);
    expect(c.verdicts.overall).toBe("fail");
    expect(JSON.stringify(s)).not.toMatch(/"score"\s*:\s*[0-9]/);
    expect(s.scoresNote).toMatch(/verdicts, not scores/i);
  });

  it("an option whose D2 result stored only one verdict says so", () => {
    const s = buildDecisionScreen(input({ results: [result({ gate: "D2", sub_gate: "candidate", judged_object: { type: "StrategyCandidate", id: CAND_A }, verdict: "pass" })] }));
    expect(s.cards[0]!.verdicts.rows).toHaveLength(1);
    expect(s.cards[0]!.verdicts.note).toMatch(/one verdict/i);
    expect(s.cards[1]!.verdicts.overall).toBeNull();
    expect(s.cards[1]!.verdicts.note).toMatch(/not been judged|no result/i);
  });

  it("lists the evidence and the knowledge each option uses, readable", () => {
    const s = buildDecisionScreen(input());
    expect(s.cards[0]!.evidence[0]!.text).toBe("We can sign next week if legal clears the DPA.");
    expect(s.cards[0]!.knowledge).toEqual([{ id: "0e7a1000-0000-4000-8000-0000000000b1", title: "Legal gates the close" }]);
    expect(s.cards[0]!.stateRefs).toEqual(["blockers", "next_step"]);
  });

  it("flags an option the checks blocked, from the stored tier inputs", () => {
    const s = buildDecisionScreen(input());
    expect(s.cards.map((c) => c.blocked)).toEqual([false, false, true]);
  });

  it("Why A won: the stored D3 rationale, pair by pair, plus the D3 verdict", () => {
    const s = buildDecisionScreen(input());
    expect(s.whyWon?.heading).toBe("Why A won");
    expect(s.whyWon?.reasons.map((r) => [r.higher, r.lower, r.text])).toEqual([
      ["A", "B", "A asks for the date legal already owes us"],
      ["B", "C", "C is blocked"],
    ]);
    expect(s.whyWon?.d3?.verdict).toBe("pass");
    expect(s.whyWon?.abstained).toBe(false);
  });

  it("without a stored ranking it says so and invents no reasons", () => {
    const s = buildDecisionScreen(input({ ranking: null }));
    expect(s.whyWon?.reasons).toEqual([]);
    expect(s.whyWon?.missing).toMatch(/not stored/i);
  });

  it("when the human picked differently it says so and shows the D4 interpretation", () => {
    const s = buildDecisionScreen(input());
    expect(s.human?.choseDifferent).toBe(true);
    expect(s.human?.headline).toMatch(/chose Option B/i);
    expect(s.human?.d4).toMatchObject({ verdict: "pass", label: "Corrects strategy" });
    expect(s.human?.interpretation).toBe("The human wanted a softer ask.");
  });

  it("when the human agreed it says so", () => {
    const s = buildDecisionScreen(input({ summary: summary({ selected_action: { candidate_id: CAND_A, ranking: 1 }, human_outcome: { agreement: "agreed", human_action: "APPROVE_UNCHANGED", send_decision: "send", edited: false } }) }));
    expect(s.human?.choseDifferent).toBe(false);
    expect(s.human?.headline).toMatch(/agreed/i);
    expect(s.edit).toBeNull();
  });

  it("before anyone chooses there is no human section", () => {
    const s = buildDecisionScreen(input({ summary: summary({ selected_action: null, human_outcome: null }), results: [] }));
    expect(s.human).toBeNull();
    expect(s.cards.every((c) => !c.chosen)).toBe(true);
  });

  it("an edit shows D5, what was invalidated and recomputed (D7) and the D8 re-run result", () => {
    const recomputation = {
      status: "reevaluated",
      edited: true,
      semantic_labels: [],
      account_state: { account_id: "a", version_before: 3, version_after: 3, preserved: true },
      entries: [{ index: 0, edit: { field: "body", kind: "paragraph_edited", before: "a", after: "b", semantic_class: "content_change" }, invalidated: [{ kind: "final_artifact", ref_id: null, field: "body", label: "Final artifact", reason: "the body changed", verdict: "pass", replaces: null }], recomputed: [{ kind: "eval_result", ref_id: null, field: null, label: "grounding", reason: "re-evaluated", verdict: "pass", replaces: "x" }], not_recomputed: [], preserved: [] }],
      preserved_overall: [],
    } as unknown as DependencyInvalidation;
    const s = buildDecisionScreen(input({ recomputation }));
    expect(s.edit?.d5?.verdict).toBe("pass");
    expect(s.edit?.d7?.verdict).toBe("pass");
    expect(s.edit?.d8).toMatchObject({ verdict: "pass", recomputed: true, previousVerdict: "fail" });
    expect(s.edit?.recomputation?.entries[0]!.invalidated[0]!.label).toBe("Final artifact");
    expect(s.edit?.recomputation?.entries[0]!.reevaluated[0]!.label.length).toBeGreaterThan(0);
  });

  it("the ranking line shows display labels and capitalised sentences, never a raw criterion id", () => {
    const why = "uncertainty_reflected: the ranking is more certain than the evidence; no_blocked_preferred: a blocked option is first";
    const s = buildDecisionScreen(input({ results: input().results.map((r) => (r.gate === "D3" ? { ...r, verdict: "warn", why } : r)) }));
    const text = s.whyWon?.d3?.why ?? "";
    expect(text).not.toMatch(/[a-z]+_[a-z]+/);
    expect(text).toContain("Uncertainty is reflected: the ranking is more certain than the evidence");
    expect(text).toContain("No blocked option is recommended: a blocked option is first");
    expect(text).toMatch(/\.$/);
  });

  it("two identical edit records are one card with a count, not the same card twice", () => {
    const entry = { index: 0, edit: { field: "body", kind: "paragraph_edited", before: "a", after: "b", semantic_class: "content_change" }, invalidated: [{ kind: "final_artifact", ref_id: null, field: "body", label: "Final artifact", reason: "the body changed", verdict: null, replaces: null }], recomputed: [], not_recomputed: [], preserved: [] };
    const recomputation = { status: "reevaluated", edited: true, semantic_labels: [], account_state: { account_id: "a", version_before: 3, version_after: 3, preserved: true }, entries: [entry, { ...entry, index: 1 }], preserved_overall: [] } as unknown as DependencyInvalidation;
    const s = buildDecisionScreen(input({ recomputation }));
    const entries = s.edit!.recomputation!.entries;
    expect(entries).toHaveLength(1);
    expect(entries[0]!.edit).toBe("body: paragraph edited (2 edits)");
  });

  it("edits that got different re-check results, reasons or text stay separate cards", () => {
    const ev = (verdict: string, reason = "re-evaluated") => ({ kind: "eval_result", ref_id: null, field: null, label: "grounding", reason, verdict, replaces: "x" });
    const entry = (index: number, after: string, recomputed: unknown[]) => ({ index, edit: { field: "body", kind: "paragraph_edited", before: "a", after, semantic_class: "content_change" }, invalidated: [], recomputed, not_recomputed: [], preserved: [] });
    const build = (entries: unknown[]) => ({ status: "reevaluated", edited: true, semantic_labels: [], account_state: { account_id: "a", version_before: 3, version_after: 3, preserved: true }, entries, preserved_overall: [] }) as unknown as DependencyInvalidation;
    const verdicts = buildDecisionScreen(input({ recomputation: build([entry(0, "b", [ev("pass")]), entry(1, "b", [ev("fail")])]) })).edit!.recomputation!.entries;
    expect(verdicts).toHaveLength(2);
    expect(verdicts.map((e) => e.reevaluated[0]!.verdict)).toEqual(["now pass", "now fail"]);
    const reasons = buildDecisionScreen(input({ recomputation: build([entry(0, "b", [ev("pass", "one")]), entry(1, "b", [ev("pass", "two")])]) })).edit!.recomputation!.entries;
    expect(reasons).toHaveLength(2);
    const texts = buildDecisionScreen(input({ recomputation: build([entry(0, "b", [ev("pass")]), entry(1, "c", [ev("pass")])]) })).edit!.recomputation!.entries;
    expect(texts).toHaveLength(2);
    expect(texts.every((e) => !/edits\)/.test(e.edit))).toBe(true);
  });

  it("is empty-safe: no strategy set gives no cards", () => {
    const s = buildDecisionScreen(input({ strategies: null }));
    expect(s.cards).toEqual([]);
    expect(s.whyWon).toBeNull();
  });
});

void (undefined as unknown as HumanStrategyDecision);
