// The MedTech world around the held-out event: account state strictly before Event 13 (v107) and with it
// (v108), the state diff, its signal, the graph before and after, the graph diff and the timeline. The
// fields Event 13 changes are exactly the report's (demo-cases-2026-10-04, held-out row). Snapshot-only
// CRM values (stage, amount, close date, quote status) are not shown: they are unknown as of Nov 9, 2023.
import { ACCOUNT_NAME, activity, activityRef, DEAL_NAME, DOMAIN, EVENTS, eventAt, HELD_OUT, IDS, PEOPLE, plusSeconds, QUOTES, ref } from "./case.mjs";

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
// The neighborhood the core's world-time read serves (core-go/internal/ctxgraph: snapshot builder + viewBuilder):
// every activity of the account before the cutoff (an email is a Conversation), every claim that holds at the
// time with ABOUT_ACCOUNT, ABOUT_OPPORTUNITY and SUPPORTED_BY its source activity (the commitment: MADE_IN and
// MADE_BY), and the signal with its ABOUT and DERIVED_FROM edges. One claim per field, as the claims table holds.
const evRef = (pos) => [{ activity_id: IDS.activity(pos) }];
const srcIds = (pos) => [IDS.sourceEvent(pos)];
const at = (quoteKey) => QUOTES[quoteKey].pos;
const CONVERSATIONS = new Set(["EmailSent", "EmailReceived"]);

/** The claims that hold before (`until` Event 13 supersedes them) or from Event 13 (`from`). */
const CLAIMS = [
  { n: 1, field: "owner", pos: 1, standing: "crm_explicit", confidence: 1 },
  { n: 2, field: "decision_criteria", pos: at("transparency") },
  { n: 3, field: "objections", pos: at("usability") },
  { n: 4, field: "current_commitments", pos: at("coordinate"), commitment: true },
  { n: 5, field: "commercial_issue", pos: at("competitor") },
  { n: 6, field: "decision_criteria", pos: at("supportAsk") },
  { n: 11, field: "product_use_case", pos: 4, until: HELD_OUT },
  { n: 7, field: "objections", pos: HELD_OUT, from: HELD_OUT },
  { n: 8, field: "next_meeting", pos: HELD_OUT, from: HELD_OUT },
  { n: 9, field: "product_use_case", pos: HELD_OUT, from: HELD_OUT },
  { n: 10, field: "decision_criteria", pos: HELD_OUT, from: HELD_OUT },
];
const holds = (c, after) => (after ? c.until === undefined : c.from === undefined);
const standingOf = (c) => c.standing ?? "first_party_ai";
const confidenceOf = (c) => c.confidence ?? 0.8;
const activityType = (pos) => (CONVERSATIONS.has(EVENTS[pos - 1].type) ? "Conversation" : "Activity");
const positions = (after) => EVENTS.map((e) => e.pos).filter((p) => after || p !== HELD_OUT);

function nodes(after) {
  const acts = positions(after).map((p) => {
    const e = EVENTS[p - 1];
    return {
      id: IDS.activity(p), type: activityType(p), label: e.type, valid_from: e.at,
      data: { activity_type: e.type, source_system: e.system, occurred_at: e.at, visibility: "org" },
      evidence_refs: evRef(p), source_event_ids: srcIds(p),
    };
  });
  const claims = CLAIMS.filter((c) => holds(c, after)).map((c) => ({
    id: C(c.n), type: c.commitment ? "Commitment" : "Claim", label: c.commitment ? "commitment" : c.field, status: "active",
    valid_from: eventAt(c.pos), data: { ...(c.commitment ? {} : { field_path: c.field }), standing: standingOf(c), confidence: confidenceOf(c) },
    evidence_refs: evRef(c.pos), source_event_ids: srcIds(c.pos),
  }));
  const out = [
    { id: IDS.account, type: "Account", label: ACCOUNT_NAME, status: "active", evidence_refs: [], source_event_ids: [] },
    { id: IDS.opportunity, type: "Opportunity", label: DEAL_NAME, status: "open", evidence_refs: evRef(1), source_event_ids: srcIds(1) },
    { id: PEOPLE.fatoumata.id, type: "Person", label: PEOPLE.fatoumata.name, status: "active", data: { title: PEOPLE.fatoumata.title }, evidence_refs: evRef(2), source_event_ids: srcIds(2) },
    { id: PEOPLE.luis.id, type: "Person", label: PEOPLE.luis.name, status: "active", evidence_refs: [], source_event_ids: [] },
    ...acts,
    ...claims,
  ];
  if (after) {
    out.push({ id: IDS.signal, type: "Signal", label: "customer_replied", status: "active", valid_from: AFTER_AT, data: { signal_type: "customer_replied", rule: "sig.customer_replied@1" }, evidence_refs: evRef(HELD_OUT), source_event_ids: srcIds(HELD_OUT) });
  }
  return out;
}

/** A relationship edge (relationships table: a uuid id, a standing and a confidence). */
function rel(n, source, target, relType, pos, standing) {
  return { id: IDS.edge(n), source, target, rel_type: relType, status: "active", evidence_refs: evRef(pos), source_event_ids: srcIds(pos), standing, confidence: 1 };
}

