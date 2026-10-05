import { describe, expect, it } from "vitest";
import type { Activity, Graph, GraphDiff } from "@/lib/api/types";
import { indexDiff } from "@/lib/view/diff";
import { buildExplorerModel } from "@/lib/graph/model";
import { diffRows, planEntrance, seedPositions, STAGGER_MS } from "@/lib/graph/transition";
import type { Point } from "@/lib/graph/geometry";
import { loadFixture } from "./contract-validator";

const graph = loadFixture<Graph>("medtech.graph.json");
const before = loadFixture<Graph>("medtech.graph-before.json");
const diff = loadFixture<GraphDiff>("medtech.graph-diff.json");
const activities = loadFixture<{ items: Activity[] }>("medtech.timeline.json").items;
const EVENT = "05e0ad00-0000-4000-8000-00000000ad0d";
const EVENT_ACTIVITY = "0ac7ad00-0000-4000-8000-00000000ad0d";
const OPPORTUNITY = "0c0fad00-0000-4000-8000-00000000ad01";
const SIGNAL = "051aad00-0000-4000-8000-00000000ad40";

const afterModel = buildExplorerModel({ graph, marks: indexDiff(diff), showMarks: true, activities, state: null, eventId: EVENT });
const beforeModel = buildExplorerModel({ graph: before, marks: indexDiff(diff), showMarks: false, activities, state: null, eventId: EVENT });

/** The Before Play layout: every Before node at a distinct point. */
const beforeLayout = new Map<string, Point>(beforeModel.nodes.map((n, i) => [n.id, { x: i * 40, y: (i % 3) * 30 }]));

describe("planEntrance", () => {
  it("enters exactly the added nodes and edges, starting from the event's activity", () => {
    const e = planEntrance(afterModel);
    expect([...e.entering].sort()).toEqual(afterModel.nodes.filter((n) => n.mark === "added").map((n) => n.id).sort());
    expect(e.entering[0]).toBe(EVENT_ACTIVITY);
    expect(e.delays.get(EVENT_ACTIVITY)).toBe(0);
    expect(e.enteringEdges).toHaveLength(diff.changes.filter((c) => c.kind === "edge" && c.op === "added").length);
  });

  it("staggers the rest by graph distance from the event", () => {
    const e = planEntrance(afterModel);
    // The signal is derived from the event's email itself: one hop, in the first wave after it.
    expect(e.delays.get(SIGNAL)).toBe(STAGGER_MS);
    for (const id of e.entering) expect(e.delays.get(id)!).toBeGreaterThanOrEqual(0);
  });

  it("anchors the event's own activity on a node that was already there", () => {
    const e = planEntrance(afterModel);
    expect(e.anchorOf.get(EVENT_ACTIVITY)).toBe(OPPORTUNITY);
    expect(e.anchorOf.get(SIGNAL)).toBe(EVENT_ACTIVITY);
  });

  it("pulses changed nodes once and enters nothing Before Play", () => {
    const changed = { ...afterModel, nodes: afterModel.nodes.map((n) => (n.id === OPPORTUNITY ? { ...n, mark: "changed" as const } : n)) };
    expect(planEntrance(changed).pulsing).toEqual([OPPORTUNITY]);
    const quiet = planEntrance(beforeModel);
    expect(quiet.entering).toEqual([]);
    expect(quiet.pulsing).toEqual([]);
    expect(quiet.enteringEdges).toEqual([]);
  });

  it("still enters added nodes when no event node is known", () => {
    const orphan = { ...afterModel, eventNodeId: null };
    const e = planEntrance(orphan);
    expect(e.entering.length).toBe(afterModel.nodes.filter((n) => n.mark === "added").length);
    expect(e.delays.get(e.entering[0]!)).toBe(0);
  });
});

