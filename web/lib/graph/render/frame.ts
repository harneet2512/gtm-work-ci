// One rendered frame of the explorer: everything the draw functions read, and the timing helpers of the
// Before -> After entrance. The draw functions are pure over a Frame and a 2D context.
import type { Camera } from "@/lib/graph/camera";
import type { Point } from "@/lib/graph/geometry";
import type { ExplorerModel } from "@/lib/graph/model";
import type { Palette } from "@/lib/graph/palette";
import type { FocusRole } from "@/lib/graph/semantic-zoom";
import type { Entrance } from "@/lib/graph/transition";

/** A node grows in over this long once its wave starts. */
export const GROW_MS = 520;
/** The event's ripple and the changed nodes' single pulse. */
export const RIPPLE_MS = 1300;
export const PULSE_DELAY_MS = 300;
export const PULSE_MS = 900;
/** How long the changed edges keep a quiet flow after the entrance. */
export const FLOW_AFTER_MS = 5000;

export interface Motion {
  /** When the entrance started (performance.now ms); null when there is none. */
  start: number | null;
  entrance: Entrance;
}

export interface Frame {
  model: ExplorerModel;
  point: (id: string) => Readonly<Point> | undefined;
  /** Where a ghost (something the event took out of the view) was, if remembered. */
  ghostPoint?: (id: string) => Readonly<Point> | undefined;
  camera: Camera;
  width: number;
  height: number;
  palette: Palette;
  /** The selected node. */
  focusId: string | null;
  hoverId: string | null;
  now: number;
  reduced: boolean;
  motion: Motion;
}

export const easeOutCubic = (t: number): number => 1 - (1 - t) ** 3;

/** easeOutBack: a small overshoot, so a new node settles into place instead of just appearing. */
export function easeOutBack(t: number): number {
  const c1 = 1.4;
  const c3 = c1 + 1;
  return 1 + c3 * (t - 1) ** 3 + c1 * (t - 1) ** 2;
}

/** Elapsed ms since the entrance started, or null when there is none (or motion is reduced). */
export function sinceStart(frame: Frame): number | null {
  if (frame.reduced || frame.motion.start === null) return null;
  return frame.now - frame.motion.start;
}

/** 0 (not there yet) .. 1 (fully grown). Nodes that are not entering are always 1. */
export function bornProgress(frame: Frame, id: string): number {
  const delay = frame.motion.entrance.delays.get(id);
  const t = sinceStart(frame);
  if (delay === undefined || t === null) return 1;
  return Math.min(1, Math.max(0, (t - delay) / GROW_MS));
}

/** How long the whole entrance runs, from its start. */
export function entranceLength(entrance: Entrance): number {
  if (entrance.entering.length === 0 && entrance.pulsing.length === 0) return 0;
  const lastDelay = Math.max(0, ...entrance.delays.values());
  return Math.max(lastDelay + GROW_MS, RIPPLE_MS, entrance.pulsing.length > 0 ? PULSE_DELAY_MS + PULSE_MS : 0);
}

/** The node whose neighborhood is lit: the hovered one, else the selected one. */
export const activeId = (frame: Frame): string | null => frame.hoverId ?? frame.focusId;

export function focusRole(frame: Frame, id: string): FocusRole {
  const active = activeId(frame);
  if (active === null || !frame.model.byId.has(active)) return "none";
  if (id === active) return "focus";
  return frame.model.neighbors.get(active)?.has(id) ? "neighbor" : "dimmed";
}

/** World point -> screen point under the frame's camera. */
export function toScreen(frame: Frame, p: Readonly<Point>): Point {
  const { x, y, k } = frame.camera;
  return { x: p.x * k + x, y: p.y * k + y };
}

/** Whether a screen point (with a margin) is inside the viewport. */
export const onScreen = (frame: Frame, p: Point, margin: number): boolean =>
  p.x >= -margin && p.y >= -margin && p.x <= frame.width + margin && p.y <= frame.height + margin;
