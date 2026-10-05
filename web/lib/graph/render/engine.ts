// The explorer's imperative core: owns the layout, the camera and the render loop, and draws to the
// canvas. React never re-renders per frame; it calls this API when the selection changes and receives
// clicks through callbacks. The loop runs only while something moves (layout settling, a camera flight,
// the entrance, a selected node breathing) and stops when the picture is still.
import { DIVE_ZOOM, FOCUS_ZOOM, fitTransform, focusTransform, invertTransform, ZOOM_STEP, type Camera, type Insets } from "../camera";
import type { Point } from "../geometry";
import { createLayout, PRELAYOUT_TICKS, seedFor, type Layout } from "../layout";
import type { LayoutMemory } from "../memory";
import type { ExplorerModel } from "../model";
import type { Palette } from "../palette";
import { planEntrance, seedPositions } from "../transition";
import { drawLabels, drawNodes, pickGhost, pickNode } from "./draw-nodes";
import { drawBackground, drawEdges, drawEffects, sceneAnimating } from "./draw-scene";
import { entranceLength, type Frame, type Motion } from "./frame";
import { attachInput, type Input } from "./input";

/** Ticks left for the visible settle after the synchronous pre-layout. */
const SETTLE_TICKS = 90;
/** The longest the synchronous pre-layout may block before the first frame; a large graph settles the rest live. */
const PRELAYOUT_BUDGET_MS = 120;
/** The page paints before the entrance starts. */
const ENTRANCE_LEAD_MS = 350;
const ENTRANCE_HEAT = 0.4;
const FLY_MS = 650;
const DIVE_MS = 800;
const PAN_MS = 450;
const FIT_MS = 600;
const ZOOM_MS = 220;
const MAX_DPR = 2;
/** Room kept clear when fitting: the search bar and zoom buttons above, the legend below, label width at the sides. */
const FIT_INSETS: Insets = { top: 64, right: 88, bottom: 72, left: 88 };
const VIEW_MARGIN = 12;

export type FocusMove = "none" | "pan" | "fly" | "dive";

export interface EngineCallbacks {
  onPick(id: string): void;
  onDive(id: string): void;
  onBackground(): void;
}

export interface EngineOptions {
  canvas: HTMLCanvasElement;
  host: HTMLElement;
  /** Where the nodes' accessible twins live (default: the host). */
  probeRoot?: ParentNode;
  model: ExplorerModel;
  palette: Palette;
  reduced: boolean;
  memory: LayoutMemory;
  callbacks: EngineCallbacks;
  clock?: () => number;
  schedule?: (cb: () => void) => number;
  cancel?: (handle: number) => void;
}

export interface GraphEngine {
  focus(id: string | null, move: FocusMove): void;
  zoomBy(factor: number): void;
  fit(): void;
  setPalette(palette: Palette): void;
  setReduced(reduced: boolean): void;
  resize(): void;
  destroy(): void;
}

interface Viewport {
  width: number;
  height: number;
}

function viewportOf(canvas: HTMLCanvasElement, host: HTMLElement): Viewport {
  return { width: canvas.clientWidth || host.clientWidth || 800, height: canvas.clientHeight || host.clientHeight || 520 };
}

/**
 * The camera as remembered: the world point at the viewport center and the zoom, so a canvas of another size
 * (a narrower one when the After Play rail opens) still looks at the same place.
 */
const toView = (c: Camera, v: Viewport): Camera => ({ x: (v.width / 2 - c.x) / c.k, y: (v.height / 2 - c.y) / c.k, k: c.k });
const fromView = (view: Camera, v: Viewport): Camera => ({ x: v.width / 2 - view.x * view.k, y: v.height / 2 - view.y * view.k, k: view.k });

/** Lays out the graph before the first frame: remembered points stay, the rest settle around them. */
function prepareLayout(model: ExplorerModel, memory: LayoutMemory, reduced: boolean): { layout: Layout; motion: Motion; remembered: boolean } {
  const recalled = memory.recall(model.accountId);
  const entrance = planEntrance(model);
  const seeded = seedPositions(model, recalled?.positions ?? null, entrance, seedFor(model));
  const layout = createLayout(model, seeded.positions, seeded.pinned);
  if (reduced) {
    // Reduced motion: the final picture at once. Everything settles now (the Before nodes stay pinned); nothing animates.
    layout.prelayout(PRELAYOUT_TICKS);
    layout.cool();
    return { layout, motion: { start: null, entrance }, remembered: recalled !== null && recalled.positions.size > 0 };
  }
  for (const id of entrance.entering) layout.hold(id, seeded.positions.get(id)!);
  const gentleSettle = seeded.needsPrelayout && entrance.entering.length === 0;
  if (seeded.needsPrelayout) layout.prelayout(PRELAYOUT_TICKS - (gentleSettle ? SETTLE_TICKS : 0), PRELAYOUT_BUDGET_MS);
  // A fresh graph keeps its remaining warmth for a visible settle; a remembered one does not move until the entrance.
  if (!seeded.needsPrelayout) layout.cool();
  // Re-seat each entering node on its anchor wherever the pre-layout put it (the event first, then the rest on it).
  for (const id of entrance.entering) {
    const anchor = entrance.anchorOf.get(id);
    const at = anchor === undefined ? undefined : layout.point(anchor);
    if (at) layout.hold(id, { x: at.x, y: at.y });
  }
  return { layout, motion: { start: null, entrance }, remembered: recalled !== null && recalled.positions.size > 0 };
}

