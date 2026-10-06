import { describe, expect, it } from "vitest";
import type { AccountState, Activity, Graph, GraphDiff } from "@/lib/api/types";
import { indexDiff } from "@/lib/view/diff";
import { buildExplorerModel } from "@/lib/graph/model";
import { createLayoutMemory, type MemoryStorage } from "@/lib/graph/memory";
import { FALLBACK_PALETTE, readPalette } from "@/lib/graph/palette";
import { ghostSelection, selectionForNode } from "@/lib/graph/selection";
import { coverageOf } from "@/lib/graph/coverage";
import { fieldHistory, standingLabel } from "@/lib/graph/history";
import { indexClaims } from "@/lib/graph/claim-index";
import { loadFixture } from "./contract-validator";

const graph = loadFixture<Graph>("medtech.graph.json");
const diff = loadFixture<GraphDiff>("medtech.graph-diff.json");
const state = loadFixture<AccountState>("medtech.state.json");
const prior = loadFixture<AccountState>("medtech.state-before.json");
const activities = loadFixture<{ items: Activity[] }>("medtech.timeline.json").items;
const EVENT = "05e0ad00-0000-4000-8000-00000000ad0d";
const model = buildExplorerModel({ graph, marks: indexDiff(diff), showMarks: true, activities, state, eventId: EVENT });

function fakeStorage(): MemoryStorage & { data: Map<string, string> } {
  const data = new Map<string, string>();
  return { data, getItem: (k) => data.get(k) ?? null, setItem: (k, v) => void data.set(k, v) };
}

describe("layout memory", () => {
  it("remembers positions and the camera per account, merging updates", () => {
    const mem = createLayoutMemory(null);
    expect(mem.recall("acc")).toBeNull();
    mem.remember("acc", new Map([["a", { x: 1, y: 2 }]]), { x: 0, y: 0, k: 1 });
    mem.remember("acc", new Map([["b", { x: 3, y: 4 }]]), { x: 5, y: 6, k: 2 });
    const got = mem.recall("acc")!;
    expect(got.positions.get("a")).toEqual({ x: 1, y: 2 });
    expect(got.positions.get("b")).toEqual({ x: 3, y: 4 });
    expect(got.camera).toEqual({ x: 5, y: 6, k: 2 });
    expect(mem.recall("other")).toBeNull();
  });

  it("survives a reload through storage, and ignores corrupt or hostile entries", () => {
    const storage = fakeStorage();
    createLayoutMemory(storage).remember("acc", new Map([["a", { x: 1, y: 2 }]]), { x: 0, y: 0, k: 1.5 });
    const fresh = createLayoutMemory(storage).recall("acc")!;
    expect(fresh.positions.get("a")).toEqual({ x: 1, y: 2 });
    expect(fresh.camera.k).toBe(1.5);

    storage.data.set("ghost.graph.v1:bad", "{not json");
    expect(createLayoutMemory(storage).recall("bad")).toBeNull();
    storage.data.set("ghost.graph.v1:evil", JSON.stringify({ positions: [["a", { x: "1", y: null }], ["b", { x: 1, y: 2 }]], camera: { x: 0, y: 0, k: -1 } }));
    const evil = createLayoutMemory(storage).recall("evil")!;
    expect(evil.positions.has("a")).toBe(false);
    expect(evil.positions.get("b")).toEqual({ x: 1, y: 2 });
    expect(evil.camera.k).toBe(1);
  });

  it("remembers the labels each node had, merged across views and through storage", () => {
    const storage = fakeStorage();
    const mem = createLayoutMemory(storage);
    mem.remember("acc", new Map([["a", { x: 1, y: 1 }]]), { x: 0, y: 0, k: 1 }, new Map([["a", "Old label"]]));
    mem.remember("acc", new Map([["b", { x: 2, y: 2 }]]), { x: 0, y: 0, k: 1 }, new Map([["b", "B"]]));
    expect(mem.recall("acc")!.labels.get("a")).toBe("Old label");
    expect(createLayoutMemory(storage).recall("acc")!.labels.get("b")).toBe("B");
    storage.data.set("ghost.graph.v1:odd", JSON.stringify({ positions: [], labels: [["x", 5], ["y", "ok"]] }));
    const odd = createLayoutMemory(storage).recall("odd")!;
    expect(odd.labels.has("x")).toBe(false);
    expect(odd.labels.get("y")).toBe("ok");
  });

  it("keeps working when storage throws (private mode, quota)", () => {
    const broken: MemoryStorage = {
      getItem: () => {
        throw new Error("denied");
      },
      setItem: () => {
        throw new Error("quota");
      },
    };
    const mem = createLayoutMemory(broken);
    expect(() => mem.remember("acc", new Map([["a", { x: 1, y: 1 }]]), { x: 0, y: 0, k: 1 })).not.toThrow();
    expect(mem.recall("acc")?.positions.get("a")).toEqual({ x: 1, y: 1 });
  });
});

