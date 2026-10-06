// The scene behind the nodes: the graphite canvas with its dot grid, every edge (status by dash as well as
// colour; a quiet flow only on what changed or what is selected) and the event's ripple. Everything is drawn
// in screen space so lines stay hairline-crisp at every zoom.
import type { Point } from "../geometry";
import { screenRadius } from "../semantic-zoom";
import { activeId, bornProgress, easeOutCubic, FLOW_AFTER_MS, entranceLength, onScreen, PULSE_DELAY_MS, PULSE_MS, RIPPLE_MS, sinceStart, toScreen, type Frame } from "./frame";

const GRID_WORLD = 32;
const GRID_MIN_PX = 14;
const GRID_MAX_PX = 56;
const GRID_DOT = 1.2;
const DIMMED_EDGE_ALPHA = 0.12;
const FLOW_DASH = [3, 9];
const CHANGED_DASH = [6, 3];
const FLOW_SPEED = 28;

export function drawBackground(ctx: CanvasRenderingContext2D, frame: Frame): void {
  const { palette, camera, width, height } = frame;
  ctx.fillStyle = palette.canvas;
  ctx.fillRect(0, 0, width, height);
  let step = GRID_WORLD * camera.k;
  while (step < GRID_MIN_PX) step *= 2;
  while (step > GRID_MAX_PX) step /= 2;
  const ox = ((camera.x % step) + step) % step;
  const oy = ((camera.y % step) + step) % step;
  ctx.fillStyle = palette.grid;
  for (let x = ox; x < width; x += step) {
    for (let y = oy; y < height; y += step) ctx.fillRect(x - GRID_DOT / 2, y - GRID_DOT / 2, GRID_DOT, GRID_DOT);
  }
}

function edgeColor(frame: Frame, mark: string | undefined, lit: boolean): string {
  const { palette } = frame;
  if (mark === "added") return palette.added;
  if (mark === "changed" || mark === "repaired") return palette.changed;
  return lit ? palette.edgeStrong : palette.edge;
}

/** Whether an edge carries the quiet flow: it touches the selection, or the event just changed it. */
function flows(frame: Frame, touchesSelection: boolean, mark: string | undefined): boolean {
  if (frame.reduced) return false;
  if (touchesSelection) return true;
  const t = sinceStart(frame);
  return mark !== undefined && t !== null && t < entranceLength(frame.motion.entrance) + FLOW_AFTER_MS;
}

export function drawEdges(ctx: CanvasRenderingContext2D, frame: Frame): void {
  const active = activeId(frame);
  ctx.save();
  ctx.lineCap = "round";
  for (const edge of frame.model.edges) {
    const a = frame.point(edge.source);
    const b = frame.point(edge.target);
    if (!a || !b) continue;
    const grown = Math.min(bornProgress(frame, edge.source), bornProgress(frame, edge.target));
    if (grown <= 0) continue;
    const sa = toScreen(frame, a);
    const sb = toScreen(frame, b);
    if (!onScreen(frame, sa, frame.width) && !onScreen(frame, sb, frame.width)) continue;
    const touches = active !== null && (edge.source === active || edge.target === active);
    const dimmed = active !== null && !touches;
    ctx.globalAlpha = (dimmed ? DIMMED_EDGE_ALPHA : 1) * easeOutCubic(grown);
    ctx.strokeStyle = edgeColor(frame, edge.mark, touches);
    ctx.lineWidth = touches || edge.mark ? 1.5 : 1;
    // A changed edge is dashed, an added one solid: the status never rests on colour alone.
    ctx.setLineDash(edge.mark === "changed" || edge.mark === "repaired" ? CHANGED_DASH : []);
    ctx.beginPath();
    ctx.moveTo(sa.x, sa.y);
    ctx.lineTo(sb.x, sb.y);
    ctx.stroke();
    const selected = frame.focusId !== null && (edge.source === frame.focusId || edge.target === frame.focusId);
    if (flows(frame, selected, edge.mark)) {
      ctx.setLineDash(FLOW_DASH);
      ctx.lineDashOffset = -((frame.now / 1000) * FLOW_SPEED);
      ctx.strokeStyle = edge.mark ? edgeColor(frame, edge.mark, true) : frame.palette.accent;
      ctx.lineWidth = 2;
      ctx.beginPath();
      ctx.moveTo(sa.x, sa.y);
      ctx.lineTo(sb.x, sb.y);
      ctx.stroke();
    }
  }
  ctx.setLineDash([]);
  ctx.restore();
}

function ring(ctx: CanvasRenderingContext2D, p: Point, r: number, color: string, alpha: number, width: number): void {
  ctx.globalAlpha = alpha;
  ctx.strokeStyle = color;
  ctx.lineWidth = width;
  ctx.beginPath();
  ctx.arc(p.x, p.y, r, 0, Math.PI * 2);
  ctx.stroke();
}

/** The event's ripple (two soft rings out of its activity) and one pulse on every changed node. */
export function drawEffects(ctx: CanvasRenderingContext2D, frame: Frame): void {
  const t = sinceStart(frame);
  if (t === null) return;
  ctx.save();
  const eventId = frame.model.eventNodeId;
  const origin = eventId ? frame.point(eventId) : undefined;
  if (eventId && origin) {
    const base = screenRadius(frame.model.byId.get(eventId)!.radius, frame.camera.k);
    for (const offset of [0, RIPPLE_MS * 0.3]) {
      const q = (t - offset) / (RIPPLE_MS * 0.7);
      if (q > 0 && q < 1) ring(ctx, toScreen(frame, origin), base + 6 + q * 72, frame.palette.added, (1 - q) * 0.55, 1.5);
    }
  }
  for (const id of frame.motion.entrance.pulsing) {
    const p = frame.point(id);
    const q = (t - PULSE_DELAY_MS) / PULSE_MS;
    if (!p || q <= 0 || q >= 1) continue;
    const r = screenRadius(frame.model.byId.get(id)!.radius, frame.camera.k);
    ring(ctx, toScreen(frame, p), r + 3 + easeOutCubic(q) * 22, frame.palette.changed, (1 - q) * 0.85, 2);
  }
  ctx.restore();
}

/** Whether the scene still has something moving on its own (so another frame is needed). */
export function sceneAnimating(frame: Frame): boolean {
  if (frame.reduced) return false;
  if (frame.focusId !== null && frame.model.byId.has(frame.focusId)) return true;
  const t = sinceStart(frame);
  if (t === null) return false;
  const marked = frame.model.edges.some((e) => e.mark !== undefined);
  return t < entranceLength(frame.motion.entrance) + (marked ? FLOW_AFTER_MS : 0);
}

