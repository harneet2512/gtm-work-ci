// Pointer input on the canvas: d3-zoom for wheel, trackpad pinch and background drag (pan), d3-drag for
// dragging nodes (it takes the gesture only when a node is under the pointer, so panning still works),
// plus click, double-click and hover. Programmatic camera moves go through the same zoom behavior, so
// the user can always take over mid-flight.
import { drag, type D3DragEvent } from "d3-drag";
import { select } from "d3-selection";
import "d3-transition";
import { zoom, zoomIdentity, zoomTransform, type D3ZoomEvent, type ZoomBehavior } from "d3-zoom";
import { clampZoom, MAX_ZOOM, MIN_ZOOM, type Camera } from "../camera";
import type { Point } from "../geometry";

export interface InputHandlers {
  /** The node under a canvas point, or null. */
  pick(p: Point): string | null;
  onCamera(camera: Camera): void;
  onCameraEnd(): void;
  onDrag(id: string, p: Point): void;
  onDrop(id: string): void;
  onClick(id: string | null): void;
  onDoubleClick(id: string | null, p: Point): void;
  onHover(id: string | null): void;
}

export interface Input {
  /** Moves the camera to `target`, animated over `ms` (0: at once). */
  moveTo(target: Camera, ms: number): void;
  /** Zooms by `factor` around the canvas center. */
  zoomBy(factor: number, ms: number): void;
  detach(): void;
}

interface Subject {
  id: string;
  x: number;
  y: number;
}

const toTransform = (c: Camera) => zoomIdentity.translate(c.x, c.y).scale(c.k);

function localPoint(canvas: HTMLCanvasElement, event: MouseEvent): Point {
  const rect = canvas.getBoundingClientRect();
  return { x: event.clientX - rect.left, y: event.clientY - rect.top };
}

/**
 * `size` is the viewport the engine draws into: d3-zoom centers zooms and sizes its fly-to path on it (its
 * default reads the element's client size, which is 0 while the canvas is hidden and breaks the path).
 */
export function attachInput(canvas: HTMLCanvasElement, initial: Camera, size: () => { width: number; height: number }, h: InputHandlers): Input {
  const sel = select<HTMLCanvasElement, unknown>(canvas);
  const zoomer: ZoomBehavior<HTMLCanvasElement, unknown> = zoom<HTMLCanvasElement, unknown>()
    .scaleExtent([MIN_ZOOM, MAX_ZOOM])
    .extent(() => {
      const { width, height } = size();
      return [
        [0, 0],
        [width, height],
      ];
    })
    .on("zoom", (e: D3ZoomEvent<HTMLCanvasElement, unknown>) => h.onCamera({ x: e.transform.x, y: e.transform.y, k: e.transform.k }))
    .on("end", () => h.onCameraEnd());

  const dragger = drag<HTMLCanvasElement, unknown, Subject | null>()
    .container(canvas)
    .subject((e: D3DragEvent<HTMLCanvasElement, unknown, Subject | null>) => {
      const id = h.pick({ x: e.x, y: e.y });
      return id === null ? null : { id, x: e.x, y: e.y };
    })
    .on("start", () => canvas.classList.add("is-dragging"))
    .on("drag", (e: D3DragEvent<HTMLCanvasElement, unknown, Subject>) => h.onDrag(e.subject.id, { x: e.x, y: e.y }))
    .on("end", (e: D3DragEvent<HTMLCanvasElement, unknown, Subject>) => {
      canvas.classList.remove("is-dragging");
      h.onDrop(e.subject.id);
    });

  // Drag first: it claims the gesture only over a node; otherwise the zoom behavior pans.
  sel.call(dragger).call(zoomer).on("dblclick.zoom", null);
  zoomer.transform(sel, toTransform(initial));

  let hovered: string | null = null;
  const onMove = (e: PointerEvent): void => {
    const id = h.pick(localPoint(canvas, e));
    canvas.style.cursor = id === null ? "" : "pointer";
    if (id !== hovered) {
      hovered = id;
      h.onHover(id);
    }
  };
  const onLeave = (): void => {
    if (hovered === null) return;
    hovered = null;
    h.onHover(null);
  };
  const onClick = (e: MouseEvent): void => h.onClick(h.pick(localPoint(canvas, e)));
  const onDouble = (e: MouseEvent): void => {
    const p = localPoint(canvas, e);
    h.onDoubleClick(h.pick(p), p);
  };
  canvas.addEventListener("pointermove", onMove);
  canvas.addEventListener("pointerleave", onLeave);
  canvas.addEventListener("click", onClick);
  canvas.addEventListener("dblclick", onDouble);

  // Where an animated move is heading: a second zoom click composes from there, not from a mid-flight frame.
  let heading: Camera | null = null;

  function moveTo(target: Camera, ms: number): void {
    sel.interrupt();
    heading = null;
    if (ms <= 0) {
      zoomer.transform(sel, toTransform(target));
      return;
    }
    heading = target;
    sel
      .transition()
      .duration(ms)
      .call(zoomer.transform, toTransform(target))
      .on("end interrupt", () => {
        if (heading === target) heading = null;
      });
  }

  return {
    moveTo,
    zoomBy(factor, ms) {
      const from = heading ?? (({ x, y, k }) => ({ x, y, k }))(zoomTransform(canvas));
      const { width, height } = size();
      const k = clampZoom(from.k * factor);
      // Zoom about the viewport center: the world point there stays there.
      const cx = (width / 2 - from.x) / from.k;
      const cy = (height / 2 - from.y) / from.k;
      moveTo({ x: width / 2 - cx * k, y: height / 2 - cy * k, k }, ms);
    },
    detach() {
      sel.interrupt();
      sel.on(".zoom", null).on(".drag", null);
      canvas.removeEventListener("pointermove", onMove);
      canvas.removeEventListener("pointerleave", onLeave);
      canvas.removeEventListener("click", onClick);
      canvas.removeEventListener("dblclick", onDouble);
    },
  };
}
