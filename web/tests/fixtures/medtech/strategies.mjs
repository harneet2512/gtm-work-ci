// The three complete strategies Ghost drafted after Event 13, and the routed evals of each. The verdicts
// are hand-authored against the real emails (no judge was called for these fixtures) and audited: every
// reason cites the sentence it relies on, kinds, evidence classes, labels and blocking follow
// contracts/evals/eval_catalog.json (web/tests/medtech-fixtures.honesty.test.ts pins all of it).
import { activityRef, eventAt, HELD_OUT, IDS, PEOPLE, plusSeconds, ref } from "./case.mjs";

const AT = eventAt(HELD_OUT);
const JUDGED_AT = plusSeconds(AT, 70);
const JUDGE = "qwen/qwen3.8-flash";
const SUBJECT = "Re: Feedback request on proposal draft";
const TIMES = "would Monday, November 13 at 10:00 or Tuesday, November 14 at 14:00 suit you?";

export const BODIES = {
  A: `Hi Fatoumata,\n\nThank you for the questions. A call is a good idea: ${TIMES}\n\nBefore the call I will send a breakdown of the long-term costs of running EduTech Lab and SecureData Nexus alongside your existing systems, so we can spend the time on your questions rather than the numbers. I can also bring our implementation lead to cover the onboarding support your team mentioned.\n\nBest regards,\nLuis`,
  B: `Hi Fatoumata,\n\nThank you for the questions. Here is how the proposal we sent on October 31 breaks down:\n\n- SecureData Nexus: 25 units at a 15% discount, $12,749.79\n- CryptGuard Module: 20 units at a 15% discount, $8,329.83\n- EduTech Lab: 10 units at a 10% discount, $3,599.91\n\nThe total is $24,679.53. Once you have had a chance to review this with your team, I would be glad to set up a call to go through integration and support in more detail.\n\nBest regards,\nLuis`,
  C: `Hi Fatoumata,\n\nThank you for being open about the alternatives you are weighing. To make the decision easier, we can include onboarding support for your team at no extra cost and take an additional 5% off the proposal if you confirm the order by November 30.\n\nWould that work for you?\n\nBest regards,\nLuis`,
};

export const B_CTA_BEFORE = "Once you have had a chance to review this with your team, I would be glad to set up a call to go through integration and support in more detail.";
export const B_CTA_AFTER = `I would also like to take you up on the call: ${TIMES}`;

const TO_FATOUMATA = { person_id: PEOPLE.fatoumata.id, role: "to", why: "Asked the cost question and for the call" };

function result(letter, n, draft, e) {
  return {
    id: IDS.result(letter, n),
    agent_run_id: IDS.run,
    draft_index: draft,
    eval_type: e.type,
    eval_version: `${e.type}:v1`,
    kind: e.kind ?? "semantic",
    verdict: e.verdict,
    label: e.label ?? null,
    diagnostics: e.diagnostics ?? [],
    score: null,
    blocking: e.blocking ?? false,
    reason: e.reason,
    state_refs: e.state,
    activity_refs: e.activities ?? [IDS.activity(HELD_OUT)],
    evidence_refs: e.evidence,
    knowledge_refs: [],
    suggested_correction: e.fix ?? null,
    confidence: null,
    evidence_class: e.cls,
    model: e.kind === "deterministic" ? null : JUDGE,
    created_at: JUDGED_AT,
  };
}

