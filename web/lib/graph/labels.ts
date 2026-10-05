// Human labels for the graph. The core labels a node with an internal key (a claim's field path, a signal's
// type, an activity's type); the explorer says what the node is in words instead: a fact by its field and value
// ("Objection: long-term cost of ..."), an activity by what happened and who said it ("Email from Fatoumata"),
// a signal in plain words ("Customer replied"). Keys are only shown, humanized, when no value is on record.
import type { Activity } from "@/lib/api/types";

const capitalize = (w: string): string => (w === "" ? w : w[0]!.toUpperCase() + w.slice(1));

/** "customer_replied" -> "Customer replied", "CRMTaskLogged" -> "CRM task logged". Acronyms keep their case. */
export function humanizeKey(key: string): string {
  const words = key
    .replace(/_/g, " ")
    .replace(/([a-z0-9])([A-Z])/g, "$1 $2")
    .replace(/([A-Z]+)([A-Z][a-z])/g, "$1 $2")
    .split(/\s+/)
    .filter(Boolean);
  return words.map((w, i) => (/^[A-Z]{2,}$/.test(w) ? w : i === 0 ? capitalize(w.toLowerCase()) : w.toLowerCase())).join(" ");
}

// A list field holds many items; one node is one of them, so it reads in the singular.
const SINGULAR: Readonly<Record<string, string>> = {
  objections: "Objection",
  blockers: "Blocker",
  decision_criteria: "Decision criterion",
  current_commitments: "Commitment",
  org_changes: "Org change",
  risks: "Risk",
};

/** The field a fact fills, as a seller would name it. */
export const fieldTitle = (fieldPath: string): string => SINGULAR[fieldPath] ?? humanizeKey(fieldPath);

/** An enum value ("at_risk") in words; prose is left as it is. */
export const humanValue = (value: string): string => (/^[a-z]+(_[a-z]+)+$/.test(value) ? value.replace(/_/g, " ") : value);

const KIND_TAGS: Readonly<Record<string, string>> = {
  Account: "Account",
  Opportunity: "Deal",
  Person: "Person",
  Activity: "Activity",
  Conversation: "Activity",
  Document: "Document",
  Claim: "Fact",
  Commitment: "Commitment",
  Signal: "Signal",
  Knowledge: "Knowledge",
  DecisionEpisode: "Decision",
};

/** The small word that says what kind of thing a node is. */
export const kindTag = (type: string): string => KIND_TAGS[type] ?? humanizeKey(type);

const MESSAGES: Readonly<Record<string, string>> = { EmailReceived: "Email", EmailSent: "Email", EmailReply: "Email", CustomerReplied: "Reply", SlackMessage: "Slack message" };
const MEETINGS: Readonly<Record<string, string>> = { MeetingCompleted: "Meeting", MeetingScheduled: "Meeting scheduled", CallStarted: "Call", CallEnded: "Call", TranscriptReady: "Call transcript" };
const PLAIN: Readonly<Record<string, string>> = {
  QuoteCreated: "Quote created",
  ContactAdded: "Contact added",
  CRMTaskLogged: "Task logged",
  CRMFieldChanged: "CRM update",
  AgentActionExecuted: "Action sent",
  AgentActionProposed: "Action drafted",
};

type Participant = Activity["participants"][number];
const firstName = (p: Participant): string => (p.display_name ?? p.raw_identity).split(" ")[0]!;

/** What happened, and with whom when the timeline says: "Email from Fatoumata", "Meeting with Priya". */
export function activityTitle(type: string, activity: Activity | undefined): string {
  const people = activity?.participants ?? [];
  if (MESSAGES[type]) {
    const sender = people.find((p) => p.role === "from");
    return sender ? `${MESSAGES[type]} from ${firstName(sender)}` : humanizeKey(type);
  }
  if (MEETINGS[type]) {
    const other = people[0];
    return other ? `${MEETINGS[type]} with ${firstName(other)}` : MEETINGS[type]!;
  }
  const mentioned = type === "ContactAdded" ? people.find((p) => p.role === "mentioned") : undefined;
  if (mentioned) return `Contact added: ${mentioned.display_name ?? mentioned.raw_identity}`;
  return PLAIN[type] ?? humanizeKey(type);
}
