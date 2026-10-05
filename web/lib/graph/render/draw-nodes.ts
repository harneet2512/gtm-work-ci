// Nodes and labels, and hit testing. Nodes are quiet and small; their shape and a small kind tag say what they
// are. What the event did is shown by geometry and a badge as well as colour (added: solid ring and "+";
// changed: dashed ring and "Δ"); what it took out of the view stays as a faded, dashed ghost where it was.
// The accent ring marks the selection and only the selected node breathes. Labels sit in screen space at a
// constant size: majors always, minors by zoom, more text as you move in, and the focus whole on a plate.
import type { GhostNode, ExplorerNode } from "../model";
import { EDGE_LABEL_PX, LABEL_PX, labelText, placeLabels, screenRadius, showEdgeLabel, showNodeLabel, wrapLabel, type LabelCandidate } from "../semantic-zoom";
import { kindOf } from "../kinds";
import type { Palette } from "../palette";
import type { Point } from "../geometry";
import { bornProgress, easeOutBack, focusRole, onScreen, toScreen, activeId, type Frame } from "./frame";
import { tracePath } from "./shapes";

const DIMMED_ALPHA = 0.22;
const GHOST_ALPHA = 0.45;
const BREATH = 0.07;
const BREATH_PERIOD_MS = 2600;
const HIT_SLOP = 5;
const LABEL_GAP = 4;
const TAG_PX = 8.5;
const LINE_GAP = 3;
const HALO = 3;
const PLATE_PAD = 6;
const FOCUS_WIDTH = 240;
const BADGE_R = 6;
const WIDTH_CACHE_MAX = 4000;
const CHANGED_RING = [3, 2];
const GHOST_RING = [2, 3];

type Op = "added" | "changed" | "repaired" | "removed";

function fillOf(node: ExplorerNode, palette: Palette): string {
  if (node.region === "account") return palette.nodeStrong;
  if (node.region === "knows" || node.region === "knowledge") return palette.accentSoft;
  return palette.node;
}

function statusOf(op: Op | undefined, palette: Palette): { color: string; glyph: string; dash: number[] } | null {
  if (op === "added") return { color: palette.added, glyph: "+", dash: [] };
  if (op === "changed" || op === "repaired") return { color: palette.changed, glyph: "Δ", dash: CHANGED_RING };
  if (op === "removed") return { color: palette.removed, glyph: "×", dash: GHOST_RING };
  return null;
}

/** A small round badge on the node's upper right, carrying the status glyph (status is never colour alone). */
function badge(ctx: CanvasRenderingContext2D, frame: Frame, s: Point, r: number, color: string, glyph: string): void {
  const x = s.x + r * 0.9;
  const y = s.y - r * 0.9;
  ctx.setLineDash([]);
  ctx.fillStyle = color;
  ctx.beginPath();
  ctx.arc(x, y, BADGE_R, 0, Math.PI * 2);
  ctx.fill();
  ctx.font = `700 9px ${frame.palette.fontFamily}`;
  ctx.textAlign = "center";
  ctx.textBaseline = "middle";
  ctx.fillStyle = frame.palette.canvas;
  ctx.fillText(glyph, x, y + 0.5);
}

function breath(frame: Frame, id: string): number {
  if (frame.reduced || frame.focusId !== id) return 1;
  return 1 + BREATH * Math.sin((frame.now / BREATH_PERIOD_MS) * Math.PI * 2);
}

