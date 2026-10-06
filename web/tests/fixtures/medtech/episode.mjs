// The decision episode around Event 13: the agent run and its trace, Luis's choice (the written answer,
// with his edit that adds the call times) and gtm_ai's inference of why. Times run on the replay clock
// from the event's own instant. Luis's choice and edit are the demo's synthetic human layer over the
// real CRM data; nothing here claims Luis Rodriguez did this in the dataset.
import { activity, eventAt, HELD_OUT, IDS, PEOPLE, plusSeconds, ref } from "./case.mjs";
import { B_CTA_AFTER, B_CTA_BEFORE, BODIES, runStrategies, SUBJECT, TO_FATOUMATA } from "./strategies.mjs";
import { signals, stateAfter, stateBefore, stateDiff } from "./world.mjs";

const AT = eventAt(HELD_OUT);
const WORLD_AS_OF = `${AT.slice(0, -1)}.000001Z`;
const CHOSEN_AT = plusSeconds(AT, 22 * 60);
const SENT_AT = plusSeconds(AT, 28 * 60);
const JUDGE = "qwen/qwen3.8-flash";

export const FINAL_BODY = BODIES.B.replace(B_CTA_BEFORE, B_CTA_AFTER);

const contextRefs = [
  { tool: "state", access_id: 2101, returned_ids: [IDS.account], bytes: 3410 },
  { tool: "evidence", access_id: 2102, returned_ids: [IDS.claim(7), IDS.claim(8), IDS.claim(10)], bytes: 1180 },
];

const step = (seq, name, status, from, to, effect = null) => ({ seq, step: name, status, started_at: plusSeconds(AT, from), finished_at: plusSeconds(AT, to), external_effect_id: effect, detail: {} });

export function agentRun() {
  const a = runStrategies().strategy_set.candidates[0];
  return {
    id: IDS.run,
    account_id: IDS.account,
    opportunity_id: IDS.opportunity,
    workflow: "post_interaction_followup",
    run_mode: "dry_run",
    status: "recorded", // a dry run records the send (ADR-0015); only a live run is "executed"
    trigger_evaluation_id: IDS.triggerEvaluation,
    trigger_activity_ids: [IDS.activity(HELD_OUT)],
    correlation_id: IDS.correlation,
    state_version: 108,
    input_context_refs: contextRefs,
    output: {
      proposed_action_type: "send_email",
      recipients: [TO_FATOUMATA],
      finished_artifact: a.full_action_artifact,
      crm_next_step_intent: { next_step: "Follow-up call with Fatoumata on long-term costs", due_at: null, stage_change: null },
      reason: "Fatoumata asked about long-term integration costs and for a follow-up call.",
      evidence_refs: [ref("costQuestion", IDS.claim(7)), ref("callAsk", IDS.claim(8))],
      knowledge_refs_used: [],
      wait_until: null,
    },
    evidence_refs: [ref("costQuestion", IDS.claim(7)), ref("callAsk", IDS.claim(8))],
    knowledge_refs_used: [],
    model: JUDGE,
    steps: [
      step(1, "build_context", "succeeded", 12, 14),
      step(2, "draft", "succeeded", 14, 68),
      step(3, "crm_intent", "recorded", 68, 68),
      step(4, "await_human", "succeeded", 90, 28 * 60),
      step(5, "execute", "recorded", 28 * 60 + 2, 28 * 60 + 3),
    ],
    error: null,
    generation: { phase: "published", attempt: 1, reason: null, strategy_set_id: IDS.strategySet, decision_episode_id: IDS.episode },
    created_at: plusSeconds(AT, 12),
    updated_at: plusSeconds(AT, 28 * 60 + 3),
  };
}

const finalArtifact = () => ({ channel: "email", subject: SUBJECT, body: FINAL_BODY, attachments: [] });

export function runTrace() {
  return {
    run: agentRun(),
    trigger_activities: [activity(HELD_OUT)],
    correlated_activities: [3, 4, 6, 9, 11, 12].map(activity),
    state_before: stateBefore(),
    state_at_run: stateAfter(),
    state_diff: stateDiff(),
    signals: signals(),
    trigger_evaluation: {
      id: IDS.triggerEvaluation,
      account_id: IDS.account,
      workflow: "post_interaction_followup",
      eligible: true,
      reason_codes: ["eligible_customer_replied"],
      explanation: "Fatoumata replied with a new cost question and asked for a call; no open run.",
      signal_ids: [IDS.signal],
      state_diff_id: IDS.stateDiff,
      agent_run_id: IDS.run,
      evaluated_at: plusSeconds(AT, 12),
    },
    context_accesses: contextRefs.map((r) => ({
      access_id: r.access_id,
      tool: r.tool,
      items: r.returned_ids.map((id) => ({ id })),
      truncated: false,
      bytes: r.bytes,
      world_as_of: WORLD_AS_OF,
      limits: { max_items: 10, max_bytes: 8192 },
    })),
    decisions: [
      {
        id: IDS.humanDecision,
        agent_run_id: IDS.run,
        decision: "edit",
        surface: "slack",
        actor_person_id: PEOPLE.luis.id,
        actor_label: PEOPLE.luis.name,
        edited_artifact: finalArtifact(),
        reason: "Put the figures in writing and offered the call times in the same email.",
        created_at: SENT_AT,
      },
    ],
    placeholders: { eval_runs: [], customer_reactions: [], knowledge_updates: [] },
  };
}

export function strategyDecision() {
  return {
    id: IDS.decision,
    decision_episode_id: IDS.episode,
    agent_run_id: IDS.run,
    strategy_set_id: IDS.strategySet,
    selected_candidate_id: IDS.candidate.B,
    original_agent_preference: IDS.candidate.A,
    surface: "slack",
    actor_person_id: PEOPLE.luis.id,
    actor_label: PEOPLE.luis.name,
    chosen_at: CHOSEN_AT,
    final_to: [TO_FATOUMATA],
    final_cc: [],
    final_artifact: finalArtifact(),
    edits: [{ kind: "cta_changed", before: B_CTA_BEFORE, after: B_CTA_AFTER }, { kind: "paragraph_edited" }],
    send_decision: "send",
    send_decided_at: SENT_AT,
    human_decision_id: IDS.humanDecision,
  };
}

export function judgmentInference() {
  return {
    id: IDS.inference,
    decision_episode_id: IDS.episode,
    human_strategy_decision_id: IDS.decision,
    agent_preference: IDS.candidate.A,
    human_choice: IDS.candidate.B,
    agreement: "overrode",
    inferred_semantic_delta: {
      statement:
        "Luis wanted the figures in writing before the call: Fatoumata said a clear understanding of future financial commitments is crucial, and the day before that pricing transparency is something her team values highly.",
    },
    evidence: {
      candidate_differences: [
        "gtm_ai's pick offers call times and promises the cost breakdown later; the chosen option puts the proposal figures in writing now, and Luis's edit adds the same call times.",
      ],
      evidence_refs: [ref("crucial"), ref("transparency")],
      eval_differences: [
        { eval_type: "cta_calibration", agent_preference_verdict: "pass", human_choice_verdict: "warn", note: "gtm_ai's pick matched her call request; Luis's edit added the call times to the written answer." },
        { eval_type: "pricing_integrity", agent_preference_verdict: "not_relevant", human_choice_verdict: "pass", note: "Only the chosen option states prices, and they match the quote." },
      ],
      knowledge_refs: [],
      no_applicable_knowledge: true,
    },
    human_verdict: "pending",
    model: JUDGE,
    generated_at: plusSeconds(SENT_AT, 40),
  };
}
