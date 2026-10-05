// Job 1 of the HAR-129 demo loop on the eval page: what Ghost now understands because Event N arrived. This is the
// object the intelligence-building evals (E1-E6) judge: each material state change, its before and after, the
// words it rests on and how it stands (CRM fact or first-party inference). Read from the run trace only.
import type { RunTrace, StateDiff } from "@/lib/api/types";
import { formatDay } from "@/lib/format";
import { evidenceSnippets, sourceWord, type EvidenceContext, type EvidenceSnippet } from "./evidence";

export interface UnderstoodChange {
  field: string;
  label: string;
  /** Scalar fields: the value before and after. Null for list fields, which use added / removed. */
  before: string | null;
  after: string | null;
  added: string[];
  removed: string[];
  evidence: EvidenceSnippet[];
  /** How the winning claim stands (crm_explicit, first_party_ai, ...); null for list fields and derived values. */
  standing: string | null;
  confidence: number | null;
}

export interface Understanding {
  event: { source: string; who: string | null; when: string; summary: string | null; href: string };
  changes: UnderstoodChange[];
  /** Changes the diff marks not material (bookkeeping such as the last interaction time). */
  bookkeeping: number;
  signals: string[];
  trigger: string | null;
  version: { from: number; to: number } | null;
}

const sentence = (s: string) => s.charAt(0).toUpperCase() + s.slice(1);
const humanize = (code: string) => sentence(code.replaceAll("_", " "));

function display(value: unknown, ctx: EvidenceContext): string | null {
  if (value === null || value === undefined) return null;
  if (typeof value === "string") return value;
  if (typeof value === "number" || typeof value === "boolean") return String(value);
  if (Array.isArray(value)) return value.length === 0 ? "none" : value.map((v) => display(v, ctx)).join("; ");
  const o = value as Record<string, unknown>;
  if (typeof o.text === "string") return o.text;
  if (typeof o.person_id === "string") {
    const name = ctx.people.get(o.person_id)?.name ?? "A person";
    const roles = Array.isArray(o.roles) && o.roles.length > 0 ? ` (${o.roles.join(", ")})` : "";
    return `${name}${roles}`;
  }
  return JSON.stringify(value);
}

function standingOf(trace: RunTrace, field: string): { standing: string | null; confidence: number | null } {
  const f = (trace.state_at_run?.fields as Record<string, { standing?: string | null; confidence?: number | null }> | undefined)?.[field];
  return { standing: f?.standing ?? null, confidence: f?.confidence ?? null };
}

/** A list change as what it added and removed; a scalar change as before and after. */
function values(before: unknown, after: unknown, ctx: EvidenceContext): Pick<UnderstoodChange, "before" | "after" | "added" | "removed"> {
  if (Array.isArray(before) && Array.isArray(after)) {
    const was = before.map((v) => display(v, ctx) ?? "");
    const now = after.map((v) => display(v, ctx) ?? "");
    return { before: null, after: null, added: now.filter((v) => !was.includes(v)), removed: was.filter((v) => !now.includes(v)) };
  }
  return { before: display(before, ctx), after: display(after, ctx), added: [], removed: [] };
}

function changesOf(diff: StateDiff | null | undefined, trace: RunTrace, ctx: EvidenceContext): UnderstoodChange[] {
  return (diff?.changes ?? [])
    .filter((c) => c.material)
    .map((c) => ({
      field: c.field,
      label: humanize(c.field),
      ...values(c.before, c.after, ctx),
      evidence: evidenceSnippets(c.evidence_refs ?? [], ctx),
      ...standingOf(trace, c.field),
    }));
}

export function buildUnderstanding(trace: RunTrace | null, ctx: EvidenceContext): Understanding | null {
  const a = trace?.trigger_activities[0];
  if (!trace || !a) return null;
  const diff = trace.state_diff;
  return {
    event: {
      source: sourceWord(a.activity_type),
      who: a.participants.find((p) => p.role === "from")?.display_name ?? null,
      when: formatDay(a.occurred_at),
      summary: a.summary ?? null,
      href: `/runs/${ctx.runId}#activity-${a.id}`,
    },
    changes: changesOf(diff, trace, ctx),
    bookkeeping: (diff?.changes ?? []).filter((c) => !c.material).length,
    signals: trace.signals.map((s) => humanize(s.signal_type)),
    trigger: trace.trigger_evaluation?.explanation ?? null,
    version: diff ? { from: diff.from_version, to: diff.to_version } : null,
  };
}
