// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from "vitest";
import type { Activity, Graph, GraphDiff } from "@/lib/api/types";
import { indexDiff } from "@/lib/view/diff";
import { buildExplorerModel, type ExplorerModel } from "@/lib/graph/model";
import { createLayoutMemory, type LayoutMemory } from "@/lib/graph/memory";
import { FALLBACK_PALETTE } from "@/lib/graph/palette";
import { createGraphEngine, type EngineCallbacks, type GraphEngine } from "@/lib/graph/render/engine";
import { loadFixture } from "./contract-validator";
import { fakeContext } from "./graph-canvas-fake";

const graph = loadFixture<Graph>("medtech.graph.json");
const before = loadFixture<Graph>("medtech.graph-before.json");
const diff = loadFixture<GraphDiff>("medtech.graph-diff.json");
const activities = loadFixture<{ items: Activity[] }>("medtech.timeline.json").items;
const EVENT = "05e0ad00-0000-4000-8000-00000000ad0d";
const ACCOUNT = "0a0cad00-0000-4000-8000-00000000ad01";
const SUPERSEDED = "0c1aad00-0000-4000-8000-00000000ad2b";

const beforeModel = buildExplorerModel({ graph: before, marks: indexDiff(diff), showMarks: false, activities, state: null, eventId: EVENT });
const afterModel = buildExplorerModel({ graph, marks: indexDiff(diff), showMarks: true, activities, state: null, eventId: EVENT });

interface Rig {
  engine: GraphEngine;
  host: HTMLDivElement;
  canvas: HTMLCanvasElement;
  texts: () => string[];
  callbacks: { [K in keyof EngineCallbacks]: ReturnType<typeof vi.fn> };
  /** Runs frames (16ms apart) until the engine stops asking, or `max` frames. */
  run: (max?: number) => number;
  pending: () => number;
  advance: (ms: number) => void;
}

const rigs: Rig[] = [];
afterEach(async () => {
  for (const r of rigs.splice(0)) r.engine.destroy();
  document.body.innerHTML = "";
  // d3-drag drops the click that ends a drag through a window listener it removes on the next macrotask.
  await new Promise((resolve) => setTimeout(resolve, 0));
});

function rig(model: ExplorerModel, memory: LayoutMemory, reduced = false, size?: { width: number; height: number }): Rig {
  // As in the app: the stage (host) holds the canvas; the nodes' text twins are its siblings.
  const root = document.createElement("div");
  const host = document.createElement("div");
  const canvas = document.createElement("canvas");
  host.append(canvas);
  root.append(host);
  if (size) {
    Object.defineProperty(canvas, "clientWidth", { value: size.width });
    Object.defineProperty(canvas, "clientHeight", { value: size.height });
  }
  for (const n of model.nodes) {
    const b = document.createElement("button");
    b.dataset.nodeId = n.id;
    root.append(b);
  }
  const hud = document.createElement("span");
  hud.setAttribute("data-hud-zoom", "");
  host.append(hud);
  document.body.append(root);
  const fake = fakeContext();
  canvas.getContext = (() => fake.ctx) as unknown as HTMLCanvasElement["getContext"];
  let now = 1000;
  let queue: (() => void)[] = [];
  const callbacks = { onPick: vi.fn(), onDive: vi.fn(), onBackground: vi.fn() };
  const engine = createGraphEngine({
    canvas,
    host,
    probeRoot: root,
    model,
    palette: FALLBACK_PALETTE,
    reduced,
    memory,
    callbacks,
    clock: () => now,
    schedule: (cb) => queue.push(cb),
    cancel: () => {
      queue = [];
    },
  });
  const run = (max = 5000): number => {
    let frames = 0;
    while (queue.length > 0 && frames < max) {
      const next = queue;
      queue = [];
      now += 16;
      for (const cb of next) cb();
      frames += 1;
    }
    return frames;
  };
  const r: Rig = { engine, host, canvas, texts: fake.texts, callbacks, run, pending: () => queue.length, advance: (ms) => void (now += ms) };
  rigs.push(r);
  return r;
}

const screenOf = (r: Rig, id: string): { x: number; y: number } => {
  const el = r.host.parentElement!.querySelector<HTMLElement>(`[data-node-id="${id}"]`)!;
  return { x: Number(el.dataset.sx), y: Number(el.dataset.sy) };
};

