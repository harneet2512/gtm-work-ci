import { describe, expect, it } from "vitest";
import { clampZoom, fitTransform, focusTransform, applyTransform, invertTransform, MAX_ZOOM, MIN_ZOOM } from "@/lib/graph/camera";
import { hashUnit, ringRadius, seedPoint } from "@/lib/graph/geometry";

describe("camera", () => {
  it("fits all points inside the viewport with padding, centered", () => {
    const t = fitTransform([{ x: -100, y: -50 }, { x: 100, y: 50 }], 800, 400, 40);
    const [a, b] = [applyTransform(t, { x: -100, y: -50 }), applyTransform(t, { x: 100, y: 50 })];
    expect(a.x).toBeGreaterThanOrEqual(40 - 1e-9);
    expect(b.x).toBeLessThanOrEqual(760 + 1e-9);
    expect(a.y).toBeGreaterThanOrEqual(40 - 1e-9);
    expect(b.y).toBeLessThanOrEqual(360 + 1e-9);
    expect((a.x + b.x) / 2).toBeCloseTo(400);
    expect((a.y + b.y) / 2).toBeCloseTo(200);
  });

  it("does not over-zoom a tiny or single-point graph, and survives an empty one", () => {
    expect(fitTransform([{ x: 5, y: 5 }], 800, 400, 40).k).toBeLessThanOrEqual(1.6);
    expect(fitTransform([], 800, 400, 40)).toEqual({ x: 400, y: 200, k: 1 });
    expect(fitTransform([{ x: 0, y: 0 }], 0, 0, 40).k).toBeGreaterThan(0);
  });

  it("centers a node at a requested zoom, clamped to the allowed range", () => {
    const t = focusTransform({ x: 10, y: 20 }, 800, 400, 2);
    const p = applyTransform(t, { x: 10, y: 20 });
    expect(p.x).toBeCloseTo(400);
    expect(p.y).toBeCloseTo(200);
    expect(focusTransform({ x: 0, y: 0 }, 800, 400, 1000).k).toBe(MAX_ZOOM);
    expect(clampZoom(0)).toBe(MIN_ZOOM);
  });

  it("inverts screen points back to the world", () => {
    const t = { x: 30, y: -12, k: 2.5 };
    const w = invertTransform(t, applyTransform(t, { x: 7, y: 9 }));
    expect(w.x).toBeCloseTo(7);
    expect(w.y).toBeCloseTo(9);
  });
});

describe("fit insets", () => {
  it("keeps the graph clear of the overlay chrome: the search bar on top, the legend below", () => {
    const insets = { top: 64, right: 56, bottom: 72, left: 56 };
    const t = fitTransform([{ x: -100, y: -50 }, { x: 100, y: 50 }], 800, 400, insets);
    const [a, b] = [applyTransform(t, { x: -100, y: -50 }), applyTransform(t, { x: 100, y: 50 })];
    expect(a.y).toBeGreaterThanOrEqual(64 - 1e-9);
    expect(b.y).toBeLessThanOrEqual(400 - 72 + 1e-9);
    expect(a.x).toBeGreaterThanOrEqual(56 - 1e-9);
    expect(b.x).toBeLessThanOrEqual(800 - 56 + 1e-9);
    // Centered in what is left, not in the whole canvas.
    expect((a.y + b.y) / 2).toBeCloseTo(64 + (400 - 64 - 72) / 2);
  });
});

describe("geometry", () => {
  it("hashes ids to stable numbers in [0, 1)", () => {
    expect(hashUnit("abc")).toBe(hashUnit("abc"));
    expect(hashUnit("abc")).not.toBe(hashUnit("abd"));
    for (const id of ["", "x", "0a0cad00-0000-4000-8000-00000000ad01"]) {
      expect(hashUnit(id)).toBeGreaterThanOrEqual(0);
      expect(hashUnit(id)).toBeLessThan(1);
    }
  });

  it("rings the graph around the account: the deal close beside it, each link further out", () => {
    expect(ringRadius(0, "Account")).toBe(0);
    expect(ringRadius(1, "Opportunity")).toBeLessThan(ringRadius(1, "Person"));
    expect(ringRadius(2, "Activity")).toBeGreaterThan(ringRadius(1, "Claim"));
  });

  it("seeds a node on its ring, deterministically", () => {
    const a = seedPoint("p1", 1, "Person");
    expect(a).toEqual(seedPoint("p1", 1, "Person"));
    expect(Math.hypot(a.x, a.y)).toBeCloseTo(ringRadius(1, "Person"), 0);
    expect(seedPoint("acc", 0, "Account")).toEqual({ x: 0, y: 0 });
  });
});