const EVALS = {
  A: [
    { type: "state_change_relevance", why: "Event 13 raised a new cost question and asked for a call.", verdict: "pass", cls: "methodology", state: ["objections", "next_meeting"], evidence: [ref("costQuestion"), ref("callAsk")], reason: "It answers both things Fatoumata raised on Nov 9: the long-term cost question and the request for a call." },
    { type: "cta_calibration", why: "The draft asks for a meeting.", verdict: "pass", label: "APPROPRIATE", cls: "methodology", state: ["next_meeting"], evidence: [ref("callAsk")], reason: "Fatoumata asked for a follow-up call, so offering two times is the size of ask she invited." },
    { type: "stakeholder_coverage", why: "The action is about budget, and one buyer contact is on the deal.", verdict: "warn", label: "SINGLE_THREADED_RISK", diagnostics: ["single_threaded"], cls: "deal_data", state: ["objections"], evidence: [ref("crucial")], reason: "Long-term cost is a budget question, but only Fatoumata is on the thread and nobody who owns the budget has appeared in the deal.", fix: "Ask Fatoumata whether someone who owns the budget should join the call." },
    { type: "next_step_quality", why: "The customer asked for a next step.", verdict: "pass", cls: "deal_data", state: ["next_meeting", "next_milestone"], evidence: [ref("availability")], reason: "The next step is a call at one of two named times, owned by Luis, with the cost breakdown sent first." },
    { type: "grounding", why: "The draft restates what the customer asked.", verdict: "pass", cls: "deal_data", state: ["objections", "decision_criteria"], evidence: [ref("costQuestion"), ref("supportAsk")], activities: [IDS.activity(HELD_OUT), IDS.activity(12)], reason: "Every point repeats what Fatoumata wrote: the cost question on Nov 9 and the implementation support her team asked about on Nov 8." },
    { type: "pricing_integrity", why: "The draft names no price or discount.", verdict: "not_relevant" },
  ],
  B: [
    { type: "pricing_integrity", why: "The draft states prices and discounts.", verdict: "pass", kind: "deterministic", cls: "product_rule", state: [], evidence: [ref("total")], activities: [IDS.activity(3), IDS.activity(4), IDS.activity(9)], reason: "Every price and discount matches the proposal of Oct 31, and the total matches the quote of Oct 15 ($24,679.53)." },
    { type: "state_change_relevance", why: "Event 13 raised a new cost question.", verdict: "warn", cls: "methodology", state: ["objections"], evidence: [ref("costQuestion")], reason: "Fatoumata asked about the long-term cost of integrating EduTech Lab and SecureData Nexus; the draft restates the one-time proposal prices and says nothing about ongoing or integration costs.", fix: "Add what running and integrating the two products costs over time, or say when that will follow." },
    { type: "cta_calibration", why: "The draft defers the meeting the customer asked for.", verdict: "warn", label: "TOO_WEAK", diagnostics: ["ask_too_weak"], cls: "methodology", state: ["next_meeting"], evidence: [ref("callAsk")], reason: "She asked for a call on Nov 9; this puts the call off until after a written review she did not ask for.", fix: "Offer two times for the call alongside the figures." },
    { type: "stakeholder_coverage", why: "The figures are for a team decision, and one buyer contact is on the deal.", verdict: "warn", label: "SINGLE_THREADED_RISK", diagnostics: ["single_threaded"], cls: "deal_data", state: ["current_commitments"], evidence: [ref("coordinate")], activities: [IDS.activity(11)], reason: "The figures go to Fatoumata alone, though she said on Nov 8 that she would gather feedback from her team.", fix: "Offer to walk her team through the figures on the call." },
    { type: "grounding", why: "The draft states figures from the deal.", verdict: "pass", cls: "deal_data", state: [], evidence: [ref("total")], activities: [IDS.activity(4), IDS.activity(9)], reason: "The figures and the discounts are the ones Luis sent on Oct 31 and Nov 8; nothing is added." },
  ],
  C: [
    { type: "pricing_integrity", why: "The draft offers a new discount and free onboarding.", verdict: "fail", kind: "deterministic", cls: "product_rule", blocking: true, state: [], evidence: [ref("total")], activities: [IDS.activity(3), IDS.activity(9)], reason: "The extra 5% discount and the free onboarding are not in the quote of Oct 15, and no approval for either is on record.", fix: "Remove the new discount and offer, or get them approved before sending." },
    { type: "cta_calibration", why: "The draft asks for an order by a fixed date.", verdict: "fail", label: "TOO_STRONG", diagnostics: ["ask_too_strong", "too_early"], cls: "methodology", blocking: true, state: ["next_meeting", "objections"], evidence: [ref("callAsk")], reason: "It asks Fatoumata to confirm the order by Nov 30, but she has only asked questions and for a call; she has not signalled she is ready to buy.", fix: "Answer the cost question and offer the call; leave the order to her." },
    { type: "state_change_relevance", why: "Event 13 raised a new cost question.", verdict: "warn", cls: "methodology", state: ["objections", "commercial_issue"], evidence: [ref("costQuestion"), ref("competitor")], activities: [IDS.activity(HELD_OUT), IDS.activity(12)], reason: "Her Nov 9 email asked about long-term cost and a call; this answers the competitor point from Nov 8 instead." },
    { type: "grounding", why: "The draft refers to the competitor the customer named.", verdict: "pass", cls: "deal_data", state: ["commercial_issue"], evidence: [ref("competitor")], activities: [IDS.activity(12)], reason: "The competitor point is Fatoumata's own: she named Quantum Circuits' initial pricing and onboarding on Nov 8." },
  ],
};

const DRAFT = { A: 1, B: 2, C: 3 };

