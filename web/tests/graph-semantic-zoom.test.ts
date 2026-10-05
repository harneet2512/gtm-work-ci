import { describe, expect, it } from "vitest";
import {
  EDGE_LABEL_K,
  LABEL_PX,
  MINOR_LABEL_K,
  labelText,
  placeLabels,
  wrapLabel,
  screenRadius,
  showEdgeLabel,
  showNodeLabel,
  type LabelCandidate,
} from "@/lib/graph/semantic-zoom";
import type { ExplorerNode } from "@/lib/graph/model";

const node = (over: Partial<ExplorerNode>): ExplorerNode => ({
  id: "n",
  type: "Activity",
  label: "EmailReceived",
  name: "Activity: EmailReceived",
  kindTag: "Activity",
  region: "activity",
  shape: "diamond",
  radius: 4,
  major: false,
  withheld: false,
  at: Date.parse("2023-11-09T09:30:00Z"),
  sourceEventIds: [],
  evidence: [],
  ...over,
});

describe("showNodeLabel", () => {
  it("always labels major nodes, at any zoom", () => {
    const major = node({ type: "Person", major: true });
    for (const k of [0.2, 0.5, 1, 4]) expect(showNodeLabel(major, k, "none")).toBe(true);
  });

  it("labels minor nodes only once zoomed in", () => {
    const minor = node({});
    expect(showNodeLabel(minor, 1, "none")).toBe(false);
    expect(showNodeLabel(minor, MINOR_LABEL_K - 0.01, "none")).toBe(false);
    expect(showNodeLabel(minor, MINOR_LABEL_K, "none")).toBe(true);
  });

  it("labels the focused node and its neighborhood at any zoom", () => {
    const minor = node({});
    expect(showNodeLabel(minor, 0.3, "focus")).toBe(true);
    expect(showNodeLabel(minor, 0.3, "neighbor")).toBe(true);
    expect(showNodeLabel(minor, 0.3, "dimmed")).toBe(false);
  });
});

describe("showEdgeLabel", () => {
  it("shows edge labels only when zoomed in, earlier for the focused node's edges", () => {
    expect(showEdgeLabel(1, false)).toBe(false);
    expect(showEdgeLabel(EDGE_LABEL_K, false)).toBe(true);
    expect(showEdgeLabel(MINOR_LABEL_K, true)).toBe(true);
    expect(showEdgeLabel(MINOR_LABEL_K - 0.01, true)).toBe(false);
    expect(EDGE_LABEL_K).toBeGreaterThan(MINOR_LABEL_K);
  });
});

describe("labelText", () => {
  const deal = node({ type: "Opportunity", major: true, label: "Advanced Data Protection and Education Enhancement Deal", at: null });

  it("says more as you zoom in: clipped when far, longer at mid zoom, whole from zoom 2", () => {
    const far = labelText(deal, 0.8);
    expect(far.length).toBeLessThanOrEqual(36);
    expect(far.endsWith("…")).toBe(true);
    expect(labelText(deal, 1.5).length).toBeGreaterThan(far.length);
    expect(labelText(deal, 2)).toBe("Advanced Data Protection and Education Enhancement Deal");
  });

  it("never clips the focused or hovered node", () => {
    expect(labelText(deal, 0.5, true)).toBe("Advanced Data Protection and Education Enhancement Deal");
  });

  it("leaves short labels alone and never reveals a withheld node", () => {
    expect(labelText(node({ label: "Email from Fatoumata · Nov 9, 2023" }), 1)).toBe("Email from Fatoumata · Nov 9, 2023");
    expect(labelText(node({ withheld: true, label: "withheld by visibility", detail: "secret" }), 4)).toBe("withheld by visibility");
  });
});

describe("wrapLabel", () => {
  const measure = (t: string): number => t.length * 6;

  it("wraps a long label onto two lines at word boundaries", () => {
    const lines = wrapLabel("Advanced Data Protection and Education Enhancement Deal", 180, measure);
    expect(lines).toHaveLength(2);
    expect(lines.join(" ")).toBe("Advanced Data Protection and Education Enhancement Deal");
    for (const l of lines) expect(measure(l)).toBeLessThanOrEqual(180);
  });

  it("keeps a short label on one line and clips what does not fit in two", () => {
    expect(wrapLabel("Customer replied", 180, measure)).toEqual(["Customer replied"]);
    const lines = wrapLabel("word ".repeat(40).trim(), 60, measure);
    expect(lines).toHaveLength(2);
    expect(lines[1]!.endsWith("…")).toBe(true);
    expect(wrapLabel("Supercalifragilisticexpialidocious", 60, measure)[0]!.endsWith("…")).toBe(true);
  });
});

describe("screenRadius", () => {
  it("grows gently with zoom, within bounds", () => {
    expect(screenRadius(6, 1)).toBe(6);
    expect(screenRadius(6, 4)).toBeGreaterThan(6);
    expect(screenRadius(6, 4)).toBeLessThan(6 * 4);
    expect(screenRadius(6, 0.1)).toBeGreaterThanOrEqual(6 * 0.5);
    expect(screenRadius(6, 100)).toBeLessThanOrEqual(6 * 2.4);
  });
});

describe("placeLabels", () => {
  const cand = (id: string, x: number, y: number, priority: number, force = false): LabelCandidate => ({ id, x, y, width: 60, height: LABEL_PX, priority, force });

  it("keeps the higher priority label when two overlap", () => {
    const placed = placeLabels([cand("low", 100, 100, 1), cand("high", 110, 102, 5)]);
    expect(placed.map((p) => p.id)).toEqual(["high"]);
  });

  it("keeps labels that do not overlap", () => {
    expect(placeLabels([cand("a", 0, 0, 1), cand("b", 200, 0, 1)])).toHaveLength(2);
  });

  it("never drops a forced (major) label", () => {
    const placed = placeLabels([cand("a", 0, 0, 9, true), cand("b", 5, 0, 1, true), cand("c", 8, 0, 3)]);
    expect(placed.map((p) => p.id).sort()).toEqual(["a", "b"]);
  });

  it("handles thousands of labels quickly", () => {
    const many = Array.from({ length: 5000 }, (_, i) => cand(`n${i}`, (i * 37) % 2000, (i * 53) % 1200, i % 7));
    const t0 = performance.now();
    const placed = placeLabels(many);
    expect(performance.now() - t0).toBeLessThan(500);
    expect(placed.length).toBeGreaterThan(0);
    expect(placed.length).toBeLessThan(many.length);
  });

  it("does not mutate its input", () => {
    const input = [cand("a", 0, 0, 1), cand("b", 1, 0, 2)];
    const copy = structuredClone(input);
    placeLabels(input);
    expect(input).toEqual(copy);
  });
});