function drawNode(ctx: CanvasRenderingContext2D, frame: Frame, node: ExplorerNode): void {
  const p = frame.point(node.id);
  const grown = bornProgress(frame, node.id);
  if (!p || grown <= 0) return;
  const s = toScreen(frame, p);
  const base = screenRadius(node.radius, frame.camera.k);
  if (!onScreen(frame, s, base * 4)) return;
  const r = base * easeOutBack(grown) * breath(frame, node.id);
  const { palette } = frame;
  ctx.globalAlpha = focusRole(frame, node.id) === "dimmed" ? DIMMED_ALPHA : Math.min(1, grown * 1.5);
  if (frame.focusId === node.id) {
    ctx.fillStyle = palette.accent;
    ctx.globalAlpha *= 0.14;
    ctx.beginPath();
    ctx.arc(s.x, s.y, r + 11, 0, Math.PI * 2);
    ctx.fill();
    ctx.globalAlpha /= 0.14;
  }
  tracePath(ctx, node.shape, s.x, s.y, r);
  ctx.fillStyle = fillOf(node, palette);
  ctx.fill();
  ctx.lineWidth = 1.5;
  ctx.strokeStyle = node.withheld ? palette.labelMuted : palette.canvas;
  ctx.setLineDash(node.withheld ? [2, 2] : []);
  ctx.stroke();
  ctx.setLineDash([]);
  if (node.shape === "ring") {
    ctx.fillStyle = palette.canvas;
    ctx.beginPath();
    ctx.arc(s.x, s.y, r * 0.42, 0, Math.PI * 2);
    ctx.fill();
  }
  const status = statusOf(node.mark, palette);
  if (status) {
    ctx.strokeStyle = status.color;
    ctx.lineWidth = 1.75;
    ctx.setLineDash(status.dash);
    tracePath(ctx, node.shape, s.x, s.y, r + 3.5);
    ctx.stroke();
    badge(ctx, frame, s, r + 3.5, status.color, status.glyph);
  }
  if (frame.focusId === node.id || frame.hoverId === node.id) {
    ctx.setLineDash([]);
    ctx.strokeStyle = frame.focusId === node.id ? palette.accent : palette.edgeStrong;
    ctx.lineWidth = frame.focusId === node.id ? 2 : 1;
    ctx.beginPath();
    ctx.arc(s.x, s.y, r + (status ? 8 : 5), 0, Math.PI * 2);
    ctx.stroke();
  }
}

function drawGhost(ctx: CanvasRenderingContext2D, frame: Frame, ghost: GhostNode): void {
  const p = frame.ghostPoint?.(ghost.id);
  if (!p) return;
  const s = toScreen(frame, p);
  const r = screenRadius(ghost.radius, frame.camera.k);
  if (!onScreen(frame, s, r * 4)) return;
  const status = statusOf(ghost.op, frame.palette) ?? statusOf("removed", frame.palette)!;
  ctx.globalAlpha = GHOST_ALPHA;
  ctx.setLineDash(GHOST_RING);
  ctx.lineWidth = 1.5;
  ctx.strokeStyle = frame.palette.labelMuted;
  tracePath(ctx, ghost.shape, s.x, s.y, r);
  ctx.stroke();
  ctx.globalAlpha = frame.focusId === ghost.id ? 1 : 0.8;
  badge(ctx, frame, s, r, status.color, status.glyph);
}

export function drawNodes(ctx: CanvasRenderingContext2D, frame: Frame): void {
  ctx.save();
  for (const ghost of frame.model.ghosts) drawGhost(ctx, frame, ghost);
  // The lit neighborhood is drawn last so it sits on top of the dimmed rest.
  const role = (n: ExplorerNode): number => (focusRole(frame, n.id) === "dimmed" ? 0 : 1);
  const order = [...frame.model.nodes].sort((a, b) => role(a) - role(b) || a.radius - b.radius);
  for (const node of order) drawNode(ctx, frame, node);
  ctx.setLineDash([]);
  ctx.restore();
}

const widths = new Map<string, number>();

function measure(ctx: CanvasRenderingContext2D, font: string, text: string): number {
  const key = `${font}|${text}`;
  const hit = widths.get(key);
  if (hit !== undefined) return hit;
  if (widths.size > WIDTH_CACHE_MAX) widths.clear();
  ctx.font = font;
  const w = ctx.measureText(text).width;
  widths.set(key, w);
  return w;
}

interface Label extends LabelCandidate {
  tag: string | null;
  lines: string[];
  font: string;
  color: string;
  alpha: number;
  plate: boolean;
}

