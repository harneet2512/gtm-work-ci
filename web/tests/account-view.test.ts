import { describe, expect, it } from "vitest";
import { buildAccountView, parseView } from "@/lib/view/account-view";
import type { Activity, Graph, GraphDiff } from "@/lib/api/types";
import { loadFixture } from "./contract-validator";

const graph = loadFixture<Graph>("acme.graph.json");
const diff = loadFixture<GraphDiff>("acme.graph-diff.json");
const activities = loadFixture<{ items: Activity[] }>("acme.timeline.json").items;
const EVENT = "05e00000-0000-4000-8000-000000000101";
const CUTOFF = "2026-09-29T15:42:00.000Z";
const ACT_101 = "0ac70000-0000-4000-8000-000000000101";

describe("parseView", () => {
  it.each([
    ["after", "after"],
    ["before", "before"],
    [undefined, "before"],
    ["bogus", "before"],
  ])("%j -> %s", (raw, expected) => {
    expect(parseView(raw)).toBe(expected);
  });
});

describe("buildAccountView", () => {
  it("Before Play keeps the world graph the core returned and cuts the timeline at the cutoff, no highlights", () => {
    const v = buildAccountView({ graph, diff, activities, view: "before", cutoff: CUTOFF, eventId: EVENT });
    // The Before graph is the core's world read; the view never derives it from the After graph.
    expect(v.graph).toEqual(graph);
    expect(v.cutoff).toBe(CUTOFF);
    expect(v.timeline.map((a) => a.id)).not.toContain(ACT_101);
    expect(v.timeline).toHaveLength(2);
    expect(v.showMarks).toBe(false);
    expect(v.eventActivityIds.size).toBe(0);
  });

  it("After Play keeps the whole graph and timeline and highlights the event's changes", () => {
    const v = buildAccountView({ graph, diff, activities, view: "after", cutoff: CUTOFF, eventId: EVENT });
    expect(v.graph.nodes).toHaveLength(graph.nodes.length);
    expect(v.timeline).toHaveLength(3);
    expect(v.showMarks).toBe(true);
    expect(v.marks.nodes.get(ACT_101)).toBe("added");
    expect([...v.eventActivityIds]).toEqual([ACT_101]);
  });

  it("uses the cutoff it is given", () => {
    const v = buildAccountView({ graph, diff, activities, view: "before", cutoff: "2026-09-19T00:00:00.000Z", eventId: EVENT });
    expect(v.timeline.map((a) => a.id)).toEqual(["0ac70000-0000-4000-8000-000000000090"]);
  });

  it("without an event nothing is highlighted and the graph is shown as the core returned it", () => {
    for (const view of ["before", "after"] as const) {
      const v = buildAccountView({ graph, diff: null, activities, view, cutoff: null, eventId: null });
      expect(v.graph).toEqual(graph);
      expect(v.showMarks).toBe(false);
      expect(v.timeline).toHaveLength(3);
    }
  });

  it("an unprojected diff changes nothing and says so", () => {
    const unprojected: GraphDiff = { ...diff, projected: false, changes: [], summary: { added: 0, changed: 0, removed: 0, repaired: 0 } };
    const v = buildAccountView({ graph, diff: unprojected, activities, view: "after", cutoff: CUTOFF, eventId: EVENT });
    expect(v.marks.projected).toBe(false);
    expect(v.graph).toEqual(graph);
  });
});
