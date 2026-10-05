// The /episodes/[id] causal trajectory (HAR-145): one node per link in the chain —
// source event → evidence → resolution → graph → state → precedents → knowledge → candidates →
// ranking → cliff → human → recompute → knowledge mutation. Every node's status and summary is
// derived from the run trace's own fields plus the strategy set, human decision, inference and posted
// surface refs. The status vocabulary is deliberately NOT an eval verdict: a node is "recorded" when its
// payload exists, "absent" when nothing was recorded and "not observable" when the core exposes no record
// at all. A check mark / PASS is reserved for real EvalResult verdicts (HAR-145, HAR-129).
import type { AgentRun, HumanStrategyDecision, JudgmentInference, RunStrategies, RunTrace } from "@/lib/api/types";

export type NodeStatus = "recorded" | "absent" | "not_observable" | "waiting";

/** Plain-language word for each status, shown beside the glyph so colour/glyph is never the only cue. */
export const NODE_WORD: Record<NodeStatus, string> = {
  recorded: "recorded",
  absent: "not recorded",
  not_observable: "not observable",
  waiting: "waiting",
};

export interface EpisodeNode {
  id: string;
  label: string;
  status: NodeStatus;
  /** The story line — one sentence of what happened, in words. */
  summary: string;
  /** Technical one-liner for the node (ids, counts, tools). */
  detail: string | null;
  /** The raw payload this node rests on — the Raw mode and the inspector show it verbatim. */
  data: unknown;
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

type Activity = NonNullable<RunTrace["trigger_activities"]>[number];
type Candidate = NonNullable<RunStrategies["strategy_set"]>["candidates"][number];

const count = (n: number, word: string) => `${n} ${n === 1 ? word : word.endsWith("y") ? `${word.slice(0, -1)}ies` : `${word}s`}`;

const node = (id: string, label: string, status: NodeStatus, summary: string, detail: string | null, data: unknown): EpisodeNode => ({
  id,
  label,
  status,
  summary,
  detail,
  data,
});

const shortId = (id: string | null | undefined) => (id ? `${id.slice(0, 13)}…` : "—");

function sourceEvent(t: RunTrace): EpisodeNode {
  const ev = t.trigger_activities?.[0];
  if (!ev) return node("source_event", "Source event", "absent", "No trigger activity recorded on the trace.", null, null);
  return node(
    "source_event",
    "Source event",
    "recorded",
    `${ev.source_system} ${ev.activity_type} — ${ev.summary ?? ev.source_object_id}`,
    `activity ${shortId(ev.id)} · ${ev.occurred_at}`,
    ev,
  );
}

function evidence(t: RunTrace): EpisodeNode {
  const acts = t.correlated_activities ?? [];
  if (acts.length === 0) return node("evidence", "Evidence", "absent", "No correlated evidence was pulled for this decision.", null, null);
  return node(
    "evidence",
    "Evidence",
    "recorded",
    `${count(acts.length, "correlated activity")} backed the read — e.g. “${acts[0]!.summary ?? acts[0]!.activity_type}”`,
    acts.map((a) => `${a.source_system} ${a.activity_type}`).join(" · "),
    acts,
  );
}

function resolution(t: RunTrace): EpisodeNode {
  const ev = t.trigger_activities?.[0];
  const resolved = Boolean(ev?.account_id);
  return node(
    "resolution",
    "Resolution",
    resolved ? "recorded" : "absent",
    resolved ? `The event resolved to account ${shortId(ev!.account_id)}${ev!.opportunity_id ? `, opportunity ${shortId(ev!.opportunity_id)}` : ""}.` : "No account linkage recorded.",
    ev?.account_hint ? `hint: ${ev.account_hint}` : null,
    { account_id: ev?.account_id ?? null, opportunity_id: ev?.opportunity_id ?? null, account_hint: ev?.account_hint ?? null, participants: ev?.participants ?? null },
  );
}

function graphNode(t: RunTrace): EpisodeNode {
  const d = t.state_diff;
  if (!d) return node("graph", "Graph mutation", "absent", "No state diff recorded on the trace.", null, null);
  const n = (d.changes ?? []).length;
  return node(
    "graph",
    "Graph mutation",
    n > 0 ? "recorded" : "absent",
    n > 0 ? `The event's diff carries ${count(n, "change")} — ${(d.changes ?? [])[0]?.field ?? "state"}.` : "The event produced no state diff.",
    `diff ${shortId(d.id)} · ${d.from_version ?? "?"}→${d.to_version ?? "?"} · material: ${d.is_material === true ? "yes" : "no"}`,
    d,
  );
}

function stateNode(t: RunTrace): EpisodeNode {
  const before = t.state_before?.version ?? null;
  const after = t.state_at_run?.version ?? null;
  if (after == null) return node("state", "State", "absent", "No account state at run time recorded on the trace.", null, { before, after });
  return node(
    "state",
    "State",
    "recorded",
    before != null && before !== after ? `Account state moved v${before} → v${after}.` : `The run read account state v${after}.`,
    t.state_diff ? `state_diff ${shortId(t.state_diff.id)}` : null,
    { state_before: t.state_before ?? null, state_at_run: t.state_at_run ?? null },
  );
}

function precedents(t: RunTrace): EpisodeNode {
  const accesses = (t.context_accesses ?? []) as { tool: string; items?: unknown[]; bytes?: number }[];
  const relevant = accesses.filter((a) => a.tool !== "state");
  if (relevant.length === 0) return node("precedents", "Precedents", "absent", "No precedent/tool reads recorded.", null, null);
  return node(
    "precedents",
    "Precedents",
    "recorded",
    `The run consulted ${count(relevant.length, "context source")} (${relevant.map((a) => a.tool).join(", ")}).`,
    relevant.map((a) => `${a.tool}: ${(a.items ?? []).length} items`).join(" · "),
    relevant,
  );
}

const citedRefs = (strategies: RunStrategies | null): string[] => {
  const refs = new Set<string>();
  for (const c of strategies?.strategy_set?.candidates ?? []) for (const k of c.knowledge_refs ?? []) refs.add(k);
  return [...refs];
};

// The four knowledge steps are kept apart on purpose (HAR-97 E7: retrieval is not influence): a read of
// company knowledge, whether it applied, which items the candidates cite, and whether it changed anything.
function knowledgeRetrieved(t: RunTrace): EpisodeNode {
  const reads = ((t.context_accesses ?? []) as { tool: string }[]).filter((a) => a.tool === "knowledge");
  if (reads.length === 0) return node("knowledge", "Knowledge retrieved", "absent", "No company-knowledge read is recorded in the run's context pulls.", null, null);
  return node("knowledge", "Knowledge retrieved", "recorded", `The run read company knowledge (${count(reads.length, "context pull")}).`, null, reads);
}

const knowledgeApplicable = (): EpisodeNode =>
  node("knowledge_applicable", "Knowledge applicable", "not_observable", "Applicability is not observable here: the run trace carries no applicability record.", null, null);

function knowledgeCited(strategies: RunStrategies | null): EpisodeNode {
  const refs = citedRefs(strategies);
  if (refs.length === 0) return node("knowledge_cited", "Knowledge cited", "absent", "No candidate cites a company-knowledge item.", null, null);
  return node("knowledge_cited", "Knowledge cited", "recorded", `${count(refs.length, "knowledge item")} cited by the candidates.`, refs.map(shortId).join(" · "), { cited: refs });
}

const knowledgeInfluence = (): EpisodeNode =>
  node("knowledge_influence", "Knowledge influence", "not_observable", "Influence: not measured. A citation is not evidence that the knowledge changed the decision.", null, null);

function candidates(strategies: RunStrategies | null): EpisodeNode {
  const cs = strategies?.strategy_set?.candidates ?? [];
  if (cs.length === 0) {
    const none = strategies?.strategy_set?.no_acceptable_candidate;
    return node("candidates", "Candidate decisions", none ? "recorded" : "absent", none ? "The run abstained — no acceptable candidate." : "No strategy set recorded on this run.", null, strategies?.strategy_set ?? null);
  }
  return node(
    "candidates",
    "Candidate decisions",
    "recorded",
    `${count(cs.length, "candidate")} generated — ${cs.map((c: Candidate) => c.title ?? c.strategy_type).join(" · ")}`,
    cs.map((c: Candidate) => `${c.candidate_id?.slice(0, 8)}… ${c.action_class ?? ""}`).join(" | "),
    cs,
  );
}

function ranking(strategies: RunStrategies | null, run: AgentRun | null): EpisodeNode {
  const cs = strategies?.strategy_set?.candidates ?? [];
  const pick = cs.find((c: Candidate) => c.preferred_by_agent);
  if (cs.length === 0) return node("ranking", "Ranking", "absent", "Nothing to rank.", null, null);
  return node(
    "ranking",
    "Ranking",
    pick ? "recorded" : "absent",
    pick ? `Ghost's pick: ${pick.title ?? pick.candidate_id}${pick.ranking != null ? ` (rank ${pick.ranking})` : ""}.` : "Candidates carry no preferred_by_agent mark.",
    `run ${run?.generation?.phase ?? "?"}`,
    { pick: pick ?? null, order: cs.map((c: Candidate) => ({ id: c.candidate_id, ranking: c.ranking, preferred: c.preferred_by_agent })) },
  );
}

function cliff(posted: readonly string[] | null): EpisodeNode {
  const label = { bi: "Message 1", chooser: "Message 2", judgment: "Message 3" } as Record<string, string>;
  if (posted === null) return node("cliff", "Cliff", "not_observable", "Surface refs unreadable on this core: not observable.", null, null);
  if (posted.length === 0) return node("cliff", "Cliff", "waiting", "No Cliff message recorded for this episode yet.", null, null);
  return node("cliff", "Cliff", "recorded", `${posted.map((k) => label[k] ?? k).join(" + ")} posted to Slack.`, posted.join(" · "), posted);
}

function human(t: RunTrace, decision: HumanStrategyDecision | null): EpisodeNode {
  const d = t.decisions?.[0];
  if (d) {
    const edit = d.decision === "edit" ? " — with an edited draft" : "";
    return node("human", "Human interaction", "recorded", `${d.actor_label ?? "A human"} chose ${d.decision}${edit}.`, `decision ${shortId(d.id)} · ${d.created_at}`, d);
  }
  if (decision) {
    return node(
      "human",
      "Human interaction",
      "recorded",
      `${decision.actor_label ?? "A human"} chose a candidate${decision.selected_candidate_id ? ` (${shortId(decision.selected_candidate_id)})` : ""}.`,
      `chosen ${decision.chosen_at}`,
      decision,
    );
  }
  return node("human", "Human interaction", "waiting", "No human decision recorded — the chooser is still open.", null, null);
}

/**
 * The core exposes no DependencyInvalidation record yet, so a recompute is never inferred from the edit
 * alone. The edit itself is on the human node; this node stays "not observable" until the recomputation
 * API (GET /runs/{id}/recomputation) lands.
 */
function recompute(t: RunTrace, decision: HumanStrategyDecision | null): EpisodeNode {
  const d = t.decisions?.[0];
  const edited = d?.decision === "edit";
  const summary = "Recomputed action: not observable — core exposes no invalidation record.";
  return node("recompute", "Recomputed action", "not_observable", summary, edited ? "the edit is recorded on the human node" : null, edited ? { trace_decision: d ?? null, strategy_decision: decision } : null);
}

function knowledgeMutation(t: RunTrace, inference: JudgmentInference | null): EpisodeNode {
  const updates = (t.placeholders as { knowledge_updates?: unknown[] } | undefined)?.knowledge_updates ?? [];
  if (updates.length > 0) return node("knowledge_mutation", "Knowledge mutation", "recorded", `${count(updates.length, "knowledge update")} recorded on the trace.`, null, updates);
  if (inference) return node("knowledge_mutation", "Knowledge mutation", "waiting", `Judgment inferred — the human ${inference.agreement} Ghost's pick; no confirmed mutation yet.`, `inference ${shortId(inference.id)}`, inference);
  return node("knowledge_mutation", "Knowledge mutation", "absent", "No learning recorded for this episode.", null, null);
}

export interface EpisodeView {
  run: AgentRun;
  nodes: EpisodeNode[];
}

/**
 * The episode's causal chain, in order. Nodes are stable ids so `?node=<id>` selects one for the
 * inspector and `?mode=` switches the presentation — both preserved in the URL.
 */
export function buildEpisodeView(
  run: AgentRun,
  trace: RunTrace,
  strategies: RunStrategies | null,
  decision: HumanStrategyDecision | null,
  inference: JudgmentInference | null,
  postedCliff: readonly string[] | null,
): EpisodeView {
  return {
    run,
    nodes: [
      sourceEvent(trace),
      evidence(trace),
      resolution(trace),
      graphNode(trace),
      stateNode(trace),
      precedents(trace),
      knowledgeRetrieved(trace),
      knowledgeApplicable(),
      knowledgeCited(strategies),
      knowledgeInfluence(),
      candidates(strategies),
      ranking(strategies, run),
      cliff(postedCliff),
      human(trace, decision),
      recompute(trace, decision),
      knowledgeMutation(trace, inference),
    ],
  };
}

/** The /episodes/[id] URL: node, mode and manifest are URL state so back/forward, sharing and Message 1 resolution work. */
export function episodeHref(episodeId: string, opts: { node?: string | null; mode?: EpisodeMode | null; manifest?: string | null } = {}): string {
  const q = new URLSearchParams();
  if (opts.node) q.set("node", opts.node);
  if (opts.mode) q.set("mode", opts.mode);
  if (opts.manifest) q.set("manifest", opts.manifest);
  const qs = q.toString();
  return `/episodes/${episodeId}${qs ? `?${qs}` : ""}`;
}
