import type { Activity } from "@/lib/api/types";
import { activityTitle } from "@/lib/graph/labels";
import { formatDay } from "@/lib/format";
// The /episodes/[id] causal trajectory (HAR-145): one node per TraceSpan the core serves, in the order it serves them
// (source event, evidence, resolution, graph, state, precedents, knowledge retrieved / applicable / cited, candidates,
// ranking, the three Cliff messages, human interaction, recomputed action, knowledge mutation). The web adds no span and
// infers nothing. A span is "recorded" when the platform persisted it, "pending" when it has not happened yet and
// "not recorded" when the platform does not persist it; none of these is an eval verdict, so no node carries a check
// mark. Retrieval, applicability and citation stay three nodes, and Influence is a fourth, synthetic node that always
// reads "not measured": no measure of influence exists, and a citation is not evidence that knowledge changed a decision.
import type { EpisodeSummary, EpisodeTrace, TraceSpan } from "@/lib/api/types";

export type NodeStatus = TraceSpan["status"] | "not_measured";

/** Plain-language word for each status, shown beside the glyph so colour/glyph is never the only cue. */
export const NODE_WORD: Record<NodeStatus, string> = {
  recorded: "recorded",
  pending: "pending",
  not_recorded: "not recorded",
  not_measured: "not measured",
};

export interface EpisodeNode {
  /** The span's stable id (`kind:primary-ref`), or `knowledge_influence` for the synthetic node. */
  id: string;
  kind: TraceSpan["kind"] | "knowledge_influence";
  label: string;
  status: NodeStatus;
  /** The story line: one sentence of what happened, as the core wrote it. */
  summary: string;
  /** Technical one-liner: the rows behind the span and how many eval results judged it. */
  detail: string | null;
  /** The verbatim span (Raw mode and the inspector). Null for the synthetic influence node. */
  data: TraceSpan | null;
  evalCount: number;
}

export type EpisodeMode = "story" | "trace" | "graphdiff" | "cliff" | "raw";

export const EPISODE_MODES: readonly { id: EpisodeMode; label: string }[] = [
  { id: "story", label: "Story" },
  { id: "trace", label: "Trace" },
  { id: "graphdiff", label: "Graph diff" },
  { id: "cliff", label: "Cliff" },
  { id: "raw", label: "Raw" },
];

export function parseMode(raw: string | undefined): EpisodeMode {
  return EPISODE_MODES.some((m) => m.id === raw) ? (raw as EpisodeMode) : "story";
}

const short = (id: string) => (id.length > 13 ? `${id.slice(0, 13)}…` : id);

const refLine = (span: TraceSpan): string | null => {
  const parts = span.refs.slice(0, 3).map((r) => `${r.kind.replaceAll("_", " ")} ${short(r.id)}`);
  if (span.refs.length > 3) parts.push(`+${span.refs.length - 3} more`);
  if (span.eval_result_ids.length > 0) parts.push(`${span.eval_result_ids.length} eval ${span.eval_result_ids.length === 1 ? "result" : "results"}`);
  return parts.length > 0 ? parts.join(" · ") : null;
};

const toNode = (span: TraceSpan): EpisodeNode => ({
  id: span.id,
  kind: span.kind,
  label: span.title,
  status: span.status,
  summary: span.summary,
  detail: refLine(span),
  data: span,
  evalCount: span.eval_result_ids.length,
});

const INFLUENCE: EpisodeNode = {
  id: "knowledge_influence",
  kind: "knowledge_influence",
  label: "Knowledge influence",
  status: "not_measured",
  summary: "Influence: not measured. A citation is not evidence that the knowledge changed the decision.",
  detail: null,
  data: null,
  evalCount: 0,
};

export interface EpisodeView {
  summary: EpisodeSummary;
  nodes: EpisodeNode[];
  /** Eval results of the validation and safety families, which have no span of their own. */
  unassignedEvalCount: number;
}

/** The trajectory in span order, with Influence placed directly after the cited-knowledge span. A missing trace has no nodes. */
export function buildEpisodeView(summary: EpisodeSummary, trace: EpisodeTrace | null): EpisodeView {
  const nodes: EpisodeNode[] = [];
  for (const span of [...(trace?.spans ?? [])].sort((a, b) => a.seq - b.seq)) {
    nodes.push(toNode(span));
    if (span.kind === "knowledge_used") nodes.push(INFLUENCE);
  }
  return { summary, nodes, unassignedEvalCount: trace?.unassigned_eval_result_ids.length ?? 0 };
}

/** The first span of a kind, for pages that read one span's attributes. */
export const spanOfKind = (trace: EpisodeTrace | null, kind: TraceSpan["kind"]): TraceSpan | null => trace?.spans.find((s) => s.kind === kind) ?? null;

/** The id of the account change behind this episode's evidence span: what ties Message 1 to this episode. */
export function accountChangeId(trace: EpisodeTrace | null): string | null {
  return spanOfKind(trace, "evidence")?.refs.find((r) => r.kind === "account_change")?.id ?? null;
}

/** The held-out/trigger event's id, for its graph projection diff. */
export function sourceEventId(trace: EpisodeTrace | null): string | null {
  return spanOfKind(trace, "source_event")?.refs.find((r) => r.kind === "source_event")?.id ?? null;
}

/** The /episodes/[id] URL: node, mode and manifest are URL state so back/forward and sharing work. */
export function episodeHref(episodeId: string, opts: { node?: string | null; mode?: EpisodeMode | null; manifest?: string | null } = {}): string {
  const q = new URLSearchParams();
  if (opts.node) q.set("node", opts.node);
  if (opts.mode) q.set("mode", opts.mode);
  if (opts.manifest) q.set("manifest", opts.manifest);
  const qs = q.toString();
  return `/episodes/${episodeId}${qs ? `?${qs}` : ""}`;
}

const EVENT_WORD: Readonly<Record<string, string>> = { email: "Email", meeting: "Meeting", call: "Call", note: "Note", message: "Message" };

/** "MedTech Advances · Email · Nov 9, 2023": the account, the triggering event's type and day. Parts never recorded are left out. */
export function episodeTitle(summary: EpisodeSummary, trigger?: Activity | null): string {
  const t = summary.triggering_event;
  // Same human label as the account graph ("Email from Fatoumata"); the plain type word when the activity was not read.
  const type = t ? (trigger ? activityTitle(t.activity_type, trigger) : (EVENT_WORD[t.activity_type] ?? activityTitle(t.activity_type, undefined))) : null;
  return [summary.account_name, type, t ? formatDay(t.occurred_at) : null].filter(Boolean).join(" · ");
}