describe("graph engine", () => {
  it("draws a fresh graph, settles gently and then stops drawing", () => {
    const r = rig(beforeModel, createLayoutMemory(null));
    expect(r.host.dataset.entrance).toBe("none");
    const frames = r.run();
    expect(frames).toBeGreaterThan(5);
    expect(frames).toBeLessThan(400);
    expect(r.pending()).toBe(0);
    expect(r.host.dataset.settled).toBe("true");
    expect(r.texts()).toContain("MedTech Advances");
    expect(r.host.querySelector("[data-hud-zoom]")!.textContent).toMatch(/^\d+%$/);
    expect(Number.isFinite(screenOf(r, ACCOUNT).x)).toBe(true);
  });

  it("applies the event on top of the Before layout: no Before node moves", () => {
    const memory = createLayoutMemory(null);
    const b = rig(beforeModel, memory);
    b.run();
    b.engine.destroy();
    const beforePositions = memory.recall(beforeModel.accountId)!.positions;

    const a = rig(afterModel, memory);
    expect(a.host.dataset.entrance).toBe("running");
    a.run();
    expect(a.host.dataset.entrance).toBe("done");
    a.engine.destroy();
    const afterPositions = memory.recall(afterModel.accountId)!.positions;
    for (const n of beforeModel.nodes) {
      expect(beforePositions.get(n.id)).toBeDefined();
      expect(afterPositions.get(n.id)).toEqual(beforePositions.get(n.id));
    }
    // The added nodes left the event's activity and found their own place.
    const added = afterModel.nodes.filter((n) => n.mark === "added").map((n) => afterPositions.get(n.id)!);
    expect(new Set(added.map((p) => `${Math.round(p.x)},${Math.round(p.y)}`)).size).toBe(added.length);
  });

  it("with reduced motion shows the final layout at once: nothing moves after the first frame", () => {
    const memory = createLayoutMemory(null);
    const b = rig(beforeModel, memory, true);
    b.run(1);
    const first = new Map(beforeModel.nodes.map((n) => [n.id, screenOf(b, n.id)]));
    // The probe is written when the picture is still: after one frame it already is.
    expect(b.host.dataset.settled).toBe("true");
    b.run();
    for (const n of beforeModel.nodes) expect(screenOf(b, n.id)).toEqual(first.get(n.id));
    b.engine.destroy();

    const a = rig(afterModel, memory, true);
    a.run(1);
    expect(a.host.dataset.entrance).toBe("done");
    const afterFirst = new Map(afterModel.nodes.map((n) => [n.id, screenOf(a, n.id)]));
    a.run();
    for (const n of afterModel.nodes) expect(screenOf(a, n.id), n.id).toEqual(afterFirst.get(n.id));
  });

  it("keeps what the event took out of the view where it was Before, named as it was, and clickable", () => {
    const memory = createLayoutMemory(null);
    const b = rig(beforeModel, memory, true);
    b.run();
    b.engine.destroy();
    const recalled = memory.recall(beforeModel.accountId)!;
    expect(recalled.labels.get(SUPERSEDED)).toMatch(/^Product use case/);
    const ghosted = buildExplorerModel({ graph, marks: indexDiff(diff), showMarks: true, activities, state: null, eventId: EVENT, previousLabels: recalled.labels });
    const a = rig(ghosted, memory, true);
    a.run();
    const was = recalled.positions.get(SUPERSEDED)!;
    const k = Number(a.host.dataset.zoom);
    a.engine.focus(SUPERSEDED, "fly");
    expect(a.host.dataset.focus).toBe(SUPERSEDED);
    // Flying to the ghost centres its remembered point.
    expect(Number(a.host.dataset.zoom)).toBeGreaterThanOrEqual(k);
    a.canvas.dispatchEvent(new MouseEvent("click", { clientX: 400, clientY: 260, bubbles: true }));
    expect(a.callbacks.onPick).toHaveBeenCalledWith(SUPERSEDED);
    expect(was).toBeDefined();
  });

  it("fits the graph clear of the search bar and the legend", () => {
    const r = rig(beforeModel, createLayoutMemory(null), true, { width: 900, height: 600 });
    r.run();
    r.engine.fit();
    for (const n of beforeModel.nodes) {
      const p = screenOf(r, n.id);
      expect(p.y, n.id).toBeGreaterThanOrEqual(64);
      expect(p.y, n.id).toBeLessThanOrEqual(600 - 72);
    }
  });

  it("stops the layout when reduced motion is switched on", () => {
    const r = rig(beforeModel, createLayoutMemory(null));
    r.run(2);
    r.engine.setReduced(true);
    expect(r.run()).toBeLessThanOrEqual(2);
  });

  it("finishes the entrance even when a node is selected while it runs", () => {
    const memory = createLayoutMemory(null);
    const b = rig(beforeModel, memory);
    b.run();
    b.engine.destroy();
    const a = rig(afterModel, memory);
    a.run(3);
    a.engine.focus(ACCOUNT, "none");
    // A selected node breathes, so frames never stop on their own: run long enough for the entrance.
    a.run(600);
    expect(a.host.dataset.entrance).toBe("done");
  });

  it("eases the camera out after the entrance if a new node landed off screen", () => {
    const memory = createLayoutMemory(null);
    const b = rig(beforeModel, memory, true);
    b.run();
    // The user had zoomed right into the account before pressing After Play.
    b.engine.focus(ACCOUNT, "dive");
    b.engine.destroy();
    const a = rig(afterModel, memory, true);
    a.run();
    expect(a.host.dataset.entrance).toBe("done");
    for (const n of afterModel.nodes) {
      const p = screenOf(a, n.id);
      expect(p.x, n.id).toBeGreaterThanOrEqual(0);
      expect(p.x, n.id).toBeLessThanOrEqual(800);
      expect(p.y, n.id).toBeGreaterThanOrEqual(0);
      expect(p.y, n.id).toBeLessThanOrEqual(520);
      const twin = a.host.parentElement!.querySelector<HTMLElement>(`[data-node-id="${n.id}"]`)!;
      expect(Number.isFinite(Number(twin.dataset.wx))).toBe(true);
    }
  });

  it("ends a fresh graph's settle centered in view, unless the user already moved the camera", () => {
    const r = rig(beforeModel, createLayoutMemory(null), true, { width: 900, height: 600 });
    r.run();
    const pts = beforeModel.nodes.map((n) => screenOf(r, n.id));
    const cx = (Math.min(...pts.map((p) => p.x)) + Math.max(...pts.map((p) => p.x))) / 2;
    const cy = (Math.min(...pts.map((p) => p.y)) + Math.max(...pts.map((p) => p.y))) / 2;
    expect(Math.abs(cx - 450)).toBeLessThanOrEqual(2);
    // Centred in the room left by the chrome: 64 px above, 72 px below.
    expect(Math.abs(cy - (64 + (600 - 64 - 72) / 2))).toBeLessThanOrEqual(2);

    const touched = rig(beforeModel, createLayoutMemory(null), true, { width: 900, height: 600 });
    touched.canvas.dispatchEvent(new WheelEvent("wheel", { deltaY: -300, clientX: 100, clientY: 100, bubbles: true, cancelable: true }));
    const k = touched.host.dataset.zoom;
    touched.run();
    expect(touched.host.dataset.zoom).toBe(k);
  });

  it("drags a node with the mouse and leaves it where it was dropped", () => {
    const r = rig(beforeModel, createLayoutMemory(null), true);
    r.run();
    const at = screenOf(r, ACCOUNT);
    // d3-drag listens on event.view for the rest of the gesture; jsdom will not take view in the constructor.
    const mouse = (type: string, x: number, y: number): MouseEvent => {
      const e = new MouseEvent(type, { clientX: x, clientY: y, bubbles: true, cancelable: true, button: 0 });
      Object.defineProperty(e, "view", { value: window });
      return e;
    };
    r.canvas.dispatchEvent(mouse("mousedown", at.x, at.y));
    window.dispatchEvent(mouse("mousemove", at.x + 40, at.y + 25));
    window.dispatchEvent(mouse("mousemove", at.x + 80, at.y + 50));
    window.dispatchEvent(mouse("mouseup", at.x + 80, at.y + 50));
    expect(screenOf(r, ACCOUNT)).toEqual({ x: at.x + 80, y: at.y + 50 });
    r.run();
    expect(screenOf(r, ACCOUNT)).toEqual({ x: at.x + 80, y: at.y + 50 });
  });

  it("flies to a focused node, zooms, fits and clears", () => {
    const r = rig(beforeModel, createLayoutMemory(null), true);
    r.run();
    r.engine.focus(ACCOUNT, "fly");
    expect(Number(r.host.dataset.zoom)).toBeGreaterThanOrEqual(1.8);
    expect(r.host.dataset.focus).toBe(ACCOUNT);
    const k = Number(r.host.dataset.zoom);
    r.engine.focus(ACCOUNT, "dive");
    expect(Number(r.host.dataset.zoom)).toBeGreaterThan(k);
    r.engine.zoomBy(1 / 1.4);
    expect(Number(r.host.dataset.zoom)).toBeLessThan(Number.POSITIVE_INFINITY);
    r.engine.fit();
    expect(Number(r.host.dataset.zoom)).toBeLessThanOrEqual(1.6);
    r.engine.focus(null, "none");
    expect(r.host.dataset.focus).toBe("");
    r.engine.focus("not-a-node", "fly");
    expect(r.host.dataset.focus).toBe("");
  });

  it("reports clicks, double-clicks and background clicks from the canvas", () => {
    const r = rig(beforeModel, createLayoutMemory(null), true);
    r.run();
    r.engine.resize();
    const at = screenOf(r, ACCOUNT);
    r.canvas.dispatchEvent(new MouseEvent("click", { clientX: at.x, clientY: at.y, bubbles: true }));
    expect(r.callbacks.onPick).toHaveBeenCalledWith(ACCOUNT);
    r.canvas.dispatchEvent(new MouseEvent("dblclick", { clientX: at.x, clientY: at.y, bubbles: true }));
    expect(r.callbacks.onDive).toHaveBeenCalledWith(ACCOUNT);
    r.canvas.dispatchEvent(new MouseEvent("click", { clientX: -900, clientY: -900, bubbles: true }));
    expect(r.callbacks.onBackground).toHaveBeenCalledOnce();
    const k = Number(r.host.dataset.zoom);
    r.canvas.dispatchEvent(new MouseEvent("dblclick", { clientX: -900, clientY: -900, bubbles: true }));
    expect(Number(r.host.dataset.zoom)).toBeGreaterThan(k);
  });

  it("lights a hovered node and sets the pointer cursor", () => {
    const r = rig(beforeModel, createLayoutMemory(null), true);
    r.run();
    const at = screenOf(r, ACCOUNT);
    r.canvas.dispatchEvent(new MouseEvent("pointermove", { clientX: at.x, clientY: at.y, bubbles: true }));
    expect(r.canvas.style.cursor).toBe("pointer");
    expect(r.pending()).toBe(1);
    r.run();
    r.canvas.dispatchEvent(new MouseEvent("pointerleave", { bubbles: true }));
    expect(r.pending()).toBe(1);
  });

  it("follows the theme and reduced motion, and stops on destroy", () => {
    const r = rig(beforeModel, createLayoutMemory(null));
    r.run();
    r.engine.setPalette({ ...FALLBACK_PALETTE, canvas: "#0c0d10" });
    expect(r.pending()).toBe(1);
    r.run();
    r.engine.setReduced(true);
    r.run();
    r.engine.destroy();
    r.engine.focus(ACCOUNT, "fly");
    expect(r.pending()).toBe(0);
    const at = screenOf(r, ACCOUNT);
    r.canvas.dispatchEvent(new MouseEvent("click", { clientX: at.x, clientY: at.y, bubbles: true }));
    expect(r.callbacks.onPick).not.toHaveBeenCalled();
  });

  it("keeps the remembered view centered when the canvas is a different size (After Play adds a rail)", () => {
    const memory = createLayoutMemory(null);
    const wide = rig(beforeModel, memory, true, { width: 1000, height: 600 });
    wide.run();
    wide.engine.focus(ACCOUNT, "dive");
    expect(screenOf(wide, ACCOUNT)).toEqual({ x: 500, y: 300 });
    wide.engine.destroy();
    const narrow = rig(beforeModel, memory, true, { width: 700, height: 600 });
    narrow.run();
    expect(screenOf(narrow, ACCOUNT)).toEqual({ x: 350, y: 300 });
  });

  it("restores the remembered camera when the account is opened again", () => {
    const memory = createLayoutMemory(null);
    const first = rig(beforeModel, memory, true);
    first.run();
    first.engine.focus(ACCOUNT, "dive");
    const k = first.host.dataset.zoom;
    first.engine.destroy();
    const second = rig(beforeModel, memory, true);
    expect(second.host.dataset.zoom).toBe(k);
  });
});
