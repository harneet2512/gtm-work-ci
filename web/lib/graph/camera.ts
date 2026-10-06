// The camera: a d3-zoom style transform (screen = world * k + (x, y)), with the fit-all and fly-to targets.
import type { Point } from "./geometry";

export interface Camera {
  x: number;
  y: number;
  k: number;
}

export const MIN_ZOOM = 0.2;
export const MAX_ZOOM = 6;
/** Fit never zooms a small graph in further than this. */
export const FIT_MAX_ZOOM = 1.6;
/** Zoom used when a click flies to a node, and when a double-click dives into one. */
export const FOCUS_ZOOM = 1.8;
export const DIVE_ZOOM = 3.2;
export const ZOOM_STEP = 1.4;
export const FIT_PADDING = 56;

export const clampZoom = (k: number): number => Math.min(MAX_ZOOM, Math.max(MIN_ZOOM, k));

export const applyTransform = (t: Camera, p: Point): Point => ({ x: p.x * t.k + t.x, y: p.y * t.k + t.y });

export const invertTransform = (t: Camera, p: Point): Point => ({ x: (p.x - t.x) / t.k, y: (p.y - t.y) / t.k });

/** Space kept clear on each side of the canvas (overlay chrome, label room). */
export interface Insets {
  top: number;
  right: number;
  bottom: number;
  left: number;
}

const uniform = (n: number): Insets => ({ top: n, right: n, bottom: n, left: n });

/** The transform that shows every point inside the viewport minus the insets, centered in what is left. */
export function fitTransform(points: readonly Point[], width: number, height: number, padding: number | Insets = FIT_PADDING): Camera {
  if (points.length === 0) return { x: width / 2, y: height / 2, k: 1 };
  const pad = typeof padding === "number" ? uniform(padding) : padding;
  const xs = points.map((p) => p.x);
  const ys = points.map((p) => p.y);
  const [x0, x1, y0, y1] = [Math.min(...xs), Math.max(...xs), Math.min(...ys), Math.max(...ys)];
  const availW = Math.max(1, width - pad.left - pad.right);
  const availH = Math.max(1, height - pad.top - pad.bottom);
  const k = clampZoom(Math.min(FIT_MAX_ZOOM, availW / Math.max(1, x1 - x0), availH / Math.max(1, y1 - y0)));
  const cx = (x0 + x1) / 2;
  const cy = (y0 + y1) / 2;
  return { x: pad.left + availW / 2 - cx * k, y: pad.top + availH / 2 - cy * k, k };
}

/** The transform that centers `p` at zoom `k`. */
export function focusTransform(p: Point, width: number, height: number, k: number): Camera {
  const z = clampZoom(k);
  return { x: width / 2 - p.x * z, y: height / 2 - p.y * z, k: z };
}
