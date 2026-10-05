// The MedTech world around the held-out event: account state strictly before Event 13 (v107) and with it
// (v108), the state diff, its signal, the graph before and after, the graph diff and the timeline. The
// fields Event 13 changes are exactly the report's (demo-cases-2026-10-04, held-out row). Snapshot-only
// CRM values (stage, amount, close date, quote status) are not shown: they are unknown as of Nov 9, 2023.
import { ACCOUNT_NAME, activity, activityRef, DEAL_NAME, DOMAIN, EVENTS, eventAt, HELD_OUT, IDS, PEOPLE, plusSeconds, ref } from "./case.mjs";

const C = IDS.claim;
const BEFORE_AT = eventAt(12);
const AFTER_AT = eventAt(HELD_OUT);

const unknown = () => ({ value: "unknown", known: false, winning_claim_id: null, standing: null, confidence: null, as_of: null, evidence_refs: [], competing_claim_ids: [], suggested_claim_ids: [] });
const scalar = (value, claimId, standing, confidence, asOf, refs, extra = {}) => ({
  value, known: true, winning_claim_id: claimId, standing, confidence, as_of: asOf, evidence_refs: refs, competing_claim_ids: [], suggested_claim_ids: [], ...extra,
});
const derived = (value, asOf, refs) => ({ ...scalar(value, null, null, null, asOf, refs), derived: true });
const item = (text, claimId, refs) => ({ text, claim_id: claimId, status: "open", evidence_refs: refs });
const list = (items) =>
  items.length === 0
    ? { ...unknown(), value: [] }
    : { value: items, known: true, winning_claim_id: null, standing: null, confidence: null, as_of: items.map((i) => i.evidence_refs[0].occurred_at).sort().at(-1), evidence_refs: items.flatMap((i) => i.evidence_refs), competing_claim_ids: [], suggested_claim_ids: [] };

const ITEMS = {
  usability: item("Reservations about EduTech Lab's early usability", C(3), [ref("usability", C(3))]),
  longTermCost: item("Long-term cost of integrating EduTech Lab and SecureData Nexus", C(7), [ref("costQuestion", C(7))]),
  transparency: item("Transparent pricing", C(2), [ref("transparency", C(2))]),
  support: item("Support during the initial implementation", C(6), [ref("supportAsk", C(6))]),
  futureCommitments: item("A clear view of future financial commitments", C(10), [ref("crucial", C(10))]),
  feedback: item("Fatoumata to gather her team's feature requests", C(4), [ref("coordinate", C(4))]),
};

function fields(after) {
  const owner = scalar(PEOPLE.luis.id, C(1), "crm_explicit", 1, eventAt(1), [activityRef(1, C(1))], { opportunity_id: IDS.opportunity });
  return {
    stage: unknown(),
    health: unknown(),
    owner,
    motion: unknown(),
    champion: unknown(),
    champion_status: unknown(),
    economic_buyer: unknown(),
    blockers: list([]),
    objections: list(after ? [ITEMS.usability, ITEMS.longTermCost] : [ITEMS.usability]),
    decision_criteria: list(after ? [ITEMS.transparency, ITEMS.support, ITEMS.futureCommitments] : [ITEMS.transparency, ITEMS.support]),
    decision_process: unknown(),
    current_commitments: list([ITEMS.feedback]),
    next_milestone: after ? scalar("Follow-up call on long-term costs", C(8), "first_party_ai", 0.9, AFTER_AT, [ref("callAsk", C(8))]) : unknown(),
    next_meeting: after ? scalar("Follow-up call requested; no time agreed", C(8), "first_party_ai", 0.9, AFTER_AT, [ref("callAsk", C(8))]) : unknown(),
    relationship_risk: scalar("medium", C(5), "first_party_ai", 0.7, BEFORE_AT, [ref("competitor", C(5))]),
    product_use_case: after
      ? scalar("CryptGuard Module for data protection; integrating EduTech Lab and SecureData Nexus", C(9), "first_party_ai", 0.85, AFTER_AT, [ref("cryptguard", C(9))])
      : scalar("Data protection and healthcare education: SecureData Nexus, CryptGuard Module, EduTech Lab", C(11), "first_party_ai", 0.8, eventAt(4), [activityRef(4, C(11))]),
    commercial_issue: scalar("Weighing Quantum Circuits Inc.'s initial pricing and onboarding", C(5), "first_party_ai", 0.8, BEFORE_AT, [ref("competitor", C(5))]),
    last_customer_interaction: derived(after ? AFTER_AT : BEFORE_AT, after ? AFTER_AT : BEFORE_AT, [activityRef(after ? HELD_OUT : 12)]),
    last_meaningful_change: after
      ? derived("Fatoumata asked about long-term integration costs and for a follow-up call", AFTER_AT, [activityRef(HELD_OUT)])
      : derived("Fatoumata asked for implementation support and named Quantum Circuits' onboarding", BEFORE_AT, [activityRef(12)]),
    summary: scalar("Fatoumata likes the proposal's flexibility, values pricing transparency and is weighing Quantum Circuits' onboarding offer.", C(5), "first_party_ai", 0.8, BEFORE_AT, [ref("competitor", C(5))]),
  };
}

