// The MedTech Advances demo case (bench/reports/demo-cases-2026-10-04.md, rank 1): the real CRMArena-Pro
// account, deal, people and the 13 events of the deal in their real order; event 13 is held out and played.
// Ids are the web fixtures' uuids; every activity keeps its Salesforce id as source_object_id so it resolves
// back to the dataset. Quotes are verbatim substrings of the exported email bodies (medtech.source-emails.json).
import { createHash } from "node:crypto";

// "ad00" in the first group keeps every MedTech short id (the first 8 characters) distinct from Acme's.
const uuid = (prefix, tail) => `${prefix.slice(0, 4)}ad00-0000-4000-8000-00000000${tail}`;
const hex2 = (n) => n.toString(16).padStart(2, "0");

export const IDS = {
  account: uuid("0a0c0000", "ad01"),
  opportunity: uuid("0c0f0000", "ad01"),
  fatoumata: uuid("0b0e0000", "ad11"),
  luis: uuid("0b0e0000", "ad21"),
  run: uuid("0f0a0000", "ad60"),
  episode: uuid("0e9e0000", "ad0a"),
  strategySet: uuid("05e70000", "ad0b"),
  guidance: uuid("06d1de00", "adc1"),
  triggerEvaluation: uuid("07e10000", "ad50"),
  stateDiff: uuid("0d1f0000", "ad30"),
  signal: uuid("051a0000", "ad40"),
  correlation: uuid("0c0c0000", "ad80"),
  decision: uuid("0d5d0000", "ad70"),
  humanDecision: uuid("0dec0000", "ad71"),
  inference: uuid("01f00000", "ad90"),
  candidate: { A: uuid("0ca00000", "ada1"), B: uuid("0ca00000", "ada2"), C: uuid("0ca00000", "ada3") },
  bundle: { A: uuid("0eb00000", "adb1"), B: uuid("0eb00000", "adb2"), C: uuid("0eb00000", "adb3") },
  claim: (n) => uuid("0c1a0000", `ad${hex2(0x20 + n)}`),
  result: (letter, n) => uuid("0e1a0000", `ad${letter.toLowerCase()}${n}`),
  edge: (n) => uuid("0ed90000", `ad${hex2(n)}`),
  activity: (pos) => uuid("0ac70000", `ad${hex2(pos)}`),
  sourceEvent: (pos) => uuid("05e00000", `ad${hex2(pos)}`),
};

export const SF = { account: "001Wt00000PHVyfIAH", opportunity: "006Wt000007BHzBIAW", fatoumata: "003Wt00000JqtK4IAJ", luis: "005Wt000003NHfFIAW" };

export const ACCOUNT_NAME = "MedTech Advances";
export const DEAL_NAME = "Advanced Data Protection and Education Enhancement Deal";
export const DOMAIN = "medtechadvances.com";
export const HELD_OUT = 13;

// The rep's address is mapped to the vendor domain, as the CRMArena loader does (docs/data/crmarena-b2b.md).
export const PEOPLE = {
  fatoumata: { id: IDS.fatoumata, name: "Fatoumata Touré", title: "Supply Chain Coordinator", email: "fatoumata.toure@medtechadvances.com" },
  luis: { id: IDS.luis, name: "Luis Rodriguez", title: null, email: "luis.rodriguez@ghostvendor.com" },
};

// Each event: position, time, source system, Salesforce record id, activity type, who, summary.
export const EVENTS = [
  { pos: 1, at: "2023-05-25T11:23:41Z", system: "crm", sfid: SF.opportunity, key: "created", type: "CRMFieldChanged", who: [], summary: "Deal created: Advanced Data Protection and Education Enhancement Deal." },
  { pos: 2, at: "2023-10-15T10:12:00Z", system: "crm", sfid: SF.fatoumata, key: "created", type: "ContactAdded", who: [["fatoumata", "mentioned", `crm:${SF.fatoumata}`]], summary: "Fatoumata Touré added as a contact." },
  { pos: 3, at: "2023-10-15T10:12:00Z", system: "crm", sfid: "0Q0Wt000001WQWiKAO", key: "created", type: "QuoteCreated", who: [], summary: "Quote created: Data Protection & Education Enhancement, $24,679.53." },
  { pos: 4, at: "2023-10-31T18:00:00Z", system: "email", sfid: "02sWt000002020jIAA", key: "sent", type: "EmailSent", who: [["luis", "from"], ["fatoumata", "to"]], summary: "Luis sent the draft proposal: SecureData Nexus, CryptGuard Module and EduTech Lab." },
  { pos: 5, at: "2023-11-01T00:00:00Z", system: "crm", sfid: "00TWt000002yrBoMAI", key: "created", type: "CRMTaskLogged", who: [], summary: "Task: prepare the proposal draft." },
  { pos: 6, at: "2023-11-01T09:30:00Z", system: "email", sfid: "02sWt000002098bIAA", key: "received", type: "EmailReceived", who: [["fatoumata", "from"], ["luis", "to"]], summary: "Fatoumata asked how CryptGuard integrates with their security protocols, and about the deployment timeline." },
  { pos: 7, at: "2023-11-07T00:00:00Z", system: "crm", sfid: "00TWt000002zGDFMA2", key: "created", type: "CRMTaskLogged", who: [], summary: "Task: organize a product demo of SecureData Nexus and TrainEDU Suite." },
  { pos: 8, at: "2023-11-08T15:00:00Z", system: "email", sfid: "02sWt000001zz4WIAQ", key: "sent", type: "EmailSent", who: [["luis", "from"], ["fatoumata", "to"]], summary: "Luis asked for feedback on the proposal draft." },
  { pos: 9, at: "2023-11-08T15:00:00Z", system: "email", sfid: "02sWt000001zz9GIAQ", key: "sent", type: "EmailSent", who: [["luis", "from"], ["fatoumata", "to"]], summary: "Luis followed up: proposal total $24,679.53, with discounts on CryptGuard Module and SecureData Nexus." },
  { pos: 10, at: "2023-11-08T15:05:00Z", system: "email", sfid: "02sWt000001zuEUIAY", key: "sent", type: "EmailSent", who: [["luis", "from"], ["fatoumata", "to"]], summary: "Luis followed up on integration flexibility and EduTech Lab results." },
  { pos: 11, at: "2023-11-08T16:00:00Z", system: "email", sfid: "02sWt000002087iIAA", key: "received", type: "EmailReceived", who: [["fatoumata", "from"], ["luis", "to"]], summary: "Fatoumata values pricing transparency and has reservations about EduTech Lab's early usability." },
  { pos: 12, at: "2023-11-08T17:30:00Z", system: "email", sfid: "02sWt000001zopGIAQ", key: "received", type: "EmailReceived", who: [["fatoumata", "from"], ["luis", "to"]], summary: "Fatoumata asked for implementation support; Quantum Circuits Inc. offers competitive initial pricing with onboarding." },
  { pos: 13, at: "2023-11-09T09:30:00Z", system: "email", sfid: "02sWt000001zsRGIAY", key: "received", type: "EmailReceived", who: [["fatoumata", "from"], ["luis", "to"]], summary: "Fatoumata asked about the long-term cost of integrating EduTech Lab and SecureData Nexus, and for a follow-up call." },
];

