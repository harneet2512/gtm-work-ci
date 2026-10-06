import { describe, expect, it } from "vitest";
import type { AccountState, Activity, Graph, GraphDiff } from "@/lib/api/types";
import { indexDiff } from "@/lib/view/diff";
import { edgeLabel, kindOf } from "@/lib/graph/kinds";
import { buildExplorerModel } from "@/lib/graph/model";
import { factFor, indexClaims } from "@/lib/graph/claim-index";
import { loadFixture } from "./contract-validator";

const graph = loadFixture<Graph>("medtech.graph.json");
const before = loadFixture<Graph>("medtech.graph-before.json");
const diff = loadFixture<GraphDiff>("medtech.graph-diff.json");
const state = loadFixture<AccountState>("medtech.state.json");
const activities = loadFixture<{ items: Activity[] }>("medtech.timeline.json").items;
const EVENT = "05e0ad00-0000-4000-8000-00000000ad0d";
const EVENT_ACTIVITY = "0ac7ad00-0000-4000-8000-00000000ad0d";
const OBJECTION_CLAIM = "0c1aad00-0000-4000-8000-00000000ad27";

const after = () => buildExplorerModel({ graph, marks: indexDiff(diff), showMarks: true, activities, state, eventId: EVENT });

describe("kinds", () => {
  it("puts every ontology label in a colour family", () => {
    expect(kindOf("Account").region).toBe("account");
    expect(kindOf("Opportunity").region).toBe("account");
    expect(kindOf("Person").region).toBe("people");
    expect(kindOf("Activity").region).toBe("activity");
    expect(kindOf("Conversation").region).toBe("activity");
    expect(kindOf("Document").region).toBe("activity");
    expect(kindOf("Claim").region).toBe("knows");
    expect(kindOf("Commitment").region).toBe("knows");
    expect(kindOf("Knowledge").region).toBe("knowledge");
    expect(kindOf("DecisionEpisode").region).toBe("knowledge");
    expect(kindOf("Signal").region).toBe("signals");
  });

  it("gives each kind a distinct shape and marks activities and claims as minor", () => {
    const shapes = ["Account", "Opportunity", "Person", "Activity", "Claim", "Signal", "Knowledge"].map((t) => kindOf(t).shape);
    expect(new Set(shapes).size).toBe(shapes.length);
    expect(kindOf("Activity").major).toBe(false);
    expect(kindOf("Claim").major).toBe(false);
    expect(kindOf("Commitment").major).toBe(false);
    expect(kindOf("Account").major).toBe(true);
    expect(kindOf("Person").major).toBe(true);
    expect(kindOf("Account").radius).toBeGreaterThan(kindOf("Activity").radius);
  });

  it("puts an unknown node type in its own region instead of guessing", () => {
    expect(kindOf("Mystery").region).toBe("other");
    expect(kindOf("Mystery").major).toBe(false);
  });

  it("reads relationship types as plain words and never words retrieval as influence", () => {
    expect(edgeLabel("WORKS_AT")).toBe("works at");
    expect(edgeLabel("SUPPORTED_BY")).toBe("supported by");
    expect(edgeLabel("USED_KNOWLEDGE")).toBe("retrieved");
    expect(edgeLabel("USED_KNOWLEDGE")).not.toMatch(/influen|used/);
    expect(edgeLabel("")).toBe("related");
  });
});

describe("indexClaims", () => {
  const COMMERCIAL = "0c1aad00-0000-4000-8000-00000000ad25";

  it("indexes list items and scalar winners by claim id, with their quotes and standing", () => {
    const claims = indexClaims(state);
    const objection = factFor(claims, OBJECTION_CLAIM, "objections");
    expect(objection?.fieldPath).toBe("objections");
    expect(objection?.text).toBe("Long-term cost of integrating EduTech Lab and SecureData Nexus");
    expect(objection?.refs.some((r) => typeof r.quote === "string" && r.quote.length > 0)).toBe(true);
    expect(factFor(claims, "0c1aad00-0000-4000-8000-00000000ad21")?.fieldPath).toBe("owner");
    expect(factFor(claims, "0c1aad00-0000-4000-8000-00000000ad21")?.standing).toBe("crm_explicit");
  });

  it("keeps every field a claim wins: each field shows its own value, never the last one indexed", () => {
    const claims = indexClaims(state);
    expect(claims.get(COMMERCIAL)?.map((f) => f.fieldPath).sort()).toEqual(["commercial_issue", "relationship_risk", "summary"]);
    expect(factFor(claims, COMMERCIAL, "commercial_issue")?.text).toBe("Weighing Quantum Circuits Inc.'s initial pricing and onboarding");
    expect(factFor(claims, COMMERCIAL, "summary")?.text).toMatch(/^Fatoumata likes the proposal/);
    expect(factFor(claims, COMMERCIAL, "relationship_risk")?.text).toBe("medium");
    // A field the claim does not fill has no fact: never borrow another field's value. With no field
    // asked for (a commitment node carries none) the first fact is the claim's.
    expect(factFor(claims, COMMERCIAL, "nope")).toBeUndefined();
    expect(factFor(claims, COMMERCIAL)?.fieldPath).toBe(claims.get(COMMERCIAL)![0]!.fieldPath);
    expect(factFor(claims, "missing")).toBeUndefined();
  });

  it("marks a competing claim the winner outranked as retained", () => {
    const owner = state.fields.owner!;
    const contested = { ...state, fields: { ...state.fields, owner: { ...owner, competing_claim_ids: ["loser"] } } } as AccountState;
    const fact = factFor(indexClaims(contested), "loser", "owner");
    expect(fact).toMatchObject({ fieldPath: "owner", outranked: true });
    expect(factFor(indexClaims(contested), "0c1aad00-0000-4000-8000-00000000ad21", "owner")?.outranked).toBe(false);
  });

  it("is empty without a state and ignores fields with no claim", () => {
    expect(indexClaims(null).size).toBe(0);
    const bare = { ...state, fields: { stage: { value: "unknown", known: false, winning_claim_id: null, evidence_refs: [] } } } as unknown as AccountState;
    expect(indexClaims(bare).size).toBe(0);
  });
});