function state(after) {
  const at = after ? AFTER_AT : BEFORE_AT;
  const last = IDS.activity(after ? HELD_OUT : 12);
  return {
    account_id: IDS.account,
    account_name: ACCOUNT_NAME,
    opportunity_id: IDS.opportunity,
    opportunities: [{ opportunity_id: IDS.opportunity, is_open: true, is_primary: true, stage: "unknown", owner: PEOPLE.luis.id, amount: null, health: "unknown", as_of: at, last_activity_id: last }],
    version: after ? 108 : 107,
    as_of: at,
    computed_at: plusSeconds(at, 9),
    last_activity_id: last,
    fields: fields(after),
    buying_group: [
      {
        person_id: PEOPLE.fatoumata.id,
        display_name: PEOPLE.fatoumata.name,
        title: PEOPLE.fatoumata.title,
        roles: ["unknown"],
        role_source: null,
        role_basis: null,
        role_provenance: [],
        status: "active",
        delegated_to_person_id: null,
        last_engaged_at: at,
        evidence_refs: [after ? ref("callAsk") : ref("supportAsk")],
      },
    ],
    open_transition: null,
    coverage_gaps: ["economic_buyer"],
  };
}

export const stateBefore = () => state(false);
export const stateAfter = () => state(true);

const texts = (f) => (Array.isArray(f.value) ? f.value.map((i) => i.text) : f.value);

/** The diff Event 13 makes: the seven fields the report names, nothing else. */
export function stateDiff() {
  const b = fields(false);
  const a = fields(true);
  const change = (field, op, refs) => ({ field, op, before: texts(b[field]), after: texts(a[field]), material: true, evidence_refs: refs });
  return {
    id: IDS.stateDiff,
    account_id: IDS.account,
    from_version: 107,
    to_version: 108,
    is_material: true,
    changes: [
      change("objections", "changed", [ref("costQuestion", C(7))]),
      change("decision_criteria", "changed", [ref("crucial", C(10))]),
      change("next_meeting", "set", [ref("callAsk", C(8))]),
      change("next_milestone", "set", [ref("callAsk", C(8))]),
      change("product_use_case", "changed", [ref("cryptguard", C(9))]),
      { ...change("last_customer_interaction", "changed", [activityRef(HELD_OUT)]), material: false },
      { ...change("last_meaningful_change", "changed", [activityRef(HELD_OUT)]), material: false },
    ],
    activity_ids: [IDS.activity(HELD_OUT)],
    created_at: plusSeconds(AFTER_AT, 11),
  };
}

export function signals() {
  return [
    {
      id: IDS.signal,
      account_id: IDS.account,
      opportunity_id: IDS.opportunity,
      signal_type: "customer_replied",
      state_diff_id: IDS.stateDiff,
      subject_person_id: PEOPLE.fatoumata.id,
      subject_claim_id: C(7),
      occurred_at: AFTER_AT,
      expires_at: plusSeconds(AFTER_AT, 14 * 24 * 3600), // EVENT signals last 14 days (signals.EventWindow)
      dedupe_key: `diff:${IDS.stateDiff}:sig.customer_replied@1:${IDS.activity(HELD_OUT)}`,
      rule: "sig.customer_replied@1",
      details: { activity_id: IDS.activity(HELD_OUT) },
      evidence_refs: [ref("costQuestion", C(7))],
      created_at: plusSeconds(AFTER_AT, 11),
    },
  ];
}

// ---- Graph ----------------------------------------------------------------------------------------
const MAP_ACTIVITIES = [3, 4, 11, 12, 13];
const evRef = (pos) => [{ activity_id: IDS.activity(pos) }];
const srcIds = (pos) => [IDS.sourceEvent(pos)];

function nodes(after) {
  const acts = MAP_ACTIVITIES.filter((p) => after || p !== HELD_OUT).map((p) => ({
    id: IDS.activity(p), type: "Activity", label: EVENTS[p - 1].type, valid_from: eventAt(p), evidence_refs: evRef(p), source_event_ids: srcIds(p),
  }));
  const out = [
    { id: IDS.account, type: "Account", label: ACCOUNT_NAME, status: "active", evidence_refs: [], source_event_ids: [] },
    { id: IDS.opportunity, type: "Opportunity", label: DEAL_NAME, status: "open", evidence_refs: evRef(1), source_event_ids: srcIds(1) },
    { id: PEOPLE.fatoumata.id, type: "Person", label: PEOPLE.fatoumata.name, status: "active", data: { title: PEOPLE.fatoumata.title }, evidence_refs: evRef(2), source_event_ids: srcIds(2) },
    { id: PEOPLE.luis.id, type: "Person", label: PEOPLE.luis.name, status: "active", evidence_refs: [], source_event_ids: [] },
    ...acts,
    { id: C(5), type: "Claim", label: "commercial_issue", status: "active", evidence_refs: evRef(12), source_event_ids: srcIds(12) },
  ];
  if (after) {
    out.push(
      { id: C(7), type: "Claim", label: "objections", status: "active", evidence_refs: evRef(HELD_OUT), source_event_ids: srcIds(HELD_OUT) },
      { id: C(8), type: "Claim", label: "next_meeting", status: "active", evidence_refs: evRef(HELD_OUT), source_event_ids: srcIds(HELD_OUT) },
      { id: IDS.signal, type: "Signal", label: "customer_replied", status: "active", evidence_refs: evRef(HELD_OUT), source_event_ids: srcIds(HELD_OUT) },
    );
  }
  return out;
}

