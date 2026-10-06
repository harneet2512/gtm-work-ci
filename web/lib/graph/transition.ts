// Before Play -> After Play. The event's graph diff is applied on top of the previous layout: every node
// that was already there stays exactly where it was (pinned, no re-layout), the added nodes and edges
// grow out of the event's own activity in waves by graph distance, and changed nodes pulse once. The side
// summary lists what was added, changed and removed; with no recorded projection it says so.
import { shortId } from "@/lib/format";
import type { GraphChange } from "@/lib/api/types";
import type { DiffIndex } from "@/lib/view/diff";
import { edgeLabel } from "./kinds";
import { kindTag } from "./labels";
import type { Point } from "./geometry";
import type { ExplorerModel, ExplorerNode } from "./model";

/** Delay between waves of entering nodes, by graph distance from the event. */
export const STAGGER_MS = 160;
const JITTER = 4;

export interface Entrance {
  /** Added node ids, in the order they appear. */
  entering: readonly string[];
  delays: ReadonlyMap<string, number>;
  /** The node each entering node grows out of. */
  anchorOf: ReadonlyMap<string, string>;
  enteringEdges: readonly string[];
  /** Changed or repaired nodes: they pulse once. */
  pulsing: readonly string[];
}

function bfsDistances(model: ExplorerModel, start: string, within: ReadonlySet<string>): Map<string, number> {
  const dist = new Map<string, number>([[start, 0]]);
  let frontier = [start];
  while (frontier.length > 0) {
    const next: string[] = [];
    for (const id of frontier) {
      for (const nb of model.neighbors.get(id) ?? []) {
        if (within.has(nb) && !dist.has(nb)) {
          dist.set(nb, dist.get(id)! + 1);
          next.push(nb);
        }
      }
    }
    frontier = next;
  }
  return dist;
}

/** The largest stable neighbor of a node (the deal over a person), else null. */
function stableAnchor(model: ExplorerModel, id: string, entering: ReadonlySet<string>): string | null {
  const candidates = [...(model.neighbors.get(id) ?? [])]
    .filter((nb) => !entering.has(nb))
    .map((nb) => model.byId.get(nb)!)
    .sort((a, b) => b.radius - a.radius || a.id.localeCompare(b.id));
  return candidates[0]?.id ?? null;
}

export function planEntrance(model: ExplorerModel): Entrance {
  const added = model.nodes.filter((n) => n.mark === "added").map((n) => n.id);
  const entering = new Set(added);
  const pulsing = model.nodes.filter((n) => n.mark === "changed" || n.mark === "repaired").map((n) => n.id);
  const enteringEdges = model.edges.filter((e) => e.mark === "added").map((e) => e.id);
  if (added.length === 0) return { entering: [], delays: new Map(), anchorOf: new Map(), enteringEdges, pulsing };

  const origin = model.eventNodeId !== null && entering.has(model.eventNodeId) ? model.eventNodeId : [...added].sort()[0]!;
  const dist = bfsDistances(model, origin, entering);
  const far = Math.max(...dist.values()) + 1;
  const delays = new Map(added.map((id) => [id, (dist.get(id) ?? far) * STAGGER_MS]));
  const order = [...added].sort((a, b) => delays.get(a)! - delays.get(b)! || a.localeCompare(b));
  const anchorOf = new Map<string, string>();
  const originAnchor = stableAnchor(model, origin, entering);
  if (originAnchor !== null) anchorOf.set(origin, originAnchor);
  for (const id of order) if (id !== origin) anchorOf.set(id, origin);
  return { entering: order, delays, anchorOf, enteringEdges, pulsing };
}

export interface Seeded {
  positions: ReadonlyMap<string, Point>;
  /** Nodes held in place while the entering ones settle. */
  pinned: ReadonlySet<string>;
  /** Some node that stays had no previous position: lay those out before the entrance. */
  needsPrelayout: boolean;
}

const GOLDEN_ANGLE = 2.399963;

/** A small offset per entering node, fanned out by the golden angle so siblings do not start stacked. */
function jitter(i: number): Point {
  const angle = (i * GOLDEN_ANGLE) % (Math.PI * 2);
  return { x: Math.cos(angle) * JITTER, y: Math.sin(angle) * JITTER };
}

/**
 * The starting point of every node: the previous layout where there is one, a fresh deterministic point
 * otherwise; entering nodes start on (a hair off) the node they grow out of.
 */
