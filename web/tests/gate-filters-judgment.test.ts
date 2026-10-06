// HAR-145 filters: Mode, Impact and the six-value Status, over rows that carry the registry's message and impact.
import { describe, expect, it } from "vitest";
import { buildGateRows, EMPTY_FILTERS, countRows, filterRows, rowStatus, type EpisodeInput, type GateCatalogEntry } from "@/lib/evals/gate-table";

const catalog: GateCatalogEntry[] = [
  { id: "B1", question: "q", improves: "i", mode: "live_required", message: "M1", impact: "monitoring only" },
  { id: "D5", question: "q", improves: "i", mode: "live_conditional", notTriggered: "no human edit", message: "M3", impact: "monitoring only" },
  { id: "D8", question: "q", improves: "i", mode: "live_required", message: "M2", impact: "blocks current action" },
];
const res = (gate: string, verdict: string) => ({ gate, sub_gate: "", label: "", judged_object: { type: "X", id: gate }, span_id: "", verdict, question: "q", observed: "o", why: "w", evidence_refs: ["r"], improves: "i", grader: { kind: "deterministic" }, calibrated: false });
const ep: EpisodeInput = { episode: { id: "e", label: "E", accountId: "a" }, results: [res("B1", "pass")] as never, spanTimes: new Map(), resolve: (ref) => ({ ref, kind: "k", id: ref, label: ref, summary: null, spanId: null, href: null }) };
const rows = buildGateRows([ep], catalog);

describe("rows carry registry data", () => {
  it("take message and impact from the catalog entry, never from the gate id", () => {
    expect(rows.find((r) => r.gate === "B1")).toMatchObject({ message: "M1", impact: "monitoring only" });
    expect(rows.find((r) => r.gate === "D8")).toMatchObject({ message: "M2", impact: "blocks current action" });
  });
});

describe("six-value status", () => {
  it("reads NOT RUN for a required gate with no result and NOT APPLICABLE for an absent trigger", () => {
    expect(rowStatus(rows.find((r) => r.gate === "D8")!)).toBe("not_run");
    expect(rowStatus(rows.find((r) => r.gate === "D5")!)).toBe("not_applicable");
    expect(rowStatus(rows.find((r) => r.gate === "B1")!)).toBe("pass");
  });
  it("counts NOT RUN and NOT APPLICABLE apart from every verdict", () => {
    expect(countRows(rows)).toMatchObject({ pass: 1, notMeasured: 1, notTriggered: 1 });
  });
});

describe("filters", () => {
  it("narrows by status", () => {
    expect(filterRows(rows, { ...EMPTY_FILTERS, statuses: ["not_run"] }).map((r) => r.gate)).toEqual(["D8"]);
    expect(filterRows(rows, { ...EMPTY_FILTERS, statuses: ["not_applicable", "pass"] }).map((r) => r.gate).sort()).toEqual(["B1", "D5"]);
  });
  it("narrows by mode", () => {
    expect(filterRows(rows, { ...EMPTY_FILTERS, modes: ["live_conditional"] }).map((r) => r.gate)).toEqual(["D5"]);
  });
  it("narrows by impact", () => {
    expect(filterRows(rows, { ...EMPTY_FILTERS, impacts: ["blocks current action"] }).map((r) => r.gate)).toEqual(["D8"]);
  });
  it("ANDs the kinds and ignores empty ones", () => {
    expect(filterRows(rows, { ...EMPTY_FILTERS, modes: ["live_required"], impacts: ["monitoring only"] }).map((r) => r.gate)).toEqual(["B1"]);
    expect(filterRows(rows, EMPTY_FILTERS)).toHaveLength(3);
  });
});