/** A graph edge; a derivation edge (SUPPORTED_BY, DERIVED_FROM) passes standing null and carries none. */
function edge(n, source, target, rel, pos, standing = "first_party_record") {
  const e = { id: IDS.edge(n), source, target, rel_type: rel, status: "active", evidence_refs: evRef(pos), source_event_ids: srcIds(pos) };
  return standing ? { ...e, standing, confidence: 1 } : e;
}

function edges(after) {
  const out = [
    edge(1, PEOPLE.fatoumata.id, IDS.account, "WORKS_AT", 2, "crm_explicit"),
    edge(2, IDS.opportunity, IDS.account, "BELONGS_TO", 1, "crm_explicit"),
    edge(3, PEOPLE.luis.id, IDS.opportunity, "OWNS", 1, "crm_explicit"),
    edge(4, IDS.activity(3), IDS.opportunity, "ABOUT", 3, "crm_explicit"),
  ];
  let n = 5;
  for (const p of [4, 11, 12]) {
    out.push(edge(n++, IDS.activity(p), IDS.opportunity, "ABOUT", p), edge(n++, IDS.activity(p), PEOPLE.fatoumata.id, "INVOLVES", p));
  }
  out.push(edge(n++, C(5), IDS.activity(12), "SUPPORTED_BY", 12, null));
  if (after) out.push(...heldOutEdges());
  return out;
}

function heldOutEdges() {
  const p = HELD_OUT;
  return [
    edge(20, IDS.activity(p), IDS.opportunity, "ABOUT", p),
    edge(21, IDS.activity(p), PEOPLE.fatoumata.id, "INVOLVES", p),
    edge(22, IDS.activity(p), PEOPLE.luis.id, "INVOLVES", p),
    edge(23, C(7), IDS.activity(p), "SUPPORTED_BY", p, null),
    edge(24, C(8), IDS.activity(p), "SUPPORTED_BY", p, null),
    edge(25, IDS.signal, C(7), "DERIVED_FROM", p, null),
  ];
}

function graph(after) {
  const ns = nodes(after);
  const ofType = (t) => ns.filter((x) => x.type === t).map((x) => x.id);
  return {
    account_id: IDS.account,
    nodes: ns,
    edges: edges(after),
    sections: {
      account: ofType("Account"),
      opportunities: ofType("Opportunity"),
      stakeholders: ofType("Person"),
      activities: ofType("Activity").reverse(),
      claims: ofType("Claim").reverse(),
      signals: ofType("Signal"),
    },
    truncated: false,
    withheld: false,
    projection: { complete: true, projected_at: after ? plusSeconds(AFTER_AT, 12) : null },
  };
}

export const graphBefore = () => graph(false);
export const graphAfter = () => graph(true);

export function graphDiff() {
  const p = HELD_OUT;
  const ev = [IDS.sourceEvent(p)];
  const node = (type, id, extra = {}) => ({ kind: "node", op: "added", type, id, ...extra, source_event_ids: ev, attributed_to_event: true });
  const props = { id: IDS.activity(p), account_id: IDS.account, activity_type: "EmailReceived", source_system: "email", occurred_at: AFTER_AT, visibility: "org" };
  const edgeChanges = heldOutEdges().map((e) => ({ kind: "edge", op: "added", type: e.rel_type, id: e.id, from: e.source, to: e.target, source_event_ids: ev, attributed_to_event: true }));
  const changes = [node("Activity", IDS.activity(p), { props }), node("Claim", C(7)), node("Claim", C(8)), node("Signal", IDS.signal), ...edgeChanges];
  return { event_id: IDS.sourceEvent(p), projected: true, job_ids: [131, 132], summary: { added: changes.length, changed: 0, removed: 0, repaired: 0 }, changes };
}

/** Newest first, as GET /accounts/{id}/timeline pages. */
export const timeline = () => ({ items: EVENTS.map((e) => activity(e.pos)).reverse() });

export const accountSummary = () => ({
  id: IDS.account,
  name: ACCOUNT_NAME,
  domain: DOMAIN,
  stage: null,
  health: null,
  motion: null,
  last_meaningful_change: "Fatoumata asked about long-term integration costs and for a follow-up call",
  last_activity_at: AFTER_AT,
  open_run_id: null,
});