describe("readPalette", () => {
  it("reads the theme tokens and falls back for any that are missing", () => {
    const vars: Record<string, string> = { "--graph-canvas": " #101114 ", "--brand": "#8b87f0" };
    const p = readPalette((name) => vars[name] ?? "");
    expect(p.canvas).toBe("#101114");
    expect(p.accent).toBe("#8b87f0");
    expect(p.edge).toBe(FALLBACK_PALETTE.edge);
    expect(p.fontFamily).toBe(FALLBACK_PALETTE.fontFamily);
  });
});

describe("fieldHistory and standingLabel", () => {
  it("says what each field used to be just before the event, and when it changed", () => {
    const history = fieldHistory(prior, state);
    expect(history.get("product_use_case")).toEqual({
      before: "Data protection and healthcare education: SecureData Nexus, CryptGuard Module, EduTech Lab",
      after: "CryptGuard Module for data protection; integrating EduTech Lab and SecureData Nexus",
      changedAt: "2023-11-09T09:30:00Z",
    });
    expect(history.get("next_meeting")?.before).toBe("not known");
    expect(history.has("owner")).toBe(false);
    expect(fieldHistory(null, state).size).toBe(0);
  });

  it("puts a standing in words", () => {
    expect(standingLabel("crm_explicit")).toBe("CRM record");
    expect(standingLabel("first_party_ai")).toBe("AI reading of first-party evidence");
    expect(standingLabel("human_approved")).toBe("Approved by a person");
    expect(standingLabel("something_new")).toBe("Something new");
  });
});

describe("selectionForNode", () => {
  const claims = indexClaims(state);
  const history = fieldHistory(prior, state);
  const COMMERCIAL = "0c1aad00-0000-4000-8000-00000000ad25";
  const USE_CASE = "0c1aad00-0000-4000-8000-00000000ad29";

  it("gives a fact its own field's value, its standing and the quotes behind it", () => {
    const sel = selectionForNode(model, "0c1aad00-0000-4000-8000-00000000ad27", claims, history)!;
    expect(sel.kind).toBe("node");
    expect(sel.title).toBe("Objection: Long-term cost of integrating EduTech Lab and SecureData Nexus");
    expect(sel.facts).toContainEqual({ label: "Field", value: "Objection" });
    expect(sel.facts).toContainEqual({ label: "Value", value: "Long-term cost of integrating EduTech Lab and SecureData Nexus" });
    expect(sel.facts).toContainEqual({ label: "This event", value: "added" });
    const quoted = sel.refs.find((r) => r.quote)!;
    expect(quoted).toMatchObject({ claimId: "0c1aad00-0000-4000-8000-00000000ad27", speaker: "0b0ead00-0000-4000-8000-00000000ad11", standing: "AI reading of first-party evidence" });
  });

  it("shows the commercial issue, not the summary the same claim also wins", () => {
    const sel = selectionForNode(model, COMMERCIAL, claims, history)!;
    expect(sel.facts).toContainEqual({ label: "Field", value: "Commercial issue" });
    expect(sel.facts).toContainEqual({ label: "Value", value: "Weighing Quantum Circuits Inc.'s initial pricing and onboarding" });
    expect(JSON.stringify(sel.facts)).not.toMatch(/likes the proposal/);
  });

  it("says what a fact the event changed used to be, and when it changed", () => {
    const sel = selectionForNode(model, USE_CASE, claims, history)!;
    expect(sel.facts).toContainEqual({ label: "Used to be", value: "Data protection and healthcare education: SecureData Nexus, CryptGuard Module, EduTech Lab (until Nov 9, 2023)" });
    // A fact the event did not touch has no history line, even though its field's list grew.
    const untouched = selectionForNode(model, "0c1aad00-0000-4000-8000-00000000ad23", claims, history)!;
    expect(untouched.facts?.some((f) => f.label === "Used to be")).toBe(false);
  });

  it("labels an outranked competing claim as retained", () => {
    const owner = state.fields.owner!;
    const contested = indexClaims({ ...state, fields: { ...state.fields, owner: { ...owner, competing_claim_ids: [COMMERCIAL] } } } as AccountState);
    const g = { ...graph, nodes: graph.nodes.map((n) => (n.id === COMMERCIAL ? { ...n, label: "owner", data: { field_path: "owner" } } : n)) };
    const m = buildExplorerModel({ graph: g, marks: indexDiff(diff), showMarks: true, activities, state, eventId: EVENT });
    const sel = selectionForNode(m, COMMERCIAL, contested, new Map())!;
    expect(sel.facts).toContainEqual({ label: "Standing", value: "Outranked, retained" });
  });

  it("lists connected nodes by name with the relationship in words, both directions", () => {
    const sel = selectionForNode(model, "0b0ead00-0000-4000-8000-00000000ad11", claims, history)!;
    expect(sel.related).toContainEqual(expect.objectContaining({ rel: "works at", name: "MedTech Advances (Account)" }));
    expect(sel.related).toContainEqual(expect.objectContaining({ rel: "involves", direction: "in" }));
  });

  it("keeps a plain node's own evidence and returns null for an unknown id", () => {
    const sel = selectionForNode(model, "0ac7ad00-0000-4000-8000-00000000ad03", claims, history)!;
    expect(sel.refs.map((r) => r.activityId)).toEqual(["0ac7ad00-0000-4000-8000-00000000ad03"]);
    expect(sel.title).toBe("Quote created · Oct 15, 2023");
    expect(sel.meta).toBe("Activity · since 2023-10-15 10:12 UTC");
    expect(selectionForNode(model, "nope", claims, history)).toBeNull();
  });

  it("hides everything about a withheld node", () => {
    const hidden = { ...graph, nodes: graph.nodes.map((n) => (n.id === "0c1aad00-0000-4000-8000-00000000ad27" ? { ...n, withheld: "visibility" as const } : n)) };
    const m = buildExplorerModel({ graph: hidden, marks: indexDiff(diff), showMarks: true, activities, state, eventId: EVENT });
    const sel = selectionForNode(m, "0c1aad00-0000-4000-8000-00000000ad27", claims, history)!;
    expect(sel.withheld).toBe(true);
    expect(sel.facts ?? []).not.toContainEqual(expect.objectContaining({ label: "Value" }));
  });
});

