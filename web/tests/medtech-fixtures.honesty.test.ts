// The MedTech eval results are hand-authored over real emails, so they are audited here beyond the schema:
// catalog agreement, evidence that resolves to the trace and is quoted verbatim from the real email it
// names, no judgment citing what came after it, blocking only where the catalog's rule allows, and each
// verdict checked against the facts it states (the figures B quotes are the proposal's; C's discount is not).
import { describe, expect, it } from "vitest";
import type { EvalResult, RunStrategies, RunTrace } from "@/lib/api/types";
import { loadFixture } from "./contract-validator";
import { catalogIssues } from "./eval-honesty";

const strategies = loadFixture<RunStrategies>("medtech.run-strategies.json");
const trace = loadFixture<RunTrace>("medtech.run-trace.json");
const sources = loadFixture<{ emails: { salesforce_id: string; body_text: string; position: number }[] }>("medtech.source-emails.json");

const results = strategies.eval_bundles.flatMap((b) => b.items.flatMap((i) => (i.result ? [i.result] : [])));
const activities = new Map([...trace.trigger_activities, ...(trace.correlated_activities ?? [])].map((a) => [a.id, a]));
const bodyOf = new Map(sources.emails.map((e) => [e.salesforce_id, e.body_text]));
const people = new Set((trace.state_at_run?.buying_group ?? []).map((m) => m.person_id).concat([...activities.values()].flatMap((a) => a.participants.flatMap((p) => (p.person_id ? [p.person_id] : [])))));
const byLetter = (letter: "A" | "B" | "C") => strategies.strategy_set.candidates[letter.charCodeAt(0) - 65]!;
const email = (pos: number) => sources.emails.find((e) => e.position === pos)!.body_text;

describe("MedTech results agree with the eval catalog", () => {
  it.each(results.map((r) => [r.id, r] as const))("%s", (_id, r) => {
    expect(catalogIssues(r)).toEqual([]);
  });
});

describe("MedTech evidence resolves, is verbatim and is never from the future", () => {
  const refs = [
    ...results.flatMap((r) => r.evidence_refs.map((e) => [r.id, e] as const)),
    ...strategies.strategy_set.candidates.flatMap((c) => c.evidence_refs.map((e) => [c.candidate_id, e] as const)),
  ];

  it.each(refs)("%s cites a recorded activity and person", (_id, ref) => {
    const a = activities.get(ref.activity_id);
    expect(a, ref.activity_id).toBeDefined();
    if (ref.speaker_person_id) expect(people.has(ref.speaker_person_id)).toBe(true);
    if (ref.occurred_at) expect(ref.occurred_at).toBe(a?.occurred_at);
  });

  it.each(refs.filter(([, r]) => r.quote))("%s quotes its email verbatim", (_id, ref) => {
    const a = activities.get(ref.activity_id)!;
    expect(bodyOf.get(a.source_object_id), a.source_object_id).toContain(ref.quote);
  });

  it.each(results.map((r) => [r.id, r] as const))("%s cites nothing after it was judged", (_id, r: EvalResult) => {
    for (const ref of r.evidence_refs) expect(Date.parse(ref.occurred_at ?? "")).toBeLessThanOrEqual(Date.parse(r.created_at));
    for (const id of r.activity_refs ?? []) expect(Date.parse(activities.get(id)?.occurred_at ?? "")).toBeLessThanOrEqual(Date.parse(r.created_at));
  });

  it("each bundle's results belong to its own draft and run", () => {
    for (const b of strategies.eval_bundles) for (const i of b.items) if (i.result) expect([i.result.draft_index, i.result.agent_run_id]).toEqual([b.draft_index, trace.run.id]);
  });
});

describe("MedTech verdicts agree with their reasons (audited by hand, pinned here)", () => {
  it("pins every verdict and the sentence each relies on", () => {
    const pinned = Object.fromEntries(
      strategies.eval_bundles.flatMap((b, i) =>
        b.items.map((item) => [`${String.fromCharCode(65 + i)}:${item.eval_type}`, [item.verdict, item.result?.blocking ?? false, (item.result?.evidence_refs ?? []).map((e) => e.quote?.slice(0, 41))]]),
      ),
    );
    expect(pinned).toEqual({
      "A:state_change_relevance": ["pass", false, ["That said, I have some questions regardin", "Could we schedule a follow-up call to dis"]],
      "A:cta_calibration": ["pass", false, ["Could we schedule a follow-up call to dis"]],
      "A:stakeholder_coverage": ["warn", false, ["Ensuring that we have a clear understandi"]],
      "A:next_step_quality": ["pass", false, ["Please let me know your availability."]],
      "A:grounding": ["pass", false, ["That said, I have some questions regardin", "Our team would also like to discuss the p"]],
      "A:pricing_integrity": ["not_relevant", false, []],
      "B:pricing_integrity": ["pass", false, ["Our total proposal amount of $24,679.53 r"]],
      "B:state_change_relevance": ["warn", false, ["That said, I have some questions regardin"]],
      "B:cta_calibration": ["warn", false, ["Could we schedule a follow-up call to dis"]],
      "B:stakeholder_coverage": ["warn", false, ["I’ll coordinate internally to gather any "]],
      "B:grounding": ["pass", false, ["Our total proposal amount of $24,679.53 r"]],
      "C:pricing_integrity": ["fail", true, ["Our total proposal amount of $24,679.53 r"]],
      "C:cta_calibration": ["fail", true, ["Could we schedule a follow-up call to dis"]],
      "C:state_change_relevance": ["warn", false, ["That said, I have some questions regardin", "Quantum Circuits Inc. offers competitive "]],
      "C:grounding": ["pass", false, ["Quantum Circuits Inc. offers competitive "]],
    });
  });

  it("A's pass on CTA calibration: the buyer asked for a call and the draft offers two times", () => {
    expect(email(13)).toContain("Could we schedule a follow-up call");
    expect(byLetter("A").full_action_artifact.body).toMatch(/Monday, November 13 at 10:00 or Tuesday, November 14 at 14:00/);
  });

  it("A's not-relevant pricing: the draft names no price or discount", () => {
    expect(byLetter("A").full_action_artifact.body).not.toMatch(/\$|discount|%/);
  });

  it("B's pass on pricing: every figure and discount is in the Oct 31 proposal, the total in the Nov 8 follow-up", () => {
    const lines = byLetter("B").full_action_artifact.body.split("\n").filter((l) => l.startsWith("- "));
    expect(lines).toHaveLength(3);
    for (const line of lines) {
      const [, product, units, pct, price] = /^- (.+): (\d+) units at a (\d+)% discount, (\$[\d,.]+)$/.exec(line) ?? [];
      expect(email(4)).toContain(`**${product}**: ${units} units at a ${pct}% discount for a total price of ${price}`);
    }
    expect(email(9)).toContain("$24,679.53");
  });

  it("B's warn on CTA calibration: the draft defers the call she asked for", () => {
    expect(byLetter("B").full_action_artifact.body).toContain("Once you have had a chance to review");
    expect(byLetter("B").full_action_artifact.body).not.toMatch(/November 1[34]/);
  });

  it("C's blocking fails: a discount and an order deadline no source offered or asked for", () => {
    const body = byLetter("C").full_action_artifact.body;
    expect(body).toMatch(/additional 5% off/);
    expect(body).toMatch(/confirm the order by November 30/);
    for (const e of sources.emails) {
      expect(e.body_text).not.toMatch(/additional 5%|5% off/);
      expect(e.body_text).not.toMatch(/confirm the order/i);
    }
  });
});
