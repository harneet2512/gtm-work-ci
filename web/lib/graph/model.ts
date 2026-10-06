// The explorer's view of one account graph read: the core's nodes and edges with their kind, human label,
// shape, time and diff mark resolved once, plus the indexes the canvas and the keyboard need (by id, neighbors,
// the event's own activity). Built from real reads only: the graph, the event's graph diff, the timeline
// and the account state. Nothing is invented; a value the reads do not carry is left undefined.
import type { AccountState, Activity, Graph, GraphChange, GraphEdge, GraphNode } from "@/lib/api/types";
import type { DiffIndex, DiffOp } from "@/lib/view/diff";
import { indexClaims, type ClaimFact } from "./claim-index";
import { edgeLabel, isActivityType, kindOf, type RegionId, type Shape } from "./kinds";
import { kindTag } from "./labels";
import { nodeLabel, type NodeLabelContext } from "./node-label";

export { WITHHELD_LABEL } from "./node-label";

/** Records a seller reads at a glance: labelled at every zoom like the account, the deal and people. */
const MAJOR_ACTIVITIES = new Set(["QuoteCreated"]);

export interface ExplorerNode {
  id: string;
  type: string;
  /** What the node is called, in words (or the withheld notice). */
  label: string;
  /** The small word for its kind ("Fact", "Deal", "Activity"). */
  kindTag: string;
  /** "label (Kind)": the accessible name, and what lists show. */
  name: string;
  /** The colour family of the kind. */
  region: RegionId;
  shape: Shape;
  radius: number;
  major: boolean;
  withheld: boolean;
  /** What the Play event did to this node (After Play only). */
  mark?: DiffOp;
  /** Epoch ms of an activity; null for timeless nodes or an activity with no known time. */
  at: number | null;
  /** A fact's value from the account state (by the node's own field). */
  detail?: string;
  /** The fact a Claim or Commitment node stands for. */
  fact?: ClaimFact;
  /** The field a fact node fills. */
  field?: string;
  /** The standing the core records on a fact node. */
  standing?: string;
  status?: string;
  validFrom?: string;
  sourceEventIds: readonly string[];
  /** Activity ids the core cites as this node's evidence. */
  evidence: readonly string[];
}

export interface ExplorerEdge {
  id: string;
  source: string;
  target: string;
  relType: string;
  label: string;
  mark?: DiffOp;
  /** The core edge (standing, confidence, evidence) for the inspector. */
  raw: GraphEdge;
}

/** Something the event took out of the view (removed, or changed so it no longer holds): drawn faded where it was. */
export interface GhostNode {
  id: string;
  type: string;
  kindTag: string;
  label: string;
  name: string;
  op: DiffOp;
  shape: Shape;
  radius: number;
  /** What changed ("status: active → superseded"), or that it was removed. */
  detail: string;
  change: GraphChange;
}

export interface ExplorerModel {
  accountId: string;
  nodes: readonly ExplorerNode[];
  edges: readonly ExplorerEdge[];
  byId: ReadonlyMap<string, ExplorerNode>;
  edgeById: ReadonlyMap<string, ExplorerEdge>;
  neighbors: ReadonlyMap<string, ReadonlySet<string>>;
  /** The activity the Play event added: where its changes animate in from. */
  eventNodeId: string | null;
  /** Edges left out because an endpoint is not in this read. */
  droppedEdges: number;
  ghosts: readonly GhostNode[];
  ghostById: ReadonlyMap<string, GhostNode>;
}

export interface ModelInput {
  graph: Graph;
  marks: DiffIndex;
  showMarks: boolean;
  activities: readonly Activity[];
  state: AccountState | null;
  eventId: string | null;
  /** The labels of the previous view (Before Play), so a ghost is named as it was. */
  previousLabels?: ReadonlyMap<string, string>;
}

function timeOf(node: GraphNode, activities: ReadonlyMap<string, Activity>): number | null {
  if (!isActivityType(node.type)) return null;
  const raw = node.valid_from ?? activities.get(node.id)?.occurred_at;
  const ms = raw === undefined ? NaN : Date.parse(raw);
  return Number.isNaN(ms) ? null : ms;
}

