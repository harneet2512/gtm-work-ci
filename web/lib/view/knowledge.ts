// View helpers for the knowledge surfaces (WP24, HAR-122): conditions to English, counts to a line,
// lifecycle status to its badge class. The status vocabulary is the contract's enum — an unknown
// status filter on the URL is ignored with a notice rather than sent to the core (which 400s).
import type { Knowledge, KnowledgeCondition, KnowledgeStatus } from "@/lib/api/types";

export const KNOWLEDGE_STATUSES: readonly KnowledgeStatus[] = ["candidate", "provisional", "supported", "confirmed", "disputed", "stale"];

/** The status filter of a ?status= param, or null when it is not in the contract vocabulary. */
export function parseKnowledgeStatus(raw: string | undefined): KnowledgeStatus | null {
  if (!raw) return null;
  return (KNOWLEDGE_STATUSES as readonly string[]).includes(raw) ? (raw as KnowledgeStatus) : null;
}

export const statusBadge = (status: KnowledgeStatus | string): string => `badge know-${status}`;

/** One applicability/signature condition as a short English line ("field op value"). */
export function conditionText(c: KnowledgeCondition): string {
  if (c.op === "exists") return `${c.field} is present`;
  if (c.op === "not_exists") return `${c.field} is absent`;
  if (c.op === "is_unknown") return `${c.field} is unknown`;
  const value = c.value === undefined ? "" : Array.isArray(c.value) ? c.value.map(String).join(", ") : typeof c.value === "object" ? JSON.stringify(c.value) : String(c.value);
  if (c.op === "eq") return `${c.field} = ${value}`;
  if (c.op === "neq") return `${c.field} ≠ ${value}`;
  if (c.op === "in") return `${c.field} ∈ {${value}}`;
  return `${c.field} ${c.op} ${value}`.trim();
}

/** The evidence tallies as a compact line ("8 decisions · 6 positive · 1 counterexample"). */
export function countsLine(k: Knowledge): string {
  const c = k.counts;
  const parts = [`${c.decisions} decisions`];
  if (c.positive_reactions > 0) parts.push(`${c.positive_reactions} positive`);
  if (c.negative_reactions > 0) parts.push(`${c.negative_reactions} negative`);
  if (c.outcomes_advanced > 0) parts.push(`${c.outcomes_advanced} outcomes advanced`);
  if (c.counterexamples > 0) parts.push(`${c.counterexamples} counterexample${c.counterexamples === 1 ? "" : "s"}`);
  return parts.join(" · ");
}

/** Status history, newest first (the API returns it oldest-first, per contract; display newest-first). */
export function historyNewestFirst(k: Knowledge): NonNullable<Knowledge["status_history"]> {
  return [...(k.status_history ?? [])].sort((a, b) => Date.parse(b.changed_at) - Date.parse(a.changed_at));
}
