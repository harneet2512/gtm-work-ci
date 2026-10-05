// The one eval vocabulary (eval design spec 4b-3): verdict order, words and icons, eval names, evidence
// tags, grader names and shared phrases. WORDING is contracts/evals/eval_wording.json verbatim (the parity
// test fails on any drift), so Slack and web say the same things. Turbopack cannot import outside web/,
// hence the twin instead of an import.

export type BundleVerdict = "fail" | "warn" | "abstain" | "pass" | "not_relevant";
/** A verdict as the core sends it: unknown is the current word for "the evidence cannot settle it", abstain its older spelling. */
export type ApiVerdict = BundleVerdict | "unknown";

/** One verdict on screen for both spellings: the wording table words it "Unsure". */
export const canonicalVerdict = <T extends ApiVerdict>(v: T): Exclude<T, "unknown"> | "abstain" => (v === "unknown" ? "abstain" : (v as Exclude<T, "unknown">));
/** A bundle verdict, or not_checked: the router did not select this eval for that candidate at all. */
export type CellVerdict = BundleVerdict | "not_checked";
export type IconName = "x-circle" | "alert-triangle" | "help-circle" | "check-circle" | "minus-circle" | "dashed-circle";
export type EvidenceClass = "deal_data" | "methodology" | "cs_ops" | "product_rule";
export type GraderKind = "deterministic" | "semantic" | "trace" | "human_delta";
export type PolicyReason = "expansion_motion" | "pricing_push" | "commercial_escalation" | "broad_outreach" | "expansion_label_mismatch";

export interface VerdictWording {
  readonly order: number;
  readonly label: string;
  readonly icon: IconName;
  readonly slack_emoji: string;
}

export interface Tag {
  readonly tag: string;
  readonly meaning: string;
}

const verdict = (order: number, label: string, icon: IconName, slack_emoji: string): VerdictWording => ({ order, label, icon, slack_emoji });
const type = (name: string, question: string) => ({ name, question });

export const WORDING = {
  wording_version: 1,
  description:
    "How every Ghost surface words evals (eval design spec 4b-3). Web renders it from web/lib/evals/vocabulary.ts (a parity test keeps the two identical); the Slack M2 copy is documented in contracts/slack/eval-copy.md against this file.",
  verdicts: {
    fail: verdict(0, "Fail", "x-circle", ":x:"),
    warn: verdict(1, "Warn", "alert-triangle", ":warning:"),
    abstain: verdict(2, "Unsure", "help-circle", ":grey_question:"),
    pass: verdict(3, "Pass", "check-circle", ":white_check_mark:"),
    not_relevant: verdict(4, "Not relevant", "minus-circle", ":heavy_minus_sign:"),
  },
  not_checked: verdict(5, "Not checked", "dashed-circle", ":white_circle:"),
  evidence_classes: {
    deal_data: { tag: "Deal evidence", meaning: "Backed by observed deal behaviour and the buyer's own words." },
    methodology: { tag: "Sales methodology", meaning: "Backed by established enterprise-sales practice (MEDDPICC, mutual action plans), not by this deal's data." },
    cs_ops: { tag: "CS practice", meaning: "Backed by customer-success operations: adoption, health, risk and support signals." },
    product_rule: { tag: "Policy rule", meaning: "A hard product or policy rule, checked by code." },
  },
  graders: { deterministic: "Rule check", semantic: "AI judge", trace: "Run audit", human_delta: "Edit analysis" },
  eval_types: {
    recipient_correctness: type("Right recipients", "Is every recipient real, on this account and safe to email?"),
    date_commitment_consistency: type("Dates match commitments", "Do the dates match what was already agreed?"),
    pricing_integrity: type("Pricing policy", "Is every price and discount backed by a source?"),
    crm_writeback: type("CRM update", "Is the CRM change allowed and up to date?"),
    duplicate_action: type("No repeat outreach", "Was this already sent, booked or done?"),
    provenance_coverage: type("Facts are sourced", "Does every factual statement point to its source?"),
    permission_policy: type("Permissions", "Is Ghost allowed to take this action on this account?"),
    state_transition_support: type("Deal change is proven", "Is the deal change this action relies on backed by evidence?"),
    buyer_readiness: type("Buyer readiness", "Has the buyer given a real reason to act now?"),
    cta_calibration: type("CTA calibration", "Is the ask the right size for where the buyer is?"),
    next_step_quality: type("Clear next step", "Is there a concrete next step with an owner and, where agreed, a date?"),
    stakeholder_selection: type("Stakeholder fit", "Are the right people on this action?"),
    stakeholder_coverage: type("Stakeholder coverage", "Does the action involve as many people as the deal needs, and no more?"),
    economic_buyer_coverage: type("Economic buyer", "Does the action help reach whoever approves the purchase?"),
    champion_strength: type("Champion strength", "Does the action make the champion stronger?"),
    champion_continuity: type("Relationship continuity", "Does the champion stay involved unless they handed off or left?"),
    decision_process: type("Decision process", "Does the action respect how the customer decides and approves?"),
    business_case: type("Business case", "Do the value claims tie to the customer's own pain and metrics?"),
    momentum: type("Momentum", "Does the action match how engaged the account is right now?"),
    action_stage_fit: type("Stage fit", "Does this kind of action fit where the deal is?"),
    expansion_readiness: type("Expansion readiness", "Do adoption, health and need support an expansion ask now?"),
    customer_risk_sensitivity: type("Customer risk", "Does the commercial ask fit the customer's current risk?"),
    relationship_pressure: type("Relationship pressure", "Is the pressure right for where the relationship is?"),
    timing_cadence: type("Timing", "Is now the right moment, given the recent back-and-forth?"),
    next_action_quality: type("Best next move", "Of the possible moves, waiting included, is this the best one?"),
    grounding: type("Grounding", "Does every claim come from this account's own evidence?"),
    commitment_consistency: type("Commitments kept", "Does the action keep to what both sides already promised?"),
    state_change_relevance: type("Responds to the change", "Does the action address what actually changed?"),
    channel_appropriateness: type("Right channel", "Is this the right channel: email, a meeting, an internal task or nothing?"),
    rep_style: type("Rep's voice", "Does it sound like the rep without changing the substance?"),
    knowledge_applicability: type("Company knowledge fits", "Does the company knowledge used actually apply to this account?"),
    exception_awareness: type("Exception awareness", "Does the action notice an exception to the usual playbook?"),
    evidence_sufficiency: type("Enough evidence", "Is there enough trusted evidence to decide at all?"),
    decision_grounding: type("Strategy grounding", "Does the strategy rest only on what the account's evidence shows?"),
    artifact_grounding: type("Message grounding", "Does every claim in the message come from this account's own evidence?"),
    decision_timing: type("Strategy timing", "Does the strategy respect the dates the buyer has set?"),
    artifact_timing: type("Message timing", "Do the dates in the message fit the buyer's window?"),
    trajectory: type("Run integrity", "Did the whole run follow the rules, from trigger to send?"),
    human_delta: type("Human edits", "What did the human change, and did an eval predict it?"),
  } as Record<string, { name: string; question: string }>,
  diagnostics: {
    too_early: "Too early",
    ask_too_strong: "Ask too strong",
    ask_too_weak: "Ask too weak",
    single_threaded: "Single-threaded",
    under_threaded: "Too few people involved",
    over_threaded_too_early: "Too many people, too early",
    wrong_stakeholder: "Wrong stakeholder",
    no_economic_buyer_access: "No path to the economic buyer",
    weak_champion: "Weak champion",
    over_reliant_on_champion: "Leans too hard on the champion",
    champion_bypassed: "Champion bypassed",
    no_mutual_next_step: "No mutual next step",
    next_step_missing_owner_or_date: "Next step has no owner or date",
    decision_process_unknown: "Decision process unknown",
    security_or_procurement_step_missing: "Security or procurement step skipped",
    weak_business_case: "Weak business case",
    no_compelling_event: "No compelling event",
    engagement_cooling: "Engagement cooling",
    expansion_signal_too_weak: "Expansion signal too weak",
    customer_risk_conflicts_with_upsell: "Customer risk conflicts with an upsell",
  } as Record<string, string>,
  policy_reasons: {
    expansion_motion: "an expansion ask",
    pricing_push: "a pricing push",
    commercial_escalation: "a commercial escalation",
    broad_outreach: "broad executive outreach",
    expansion_label_mismatch: "an expansion ask labelled as something else",
  },
  phrases: {
    ghost_pick: "Ghost's pick",
    human_choice: "Chosen by {actor}",
    blocking: "Blocks send",
    send_blocked: "Send is blocked: {eval} still fails.",
    after_edit: "{eval} {from} → {to} after your edit",
    not_reevaluated: "Not re-evaluated yet",
    not_relevant_summary: "Not relevant here ({count})",
    evidence: "Evidence",
    dispute: "This eval is wrong",
    held_for_review: "Held for review: {reasons} while the account change is unconfirmed.",
  },
} as const;