function toNode(node: GraphNode, input: ModelInput, activities: ReadonlyMap<string, Activity>, ctx: NodeLabelContext): ExplorerNode {
  const kind = kindOf(node.type);
  const withheld = node.withheld === "visibility";
  const shown = nodeLabel(node, ctx);
  const tag = kindTag(node.type);
  const mark = input.showMarks ? input.marks.nodes.get(node.id) : undefined;
  const activityType = activities.get(node.id)?.activity_type ?? node.label;
  const detail = withheld || !shown.fact || shown.fact.text === "" ? undefined : shown.fact.text;
  return {
    id: node.id,
    type: node.type,
    label: shown.label,
    kindTag: tag,
    name: `${shown.label} (${tag})`,
    region: kind.region,
    shape: kind.shape,
    radius: kind.radius,
    major: kind.major || (isActivityType(node.type) && MAJOR_ACTIVITIES.has(activityType)),
    withheld,
    at: timeOf(node, activities),
    sourceEventIds: node.source_event_ids,
    evidence: node.evidence_refs.map((r) => r.activity_id),
    ...(mark ? { mark } : {}),
    ...(detail ? { detail } : {}),
    ...(shown.fact && !withheld ? { fact: shown.fact } : {}),
    ...(shown.field && !withheld ? { field: shown.field } : {}),
    ...(shown.standing && !withheld ? { standing: shown.standing } : {}),
    ...(node.status ? { status: node.status } : {}),
    ...(node.valid_from ? { validFrom: node.valid_from } : {}),
  };
}

function neighborIndex(nodes: readonly ExplorerNode[], edges: readonly ExplorerEdge[]): ReadonlyMap<string, ReadonlySet<string>> {
  const out = new Map<string, Set<string>>(nodes.map((n) => [n.id, new Set<string>()]));
  for (const e of edges) {
    out.get(e.source)!.add(e.target);
    out.get(e.target)!.add(e.source);
  }
  return out;
}

function eventNode(nodes: readonly ExplorerNode[], eventId: string | null): string | null {
  const added = nodes.filter((n) => n.mark === "added" && isActivityType(n.type));
  const own = eventId === null ? undefined : added.find((n) => n.sourceEventIds.includes(eventId));
  return (own ?? added[0])?.id ?? null;
}

const changeText = (c: GraphChange): string =>
  c.op === "removed"
    ? "removed by this event"
    : Object.entries(c.changed ?? {})
        .map(([k, v]) => `${k}: ${String(v.before)} → ${String(v.after)}`)
        .join("; ");

/** The event's own changes to nodes that are not in this view (After Play only). */
function ghostsOf(input: ModelInput, present: ReadonlyMap<string, ExplorerNode>): GhostNode[] {
  if (!input.showMarks) return [];
  return input.marks.changes
    .filter((c) => c.kind === "node" && c.attributed_to_event && !present.has(c.id) && (c.op === "removed" || c.op === "changed"))
    .map((c) => {
      const kind = kindOf(c.type);
      const tag = kindTag(c.type);
      const props = (c.props ?? {}) as Record<string, unknown>;
      const named = [props.name, props.label].find((v): v is string => typeof v === "string" && v !== "");
      const label = input.previousLabels?.get(c.id) ?? named ?? tag;
      return { id: c.id, type: c.type, kindTag: tag, label, name: `${label} (${tag}, no longer in the graph)`, op: c.op, shape: kind.shape, radius: kind.radius, detail: changeText(c), change: c };
    });
}

export function buildExplorerModel(input: ModelInput): ExplorerModel {
  const activities = new Map(input.activities.map((a) => [a.id, a]));
  const ctx: NodeLabelContext = { claims: indexClaims(input.state), activities, labelsById: new Map(input.graph.nodes.map((n) => [n.id, n.label])) };
  const nodes = input.graph.nodes.map((n) => toNode(n, input, activities, ctx));
  const byId = new Map(nodes.map((n) => [n.id, n]));
  const edges: ExplorerEdge[] = [];
  for (const e of input.graph.edges) {
    if (!byId.has(e.source) || !byId.has(e.target)) continue;
    const mark = input.showMarks ? input.marks.edges.get(e.id) : undefined;
    edges.push({ id: e.id, source: e.source, target: e.target, relType: e.rel_type, label: edgeLabel(e.rel_type), raw: e, ...(mark ? { mark } : {}) });
  }
  const ghosts = ghostsOf(input, byId);
  return {
    accountId: input.graph.account_id,
    nodes,
    edges,
    byId,
    edgeById: new Map(edges.map((e) => [e.id, e])),
    neighbors: neighborIndex(nodes, edges),
    eventNodeId: eventNode(nodes, input.eventId),
    droppedEdges: input.graph.edges.length - edges.length,
    ghosts,
    ghostById: new Map(ghosts.map((g) => [g.id, g])),
  };
}
