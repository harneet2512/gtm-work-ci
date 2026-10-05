import { describe, expect, it } from "vitest";
import type { Activity, Graph, GraphDiff } from "@/lib/api/types";
import { indexDiff } from "@/lib/view/diff";
import { buildExplorerModel, type ExplorerModel } from "@/lib/graph/model";
import { FALLBACK_PALETTE } from "@/lib/graph/palette";
import { planEntrance } from "@/lib/graph/transition";
import type { Point } from "@/lib/graph/geometry";
import { seedFor } from "@/lib/graph/layout";
import { bornProgress, easeOutBack, entranceLength, focusRole, GROW_MS, type Frame } from "@/lib/graph/render/frame";
import { drawBackground, drawEdges, drawEffects, sceneAnimating } from "@/lib/graph/render/draw-scene";
import { drawLabels, drawNodes, pickGhost, pickNode } from "@/lib/graph/render/draw-nodes";
import { tracePath } from "@/lib/graph/render/shapes";
import { loadFixture } from "./contract-validator";
import { fakeContext } from "./graph-canvas-fake";

const graph = loadFixture<Graph>("medtech.graph.json");
const diff = loadFixture<GraphDiff>("medtech.graph-diff.json");
const activities = loadFixture<{ items: Activity[] }>("medtech.timeline.json").items;
const EVENT = "05e0ad00-0000-4000-8000-00000000ad0d";
const EVENT_ACTIVITY = "0ac7ad00-0000-4000-8000-00000000ad0d";
const FATOUMATA = "0b0ead00-0000-4000-8000-00000000ad11";
const ACCOUNT = "0a0cad00-0000-4000-8000-00000000ad01";

const after = buildExplorerModel({ graph, marks: indexDiff(diff), showMarks: true, activities, state: null, eventId: EVENT });

/** Nodes spread on a wide grid so labels never collide unless a test wants them to. */
function spread(model: ExplorerModel): Map<string, Point> {
  return new Map(model.nodes.map((n, i) => [n.id, { x: (i % 5) * 160, y: Math.floor(i / 5) * 120 }]));
}

function frame(over: Partial<Frame> = {}, model: ExplorerModel = after): Frame {
  const pos = spread(model);
  return {
    model,
    point: (id) => pos.get(id),
    camera: { x: 40, y: 40, k: 1 },
    width: 1000,
    height: 600,
    palette: FALLBACK_PALETTE,
    focusId: null,
    hoverId: null,
    now: 0,
    reduced: false,
    motion: { start: null, entrance: planEntrance(model) },
    ...over,
  };
}

describe("frame timing", () => {
  it("grows entering nodes in their wave and leaves the rest alone", () => {
    const f = frame({ motion: { start: 0, entrance: planEntrance(after) }, now: 0 });
    expect(bornProgress(f, EVENT_ACTIVITY)).toBe(0);
    expect(bornProgress({ ...f, now: GROW_MS / 2 }, EVENT_ACTIVITY)).toBeCloseTo(0.5);
    expect(bornProgress({ ...f, now: GROW_MS * 3 }, EVENT_ACTIVITY)).toBe(1);
    expect(bornProgress(f, ACCOUNT)).toBe(1);
    expect(bornProgress({ ...f, reduced: true }, EVENT_ACTIVITY)).toBe(1);
  });

  it("knows how long an entrance runs and eases with a small overshoot", () => {
    expect(entranceLength(planEntrance(after))).toBeGreaterThan(GROW_MS);
    expect(entranceLength({ entering: [], delays: new Map(), anchorOf: new Map(), enteringEdges: [], pulsing: [] })).toBe(0);
    expect(easeOutBack(0)).toBeCloseTo(0);
    expect(easeOutBack(1)).toBeCloseTo(1);
    expect(Math.max(...[0.6, 0.7, 0.8, 0.9].map(easeOutBack))).toBeGreaterThan(1);
  });

  it("lights the hovered or selected node's neighborhood and dims the rest", () => {
    expect(focusRole(frame(), ACCOUNT)).toBe("none");
    const f = frame({ focusId: FATOUMATA });
    expect(focusRole(f, FATOUMATA)).toBe("focus");
    expect(focusRole(f, ACCOUNT)).toBe("neighbor");
    expect(focusRole(f, "0c1aad00-0000-4000-8000-00000000ad27")).toBe("dimmed");
    expect(focusRole(frame({ focusId: FATOUMATA, hoverId: ACCOUNT }), ACCOUNT)).toBe("focus");
    expect(focusRole(frame({ focusId: "gone" }), ACCOUNT)).toBe("none");
  });
});