export const eventAt = (pos) => EVENTS[pos - 1].at;

/** Verbatim sentences of the real emails, with the event that said them and who spoke. */
export const QUOTES = {
  costQuestion: { pos: 13, by: "fatoumata", text: "That said, I have some questions regarding the long-term cost implications of integrating both the EduTech Lab and the SecureData Nexus into our existing systems." },
  crucial: { pos: 13, by: "fatoumata", text: "Ensuring that we have a clear understanding of future financial commitments is crucial for us." },
  callAsk: { pos: 13, by: "fatoumata", text: "Could we schedule a follow-up call to discuss these points in more detail?" },
  availability: { pos: 13, by: "fatoumata", text: "Please let me know your availability." },
  cryptguard: { pos: 13, by: "fatoumata", text: "I am particularly interested in the CryptGuard Module due to its adaptability and potential for enhancing our data protection efforts." },
  supportAsk: { pos: 12, by: "fatoumata", text: "Our team would also like to discuss the potential for additional support during the initial implementation phase, to streamline the transition effectively." },
  competitor: { pos: 12, by: "fatoumata", text: "Quantum Circuits Inc. offers competitive initial pricing with comprehensive onboarding, and we are considering all options that maximize our long-term value." },
  transparency: { pos: 11, by: "fatoumata", text: "Transparency in terms of pricing is indeed something we value highly." },
  usability: { pos: 11, by: "fatoumata", text: "We've also noted the positive feedback about EduTech Lab, although we do have some reservations based on initial usability with other solutions." },
  coordinate: { pos: 11, by: "fatoumata", text: "I’ll coordinate internally to gather any additional feedback from my team regarding specific feature requests or adjustments." },
  integration: { pos: 6, by: "fatoumata", text: "Could you provide more information on how the CryptGuard Module integrates with our existing security protocols?" },
  total: { pos: 9, by: "luis", text: "Our total proposal amount of $24,679.53 reflects competitive pricing with thoughtful considerations, including discounts on CryptGuard Module and SecureData Nexus." },
};

/** An evidence ref for a quote, optionally tied to a claim. */
export function ref(quoteKey, claimId) {
  const q = QUOTES[quoteKey];
  const r = { activity_id: IDS.activity(q.pos) };
  if (claimId) r.claim_id = claimId;
  return { ...r, quote: q.text, speaker_person_id: PEOPLE[q.by].id, occurred_at: eventAt(q.pos) };
}

/** A bare activity ref (no quote), for derived and CRM-structured fields. */
export const activityRef = (pos, claimId) => ({ activity_id: IDS.activity(pos), ...(claimId ? { claim_id: claimId } : {}), occurred_at: eventAt(pos) });

const plusSeconds = (iso, s) => new Date(Date.parse(iso) + s * 1000).toISOString().replace(".000Z", "Z");

function participant([who, role, raw]) {
  const p = PEOPLE[who];
  return { raw_identity: raw ?? p.email, display_name: p.name, role, person_id: p.id };
}

/** The canonical Activity of one event (contracts/schemas/activity.v1.json). */
export function activity(pos) {
  const e = EVENTS[pos - 1];
  const connector = { source_system: e.system, source_object_id: e.sfid, connector: "crmarena-loader", connector_version: "wp31-v1" };
  return {
    id: IDS.activity(pos),
    idempotency_key: createHash("sha256").update(`${e.system}|${e.sfid}|${e.key}`).digest("hex"),
    activity_type: e.type,
    source_system: e.system,
    source_object_id: e.sfid,
    source_event_id: IDS.sourceEvent(pos),
    occurred_at: e.at,
    ingested_at: plusSeconds(e.at, 4),
    participants: e.who.map(participant),
    account_id: IDS.account,
    opportunity_id: IDS.opportunity,
    account_hint: e.system === "email" ? DOMAIN : SF.account,
    opportunity_hint: SF.opportunity,
    payload_ref: `source_events/${IDS.sourceEvent(pos)}`,
    summary: e.summary,
    permissions: { visibility: "org" },
    provenance: connector,
    caused_by_activity_id: null,
    correlation_id: pos === HELD_OUT ? IDS.correlation : null,
  };
}

export { plusSeconds };
