// Semantic zoom: what is labelled at which zoom. Major kinds are always labelled; minor ones (activities,
// claims) and edge labels appear as the user moves in. Labels are drawn in screen space at a constant size,
// so zooming changes what is said, never how big it is. Overlapping minor labels give way to more
// important ones.
import type { ExplorerNode } from "./model";

/** Zoom at which minor node labels appear. */
export const MINOR_LABEL_K = 1.4;
/** Zoom at which every edge is labelled (the focused node's edges from MINOR_LABEL_K). */
export const EDGE_LABEL_K = 2;
/** On-screen label size in CSS px, at every zoom. */
export const LABEL_PX = 11.5;
export const EDGE_LABEL_PX = 10;
/** Characters a label may use when far out and at mid zoom; from FULL_LABEL_K it is never clipped. */
const FAR_LABEL = 36;
const MID_LABEL = 56;
const MID_LABEL_K = 1.2;
export const FULL_LABEL_K = 2;
const MIN_RADIUS_SCALE = 0.5;
const MAX_RADIUS_SCALE = 2.4;

/** Where a node stands relative to the focus: the focus itself, a neighbor, dimmed away, or no focus at all. */
export type FocusRole = "focus" | "neighbor" | "dimmed" | "none";

export function showNodeLabel(node: ExplorerNode, k: number, role: FocusRole): boolean {
  if (role === "focus" || role === "neighbor") return true;
  if (role === "dimmed") return node.major;
  return node.major || k >= MINOR_LABEL_K;
}

export function showEdgeLabel(k: number, touchesFocus: boolean): boolean {
  return k >= EDGE_LABEL_K || (touchesFocus && k >= MINOR_LABEL_K);
}

/** Clips at a word boundary when one is reasonably close, so a label never ends mid-word. */
function clip(text: string, max: number): string {
  if (text.length <= max) return text;
  const cut = text.slice(0, max - 1);
  const space = cut.lastIndexOf(" ");
  return `${(space > max * 0.6 ? cut.slice(0, space) : cut).trimEnd()}…`;
}

/** The label as far as the zoom allows: more as you move in, all of it from FULL_LABEL_K or for the focus. */
export function labelText(node: Pick<ExplorerNode, "label">, k: number, full = false): string {
  if (full || k >= FULL_LABEL_K) return node.label;
  return clip(node.label, k >= MID_LABEL_K ? MID_LABEL : FAR_LABEL);
}

/** Splits a label into at most two lines no wider than `maxWidth`; whatever still does not fit ends in "…". */
export function wrapLabel(text: string, maxWidth: number, measure: (t: string) => number): string[] {
  const lines: string[] = [];
  let line = "";
  const words = text.split(" ");
  for (let i = 0; i < words.length; i += 1) {
    const next = line === "" ? words[i]! : `${line} ${words[i]}`;
    if (measure(next) <= maxWidth || line === "") {
      line = next;
      continue;
    }
    lines.push(line);
    line = words[i]!;
    if (lines.length === 1) continue;
  }
  lines.push(line);
  if (lines.length <= 2 && lines.every((l) => measure(l) <= maxWidth)) return lines;
  const [first, ...rest] = lines;
  const tail = lines.length === 1 ? first! : rest.join(" ");
  const fitted = fitWithEllipsis(tail, maxWidth, measure);
  return lines.length === 1 ? [fitted] : [first!, fitted];
}

function fitWithEllipsis(text: string, maxWidth: number, measure: (t: string) => number): string {
  if (measure(text) <= maxWidth) return text;
  let end = text.length;
  while (end > 1 && measure(`${text.slice(0, end).trimEnd()}…`) > maxWidth) end -= 1;
  return `${text.slice(0, end).trimEnd()}…`;
}

/** Node radius on screen: grows with zoom, but slower, so nodes stay quiet and small. */
export function screenRadius(radius: number, k: number): number {
  const scale = Math.min(MAX_RADIUS_SCALE, Math.max(MIN_RADIUS_SCALE, Math.sqrt(k)));
  return radius * scale;
}

export interface LabelCandidate {
  id: string;
  /** Center x and top y of the label box, in screen px. */
  x: number;
  y: number;
  width: number;
  height: number;
  priority: number;
  /** Major labels are always placed. */
  force: boolean;
}

const CELL = 64;
const PAD = 2;

interface Box {
  x0: number;
  y0: number;
  x1: number;
  y1: number;
}

const boxOf = (c: LabelCandidate): Box => ({ x0: c.x - c.width / 2 - PAD, y0: c.y - PAD, x1: c.x + c.width / 2 + PAD, y1: c.y + c.height + PAD });
const overlaps = (a: Box, b: Box): boolean => a.x0 < b.x1 && b.x0 < a.x1 && a.y0 < b.y1 && b.y0 < a.y1;

function cellsOf(b: Box): string[] {
  const out: string[] = [];
  for (let cx = Math.floor(b.x0 / CELL); cx <= Math.floor(b.x1 / CELL); cx += 1) {
    for (let cy = Math.floor(b.y0 / CELL); cy <= Math.floor(b.y1 / CELL); cy += 1) out.push(`${cx},${cy}`);
  }
  return out;
}

/** Greedy label placement: forced labels first, then by priority; a label that overlaps a placed one is dropped. */
export function placeLabels(candidates: readonly LabelCandidate[]): LabelCandidate[] {
  const order = [...candidates].sort((a, b) => Number(b.force) - Number(a.force) || b.priority - a.priority || a.id.localeCompare(b.id));
  // Scratch spatial index local to this call (a uniform grid): nothing outside it is mutated.
  const grid = new Map<string, Box[]>();
  const placed: LabelCandidate[] = [];
  for (const c of order) {
    const box = boxOf(c);
    const cells = cellsOf(box);
    if (!c.force && cells.some((key) => grid.get(key)?.some((other) => overlaps(box, other)))) continue;
    for (const key of cells) {
      const bucket = grid.get(key);
      if (bucket) bucket.push(box);
      else grid.set(key, [box]);
    }
    placed.push(c);
  }
  return placed;
}