describe("drawing", () => {
  it("draws the canvas, every edge and every node as one structure, with no regions", () => {
    const { ctx, calls, texts } = fakeContext();
    const f = frame({ width: 4000, height: 3000 });
    drawBackground(ctx, f);
    drawEdges(ctx, f);
    drawNodes(ctx, f);
    expect(calls.filter((c) => c.name === "fillRect").length).toBeGreaterThan(10);
    expect(texts()).not.toContain("PEOPLE");
    expect(calls.filter((c) => c.name === "lineTo").length).toBeGreaterThanOrEqual(after.edges.length);
    expect(calls.filter((c) => c.name === "fill").length).toBeGreaterThanOrEqual(after.nodes.length);
  });

  it("labels major nodes when zoomed out and minor ones only when zoomed in, each under a small kind tag", () => {
    const far = fakeContext();
    drawLabels(far.ctx, frame({ camera: { x: 40, y: 40, k: 0.6 } }));
    expect(far.texts()).toContain("MedTech Advances");
    expect(far.texts()).toContain("ACCOUNT");
    expect(far.texts().some((t) => t.startsWith("Email from"))).toBe(false);
    const near = fakeContext();
    const n = drawLabels(near.ctx, frame({ camera: { x: 0, y: 0, k: 1.6 }, width: 4000, height: 3000 }));
    expect(near.texts().some((t) => t.startsWith("Email from"))).toBe(true);
    expect(near.texts()).toContain("ACTIVITY");
    expect(n).toBeGreaterThan(0);
    expect(String((near.ctx as unknown as { font: string }).font)).not.toContain("NaN");
  });

  it("draws the selected node's whole label on a plate, in ink and heavier, even for a minor kind", () => {
    const { ctx, calls } = fakeContext();
    drawLabels(ctx, frame({ focusId: EVENT_ACTIVITY, camera: { x: 40, y: 40, k: 1 } }));
    const own = calls.find((c) => c.name === "fillText" && String(c.args[0]).startsWith("Email from Fatoumata"))!;
    expect(own.fillStyle).toBe(FALLBACK_PALETTE.label);
    expect(String(own.font)).toMatch(/^600 /);
    expect(calls.some((c) => c.name === "roundRect")).toBe(true);
    const deal = fakeContext();
    drawLabels(deal.ctx, frame({ focusId: "0c0fad00-0000-4000-8000-00000000ad01", camera: { x: 40, y: 40, k: 0.5 } }));
    expect(deal.texts().join(" ")).toContain("Advanced Data Protection and Education Enhancement Deal");
  });

  it("never shows status by colour alone: added nodes carry a + badge, changed a Δ badge and a dashed ring", () => {
    const changed = { ...after, nodes: after.nodes.map((n) => (n.id === ACCOUNT ? { ...n, mark: "changed" as const } : n)) };
    const { ctx, calls, texts } = fakeContext();
    drawNodes(ctx, frame({ width: 4000, height: 3000 }, changed));
    expect(texts()).toContain("+");
    expect(texts()).toContain("Δ");
    expect(calls.some((c) => c.name === "setLineDash" && JSON.stringify(c.args[0]) === "[3,2]")).toBe(true);
  });

  it("dashes a changed edge differently from an added one", () => {
    const edge = after.edges.find((e) => e.mark === "added")!;
    const m = { ...after, edges: after.edges.map((e) => (e.id === edge.id ? { ...e, mark: "changed" as const } : e)) };
    const rec = fakeContext();
    drawEdges(rec.ctx, frame({ width: 4000, height: 3000 }, m));
    expect(rec.calls.some((c) => c.name === "setLineDash" && JSON.stringify(c.args[0]) === "[6,3]")).toBe(true);
  });

  it("labels edges only once zoomed in, earlier around the selection", () => {
    const out = fakeContext();
    drawLabels(out.ctx, frame({ camera: { x: 0, y: 0, k: 1 } }));
    expect(out.texts()).not.toContain("works at");
    const deep = fakeContext();
    drawLabels(deep.ctx, frame({ camera: { x: 0, y: 0, k: 2.2 }, width: 5000, height: 4000 }));
    expect(deep.texts()).toContain("works at");
    const sel = fakeContext();
    drawLabels(sel.ctx, frame({ camera: { x: 0, y: 0, k: 1.5 }, width: 5000, height: 4000, focusId: FATOUMATA }));
    expect(sel.texts()).toContain("works at");
    expect(sel.texts()).not.toContain("supported by");
  });

  it("draws what the event took out of the view as a faded ghost where it was, with its own badge", () => {
    const labels = new Map([["0c1aad00-0000-4000-8000-00000000ad2b", "Product use case: Data protection"]]);
    const m = buildExplorerModel({ graph, marks: indexDiff(diff), showMarks: true, activities, state: null, eventId: EVENT, previousLabels: labels });
    const ghostAt = new Map([["0c1aad00-0000-4000-8000-00000000ad2b", { x: 100, y: 100 }]]);
    const { ctx, texts, calls } = fakeContext();
    const f = frame({ ghostPoint: (id) => ghostAt.get(id) }, m);
    drawNodes(ctx, f);
    drawLabels(ctx, f);
    expect(texts()).toContain("Product use case: Data protection");
    expect(texts()).toContain("FACT · NO LONGER HELD");
    expect(calls.some((c) => c.name === "setLineDash" && JSON.stringify(c.args[0]) === "[2,3]")).toBe(true);
    expect(pickGhost(f, 140, 140)).toBe("0c1aad00-0000-4000-8000-00000000ad2b");
    // A long ghost label is clipped by zoom like any other, so it never runs across its neighbours.
    const long = new Map([["0c1aad00-0000-4000-8000-00000000ad2b", "Product use case: Data protection and healthcare education: SecureData Nexus, CryptGuard Module, EduTech Lab"]]);
    const lm = buildExplorerModel({ graph, marks: indexDiff(diff), showMarks: true, activities, state: null, eventId: EVENT, previousLabels: long });
    const rec = fakeContext();
    drawLabels(rec.ctx, frame({ ghostPoint: (id) => ghostAt.get(id), camera: { x: 40, y: 40, k: 1 } }, lm));
    const ghostText = rec.texts().find((t) => t.startsWith("Product use case"))!;
    expect(ghostText.length).toBeLessThanOrEqual(36);
    expect(ghostText.endsWith("…")).toBe(true);
    expect(pickGhost(f, 900, 900)).toBeNull();
  });

  it("draws the event's ripple and nothing when motion is reduced", () => {
    const f = frame({ motion: { start: 0, entrance: planEntrance(after) }, now: 300 });
    const live = fakeContext();
    drawEffects(live.ctx, f);
    expect(live.calls.some((c) => c.name === "arc")).toBe(true);
    const calm = fakeContext();
    drawEffects(calm.ctx, { ...f, reduced: true });
    expect(calm.calls).toHaveLength(0);
  });

  it("pulses a changed node once", () => {
    const changed = { ...after, nodes: after.nodes.map((n) => (n.id === ACCOUNT ? { ...n, mark: "changed" as const } : n)) };
    const entrance = planEntrance(changed);
    const at = (now: number) => {
      const fk = fakeContext();
      drawEffects(fk.ctx, frame({ motion: { start: 0, entrance }, now }, changed));
      return fk.calls.filter((c) => c.name === "arc").length;
    };
    expect(at(700)).toBeGreaterThan(at(5000));
  });

  it("keeps animating only while something moves", () => {
    expect(sceneAnimating(frame())).toBe(false);
    expect(sceneAnimating(frame({ focusId: ACCOUNT }))).toBe(true);
    expect(sceneAnimating(frame({ focusId: ACCOUNT, reduced: true }))).toBe(false);
    const f = frame({ motion: { start: 0, entrance: planEntrance(after) }, now: 100 });
    expect(sceneAnimating(f)).toBe(true);
    expect(sceneAnimating({ ...f, now: 60_000 })).toBe(false);
  });

  it("flows along changed edges during the entrance and along the selection's edges", () => {
    const dashes = (f: Frame) => {
      const fk = fakeContext();
      drawEdges(fk.ctx, f);
      return fk.calls.filter((c) => c.name === "setLineDash" && (c.args[0] as number[]).length > 0).length;
    };
    expect(dashes(frame())).toBe(0);
    expect(dashes(frame({ motion: { start: 0, entrance: planEntrance(after) }, now: 1500 }))).toBeGreaterThan(0);
    expect(dashes(frame({ focusId: FATOUMATA }))).toBeGreaterThan(0);
    expect(dashes(frame({ focusId: FATOUMATA, reduced: true }))).toBe(0);
  });

  it("draws a distinct outline for every shape", () => {
    for (const shape of ["ring", "hexagon", "circle", "diamond", "square", "triangle", "pentagon"] as const) {
      const { ctx, calls } = fakeContext();
      tracePath(ctx, shape, 0, 0, 5);
      expect(calls[0]!.name).toBe("beginPath");
      expect(calls.length).toBeGreaterThan(1);
    }
  });

  it("draws a withheld node with a dashed outline", () => {
    const hidden = buildExplorerModel({ graph: { ...graph, nodes: graph.nodes.map((n, i) => (i === 0 ? { ...n, withheld: "visibility" as const } : n)) }, marks: indexDiff(null), showMarks: false, activities, state: null, eventId: null });
    const { ctx, calls } = fakeContext();
    drawNodes(ctx, frame({}, hidden));
    expect(calls.some((c) => c.name === "setLineDash" && (c.args[0] as number[]).length === 2)).toBe(true);
  });
});

