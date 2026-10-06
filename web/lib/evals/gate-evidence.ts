// Resolves a gate result's evidence ref ("kind:id") against the episode's real records: the trace span it sits in (the link
// target), and for an activity the recorded summary of the email or meeting. A ref that resolves to nothing stays a plain
// label with no link: it is never dressed up as a summary.
import { formatDay } from "@/lib/format";
import type { EvidenceItem } from "./gate-table";

export interface SpanLite {
  id: string;
  kind: string;
  title: string;
  refs: readonly { kind: string; id: string }[];
}

export interface ActivityLite {
  id: string;
  summary: string | null;
  activityType: string;
  occurredAt?: string;
}

export const spanHref = (episodeId: string, spanId: string): string => `/episodes/${episodeId}?mode=trace&span=${encodeURIComponent(spanId)}`;

const WORD: Readonly<Record<string, string>> = { candidate: "option", strategy_candidate: "option", activity: "activity", claim: "claim", span: "trace span" };

export function parseRef(ref: string): { kind: string; id: string } {
  const i = ref.indexOf(":");
  return i < 0 ? { kind: "record", id: ref } : { kind: ref.slice(0, i), id: ref.slice(i + 1) };
}

export function makeResolver(opts: { episodeId: string; accountId: string; spans: readonly SpanLite[]; activities: ReadonlyMap<string, ActivityLite> }): (ref: string) => EvidenceItem {
  return (ref) => {
    const { kind, id } = parseRef(ref);
    const span = opts.spans.find((s) => s.id === ref) ?? opts.spans.find((s) => s.refs.some((r) => r.id === id));
    const activity = kind === "activity" ? (opts.activities.get(id) ?? null) : null;
    const word = WORD[kind] ?? kind.replaceAll("_", " ");
    const short = id.length > 13 ? `${id.slice(0, 8)}…` : id;
    const accountHref = activity && opts.accountId ? `/accounts/${encodeURIComponent(opts.accountId)}?activity=${encodeURIComponent(id)}` : null;
    return {
      ref,
      kind,
      id,
      label: `${word} ${short}`,
      summary: activity?.summary?.trim() || null,
      source: activity ? [activity.activityType.replaceAll("_", " ").replace(/^./, (c) => c.toUpperCase()), activity.occurredAt ? formatDay(activity.occurredAt) : null].filter(Boolean).join(" · ") : null,
      spanId: span?.id ?? null,
      href: span ? spanHref(opts.episodeId, span.id) : accountHref,
    };
  };
}
