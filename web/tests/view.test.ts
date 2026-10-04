import { describe, expect, it } from "vitest";
import { indexDiff } from "@/lib/view/diff";
import { layoutGraph } from "@/lib/view/layout";
import { addMicrosecond, parseCutoff, timelineUpTo } from "@/lib/view/timeline";
import { transitionBadge } from "@/lib/view/transition";
import { claimSelection, nodeSelection, edgeSelection, resolveProvenance } from "@/lib/view/provenance";
import type { AccountState, Activity, Graph, GraphDiff } from "@/lib/api/types";
import { loadExample, loadFixture } from "./contract-validator";

const graph = loadFixture<Graph>("acme.graph.json");
const diff = loadFixture<GraphDiff>("acme.graph-diff.json");
const state = loadExample<AccountState>("account_state");
const activities = loadFixture<{ items: Activity[] }>("acme.timeline.json").items;
const ACT_101 = "0ac70000-0000-4000-8000-000000000101";
const ACT_100 = "0ac70000-0000-4000-8000-000000000100";

describe("indexDiff", () => {
  it("marks nodes and edges by op and splits out removals", () => {
    const idx = indexDiff(diff);
    expect(idx.nodes.get(ACT_101)).toBe("added");
    expect(idx.nodes.get("0c0f0000-0000-4000-8000-000000000004")).toBe("changed");
    expect(idx.edges.get("0ed90000-0000-4000-8000-000000000010")).toBe("added");
    expect(idx.removed.map((c) => c.type)).toEqual(["INFLUENCES"]);
    expect(idx.projected).toBe(true);
    expect(idx.summary).toEqual({ added: 10, changed: 1, removed: 1, repaired: 0 });
  });

  it("is empty and unprojected for a missing diff", () => {
    const idx = indexDiff(null);
    expect(idx.nodes.size + idx.edges.size + idx.removed.length).toBe(0);
    expect(idx.projected).toBe(false);
  });

  it("keeps the strongest op when an element appears twice", () => {
    const dup: GraphDiff = {
      ...diff,
      changes: [
        { kind: "node", op: "changed", type: "Claim", id: "n1", source_event_ids: [], attributed_to_event: true },
        { kind: "node", op: "added", type: "Claim", id: "n1", source_event_ids: [], attributed_to_event: true },
      ],
    };
    expect(indexDiff(dup).nodes.get("n1")).toBe("added");
  });

  it("highlights only the changes attributed to the event (M2)", () => {
    const idx = indexDiff(diff);
    // The technical-evaluator edge changed in the same coalesced burst but is not evidence of N.
    expect(idx.edges.has("0ed90000-0000-4000-8000-000000000005")).toBe(false);
    expect(idx.changes.some((c) => c.id === "0ed90000-0000-4000-8000-000000000005")).toBe(true);
  });
});

describe("layoutGraph", () => {
  it("places every node once, people left of the account, evidence right, inside the canvas", () => {
    const l = layoutGraph(graph.nodes);
    expect(l.positions.size).toBe(graph.nodes.length);
    const x = (id: string) => l.positions.get(id)!.x;
    expect(x("0b0e0000-0000-4000-8000-000000000017")).toBeLessThan(x("0a0c0000-0000-4000-8000-000000000001"));
    expect(x(ACT_100)).toBeGreaterThan(x("0a0c0000-0000-4000-8000-000000000001"));
    for (const p of l.positions.values()) {
      expect(p.x).toBeGreaterThan(0);
      expect(p.x).toBeLessThan(l.width);
      expect(p.y).toBeGreaterThan(0);
      expect(p.y).toBeLessThan(l.height);
    }
  });

  it("is deterministic and handles an empty graph", () => {
    expect([...layoutGraph(graph.nodes).positions]).toEqual([...layoutGraph([...graph.nodes].reverse()).positions]);
    expect(layoutGraph([]).positions.size).toBe(0);
  });

  it("puts unknown node types in the evidence column", () => {
    const l = layoutGraph([{ ...graph.nodes[0]!, id: "z", type: "Mystery" }]);
    expect(l.positions.get("z")).toBeDefined();
  });
});

