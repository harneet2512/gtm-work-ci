// The run-chain view model (WP24): the "why did this action change" comparison is honest — the
// inference's own numbers when it exists, a computed bundle diff when it does not, never a
// fabricated counterfactual.
import { describe, expect, it } from "vitest";
import { buildChain, buildWhyChanged, bundleFor, knowledgeIds, knowledgeLine, literalEdits, verdictCounts } from "@/lib/view/run-chain";
import type { HumanStrategyDecision, JudgmentInference, Knowledge, RunStrategies } from "@/lib/api/types";
import { loadExample, loadFixture } from "./contract-validator";

const strategies = loadFixture<RunStrategies>("acme.run-strategies.json");
const decision = loadExample<HumanStrategyDecision>("human_strategy_decision");
const inference = loadExample<JudgmentInference>("judgment_inference");
const knowledge = loadExample<Knowledge>("knowledge");

const A1 = "0ca00000-0000-4000-8000-0000000000a1"; // Ghost's pick
const A2 = "0ca00000-0000-4000-8000-0000000000a2"; // the human's choice

describe("buildChain", () => {
  it("pairs each candidate with its eval bundle and counts the verdicts", () => {
    const chain = buildChain(strategies);
    expect(chain).toHaveLength(3);
    const [first, second, third] = chain;
    // Candidate 1: the cta_calibration fail plus two passes.
    expect(first!.counts).toMatchObject({ fail: 2, pass: 1 });
    expect(first!.attention.map((i) => i.eval_type)).toEqual(["cta_calibration", "champion_continuity"]);
    // Candidate 2 (the example bundle) and 3 keep their own counts.
    expect(second!.bundle!.strategy_candidate_id).toBe(A2);
    expect(third!.counts.warn).toBe(1);
    expect(third!.counts.notRelevant).toBe(1);
    expect(third!.attention.map((i) => i.eval_type)).toEqual(["next_step_quality"]);
  });

  it("is empty (never a crash) when no strategy set exists", () => {
    expect(buildChain(null)).toEqual([]);
  });
});

describe("bundleFor", () => {
  it("resolves by eval_bundle_ref, falls back to the candidate FK, and is null without either", () => {
    const [c1] = strategies.strategy_set.candidates;
    expect(bundleFor(c1!, strategies.eval_bundles)!.id).toBe(c1!.eval_bundle_ref);
    // A dangling ref still finds the FK'd bundle.
    const dangling = { ...c1!, eval_bundle_ref: "0eb00000-0000-4000-8000-00000000ffff" };
    expect(bundleFor(dangling, strategies.eval_bundles)!.strategy_candidate_id).toBe(c1!.candidate_id);
    const lonely = { ...c1!, candidate_id: "0ca00000-0000-4000-8000-00000000fff0", eval_bundle_ref: undefined };
    expect(bundleFor(lonely, strategies.eval_bundles)).toBeNull();
  });
});

describe("verdictCounts", () => {
  it("is all zeros for a missing bundle", () => {
    expect(verdictCounts(null)).toEqual({ pass: 0, warn: 0, fail: 0, abstain: 0, notRelevant: 0 });
  });
});

describe("knowledgeIds", () => {
  it("collects every id the chain cites: candidates, eval results, the inference", () => {
    const ids = knowledgeIds(strategies, inference);
    expect(ids).toContain("0c17c000-0000-4000-8000-000000000017"); // champion-continuity result + candidate
    expect([...ids]).toEqual([...ids].sort());
  });

  it("is empty when nothing cites knowledge", () => {
    expect(knowledgeIds(null, null)).toEqual([]);
  });
});

describe("buildWhyChanged — with the judgment inference", () => {
  const why = buildWhyChanged(strategies, decision, inference);

  it("records an override: Ghost preferred a1, the human chose a2", () => {
    expect(why.decided).toBe(true);
    expect(why.agreed).toBe(false);
    expect(why.preferredId).toBe(A1);
    expect(why.chosenId).toBe(A2);
    expect(why.preferredTitle).toBeTruthy();
    expect(why.chosenTitle).toBeTruthy();
    expect(why.comparisonOnly).toBe(false);
  });

  it("takes the eval differences from the inference's own record, not a recomputation", () => {
    expect(why.evalDifferences.length).toBe(inference.evidence.eval_differences.length);
    const first = why.evalDifferences[0]!;
    expect(first.evalType).toBe(inference.evidence.eval_differences[0]!.eval_type);
    expect(first.preferredVerdict).toBe(inference.evidence.eval_differences[0]!.agent_preference_verdict);
    expect(first.chosenVerdict).toBe(inference.evidence.eval_differences[0]!.human_choice_verdict);
  });

  it("carries the semantic delta, labels and the human's verdict on it", () => {
    expect(why.statement).toBe(inference.inferred_semantic_delta.statement);
    expect(why.semanticLabels).toEqual(inference.inferred_semantic_delta.semantic_labels);
    expect(why.humanVerdict).toBe(inference.human_verdict);
    expect(why.candidateDifferences).toEqual(inference.evidence.candidate_differences);
    expect(why.noApplicableKnowledge).toBe(inference.evidence.no_applicable_knowledge);
  });
});

describe("buildWhyChanged — without the inference (the honest fallback)", () => {
  const why = buildWhyChanged(strategies, decision, null);

  it("still answers with the bundle diff and is marked comparison-only", () => {
    expect(why.comparisonOnly).toBe(true);
    expect(why.statement).toBeNull();
    // cta_calibration differs between the bundles: fail on the preferred, warn on the chosen.
    const diff = why.evalDifferences.find((d) => d.evalType === "cta_calibration");
    expect(diff).toMatchObject({ preferredVerdict: "fail", chosenVerdict: "warn", note: null });
    // No candidate-level text, no semantic delta: only what the bundles record.
    expect(why.candidateDifferences).toEqual([]);
    expect(why.noApplicableKnowledge).toBeNull();
  });
});

describe("buildWhyChanged — nobody decided yet, or no set", () => {
  it("a pending run is undecided: the panel says so instead of guessing", () => {
    const why = buildWhyChanged(strategies, null, null);
    expect(why.decided).toBe(false);
    expect(why.agreed).toBe(false);
    expect(why.chosenId).toBeNull();
    // The preferred side still renders so the knowledge comparison has content.
    expect(why.preferredId).toBe(A1);
  });

  it("agreement: choosing the preferred candidate marks the decision agreed", () => {
    const agree: HumanStrategyDecision = { ...decision, selected_candidate_id: A1 };
    const why = buildWhyChanged(strategies, agree, null);
    expect(why.decided).toBe(true);
    expect(why.agreed).toBe(true);
  });

  it("no strategies at all: nothing to compare", () => {
    const why = buildWhyChanged(null, null, null);
    expect(why.decided).toBe(false);
    expect(why.preferredId).toBeNull();
    expect(why.evalDifferences).toEqual([]);
  });
});

describe("literalEdits and knowledgeLine", () => {
  it("returns the human's literal changes (the semantic delta's input)", () => {
    expect(literalEdits(decision)).toEqual(decision.edits);
    expect(literalEdits(null)).toEqual([]);
  });

  it("renders a cited knowledge object as 'K<n>: title', and degrades to the id when it failed to load", () => {
    expect(knowledgeLine(knowledge.id, knowledge)).toEqual({ id: knowledge.id, label: `${knowledge.key}: ${knowledge.title}`, status: knowledge.status });
    const missing = knowledgeLine("0c17c000-0000-4000-8000-0000000000ff", null);
    expect(missing.label).toContain("0c17c000");
    expect(missing.status).toBeNull();
  });
});