/** A derived edge, keyed like the core's (`claim:<id>:<TYPE>`, `signal:<id>:<TYPE>:<to>`). */
function derivedEdge(id, source, target, relType, pos, extra = {}) {
  return { id, source, target, rel_type: relType, status: "active", evidence_refs: evRef(pos), source_event_ids: srcIds(pos), ...extra };
}

function activityEdges(after) {
  const out = [
    rel(1, PEOPLE.fatoumata.id, IDS.account, "WORKS_AT", 2, "crm_explicit"),
    rel(2, IDS.opportunity, IDS.account, "BELONGS_TO", 1, "crm_explicit"),
    rel(3, PEOPLE.luis.id, IDS.opportunity, "OWNS", 1, "crm_explicit"),
  ];
  for (const p of positions(after)) {
    const e = EVENTS[p - 1];
    const standing = e.system === "crm" ? "crm_explicit" : "first_party_record";
    out.push(rel(16 + p * 3, IDS.activity(p), IDS.opportunity, "ABOUT", p, standing));
    e.who.filter(([, role]) => role === "from" || role === "to").forEach(([who], i) => out.push(rel(17 + p * 3 + i, IDS.activity(p), PEOPLE[who].id, "INVOLVES", p, standing)));
  }
  return out;
}

function claimEdges(after) {
  return CLAIMS.filter((c) => holds(c, after)).flatMap((c) => {
    const id = C(c.n);
    const props = { standing: standingOf(c), confidence: confidenceOf(c) };
    const edge = (relType, target) => derivedEdge(`claim:${id}:${relType}`, id, target, relType, c.pos, props);
    const source = c.commitment
      ? [edge("MADE_IN", IDS.activity(c.pos)), edge("MADE_BY", PEOPLE.fatoumata.id)]
      : [edge("SUPPORTED_BY", IDS.activity(c.pos))];
    return [edge("ABOUT_ACCOUNT", IDS.account), edge("ABOUT_OPPORTUNITY", IDS.opportunity), ...source];
  });
}

function signalEdges() {
  const s = IDS.signal;
  const edge = (relType, target) => derivedEdge(`signal:${s}:${relType}:${target}`, s, target, relType, HELD_OUT);
  return [edge("ABOUT_ACCOUNT", IDS.account), edge("ABOUT_OPPORTUNITY", IDS.opportunity), edge("ABOUT_PERSON", PEOPLE.fatoumata.id), edge("DERIVED_FROM", C(7)), edge("DERIVED_FROM", IDS.activity(HELD_OUT))];
}

const edges = (after) => [...activityEdges(after), ...claimEdges(after), ...(after ? signalEdges() : [])];

function graph(after) {
  const ns = nodes(after);
  const ofType = (...types) => ns.filter((x) => types.includes(x.type)).map((x) => x.id);
  return {
    account_id: IDS.account,
    nodes: ns,
    edges: edges(after),
    sections: {
      account: ofType("Account"),
      opportunities: ofType("Opportunity"),
      stakeholders: ofType("Person"),
      activities: ofType("Activity", "Conversation").reverse(),
      claims: ofType("Claim").reverse(),
      commitments: ofType("Commitment"),
      ...(after ? { signals: ofType("Signal") } : {}),
    },
    truncated: false,
    withheld: false,
    projection: { complete: true, projected_at: after ? plusSeconds(AFTER_AT, 12) : null },
  };
}

export const graphBefore = () => graph(false);
export const graphAfter = () => graph(true);

/**
 * What Event 13's projection did: everything the After graph has and the N-1 graph lacks is added; the claim it
 * superseded (product_use_case, now held by C9) is changed: its status left "active", so it drops out of the view.
 */
export function graphDiff() {
  const p = HELD_OUT;
  const ev = [IDS.sourceEvent(p)];
  const before = graphBefore();
  const after = graphAfter();
  const was = new Set([...before.nodes.map((n) => n.id), ...before.edges.map((e) => e.id)]);
  const props = { id: IDS.activity(p), account_id: IDS.account, activity_type: "EmailReceived", source_system: "email", occurred_at: AFTER_AT, visibility: "org" };
  const base = { source_event_ids: ev, attributed_to_event: true };
  const nodes = after.nodes.filter((n) => !was.has(n.id)).map((n) => ({ kind: "node", op: "added", type: n.type, id: n.id, ...(n.id === IDS.activity(p) ? { props } : {}), ...base }));
  const edgeChanges = after.edges.filter((e) => !was.has(e.id)).map((e) => ({ kind: "edge", op: "added", type: e.rel_type, id: e.id, from: e.source, to: e.target, ...base }));
  const superseded = { kind: "node", op: "changed", type: "Claim", id: C(11), changed: { status: { before: "active", after: "superseded" } }, ...base };
  const changes = [...nodes, ...edgeChanges, superseded];
  return { event_id: IDS.sourceEvent(p), projected: true, job_ids: [131, 132], summary: { added: nodes.length + edgeChanges.length, changed: 1, removed: 0, repaired: 0 }, changes };
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
