// The eval pages render fixture EvalResults as if a real core had produced them, so the fixtures must be
// honest beyond schema validity: every result agrees with the eval catalog (kind, the ONE evidence class,
// the label vocabulary, whether it may block), its evidence resolves to a recorded activity and person,
// nothing it cites happened after it was judged, and the human decision comes after the judgments.
import { describe, expect, it } from "vitest";
import type { EvalResult, HumanStrategyDecision, RunStrategies, RunTrace } from "@/lib/api/types";
import { loadExample, loadFixture } from "./contract-validator";
import { catalogIssues } from "./eval-honesty";

const strategies = loadFixture<RunStrategies>("acme.run-strategies.json");
const trace = loadFixture<RunTrace>("acme.run-trace.json");
const decision = loadExample<HumanStrategyDecision>("human_strategy_decision");
const bundleExample = loadExample<RunStrategies["eval_bundles"][number]>("eval_bundle");
const resultExample = loadExample<EvalResult>("eval_result");

const fixtureResults = strategies.eval_bundles.flatMap((b) => b.items.flatMap((i) => (i.result ? [i.result] : [])));
const exampleResults = [...bundleExample.items.flatMap((i) => (i.result ? [i.result] : [])), resultExample];

describe("eval fixtures agree with the eval catalog", () => {
  it("the check is not vacuous", () => {
    const wrong = { ...resultExample, evidence_class: "deal_data", label: "TOO_EARLY" } as EvalResult;
    expect(catalogIssues(wrong)).toHaveLength(2);
  });

  it.each(fixtureResults.map((r) => [r.eval_type, r] as const))("fixture result %s", (_type, r) => {
    expect(catalogIssues(r)).toEqual([]);
  });

  it.each(exampleResults.map((r) => [r.eval_type, r] as const))("contract example result %s", (_type, r) => {
    expect(catalogIssues(r)).toEqual([]);
  });
});

describe("eval fixture evidence resolves and respects time", () => {
  const activities = new Map([...trace.trigger_activities, ...(trace.correlated_activities ?? [])].map((a) => [a.id, a]));
  const people = new Set([
    ...(trace.state_at_run?.buying_group ?? []).map((m) => m.person_id),
    ...[...activities.values()].flatMap((a) => a.participants.flatMap((p) => (p.person_id ? [p.person_id] : []))),
  ]);

  it.each(fixtureResults.map((r) => [r.id, r] as const))("result %s", (_id, r) => {
    expect(r.agent_run_id).toBe(trace.run.id);
    for (const ref of r.evidence_refs) {
      const activity = activities.get(ref.activity_id);
      expect(activity, `activity ${ref.activity_id}`).toBeDefined();
      if (ref.speaker_person_id) expect(people.has(ref.speaker_person_id), `speaker ${ref.speaker_person_id}`).toBe(true);
      if (ref.occurred_at) {
        expect(ref.occurred_at).toBe(activity?.occurred_at);
        // No future evidence: a judgment never cites something that happened after it.
        expect(Date.parse(ref.occurred_at)).toBeLessThanOrEqual(Date.parse(r.created_at));
      }
    }
    for (const a of r.activity_refs ?? []) expect(activities.has(a), `activity_ref ${a}`).toBe(true);
    // The human chose after gtm_ai judged.
    expect(Date.parse(r.created_at)).toBeLessThan(Date.parse(decision.chosen_at));
  });

  it("every quote is verbatim in the recorded source email", () => {
    const email = loadExample<{ payload: { body_text: string } }>("source_event").payload.body_text;
    for (const r of fixtureResults) for (const ref of r.evidence_refs) if (ref.quote) expect(email, `${r.id}: ${ref.quote}`).toContain(ref.quote);
  });

  it("each result cites the sentence its reason relies on (audited by hand, pinned here)", () => {
    const SOC2 = "Before we can sign off on the EU rollout we'll need your SOC2 Type II report and the pen-test summary.";
    const NO_DATE = "I can't commit to a review date until our team has read them.";
    const PRIYA = "Priya will stay the point of contact on the commercial side.";
    const cited: Record<string, string[]> = {
      // A: the dated ask ignores "no date yet"; dropping Priya breaks "Priya stays the contact"; both statements quoted.
      "0e1a0000-0000-4000-8000-000000000910": [NO_DATE],
      "0e1a0000-0000-4000-8000-000000000911": [PRIYA],
      "0e1a0000-0000-4000-8000-000000000912": [SOC2, NO_DATE],
      // B: Priya kept on cc; the call offer should wait for Marco's timing.
      "0e1a0000-0000-4000-8000-000000009200": [PRIYA],
      "0e1a0000-0000-4000-8000-000000009201": [NO_DATE],
      // C: checking the gate does not deliver the documents Marco asked for; the blocker is quoted.
      "0e1a0000-0000-4000-8000-000000009300": [SOC2],
      "0e1a0000-0000-4000-8000-000000009301": [SOC2],
    };
    expect(Object.fromEntries(fixtureResults.map((r) => [r.id, r.evidence_refs.map((e) => e.quote)]))).toEqual(cited);
  });

  it("each bundle's results belong to its own draft", () => {
    for (const b of strategies.eval_bundles) {
      for (const item of b.items) if (item.result) expect(item.result.draft_index).toBe(b.draft_index);
    }
  });
});