const tagFont = (frame: Frame): string => `600 ${TAG_PX}px ${frame.palette.fontFamily}`;
const nodeFont = (frame: Frame, major: boolean, focus: boolean): string =>
  `${focus ? 600 : major ? 500 : 400} ${major || focus ? LABEL_PX : LABEL_PX - 0.5}px ${frame.palette.fontFamily}`;

/** A label block under a node at screen point `s`: kind tag, then one or two lines. */
function block(ctx: CanvasRenderingContext2D, frame: Frame, at: { s: Point; r: number }, tag: string, lines: string[], font: string): Pick<Label, "x" | "y" | "width" | "height"> {
  const width = Math.max(measure(ctx, tagFont(frame), tag), ...lines.map((l) => measure(ctx, font, l)));
  return { x: at.s.x, y: at.s.y + at.r + LABEL_GAP, width, height: TAG_PX + LINE_GAP + lines.length * (LABEL_PX + LINE_GAP) };
}

function nodeLabels(ctx: CanvasRenderingContext2D, frame: Frame): Label[] {
  const out: Label[] = [];
  const k = frame.camera.k;
  for (const node of frame.model.nodes) {
    const p = frame.point(node.id);
    const role = focusRole(frame, node.id);
    if (!p || bornProgress(frame, node.id) < 0.6 || !showNodeLabel(node, k, role)) continue;
    const s = toScreen(frame, p);
    if (!onScreen(frame, s, 40)) continue;
    const focus = role === "focus";
    const font = nodeFont(frame, node.major, focus);
    const lines = focus ? wrapLabel(node.label, FOCUS_WIDTH, (t) => measure(ctx, font, t)) : [labelText(node, k)];
    const tag = node.kindTag.toUpperCase();
    const boost = focus ? 100 : role === "neighbor" ? 50 : 0;
    out.push({
      id: node.id,
      ...block(ctx, frame, { s, r: screenRadius(node.radius, k) }, tag, lines, font),
      priority: kindOf(node.type).rank + boost,
      force: node.major || focus,
      tag,
      lines,
      font,
      color: node.major || focus ? frame.palette.label : frame.palette.labelMuted,
      alpha: role === "dimmed" ? 0.4 : 1,
      plate: focus,
    });
  }
  return out;
}

function ghostLabels(ctx: CanvasRenderingContext2D, frame: Frame): Label[] {
  const out: Label[] = [];
  for (const ghost of frame.model.ghosts) {
    const p = frame.ghostPoint?.(ghost.id);
    if (!p) continue;
    const s = toScreen(frame, p);
    if (!onScreen(frame, s, 40)) continue;
    const font = nodeFont(frame, false, false);
    const tag = `${ghost.kindTag.toUpperCase()} · NO LONGER HELD`;
    const lines = [labelText(ghost, frame.camera.k)];
    out.push({ id: `ghost:${ghost.id}`, ...block(ctx, frame, { s, r: screenRadius(ghost.radius, frame.camera.k) }, tag, lines, font), priority: 2, force: true, tag, lines, font, color: frame.palette.labelMuted, alpha: 0.75, plate: false });
  }
  return out;
}

function edgeLabels(ctx: CanvasRenderingContext2D, frame: Frame): Label[] {
  const out: Label[] = [];
  const active = activeId(frame);
  const font = `400 ${EDGE_LABEL_PX}px ${frame.palette.fontFamily}`;
  for (const edge of frame.model.edges) {
    const touches = active !== null && (edge.source === active || edge.target === active);
    if (!showEdgeLabel(frame.camera.k, touches) || (active !== null && !touches)) continue;
    const a = frame.point(edge.source);
    const b = frame.point(edge.target);
    if (!a || !b || bornProgress(frame, edge.source) < 1 || bornProgress(frame, edge.target) < 1) continue;
    const [sa, sb] = [toScreen(frame, a), toScreen(frame, b)];
    const width = measure(ctx, font, edge.label);
    if (Math.hypot(sb.x - sa.x, sb.y - sa.y) < width + 28) continue;
    const mid = { x: (sa.x + sb.x) / 2, y: (sa.y + sb.y) / 2 - EDGE_LABEL_PX / 2 };
    if (!onScreen(frame, mid, 24)) continue;
    out.push({ id: `edge:${edge.id}`, ...mid, width, height: EDGE_LABEL_PX + 2, priority: touches ? 40 : 0, force: false, tag: null, lines: [edge.label], font, color: frame.palette.labelMuted, alpha: 1, plate: false });
  }
  return out;
}