describe("coverageOf", () => {
  it("says plainly that everything on record is in the graph", () => {
    const c = coverageOf(model, activities, indexClaims(state));
    expect(c).toMatchObject({ activities: { shown: 13, total: 13 }, facts: { shown: 10, total: 10 } });
    expect(c.text).toBe("The graph holds all 13 activities and all 10 facts on record.");
  });

  it("says how much is missing when the graph holds less than the record", () => {
    const sparse = { ...graph, nodes: graph.nodes.filter((n) => !["0ac7ad00-0000-4000-8000-00000000ad01", "0c1aad00-0000-4000-8000-00000000ad22"].includes(n.id)) };
    const m = buildExplorerModel({ graph: sparse, marks: indexDiff(null), showMarks: false, activities, state, eventId: null });
    expect(coverageOf(m, activities, indexClaims(state)).text).toBe("The graph holds 12 of 13 activities and 9 of 10 facts on record.");
    expect(coverageOf(m, activities.slice(0, 1), new Map()).text).toBe("The graph holds 12 activities (1 on record in this window) and 9 facts.");
    const empty = buildExplorerModel({ graph: { ...graph, nodes: [], edges: [] }, marks: indexDiff(null), showMarks: false, activities: [], state: null, eventId: null });
    expect(coverageOf(empty, [], new Map()).text).toBe("No activities or facts are on record yet.");
  });
});

describe("ghostSelection", () => {
  it("opens what the event took out of the view: what it was and what changed", () => {
    const labels = new Map([["0c1aad00-0000-4000-8000-00000000ad2b", "Product use case: Data protection"]]);
    const m = buildExplorerModel({ graph, marks: indexDiff(diff), showMarks: true, activities, state, eventId: EVENT, previousLabels: labels });
    const sel = ghostSelection(m, "0c1aad00-0000-4000-8000-00000000ad2b")!;
    expect(sel).toMatchObject({ kind: "node", title: "Product use case: Data protection", meta: "Fact · no longer in the graph", withheld: false });
    expect(sel.facts).toEqual([
      { label: "This event", value: "changed" },
      { label: "What changed", value: "status: active → superseded" },
    ]);
    expect(ghostSelection(m, "nope")).toBeNull();
  });
});
