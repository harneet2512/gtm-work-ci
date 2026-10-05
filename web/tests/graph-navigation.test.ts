import { describe, expect, it } from "vitest";
import type { ExplorerNode } from "@/lib/graph/model";
import { chronologicalActivities, pushTrail, stepThrough, TRAIL_MAX } from "@/lib/graph/stepping";
import { normalizeText, searchNodes } from "@/lib/graph/search";

const n = (id: string, type: string, label: string, at: number | null = null, withheld = false): ExplorerNode => ({
  id,
  type,
  label: withheld ? "withheld by visibility" : label,
  name: `${type}: ${withheld ? "withheld by visibility" : label}`,
  kindTag: type,
  region: "activity",
  shape: "diamond",
  radius: 4,
  major: false,
  withheld,
  at,
  sourceEventIds: [],
  evidence: [],
});

const nodes = [
  n("acc", "Account", "MedTech Advances"),
  n("a3", "Activity", "EmailReceived", 300),
  n("a1", "Activity", "QuoteCreated", 100),
  n("a2b", "Conversation", "Call", 200),
  n("a2a", "Activity", "EmailSent", 200),
  n("undated", "Activity", "CRMTaskLogged", null),
  n("p1", "Person", "Fatoumata Touré"),
  n("claim", "Claim", "objections"),
];

describe("chronologicalActivities", () => {
  it("orders activities by time, ties by id, undated last, non-activities excluded", () => {
    expect(chronologicalActivities(nodes)).toEqual(["a1", "a2a", "a2b", "a3", "undated"]);
  });

  it("is empty when there are no activities", () => {
    expect(chronologicalActivities([n("acc", "Account", "X")])).toEqual([]);
  });
});

describe("stepThrough", () => {
  const order = ["a1", "a2", "a3"];

  it("steps forward and back through time", () => {
    expect(stepThrough(order, "a1", 1)).toBe("a2");
    expect(stepThrough(order, "a2", -1)).toBe("a1");
  });

  it("stops at the ends instead of wrapping", () => {
    expect(stepThrough(order, "a3", 1)).toBe("a3");
    expect(stepThrough(order, "a1", -1)).toBe("a1");
  });

  it("starts at the oldest going forward and the newest going back when nothing is focused", () => {
    expect(stepThrough(order, null, 1)).toBe("a1");
    expect(stepThrough(order, "not-an-activity", -1)).toBe("a3");
  });

  it("returns null when there is nothing to step through", () => {
    expect(stepThrough([], null, 1)).toBeNull();
  });
});

describe("pushTrail", () => {
  it("appends, moves a revisited node to the end and caps the length", () => {
    expect(pushTrail([], "a")).toEqual(["a"]);
    expect(pushTrail(["a", "b"], "a")).toEqual(["b", "a"]);
    const long = Array.from({ length: TRAIL_MAX }, (_, i) => `n${i}`);
    const next = pushTrail(long, "x");
    expect(next).toHaveLength(TRAIL_MAX);
    expect(next.at(-1)).toBe("x");
    expect(next[0]).toBe("n1");
  });

  it("does not mutate the trail", () => {
    const trail = ["a"];
    pushTrail(trail, "b");
    expect(trail).toEqual(["a"]);
  });

  it("ignores pushing the current node again", () => {
    const trail = ["a", "b"];
    expect(pushTrail(trail, "b")).toBe(trail);
  });
});

describe("searchNodes", () => {
  it("matches case- and accent-insensitively", () => {
    expect(searchNodes(nodes, "toure").map((r) => r.id)).toEqual(["p1"]);
    expect(normalizeText("Fatoumata Touré")).toBe("fatoumata toure");
  });

  it("ranks a label prefix above a word prefix above a substring", () => {
    const pool = [n("sub", "Person", "Panamericana"), n("word", "Person", "North America"), n("pre", "Person", "America Inc")];
    expect(searchNodes(pool, "ameri").map((r) => r.id)).toEqual(["pre", "word", "sub"]);
  });

  it("prefers the more important kind when two matches rank the same", () => {
    const pool = [n("act", "Activity", "Contact added: Fatoumata Touré · Oct 15, 2023", 1), n("per", "Person", "Fatoumata Touré")];
    expect(searchNodes(pool, "toure").map((r) => r.id)).toEqual(["per", "act"]);
  });

  it("finds nodes by kind and by claim value", () => {
    expect(searchNodes(nodes, "claim").map((r) => r.id)).toContain("claim");
    const valued = [{ ...n("c", "Claim", "objections"), detail: "Long-term cost of integrating EduTech Lab" }];
    expect(searchNodes(valued, "edutech").map((r) => r.id)).toEqual(["c"]);
  });

  it("never matches the hidden label of a withheld node", () => {
    const pool = [n("w", "Person", "Secret Name", null, true)];
    expect(searchNodes(pool, "secret")).toEqual([]);
    expect(searchNodes(pool, "person").map((r) => r.id)).toEqual(["w"]);
  });

  it("returns nothing for a blank query, caps results, and tolerates special characters", () => {
    expect(searchNodes(nodes, "   ")).toEqual([]);
    expect(searchNodes(nodes, "e", 2)).toHaveLength(2);
    expect(searchNodes(nodes, "(.*'; DROP")).toEqual([]);
    expect(searchNodes([n("emoji", "Person", "Zoë 🚀 Lab")], "🚀").map((r) => r.id)).toEqual(["emoji"]);
  });

  it("searches ten thousand nodes quickly", () => {
    const many = Array.from({ length: 10_000 }, (_, i) => n(`n${i}`, "Activity", `Email ${i}`, i));
    const t0 = performance.now();
    expect(searchNodes(many, "email 99", 8)).toHaveLength(8);
    expect(performance.now() - t0).toBeLessThan(300);
  });
});