export function seedPositions(model: ExplorerModel, previous: ReadonlyMap<string, Point> | null, entrance: Entrance, fresh: (node: ExplorerNode) => Point): Seeded {
  const entering = new Set(entrance.entering);
  const positions = new Map<string, Point>();
  const pinned = new Set<string>();
  let needsPrelayout = false;
  for (const node of model.nodes) {
    if (entering.has(node.id)) continue;
    const before = previous?.get(node.id);
    if (before) pinned.add(node.id);
    else needsPrelayout = true;
    positions.set(node.id, before ?? fresh(node));
  }
  entrance.entering.forEach((id, i) => {
    const anchorId = entrance.anchorOf.get(id);
    const base = (anchorId === undefined ? undefined : positions.get(anchorId)) ?? fresh(model.byId.get(id)!);
    const j = jitter(i);
    positions.set(id, { x: base.x + j.x, y: base.y + j.y });
  });
  // With nothing remembered, nothing is pinned: the whole graph settles together.
  return { positions, pinned: previous === null ? new Set() : pinned, needsPrelayout };
}

export interface DiffRow {
  key: string;
  kind: "node" | "edge";
  /** The whole row as one sentence (also its accessible name). */
  text: string;
  /** Node kind, or the edge's plain-word relationship. */
  lead: string;
  /** Node label, or "source → target". */
  body: string;
  /** What changed on the element ("stage: open → won"), if anything. */
  detail: string;
  /** The node to fly to; null for an element that is not in the graph any more. */
  focusId: string | null;
}

export interface DiffRows {
  recorded: boolean;
  note: string | null;
  added: DiffRow[];
  changed: DiffRow[];
  removed: DiffRow[];
  /** Changes in the same update that the event is not evidence for. */
  unattributed: number;
}

export const NOT_RECORDED = "Graph change not recorded.";

function nameOf(model: ExplorerModel, change: GraphChange | undefined, id: string | undefined): string {
  if (!id) return "?";
  const node = model.byId.get(id) ?? model.ghostById.get(id);
  if (node) return node.label;
  const props = (change?.props ?? {}) as Record<string, unknown>;
  const named = [props.name, props.label].find((v): v is string => typeof v === "string" && v !== "");
  return named ?? shortId(id);
}

function changedText(change: GraphChange): string {
  return Object.entries(change.changed ?? {})
    .map(([key, v]) => `${key}: ${String(v.before)} → ${String(v.after)}`)
    .join("; ");
}

function rowOf(model: ExplorerModel, change: GraphChange, byId: ReadonlyMap<string, GraphChange>): DiffRow {
  const key = `${change.op}:${change.kind}:${change.id}`;
  if (change.kind === "node") {
    // A node still in the view, or the ghost of one the event took out: either can be opened.
    const target = model.byId.get(change.id) ?? model.ghostById.get(change.id);
    const detail = change.op === "changed" || change.op === "repaired" ? changedText(change) : "";
    const body = nameOf(model, change, change.id);
    const lead = kindTag(change.type);
    const text = `${body} (${lead})${detail ? ` · ${detail}` : ""}`;
    return { key, kind: "node", text, lead, body, detail, focusId: target ? target.id : null };
  }
  const edge = model.edgeById.get(change.id);
  const from = change.from ?? edge?.source;
  const to = change.to ?? edge?.target;
  const body = `${nameOf(model, byId.get(from ?? ""), from)} → ${nameOf(model, byId.get(to ?? ""), to)}`;
  const lead = edgeLabel(change.type);
  return { key, kind: "edge", text: `${lead}: ${body}`, lead, body, detail: "", focusId: change.op === "removed" || !edge ? null : edge.source };
}

export function diffRows(model: ExplorerModel, marks: DiffIndex): DiffRows {
  if (!marks.projected) return { recorded: false, note: NOT_RECORDED, added: [], changed: [], removed: [], unattributed: 0 };
  const own = marks.changes.filter((c) => c.attributed_to_event);
  const byId = new Map(marks.changes.map((c) => [c.id, c]));
  const rows = (ops: readonly string[]): DiffRow[] =>
    own
      .filter((c) => ops.includes(c.op))
      .sort((a, b) => Number(a.kind === "edge") - Number(b.kind === "edge"))
      .map((c) => rowOf(model, c, byId));
  return {
    recorded: true,
    note: null,
    added: rows(["added"]),
    changed: rows(["changed", "repaired"]),
    removed: rows(["removed"]),
    unattributed: marks.changes.length - own.length,
  };
}
