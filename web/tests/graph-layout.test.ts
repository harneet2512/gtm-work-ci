import { describe, expect, it } from "vitest";
import type { Activity, Graph, GraphDiff } from "@/lib/api/types";
import { indexDiff } from "@/lib/view/diff";
import { buildExplorerModel, type ExplorerModel } from "@/lib/graph/model";
import { connectionDepths, createLayout, PRELAYOUT_TICKS, seedFor } from "@/lib/graph/layout";
import type { Point } from "@/lib/graph/geometry";
import { planEntrance, seedPositions } from "@/lib/graph/transition";
import { loadFixture } from "./contract-validator";

const graph = loadFixture<Graph>("medtech.graph.json");
const diff = loadFixture<GraphDiff>("medtech.graph-diff.json");
const activities = loadFixture<{ items: Activity[] }>("medtech.timeline.json").items;
const model = buildExplorerModel({ graph, marks: indexDiff(diff), showMarks: false, activities, state: null, eventId: null });

function settled(m: ExplorerModel, previous: ReadonlyMap<string, Point> | null = null) {
  const seeded = seedPositions(m, previous, planEntrance(m), seedFor(m));
  const layout = createLayout(m, seeded.positions, seeded.pinned);
  layout.prelayout(PRELAYOUT_TICKS);
  const out = layout.positions();
  layout.stop();
  return out;
}

const dist = (a: Point, b: Point): number => Math.hypot(a.x - b.x, a.y - b.y);

/** A synthetic account of about two hundred nodes, built like the core's neighborhood. */
function bigGraph(): Graph {
  const nodes: Graph["nodes"] = [{ id: "acc", type: "Account", label: "Big Co", evidence_refs: [], source_event_ids: [] }];
  const edges: Graph["edges"] = [];
  const types = ["Person", "Activity", "Claim", "Signal", "Opportunity"];
  for (let i = 0; i < 200; i += 1) {
    const type = types[i % types.length]!;
    nodes.push({ id: `n${i}`, type, label: `${type} ${i}`, valid_from: type === "Activity" ? new Date(Date.UTC(2023, 0, 1 + i)).toISOString() : undefined, evidence_refs: [], source_event_ids: [] });
    edges.push({ id: `e${i}`, source: `n${i}`, target: i % 3 === 0 ? "acc" : `n${Math.max(0, i - 5)}`, rel_type: "ABOUT", status: "active", evidence_refs: [], source_event_ids: [] });
  }
  return { account_id: "acc", nodes, edges, sections: {}, truncated: false, withheld: false, projection: { complete: true, projected_at: null } };
}