function bundle(letter) {
  const draft = DRAFT[letter];
  const items = EVALS[letter].map((e, i) =>
    e.verdict === "not_relevant"
      ? { eval_type: e.type, relevance_reason: e.why, verdict: "not_relevant", result: null }
      : { eval_type: e.type, relevance_reason: e.why, verdict: e.verdict, result: result(letter, i + 1, draft, e) },
  );
  return { id: IDS.bundle[letter], agent_run_id: IDS.run, draft_index: draft, strategy_candidate_id: IDS.candidate[letter], generated_at: plusSeconds(AT, 72), items };
}

const FIVE = {
  what_changed: "Fatoumata asked about the long-term cost of integrating EduTech Lab and SecureData Nexus, and for a follow-up call.",
  why_state_changed: "She wrote it first-party on Nov 9, 2023; the deal had no open cost objection and no next meeting before.",
};

const CANDIDATES = {
  A: {
    strategy_type: "propose_call_with_cost_breakdown",
    title: "Book the call, bring the cost model",
    description: "Offer two times for the call Fatoumata asked for, and send the long-term cost breakdown ahead of it.",
    rationale: "Fatoumata asked for a call and called a clear view of future commitments crucial; offering times answers the ask, and the breakdown sent first makes the call about decisions rather than discovery.",
    evidence: [ref("costQuestion"), ref("callAsk")],
    unknown: "Who owns the budget at MedTech, and what the ongoing costs are for her setup.",
    next: "A dated call answers her request; the written breakdown answers the cost question before it.",
  },
  B: {
    strategy_type: "written_answer_then_call",
    title: "Put the costs in writing first",
    description: "Answer the cost question in writing now, from the proposal figures, and offer the call once she has read it.",
    rationale: "Fatoumata said pricing transparency matters to her team and that clarity on future commitments is crucial; the figures in writing give her something to share internally before a call.",
    evidence: [ref("crucial"), ref("transparency")],
    unknown: "Whether the one-time proposal prices are the long-term costs she means.",
    next: "Written figures give her team something concrete before the call.",
  },
  C: {
    strategy_type: "competitive_concession",
    title: "Match Quantum Circuits on onboarding",
    description: "Answer the competitor: offer free onboarding support and an extra 5% off if MedTech confirms the order this month.",
    rationale: "Fatoumata named Quantum Circuits' initial pricing and onboarding as an option she is weighing; matching it could keep the deal from slipping.",
    evidence: [ref("competitor")],
    unknown: "Whether a further discount or free onboarding can be approved.",
    next: "Matching the competitor's onboarding removes the reason to switch.",
  },
};

function candidate(letter, rank) {
  const c = CANDIDATES[letter];
  const body = BODIES[letter];
  return {
    candidate_id: IDS.candidate[letter],
    strategy_type: c.strategy_type,
    title: c.title,
    description: c.description,
    ranking: rank,
    preferred_by_agent: letter === "A",
    rationale: c.rationale,
    state_refs: ["objections", "decision_criteria", "next_meeting", "commercial_issue"],
    evidence_refs: c.evidence,
    knowledge_refs: [],
    action_type: "send_email",
    action_class: "REPLY",
    to: [TO_FATOUMATA],
    cc: [],
    subject: SUBJECT,
    full_action_artifact: { channel: "email", subject: SUBJECT, body, attachments: [] },
    five_questions: { ...FIVE, what_remains_unknown: c.unknown, prior_knowledge_applies: "No company knowledge applies to this situation yet.", why_next_action: c.next },
    preview: `${body.split("\n\n")[1].slice(0, 150)}…`,
    draft_index: DRAFT[letter],
    eval_bundle_ref: IDS.bundle[letter],
  };
}

export function runStrategies() {
  return {
    strategy_set: {
      id: IDS.strategySet,
      decision_episode_id: IDS.episode,
      agent_run_id: IDS.run,
      account_id: IDS.account,
      opportunity_id: IDS.opportunity,
      generated_at: plusSeconds(AT, 90),
      state_ref: { account_id: IDS.account, opportunity_id: IDS.opportunity, version: 108 },
      state_diff_id: IDS.stateDiff,
      trigger_activity_ids: [IDS.activity(HELD_OUT)],
      decision_guidance_id: IDS.guidance,
      no_acceptable_candidate: false,
      candidates: [candidate("A", 1), candidate("B", 2), candidate("C", 3)],
    },
    eval_bundles: [bundle("A"), bundle("B"), bundle("C")],
  };
}

export { activityRef, SUBJECT, TO_FATOUMATA };