describe("timeline", () => {
  it("orders oldest first", () => {
    expect(timelineUpTo(activities, null).map((a) => a.id)).toEqual([
      "0ac70000-0000-4000-8000-000000000090",
      ACT_100,
      ACT_101,
      "0ac70000-0000-4000-8000-000000000103",
      "0ac70000-0000-4000-8000-000000000102",
    ]);
  });

  it("keeps only activities strictly before the cutoff (the core's `before` semantics)", () => {
    const cutoff = "2026-09-29T15:42:00Z";
    expect(timelineUpTo(activities, cutoff).map((a) => a.id)).not.toContain(ACT_101);
    expect(timelineUpTo(activities, cutoff)).toHaveLength(2);
    expect(timelineUpTo(activities, "2026-09-29T15:42:00.001Z")).toHaveLength(3);
  });

  it("does not mutate its input", () => {
    const ids = activities.map((a) => a.id);
    timelineUpTo(activities, null);
    expect(activities.map((a) => a.id)).toEqual(ids);
  });

  it.each([
    [undefined, null],
    ["", null],
    ["not-a-date", null],
    ["2026-09-29T15:42:00Z", "2026-09-29T15:42:00.000Z"],
  ])("parseCutoff(%j) -> %j", (input, expected) => {
    expect(parseCutoff(input)).toBe(expected);
  });

  it("addMicrosecond includes the event itself and keeps sub-millisecond precision", () => {
    expect(addMicrosecond("2026-09-29T15:42:00Z")).toBe("2026-09-29T15:42:00.000001Z");
    expect(addMicrosecond("2026-09-29T15:42:00.000000Z")).toBe("2026-09-29T15:42:00.000001Z");
    expect(addMicrosecond("2026-09-29T15:42:00.123456Z")).toBe("2026-09-29T15:42:00.123457Z");
  });

  it("addMicrosecond carries into the next second at 999999µs and leaves non-UTC input alone", () => {
    expect(addMicrosecond("2026-09-29T15:42:00.999999Z")).toBe("2026-09-29T15:42:01.000000Z");
    expect(addMicrosecond("not-a-time")).toBe("not-a-time");
  });
});

describe("transitionBadge", () => {
  it("shows CANDIDATE for an open candidate transition with its target and missing required facts", () => {
    const b = transitionBadge(state);
    expect(b.kind).toBe("CANDIDATE");
    expect(b.detail).toContain("REORG");
    expect(b.detail).toContain("EXPANSION");
    expect(b.missingRequired).toEqual(["owner_stabilized", "value_signal_present", "commercial_next_step"]);
    expect(b.confidence).toBe(0.4);
  });

  it("shows CONFIRMED when only a confirmed relationship state exists", () => {
    const b = transitionBadge({ ...state, open_transition: null });
    expect(b.kind).toBe("CONFIRMED");
    expect(b.detail).toContain("REORG");
  });

  it("shows UNRESOLVED for an open unresolved transition without a target", () => {
    const open = { ...state.open_transition!, status: "UNRESOLVED" as const, to_state_candidate: null };
    const b = transitionBadge({ ...state, open_transition: open });
    expect(b.kind).toBe("UNRESOLVED");
    expect(b.detail).toContain("no target");
  });

  it("is none for unknown state, no state, and a state with neither field", () => {
    const unknown = { value: "unknown" as const, transition_id: null, confirmed_at: null };
    expect(transitionBadge({ ...state, open_transition: null, relationship_state: unknown }).kind).toBe("none");
    expect(transitionBadge(null).kind).toBe("none");
    const bare = { ...state, open_transition: undefined, relationship_state: undefined };
    expect(transitionBadge(bare).kind).toBe("none");
  });
});

describe("resolveProvenance", () => {
  const lookup = new Map(activities.map((a) => [a.id, a]));

  it("resolves a node's evidence refs to activities", () => {
    const node = graph.nodes.find((n) => n.id === "0c1a0000-0000-4000-8000-000000000201")!;
    const p = resolveProvenance(nodeSelection(node), lookup);
    expect(p.title).toBe("Claim: health");
    expect(p.items).toHaveLength(1);
    expect(p.items[0]!.activity?.summary).toContain("SOC2");
    expect(p.items[0]!.activityId).toBe(ACT_101);
  });

  it("falls back to the activity itself for an activity node, and keeps unresolved ids visible", () => {
    const act = graph.nodes.find((n) => n.id === ACT_101)!;
    expect(resolveProvenance(nodeSelection(act), lookup).items[0]!.activity?.id).toBe(ACT_101);
    const missing = resolveProvenance(nodeSelection(act), new Map());
    expect(missing.items[0]!.activity).toBeUndefined();
    expect(missing.items[0]!.activityId).toBe(ACT_101);
  });

  it("resolves a state claim with its quote, speaker, standing and claim id", () => {
    const field = state.fields["health"]!;
    const p = resolveProvenance(claimSelection("health", field), lookup);
    expect(p.title).toBe("Claim: health");
    expect(p.items[0]!.quote).toContain("SOC2 Type II");
    expect(p.items[0]!.claimId).toBe("0c1a0000-0000-4000-8000-000000000201");
    expect(p.meta).toContain("first_party_ai");
  });

  it("explains a withheld node instead of showing evidence", () => {
    const node = { ...graph.nodes[0]!, withheld: "visibility" as const };
    const p = resolveProvenance(nodeSelection(node), lookup);
    expect(p.withheld).toBe(true);
    expect(p.items).toEqual([]);
  });

  it("resolves an edge to its evidence, labelled with its relationship", () => {
    const edge = graph.edges.find((e) => e.id === "0ed90000-0000-4000-8000-000000000010")!;
    const p = resolveProvenance(edgeSelection(edge, "Acme email", "Marco Ruiz"), lookup);
    expect(p.title).toBe("INVOLVES: Acme email -> Marco Ruiz");
    expect(p.items[0]!.activityId).toBe(ACT_101);
  });

  it("says when an element has no evidence", () => {
    const acct = graph.nodes[0]!;
    const p = resolveProvenance(nodeSelection(acct), lookup);
    expect(p.items).toEqual([]);
    expect(p.withheld).toBe(false);
  });
});