describe("seedPositions", () => {
  it("keeps every Before node exactly where it was: no re-layout", () => {
    const e = planEntrance(afterModel);
    const seeded = seedPositions(afterModel, beforeLayout, e, () => ({ x: 999, y: 999 }));
    for (const [id, p] of beforeLayout) {
      if (afterModel.byId.has(id)) expect(seeded.positions.get(id)).toEqual(p);
    }
    expect([...seeded.pinned].sort()).toEqual([...beforeLayout.keys()].filter((id) => afterModel.byId.has(id)).sort());
  });

  it("starts the added nodes at their anchor, close to it", () => {
    const e = planEntrance(afterModel);
    const seeded = seedPositions(afterModel, beforeLayout, e, () => ({ x: 999, y: 999 }));
    const opp = beforeLayout.get(OPPORTUNITY)!;
    const evt = seeded.positions.get(EVENT_ACTIVITY)!;
    expect(Math.hypot(evt.x - opp.x, evt.y - opp.y)).toBeLessThan(8);
    expect(seeded.pinned.has(EVENT_ACTIVITY)).toBe(false);
  });

  it("falls back to a fresh point for nodes with no history and asks for a pre-layout", () => {
    const seeded = seedPositions(beforeModel, null, planEntrance(beforeModel), (n) => ({ x: n.radius, y: 1 }));
    expect(seeded.positions.size).toBe(beforeModel.nodes.length);
    expect(seeded.needsPrelayout).toBe(true);
    expect(seeded.pinned.size).toBe(0);
    const again = seedPositions(beforeModel, beforeLayout, planEntrance(beforeModel), () => ({ x: 0, y: 0 }));
    expect(again.needsPrelayout).toBe(false);
  });

  it("is deterministic and does not mutate the previous layout", () => {
    const e = planEntrance(afterModel);
    const prev = new Map(beforeLayout);
    const a = seedPositions(afterModel, beforeLayout, e, () => ({ x: 1, y: 1 }));
    const b = seedPositions(afterModel, beforeLayout, e, () => ({ x: 1, y: 1 }));
    expect([...a.positions]).toEqual([...b.positions]);
    expect([...beforeLayout]).toEqual([...prev]);
  });
});

describe("diffRows", () => {
  it("lists what the event added with readable names", () => {
    const rows = diffRows(afterModel, indexDiff(diff));
    expect(rows.recorded).toBe(true);
    expect(rows.added).toHaveLength(diff.summary.added);
    expect(rows.added.map((r) => r.text)).toContain("Email from Fatoumata · Nov 9, 2023 (Activity)");
    expect(rows.added.map((r) => r.text)).toContain("derived from: Customer replied → Objection");
    // The superseded use case is listed as changed and opens as a ghost.
    expect(rows.changed.map((r) => r.text)).toEqual(["Fact (Fact) · status: active → superseded"]);
    expect(rows.changed[0]!.focusId).toBe("0c1aad00-0000-4000-8000-00000000ad2b");
    expect(rows.removed).toEqual([]);
    expect(rows.unattributed).toBe(0);
    const edge = rows.added.find((r) => r.kind === "edge")!;
    expect(edge.focusId).toBeTruthy();
  });

  it("reads 'graph change not recorded' when the event has no projection", () => {
    const rows = diffRows(afterModel, indexDiff({ ...diff, projected: false }));
    expect(rows.recorded).toBe(false);
    expect(rows.note).toBe("Graph change not recorded.");
    expect(diffRows(afterModel, indexDiff(null)).note).toBe("Graph change not recorded.");
  });

  it("lists changed and removed elements; a removed node opens as its ghost", () => {
    const d: GraphDiff = {
      ...diff,
      summary: { added: 0, changed: 1, removed: 2, repaired: 0 },
      changes: [
        { kind: "node", op: "changed", type: "Opportunity", id: OPPORTUNITY, changed: { stage: { before: "open", after: "won" } }, source_event_ids: [EVENT], attributed_to_event: true },
        { kind: "node", op: "removed", type: "Person", id: "gone", props: { name: "Old Contact" }, source_event_ids: [EVENT], attributed_to_event: true },
        { kind: "edge", op: "removed", type: "WORKS_AT", id: "e-gone", from: "gone", to: "0a0cad00-0000-4000-8000-00000000ad01", source_event_ids: [EVENT], attributed_to_event: true },
        { kind: "node", op: "added", type: "Claim", id: "other", source_event_ids: ["x"], attributed_to_event: false },
      ],
    };
    const rows = diffRows(afterModel, indexDiff(d));
    expect(rows.changed[0]!.text).toBe("Advanced Data Protection and Education Enhancement Deal (Deal) · stage: open → won");
    expect(rows.removed.map((r) => r.text)).toEqual(["Old Contact (Person)", "works at: Old Contact → MedTech Advances"]);
    // Removed from this view: openable as a ghost when the model was built with the same diff.
    const withGhosts = buildExplorerModel({ graph, marks: indexDiff(d), showMarks: true, activities, state: null, eventId: EVENT });
    expect(diffRows(withGhosts, indexDiff(d)).removed[0]!.focusId).toBe("gone");
    expect(rows.removed.every((r) => r.focusId === null)).toBe(true);
    expect(rows.unattributed).toBe(1);
  });
});