describe("ghosts", () => {
  const SUPERSEDED = "0c1aad00-0000-4000-8000-00000000ad2b";

  it("keeps what the event took out of the view as a ghost, named as it was Before", () => {
    const labels = new Map([[SUPERSEDED, "Product use case: Data protection and healthcare education"]]);
    const m = buildExplorerModel({ graph, marks: indexDiff(diff), showMarks: true, activities, state, eventId: EVENT, previousLabels: labels });
    expect(m.byId.has(SUPERSEDED)).toBe(false);
    const ghost = m.ghostById.get(SUPERSEDED)!;
    expect(ghost).toMatchObject({ op: "changed", kindTag: "Fact", label: "Product use case: Data protection and healthcare education", detail: "status: active → superseded" });
    expect(ghost.name).toBe("Product use case: Data protection and healthcare education (Fact, no longer in the graph)");
  });

  it("falls back to the kind when nothing remembers the old label, and has no ghosts Before Play", () => {
    const m = after();
    expect(m.ghostById.get(SUPERSEDED)?.label).toBe("Fact");
    const quiet = buildExplorerModel({ graph, marks: indexDiff(diff), showMarks: false, activities, state, eventId: EVENT });
    expect(quiet.ghosts).toEqual([]);
  });
});

describe("buildExplorerModel", () => {
  it("keeps every node and every edge whose endpoints are present", () => {
    const m = after();
    expect(m.nodes).toHaveLength(graph.nodes.length);
    expect(m.edges).toHaveLength(graph.edges.length);
    const dangling = { ...graph, edges: [...graph.edges, { ...graph.edges[0]!, id: "dangling", target: "nope" }] };
    const m2 = buildExplorerModel({ graph: dangling, marks: indexDiff(diff), showMarks: true, activities, state, eventId: EVENT });
    expect(m2.edges.some((e) => e.id === "dangling")).toBe(false);
    expect(m2.droppedEdges).toBe(1);
  });

  it("marks only After Play, and only what the event changed", () => {
    const m = after();
    expect(m.byId.get(EVENT_ACTIVITY)?.mark).toBe("added");
    const addedEdges = diff.changes.filter((c) => c.kind === "edge" && c.op === "added").length;
    expect(addedEdges).toBeGreaterThan(0);
    expect(m.edges.filter((e) => e.mark === "added")).toHaveLength(addedEdges);
    const quiet = buildExplorerModel({ graph, marks: indexDiff(diff), showMarks: false, activities, state, eventId: EVENT });
    expect(quiet.nodes.some((n) => n.mark !== undefined)).toBe(false);
    expect(quiet.edges.some((e) => e.mark !== undefined)).toBe(false);
  });

  it("finds the event's own activity as the origin of the change", () => {
    expect(after().eventNodeId).toBe(EVENT_ACTIVITY);
    const b = buildExplorerModel({ graph: before, marks: indexDiff(null), showMarks: false, activities, state: null, eventId: EVENT });
    expect(b.eventNodeId).toBeNull();
  });

  it("dates activities from the graph or the timeline, and fills claim values from the state", () => {
    const m = after();
    expect(m.byId.get(EVENT_ACTIVITY)?.at).toBe(Date.parse("2023-11-09T09:30:00Z"));
    expect(m.byId.get(OBJECTION_CLAIM)?.detail).toBe("Long-term cost of integrating EduTech Lab and SecureData Nexus");
    expect(m.byId.get("0a0cad00-0000-4000-8000-00000000ad01")?.at).toBeNull();
    const undated = { ...graph, nodes: graph.nodes.map((n) => (n.id === EVENT_ACTIVITY ? { ...n, valid_from: undefined } : n)) };
    const m2 = buildExplorerModel({ graph: undated, marks: indexDiff(diff), showMarks: true, activities, state, eventId: EVENT });
    expect(m2.byId.get(EVENT_ACTIVITY)?.at).toBe(Date.parse("2023-11-09T09:30:00Z"));
  });

  it("indexes neighbors both ways", () => {
    const m = after();
    const fatoumata = "0b0ead00-0000-4000-8000-00000000ad11";
    expect(m.neighbors.get(fatoumata)?.has(EVENT_ACTIVITY)).toBe(true);
    expect(m.neighbors.get(EVENT_ACTIVITY)?.has(fatoumata)).toBe(true);
  });

  it("never shows a withheld label", () => {
    const hidden = { ...graph, nodes: graph.nodes.map((n, i) => (i === 2 ? { ...n, withheld: "visibility" as const } : n)) };
    const m = buildExplorerModel({ graph: hidden, marks: indexDiff(null), showMarks: false, activities, state, eventId: null });
    const node = m.nodes[2]!;
    expect(node.withheld).toBe(true);
    expect(node.label).toBe("withheld by visibility");
    expect(node.name).toBe("withheld by visibility (Person)");
    expect(node.kindTag).toBe("Person");
  });

  it("does not mutate its input", () => {
    const copy = structuredClone(graph);
    after();
    expect(graph).toEqual(copy);
  });
});