function drawPlate(ctx: CanvasRenderingContext2D, frame: Frame, label: Label): void {
  ctx.globalAlpha = 0.96;
  ctx.fillStyle = frame.palette.plate;
  ctx.strokeStyle = frame.palette.plateLine;
  ctx.lineWidth = 1;
  ctx.beginPath();
  ctx.roundRect(label.x - label.width / 2 - PLATE_PAD, label.y - PLATE_PAD / 2, label.width + PLATE_PAD * 2, label.height + PLATE_PAD, 6);
  ctx.fill();
  ctx.stroke();
}

function drawText(ctx: CanvasRenderingContext2D, frame: Frame, text: string, x: number, y: number, halo: boolean): void {
  if (halo) {
    ctx.lineWidth = HALO;
    ctx.strokeStyle = frame.palette.labelHalo;
    ctx.strokeText(text, x, y);
  }
  ctx.fillText(text, x, y);
}

/** Places and draws node, ghost and edge labels; returns how many were drawn. */
export function drawLabels(ctx: CanvasRenderingContext2D, frame: Frame): number {
  const all = [...nodeLabels(ctx, frame), ...ghostLabels(ctx, frame), ...edgeLabels(ctx, frame)];
  const byId = new Map(all.map((l) => [l.id, l]));
  const placed = placeLabels(all);
  ctx.save();
  ctx.textAlign = "center";
  ctx.textBaseline = "top";
  ctx.lineJoin = "round";
  for (const c of placed) {
    const label = byId.get(c.id)!;
    if (label.plate) drawPlate(ctx, frame, label);
    ctx.globalAlpha = label.alpha;
    let y = label.y;
    if (label.tag) {
      ctx.font = tagFont(frame);
      ctx.fillStyle = frame.palette.labelMuted;
      drawText(ctx, frame, label.tag, label.x, y, !label.plate);
      y += TAG_PX + LINE_GAP;
    }
    ctx.font = label.font;
    ctx.fillStyle = label.color;
    for (const line of label.lines) {
      drawText(ctx, frame, line, label.x, y, !label.plate);
      y += LABEL_PX + LINE_GAP;
    }
  }
  ctx.restore();
  return placed.length;
}

/** The node under a screen point (the nearest within its radius plus a little slop), or null. */
export function pickNode(frame: Frame, sx: number, sy: number): string | null {
  let best: string | null = null;
  let bestD = Infinity;
  for (const node of frame.model.nodes) {
    const p = frame.point(node.id);
    if (!p || bornProgress(frame, node.id) < 0.5) continue;
    const s = toScreen(frame, p);
    const d = Math.hypot(s.x - sx, s.y - sy);
    if (d <= screenRadius(node.radius, frame.camera.k) + HIT_SLOP && d < bestD) {
      best = node.id;
      bestD = d;
    }
  }
  return best;
}

/** The ghost under a screen point, or null. */
export function pickGhost(frame: Frame, sx: number, sy: number): string | null {
  for (const ghost of frame.model.ghosts) {
    const p = frame.ghostPoint?.(ghost.id);
    if (!p) continue;
    const s = toScreen(frame, p);
    if (Math.hypot(s.x - sx, s.y - sy) <= screenRadius(ghost.radius, frame.camera.k) + HIT_SLOP) return ghost.id;
  }
  return null;
}
