// The "[evidence]" behind an eval verdict (eval design spec 4b-3; HAR-97 trace viewer: every eval result links
// to the trace evidence that justified it): who said it, when, what was said, and a deep link into the run
// trace. Names and dates come only from the recorded trace; nothing is filled in when the trace lacks them.
import type { Activity, EvidenceRef, Knowledge, RunTrace } from "@/lib/api/types";
import { formatDay } from "@/lib/format";

export interface Person {
  name: string;
  title: string | null;
}

export interface EvidenceContext {
  runId: string;
  people: ReadonlyMap<string, Person>;
  activities: ReadonlyMap<string, Activity>;
  knowledge: Readonly<Record<string, Knowledge | null>>;
}

export interface EvidenceSnippet {
  activityId: string;
  quote: string | null;
  who: string | null;
  whoTitle: string | null;
  /** The day it was said ("Sep 29, 2026"), from the ref or its activity. */
  when: string | null;
  /** The kind of source in plain words ("Email", "Meeting", "CRM update"). */
  source: string | null;
  summary: string | null;
  /** The activity in the run trace; null when the trace does not hold it (no dead links). */
  href: string | null;
}

const SOURCE_WORDS: Readonly<Record<string, string>> = {
  Email: "Email",
  Call: "Call",
  Transcript: "Call transcript",
  Meeting: "Meeting",
  Slack: "Slack message",
  CRM: "CRM update",
  Opportunity: "CRM update",
  Contact: "CRM update",
  Stakeholder: "CRM update",
  Quote: "Quote",
  Contract: "Contract",
  Order: "Order",
  Support: "Support ticket",
  Document: "Shared document",
  Customer: "Customer reply",
};

/** "EmailReceived" -> "Email", "OpportunityStageChanged" -> "CRM update"; anything else reads as "Activity". */
export function sourceWord(activityType: string): string {
  const prefix = Object.keys(SOURCE_WORDS).find((p) => activityType.startsWith(p));
  return prefix ? (SOURCE_WORDS[prefix] ?? "Activity") : "Activity";
}

/** People and activities the run's trace recorded, for resolving evidence refs. */
export function evidenceContext(trace: RunTrace | null, runId: string, knowledge: Readonly<Record<string, Knowledge | null>>): EvidenceContext {
  const people = new Map<string, Person>();
  const activities = new Map<string, Activity>();
  if (trace) {
    for (const a of [...trace.trigger_activities, ...(trace.correlated_activities ?? [])]) {
      activities.set(a.id, a);
      for (const p of a.participants) {
        if (p.person_id && p.display_name && !people.has(p.person_id)) people.set(p.person_id, { name: p.display_name, title: null });
      }
    }
    // The account state names people with their titles; it wins over a bare participant name.
    for (const state of [trace.state_before, trace.state_at_run]) {
      for (const m of state?.buying_group ?? []) {
        if (m.display_name) people.set(m.person_id, { name: m.display_name, title: m.title ?? null });
      }
    }
  }
  return { runId, people, activities, knowledge };
}

export function evidenceSnippets(refs: readonly EvidenceRef[], ctx: EvidenceContext): EvidenceSnippet[] {
  return refs.map((ref) => {
    const activity = ctx.activities.get(ref.activity_id) ?? null;
    const person = ref.speaker_person_id ? (ctx.people.get(ref.speaker_person_id) ?? null) : null;
    const at = ref.occurred_at ?? activity?.occurred_at ?? null;
    return {
      activityId: ref.activity_id,
      quote: ref.quote ?? null,
      who: person?.name ?? null,
      whoTitle: person?.title ?? null,
      when: at ? formatDay(at) : null,
      source: activity ? sourceWord(activity.activity_type) : null,
      summary: activity?.summary ?? null,
      href: activity ? `/runs/${ctx.runId}#activity-${activity.id}` : null,
    };
  });
}
