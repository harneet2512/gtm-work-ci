// The recorded core responses the web tests and the Playwright run render must satisfy the contract,
// so a contract change fails here before it can silently stale the fixtures.
import { describe, expect, it } from "vitest";
import { loadExample, loadFixture, validateComponent, validateSchema } from "./contract-validator";
import type { Graph, GraphDiff } from "@/lib/api/types";

describe("recorded core fixtures conform to the contract", () => {
  it("rejects a broken document (the validator is not vacuous)", () => {
    expect(validateComponent("Graph", { account_id: "x" }).valid).toBe(false);
    expect(validateComponent("GraphDiff", {}).valid).toBe(false);
  });

  it("GET /accounts/{id}/graph", () => {
    const result = validateComponent("Graph", loadFixture("acme.graph.json"));
    expect(result.errors).toEqual([]);
  });

  it("GET /accounts/{id}/graph?world_as_of=<occurred_at(N)> (the N-1 world graph)", () => {
    const before = loadFixture<Graph>("acme.graph-before.json");
    expect(validateComponent("Graph", before).errors).toEqual([]);
    // The world strictly before N has none of N's additions and has the edge N removed.
    const ids = new Set(before.nodes.map((n) => n.id));
    expect(ids.has("0ac70000-0000-4000-8000-000000000101")).toBe(false);
    expect(ids.has("0c1a0000-0000-4000-8000-000000000201")).toBe(false);
    expect(before.edges.some((e) => e.id === "0ed90000-0000-4000-8000-000000000099")).toBe(true);
    expect(before.projection.projected_at).toBeNull();
  });

  it("GET /events/{id}/graph-diff", () => {
    const result = validateComponent("GraphDiff", loadFixture("acme.graph-diff.json"));
    expect(result.errors).toEqual([]);
  });

  it("GET /accounts/{id}/timeline items", () => {
    const timeline = loadFixture<{ items: unknown[] }>("acme.timeline.json");
    expect(timeline.items.length).toBeGreaterThan(1);
    for (const item of timeline.items) expect(validateSchema("activity", item).errors).toEqual([]);
  });

  it("GET /accounts list", () => {
    const list = loadFixture<{ items: unknown[] }>("accounts.json");
    for (const item of list.items) expect(validateComponent("AccountSummary", item).errors).toEqual([]);
  });

  it("GET /accounts/{id}/state is the contract example", () => {
    expect(validateSchema("account_state", loadExample("account_state")).errors).toEqual([]);
  });

  it("GET /accounts/{id}/state?world_as_of=<occurred_at(N)> (the state strictly before the Play event)", () => {
    const before = loadFixture<{
      open_transition: unknown;
      fields: Record<string, { known: boolean }>;
      opportunities: { health: string; as_of: string; last_activity_id: string }[];
    }>("acme.state-before.json");
    expect(validateSchema("account_state", before).errors).toEqual([]);
    expect(before.open_transition).toBeNull();
    expect(before.fields["health"]!.known).toBe(false);
    // The before state carries no value or evidence of event N.
    expect(before.opportunities[0]!.health).toBe("unknown");
    expect(before.opportunities[0]!.last_activity_id).toBe("0ac70000-0000-4000-8000-000000000100");
    expect(JSON.stringify(before)).not.toContain("2026-09-29T15:42:00Z");
  });

  it("the diff summary counts match its changes and every change targets a graph element or a removal", () => {
    const diff = loadFixture<GraphDiff>("acme.graph-diff.json");
    const graph = loadFixture<Graph>("acme.graph.json");
    const count = (op: string) => diff.changes.filter((c) => c.op === op).length;
    expect(diff.summary).toEqual({ added: count("added"), changed: count("changed"), removed: count("removed"), repaired: count("repaired") });
    const ids = new Set([...graph.nodes.map((n) => n.id), ...graph.edges.map((e) => e.id)]);
    for (const change of diff.changes.filter((c) => c.op !== "removed")) expect(ids.has(change.id), change.id).toBe(true);
  });
});
