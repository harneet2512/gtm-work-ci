// Human wording for the criteria the gates check (HAR-149: "Relevant precedent found  PASS"). The ids are what the backend
// stores (Bucket 1 assertion names, Bucket 2 judge dimensions); the sentence is what a person reads. An id with no entry is
// shown as its own words, sentence-cased, never hidden.
const WORDS: Readonly<Record<string, string>> = {
  // Bucket 1 assertions
  no_future_leakage: "No case is dated after this event",
  situational_not_textual: "Cases match in situation, not just wording",
  relevant_precedent_found: "Relevant precedent found",
  important_precedent_missed: "Important precedent missed",
  analogy_not_overclaimed: "The similarity is not over-claimed",
  human_choice_not_truth: "A past human choice is not treated as proof",
  speaker_correct: "The speaker is right",
  dates_correct: "The dates are right",
  source_preserved: "The source wording is kept",
  fact_inference_separated: "Facts and inferences are kept apart",
  no_double_count: "The event is not counted twice",
  source_mapping_retained: "Each claim links back to its source",
  replay_time: "Replay time is respected",
  record_consistent: "The record is consistent",
  person_resolved: "Each person is the right person",
  account_opportunity_correct: "Account and opportunity match",
  internal_external_correct: "Internal and external people are told apart",
  ambiguous_identity_abstains: "An ambiguous identity is not guessed",
  no_cross_account_contamination: "Nothing leaks from another account",
  chronology_respected: "Time order is respected",
  conflicts_surfaced: "Conflicts with earlier beliefs are shown",
  supersession_justified: "Replacing an earlier fact is justified",
  history_preserved: "Earlier history is kept",
  unresolved_kept: "Open questions stay open",
  diff_traceable_to_evidence: "Every change traces to evidence",
  diff_matches_graph_diff: "The state change matches the graph change",
  no_unsupported_transition: "No stage change without support",
  unrelated_state_preserved: "Unrelated state is untouched",
  material_vs_nonmaterial: "Material and minor changes are told apart",
  mutation_locality: "The change stays local",
  contradictory_knowledge_surfaced: "Contradicting knowledge is shown",
  contradictions_preserved: "Contradictions are kept",
  freshness_and_version: "Knowledge is fresh and the right version",
  beliefs_traceable: "Each belief traces to evidence",
  blockers_commitments_stakeholders_next_decision: "Blockers, commitments, stakeholders and the next decision are covered",
  counter_evidence_not_omitted: "Counter-evidence is not left out",
  critical_facts_present: "No critical fact is missing",
  scope_explicit: "The scope of the change is explicit",
  customer_evidence_separate: "Customer evidence is kept apart from the human's edit",
  outcome_represented_and_separate: "The outcome is recorded separately",
  // Bucket 2 judge dimensions
  intent_follows_state: "The intent follows from the account state",
  timing: "Timing",
  relationship_supports: "The relationship supports the move",
  uncertainty_handled: "Uncertainty is handled",
  knowledge_used_correctly: "Company knowledge is used correctly",
  quiet_move_considered: "A quiet move (wait or ask) was weighed",
  materially_different: "The options are genuinely different",
  coverage: "The options cover the defensible moves",
  no_dominated: "No clearly worse option is included",
  fit: "Fits the account state",
  grounding: "Grounded in evidence",
  intent_coherence: "The action matches the strategy",
  recipients: "Right recipients",
  cta: "Call to action",
  factual_integrity: "Facts, numbers and dates are right",
  knowledge_use: "Company knowledge is used",
  unsupported_assumptions: "No unsupported assumptions",
  risk: "Relationship and policy risk",
  supported_by_evidence_state: "Supported by the evidence and state",
  supported_by_knowledge: "Supported by company knowledge",
  uncertainty_reflected: "Uncertainty is reflected",
  no_blocked_preferred: "No blocked option is recommended",
  rationale_matches_basis: "The reasons match the real basis of the order",
  intent_preserved: "The intent is preserved",
  claims_grounded: "Claims are grounded in evidence",
  cta_timing_correct: "Call to action and timing are right",
  inference_boundary: "Facts and inferences are kept apart",
  supporting_and_conflicting_links: "Supporting and conflicting earlier claims are linked",
  relevance_and_misses: "Cases are relevant and none was missed",
  confidence_and_omissions: "Confidence is stated and nothing is left out",
};

const ACRONYMS: Readonly<Record<string, string>> = { cta: "CTA", crm: "CRM", dpa: "DPA", id: "ID", ids: "IDs" };

export function criterionLabel(id: string): string {
  const known = WORDS[id];
  if (known) return known;
  const words = id.trim().replaceAll("_", " ").split(/\s+/).filter(Boolean).map((w) => ACRONYMS[w.toLowerCase()] ?? w);
  const s = words.join(" ");
  return s ? s.charAt(0).toUpperCase() + s.slice(1) : id;
}
