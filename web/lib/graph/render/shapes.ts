// One shape per node kind (the Linkurious cue): ring (account), hexagon (deal), circle (person), diamond
// (activity), square (claim), triangle (signal), pentagon (knowledge). Paths only; the caller fills and strokes.
import type { Shape } from "@/lib/graph/kinds";

type PathCtx = Pick<CanvasRenderingContext2D, "beginPath" | "arc" | "moveTo" | "lineTo" | "closePath">;

function polygon(ctx: PathCtx, x: number, y: number, r: number, sides: number, rotation: number): void {
  ctx.beginPath();
  for (let i = 0; i < sides; i += 1) {
    const a = rotation + (i / sides) * Math.PI * 2;
    const px = x + Math.cos(a) * r;
    const py = y + Math.sin(a) * r;
    if (i === 0) ctx.moveTo(px, py);
    else ctx.lineTo(px, py);
  }
  ctx.closePath();
}

/** Traces the outline of `shape` centered on (x, y) with radius r (similar visual weight across shapes). */
export function tracePath(ctx: PathCtx, shape: Shape, x: number, y: number, r: number): void {
  switch (shape) {
    case "hexagon":
      return polygon(ctx, x, y, r * 1.08, 6, Math.PI / 6);
    case "diamond":
      return polygon(ctx, x, y, r * 1.25, 4, -Math.PI / 2);
    case "square":
      return polygon(ctx, x, y, r * 1.2, 4, Math.PI / 4);
    case "triangle":
      return polygon(ctx, x, y + r * 0.15, r * 1.3, 3, -Math.PI / 2);
    case "pentagon":
      return polygon(ctx, x, y, r * 1.12, 5, -Math.PI / 2);
    case "ring":
    case "circle":
      ctx.beginPath();
      ctx.arc(x, y, r, 0, Math.PI * 2);
      return;
  }
}
