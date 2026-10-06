import type { GraphChange, GraphDiff } from "@/lib/api/types";

export type DiffOp = GraphChange["op"];

export interface DiffIndex {
  /** False when the core has not recorded a projection of the event yet. */
  projected: boolean;
  nodes: ReadonlyMap<string, DiffOp>;
  edges: ReadonlyMap<string, DiffOp>;
  /** Elements the event removed: they are not in the graph any more, so they are listed apart. */
  removed: readonly GraphChange[];
  summary: GraphDiff["summary"];
  changes: readonly GraphChange[];
}

// An element that is both added and changed in the merged diff reads as added (it is new to the account).
const STRENGTH: Record<DiffOp, number> = { repaired: 0, changed: 1, added: 2, removed: 3 };

const EMPTY_SUMMARY: GraphDiff["summary"] = { added: 0, changed: 0, removed: 0, repaired: 0 };

function strongest(map: Map<string, DiffOp>, id: string, op: DiffOp): void {
  const current = map.get(id);
  if (current === undefined || STRENGTH[op] > STRENGTH[current]) map.set(id, op);
}

export function indexDiff(diff: GraphDiff | null): DiffIndex {
  const nodes = new Map<string, DiffOp>();
  const edges = new Map<string, DiffOp>();
  if (!diff) return { projected: false, nodes, edges, removed: [], summary: EMPTY_SUMMARY, changes: [] };
  for (const change of diff.changes) {
    // Only the elements the event is itself evidence for are highlighted (M2). The others changed in
    // the same coalesced burst and are still listed in the summary, but they are not "what N did".
    if (!change.attributed_to_event) continue;
    strongest(change.kind === "node" ? nodes : edges, change.id, change.op);
  }
  return {
    projected: diff.projected,
    nodes,
    edges,
    removed: diff.changes.filter((c) => c.op === "removed"),
    summary: diff.summary,
    changes: diff.changes,
  };
}
