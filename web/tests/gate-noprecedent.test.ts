// H-3: a conditional precedent gate that retrieved nothing reads NOT APPLICABLE (with the reason), not UNKNOWN. Presentation only.
import { describe, expect, it } from "vitest";
import { buildGateRows, rowStatus, type GateCatalogEntry } from "@/lib/evals/gate-table";

const cat: GateCatalogEntry[] = [
  { id: "B5", question: "q", improves: "i", mode: "live_conditional", notTriggered: "no precedent retrieved" },
  { id: "B6", question: "q", improves: "i", mode: "live_conditional", notTriggered: "no precedent retrieved" },
  { id: "B8", question: "q", improves: "i", mode: "live_required" },
];
const unknown = (gate: string, observed: string, why: string) => ({ gate, sub_gate: "", label: "", judged_object: { type: "X", id: gate }, span_id: "", verdict: "unknown", question: "q", observed, why, evidence_refs: ["r"], improves: "i", grader: { kind: "deterministic" }, calibrated: false });
const build = (...r: unknown[]) =>
  buildGateRows([{ episode: { id: "e", label: "E", accountId: "a" }, results: r as never, spanTimes: new Map(), resolve: (ref) => ({ ref, kind: "k", id: ref, label: ref, summary: null, spanId: null, href: null }) }], cat);

describe("no precedents retrieved reads NOT APPLICABLE (B5, B6)", () => {
  it("maps an unknown '0 precedents' result on a conditional gate to NOT APPLICABLE with the reason", () => {
    const b5 = build(unknown("B5", "0 precedents", "no precedent: retrieval returned no earlier case for this situation")).find((r) => r.gate === "B5")!;
    expect(rowStatus(b5)).toBe("not_applicable");
    expect(b5.notTriggered).toBe("no precedents retrieved");
    expect(b5.verdict).toBeNull();
  });
  it("also maps the reason 'no precedent source'", () => {
    const b6 = build(unknown("B6", "", "no precedent source")).find((r) => r.gate === "B6")!;
    expect(rowStatus(b6)).toBe("not_applicable");
    expect(b6.notTriggered).toBe("no precedents retrieved");
  });
  it("leaves a real unknown and any other gate alone", () => {
    const rows = build(unknown("B5", "3 precedents", "the model could not judge"), unknown("B8", "0 precedents", "x"));
    expect(rows.filter((r) => r.gate === "B5" || r.gate === "B8").map((r) => rowStatus(r))).toEqual(["unknown", "unknown"]);
  });
});