describe("createLayout", () => {
  it("is deterministic: the same graph settles on the same points", () => {
    const a = settled(model);
    const b = settled(model);
    expect([...a]).toEqual([...b]);
  });

  it("counts links from the account: the deal, people and facts one away, activities further", () => {
    const depth = connectionDepths(model);
    expect(depth.get("0a0cad00-0000-4000-8000-00000000ad01")).toBe(0);
    expect(depth.get("0c0fad00-0000-4000-8000-00000000ad01")).toBe(1);
    expect(depth.get("0b0ead00-0000-4000-8000-00000000ad11")).toBe(1);
    expect(depth.get("0ac7ad00-0000-4000-8000-00000000ad04")).toBe(2);
    // An island with no path to the account is placed one ring beyond the farthest connected node.
    const lonely = { ...model, nodes: [...model.nodes, { ...model.nodes[4]!, id: "lonely" }], neighbors: new Map([...model.neighbors, ["lonely", new Set<string>()]]) };
    expect(connectionDepths(lonely).get("lonely")).toBe(Math.max(...depth.values()) + 1);
  });

  it("is one connected structure: the account at the centre, the deal beside it, the rest radiating by link", () => {
    const pos = settled(model);
    const at = (id: string) => pos.get(id)!;
    const account = at("0a0cad00-0000-4000-8000-00000000ad01");
    expect(Math.hypot(account.x, account.y)).toBeLessThan(1);
    const depth = connectionDepths(model);
    const mean = (d: number) => {
      const ds = model.nodes.filter((n) => depth.get(n.id) === d).map((n) => dist(at(n.id), account));
      return ds.reduce((x, y) => x + y, 0) / ds.length;
    };
    expect(mean(1)).toBeLessThan(mean(2));
    expect(dist(at("0c0fad00-0000-4000-8000-00000000ad01"), account)).toBeLessThan(mean(1));
  });

  it("keeps pinned nodes exactly in place while the rest settles", () => {
    const first = settled(model);
    const pinned = new Set([...first.keys()].slice(0, 5));
    const seeded = new Map(first);
    const layout = createLayout(model, seeded, pinned);
    layout.prelayout(80);
    const after = layout.positions();
    for (const id of pinned) expect(after.get(id)).toEqual(first.get(id));
    layout.release();
    layout.stop();
  });

  it("keeps nodes from overlapping", () => {
    const pos = settled(model);
    const nodes = model.nodes;
    for (let i = 0; i < nodes.length; i += 1) {
      for (let j = i + 1; j < nodes.length; j += 1) {
        expect(dist(pos.get(nodes[i]!.id)!, pos.get(nodes[j]!.id)!)).toBeGreaterThan(nodes[i]!.radius + nodes[j]!.radius);
      }
    }
  });

  it("never blocks the first frame for long: the pre-layout stops at its time budget", () => {
    const big = buildExplorerModel({ graph: bigGraph(), marks: indexDiff(null), showMarks: false, activities: [], state: null, eventId: null });
    expect(big.nodes.length).toBeGreaterThan(200);
    const seeded = seedPositions(big, null, planEntrance(big), seedFor(big));
    const layout = createLayout(big, seeded.positions, seeded.pinned);
    // A clock that advances 10ms per read: the budget of 50ms allows only a few ticks.
    let now = 0;
    const ran = layout.prelayout(PRELAYOUT_TICKS, 50, () => (now += 10));
    expect(ran).toBeGreaterThan(0);
    expect(ran).toBeLessThan(PRELAYOUT_TICKS);
    // The rest of the settle runs live, one tick per frame, and each tick is cheap: about 2 ms at this size in
    // isolation. The median against two frames (33 ms) tolerates a loaded CI runner with coverage.
    const samples: number[] = [];
    for (;;) {
      const t0 = performance.now();
      if (!layout.tick()) break;
      samples.push(performance.now() - t0);
    }
    expect(samples.length).toBeGreaterThan(0);
    samples.sort((a, b) => a - b);
    expect(samples[Math.floor(samples.length / 2)]).toBeLessThan(33);
    for (const p of layout.positions().values()) expect(Number.isFinite(p.x) && Number.isFinite(p.y)).toBe(true);
    layout.stop();
  });

  it("ticks live and reports when it has cooled", () => {
    const seeded = seedPositions(model, null, planEntrance(model), seedFor(model));
    const layout = createLayout(model, seeded.positions, seeded.pinned);
    layout.reheat(0.3);
    let ticks = 0;
    while (layout.tick() && ticks < 1000) ticks += 1;
    expect(ticks).toBeGreaterThan(0);
    expect(layout.tick()).toBe(false);
    layout.stop();
  });

  it("moves a dragged node and lets it go", () => {
    const seeded = seedPositions(model, null, planEntrance(model), seedFor(model));
    const layout = createLayout(model, seeded.positions, seeded.pinned);
    const id = model.nodes[1]!.id;
    layout.drag(id, { x: 500, y: 500 });
    expect(layout.positions().get(id)).toEqual({ x: 500, y: 500 });
    layout.drop(id);
    layout.drag("missing", { x: 0, y: 0 });
    layout.drop("missing");
    layout.stop();
  });
});