describe("frame cost", () => {
  it("draws a two-hundred-node account well inside a frame budget", () => {
    const nodes: Graph["nodes"] = [{ id: "acc", type: "Account", label: "Big Co", evidence_refs: [], source_event_ids: [] }];
    const edges: Graph["edges"] = [];
    const types = ["Person", "Activity", "Claim", "Signal", "Opportunity", "Knowledge"];
    for (let i = 0; i < 200; i += 1) {
      const type = types[i % types.length]!;
      nodes.push({ id: `n${i}`, type, label: `${type} number ${i}`, valid_from: type === "Activity" ? "2023-11-01T00:00:00Z" : undefined, evidence_refs: [], source_event_ids: [] });
      edges.push({ id: `e${i}`, source: `n${i}`, target: i % 4 === 0 ? "acc" : `n${Math.max(0, i - 3)}`, rel_type: "ABOUT", status: "active", evidence_refs: [], source_event_ids: [] });
    }
    const big = buildExplorerModel({ graph: { ...graph, nodes, edges }, marks: indexDiff(null), showMarks: false, activities: [], state: null, eventId: null });
    const pos = new Map(big.nodes.map((n) => [n.id, seedFor(big)(n)]));
    const { ctx } = fakeContext();
    const draw = (f: Frame) => {
      drawBackground(ctx, f);
      drawEdges(ctx, f);
      drawEffects(ctx, f);
      drawNodes(ctx, f);
      drawLabels(ctx, f);
    };
    const f = frame({ point: (id) => pos.get(id), camera: { x: 700, y: 450, k: 1.6 }, width: 1400, height: 900, focusId: "n7" }, big);
    draw(f); // warm up
    const samples = Array.from({ length: 21 }, (_, i) => {
      const t0 = performance.now();
      draw({ ...f, now: i * 16 });
      return performance.now() - t0;
    }).sort((a, b) => a - b);
    // About 12 ms per frame in isolation through this recording proxy (slower than a real canvas). The median of
    // the samples, against two frames (33 ms), tolerates a loaded CI runner with coverage and still fails on an
    // order-of-magnitude regression such as quadratic work per frame.
    expect(samples[10]).toBeLessThan(33);
  });
});

describe("pickNode", () => {
  it("finds the node under the pointer, and nothing on empty canvas", () => {
    const f = frame();
    const p = spread(after).get(ACCOUNT)!;
    expect(pickNode(f, p.x + 40 + 2, p.y + 40)).toBe(ACCOUNT);
    expect(pickNode(f, -500, -500)).toBeNull();
  });

  it("does not pick a node that has not appeared yet", () => {
    const p = spread(after).get(EVENT_ACTIVITY)!;
    const f = frame({ motion: { start: 0, entrance: planEntrance(after) }, now: 0 });
    expect(pickNode(f, p.x + 40, p.y + 40)).toBeNull();
  });

  it("works from fresh seed points too", () => {
    const pos = new Map(after.nodes.map((n) => [n.id, seedFor(after)(n)]));
    const f = frame({ point: (id) => pos.get(id), camera: { x: 500, y: 300, k: 1 } });
    const a = pos.get(ACCOUNT)!;
    expect(pickNode(f, a.x + 500, a.y + 300)).not.toBeNull();
  });
});