export function createGraphEngine(opts: EngineOptions): GraphEngine {
  const { canvas, host, model, memory, callbacks } = opts;
  const clock = opts.clock ?? (() => performance.now());
  const schedule = opts.schedule ?? ((cb: () => void) => requestAnimationFrame(cb));
  const cancel = opts.cancel ?? ((h: number) => cancelAnimationFrame(h));
  const ctx = canvas.getContext("2d");
  let palette = opts.palette;
  let reduced = opts.reduced;
  let viewport = viewportOf(canvas, host);
  const { layout, motion, remembered } = prepareLayout(model, memory, opts.reduced);
  const recalled = memory.recall(model.accountId);
  const recalledView = remembered ? recalled?.camera : undefined;
  // Where each ghost (what the event took out of the view) was, from the previous view's layout.
  const ghostPositions = new Map(model.ghosts.flatMap((g) => { const at = recalled?.positions.get(g.id); return at ? [[g.id, at] as const] : []; }));
  const fitAll = (): Camera => fitTransform([...layout.positions().values()], viewport.width, viewport.height, FIT_INSETS);
  let camera: Camera = recalledView ? fromView(recalledView, viewport) : fitAll();
  let focusId: string | null = null;
  let hoverId: string | null = null;
  let handle: number | null = null;
  let moving = true;
  let released = false;
  const freed = new Set<string>();
  let destroyed = false;

  const frameAt = (now: number): Frame => ({ model, point: layout.point, ghostPoint: (id) => ghostPositions.get(id), camera, ...viewport, palette, focusId, hoverId, now, reduced, motion });
  const status = (key: string, value: string): void => void host.setAttribute(`data-${key}`, value);
  const zoomLabel = host.querySelector<HTMLElement>("[data-hud-zoom]");
  function showZoom(): void {
    status("zoom", camera.k.toFixed(2));
    if (zoomLabel) zoomLabel.textContent = `${Math.round(camera.k * 100)}%`;
  }

  function entranceDone(now: number): boolean {
    if (motion.start === null) return motion.entrance.entering.length === 0 && motion.entrance.pulsing.length === 0;
    return reduced || now - motion.start >= entranceLength(motion.entrance);
  }

  /** Writes each node's screen point onto its accessible twin, so tests and tools can find it. */
  function writeProbe(): void {
    for (const el of (opts.probeRoot ?? host).querySelectorAll<HTMLElement>("[data-node-id]")) {
      const p = layout.point(el.dataset.nodeId ?? "");
      if (!p) continue;
      el.dataset.sx = String(Math.round(p.x * camera.k + camera.x));
      el.dataset.sy = String(Math.round(p.y * camera.k + camera.y));
      el.dataset.wx = p.x.toFixed(2);
      el.dataset.wy = p.y.toFixed(2);
    }
  }

  function remember(): void {
    memory.remember(model.accountId, layout.positions(), toView(camera, viewport), new Map(model.nodes.map((n) => [n.id, n.label])));
  }

  function releaseDue(now: number): void {
    if (motion.start === null) return;
    for (const id of motion.entrance.entering) {
      if (freed.has(id) || (!reduced && now - motion.start < motion.entrance.delays.get(id)!)) continue;
      freed.add(id);
      layout.free(id);
      // Under reduced motion the entering nodes were settled in place before the first frame: nothing to animate.
      if (!reduced) layout.reheat(ENTRANCE_HEAT);
    }
  }

  /** Whether every node is inside the viewport (with a margin for its label). */
  function allInView(): boolean {
    return model.nodes.every((n) => {
      const p = layout.point(n.id);
      if (!p) return true;
      const x = p.x * camera.k + camera.x;
      const y = p.y * camera.k + camera.y;
      return x >= VIEW_MARGIN && y >= VIEW_MARGIN && x <= viewport.width - VIEW_MARGIN && y <= viewport.height - VIEW_MARGIN;
    });
  }

  function settled(now: number): void {
    if (!released && entranceDone(now)) {
      released = true;
      layout.release();
      status("entrance", "done");
      // The camera explains the change: if something new landed out of view, ease out to show it all.
      if (motion.start !== null && !allInView()) input.moveTo(fitAll(), reduced ? 0 : FIT_MS);
    }
    status("settled", "true");
    remember();
    writeProbe();
  }

  function render(): void {
    handle = null;
    if (destroyed) return;
    const now = clock();
    releaseDue(now);
    const wasMoving = moving;
    moving = layout.tick();
    const frame = frameAt(now);
    if (ctx) {
      const dpr = Math.min(MAX_DPR, window.devicePixelRatio || 1);
      ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
      drawBackground(ctx, frame);
      drawEdges(ctx, frame);
      drawEffects(ctx, frame);
      drawNodes(ctx, frame);
      drawLabels(ctx, frame);
    }
    const busy = !entranceDone(now) || sceneAnimating(frame);
    if (moving || busy) request();
    // Still again (or the entrance just ended while something else keeps frames coming, like a selection).
    if (!moving && (wasMoving || (!released && entranceDone(now)))) settled(now);
  }

  function request(): void {
    if (handle === null && !destroyed) handle = schedule(render);
  }

  function sizeCanvas(): void {
    viewport = viewportOf(canvas, host);
    const dpr = Math.min(MAX_DPR, window.devicePixelRatio || 1);
    canvas.width = Math.round(viewport.width * dpr);
    canvas.height = Math.round(viewport.height * dpr);
  }

  function pointOf(id: string): Point | null {
    const p = layout.point(id);
    return p ? { x: p.x, y: p.y } : null;
  }

  const input: Input = attachInput(canvas, camera, () => viewport, {
    pick: (p) => {
      const frame = frameAt(clock());
      return pickNode(frame, p.x, p.y) ?? pickGhost(frame, p.x, p.y);
    },
    onCamera(next) {
      camera = next;
      showZoom();
      request();
    },
    onCameraEnd() {
      remember();
      writeProbe();
    },
    onDrag(id, p) {
      layout.drag(id, invertTransform(camera, p));
      request();
    },
    onDrop(id) {
      layout.drop(id);
      remember();
      writeProbe();
      request();
    },
    onClick: (id) => (id === null ? callbacks.onBackground() : callbacks.onPick(id)),
    onDoubleClick(id, p) {
      if (id !== null) callbacks.onDive(id);
      else input.moveTo({ x: p.x - (p.x - camera.x) * ZOOM_STEP, y: p.y - (p.y - camera.y) * ZOOM_STEP, k: camera.k * ZOOM_STEP }, reduced ? 0 : ZOOM_MS);
    },
    onHover(id) {
      hoverId = id;
      request();
    },
  });

  sizeCanvas();
  showZoom();
  status("settled", "false");
  status("entrance", motion.entrance.entering.length + motion.entrance.pulsing.length > 0 ? "running" : "none");
  if (motion.entrance.entering.length + motion.entrance.pulsing.length > 0) motion.start = clock() + ENTRANCE_LEAD_MS;
  request();

  return {
    focus(id, move) {
      focusId = id !== null && (model.byId.has(id) || ghostPositions.has(id)) ? id : null;
      status("focus", focusId ?? "");
      const p = focusId === null ? null : (pointOf(focusId) ?? ghostPositions.get(focusId) ?? null);
      if (p && move !== "none") {
        const k = move === "dive" ? Math.max(camera.k * 1.5, DIVE_ZOOM) : move === "fly" ? Math.max(camera.k, FOCUS_ZOOM) : camera.k;
        const ms = reduced ? 0 : move === "dive" ? DIVE_MS : move === "fly" ? FLY_MS : PAN_MS;
        input.moveTo(focusTransform(p, viewport.width, viewport.height, k), ms);
      }
      request();
    },
    zoomBy(factor) {
      input.zoomBy(factor, reduced ? 0 : ZOOM_MS);
    },
    fit() {
      input.moveTo(fitAll(), reduced ? 0 : FIT_MS);
    },
    setPalette(next) {
      palette = next;
      request();
    },
    setReduced(next) {
      reduced = next;
      if (next) {
        // Stop the motion now: let any node still waiting to enter take its place, settle, and hold still.
        for (const id of motion.entrance.entering) if (!freed.has(id)) layout.free(id);
        layout.prelayout(PRELAYOUT_TICKS);
        layout.cool();
      }
      request();
    },
    resize() {
      // Keep the same world point at the center when the canvas changes size. Only then: re-centering
      // interrupts a camera flight, and a resize report with no change (or the web font arriving) must not.
      const before = viewport;
      const view = toView(camera, before);
      sizeCanvas();
      if (viewport.width !== before.width || viewport.height !== before.height) input.moveTo(fromView(view, viewport), 0);
      request();
      writeProbe();
    },
    destroy() {
      destroyed = true;
      if (handle !== null) cancel(handle);
      input.detach();
      layout.stop();
      remember();
    },
  };
}