export type PhraseKey = keyof typeof WORDING.phrases;

export function verdictWording(v: CellVerdict): VerdictWording {
  return v === "not_checked" ? WORDING.not_checked : WORDING.verdicts[v];
}

/** Worst first, the order every surface lists verdicts in. */
export const compareVerdicts = (a: CellVerdict, b: CellVerdict): number => verdictWording(a).order - verdictWording(b).order;

export function worstVerdict(verdicts: readonly CellVerdict[]): CellVerdict | null {
  return verdicts.length === 0 ? null : [...verdicts].sort(compareVerdicts)[0] ?? null;
}

/** "brand_new_eval" -> "Brand new eval": never shows a raw code, even for a type the table lacks. */
const humanize = (code: string): string => {
  const s = code.replaceAll("_", " ");
  return s.charAt(0).toUpperCase() + s.slice(1);
};

export const evalName = (evalType: string): string => WORDING.eval_types[evalType]?.name ?? humanize(evalType);
export const evalQuestion = (evalType: string): string | null => WORDING.eval_types[evalType]?.question ?? null;
export const evidenceTag = (cls: EvidenceClass): Tag => WORDING.evidence_classes[cls];
export const diagnosticPhrase = (code: string): string => WORDING.diagnostics[code] ?? humanize(code);

/** The grader, shown only in the expanded detail: "Rule check", or "AI judge · <model>". */
export function graderLabel(kind: GraderKind, model: string | null | undefined): string {
  const name = WORDING.graders[kind];
  return model ? `${name} · ${model}` : name;
}

/** A shared phrase with {placeholders} filled; a missing value stays visible rather than vanishing. */
export function fill(key: PhraseKey, vars: Readonly<Record<string, string | number>>): string {
  return WORDING.phrases[key].replace(/\{(\w+)\}/g, (whole, name: string) => (name in vars ? String(vars[name]) : whole));
}

/** "an expansion ask, a pricing push and broad executive outreach". */
function joinPlain(parts: readonly string[]): string {
  if (parts.length <= 1) return parts[0] ?? "";
  return `${parts.slice(0, -1).join(", ")} and ${parts[parts.length - 1]}`;
}

export function heldForReview(reasons: readonly PolicyReason[]): string {
  return fill("held_for_review", { reasons: joinPlain(reasons.map((r) => WORDING.policy_reasons[r])) });
}
