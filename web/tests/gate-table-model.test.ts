// The gate results table model (HAR-145, Braintrust-style): rows, filters, sort by severity, saved views, search and the
// bucket summary. Pure functions: nothing here reads a network or invents a number.
import { describe, expect, it } from "vitest";
import {
  applyView,
  bucketOfGate,
  bucketSummaries,
  collapseNotMeasured,
  buildGateRows,
  compareRows,
  EMPTY_FILTERS,
  filterRows,
  toggleIn,
  VIEWS,
  type GateCatalogEntry,
  type GateRow,
} from "@/lib/evals/gate-table";

const EP_A = "0e9e0000-0000-4000-8000-000000000a01";
const EP_B = "0de50000-0000-4000-8000-000000000a02";

const CATALOG: GateCatalogEntry[] = [
  { id: "B1", question: "Did gtm_ai understand the new event correctly?", improves: "Stops corrupted context." },
  { id: "D2", question: "Are the three actions plausible?", improves: "Real options." },
  { id: "D4", question: "What does the choice tell us?", improves: "Structured supervision." },
  { id: "S1", question: "Can we trust the graders?", improves: "Trust." },
];

const result = (over: Record<string, unknown> = {}) => ({
  id: "0e9e0000-0000-4000-8000-0000000d0001",
  gate: "D2",
  sub_gate: "candidate",
  label: "",
  judged_object: { type: "StrategyCandidate", id: "c1" },
  span_id: "candidates:s1",
  verdict: "warn",
  question: "Are the three actions plausible?",
  observed: "cta=warn",
  why: "the ask is stronger than the stated timing",
  evidence_refs: ["candidate:c1"],
  improves: "Real options.",
  grader: { kind: "model", model: "judge-1", prompt_version: "v1" },
  calibrated: false,
  ...over,
});

const ep = (id: string, label: string, results: unknown[]) => ({
  episode: { id, label, accountId: "acc" },
  results: results as never,
  spanTimes: new Map([["candidates:s1", "2026-10-05T10:00:00Z"]]),
  resolve: (ref: string) => ({ ref, kind: ref.split(":")[0]!, id: ref.split(":")[1]!, label: ref, summary: null, spanId: null, href: null }),
});

function rows(): GateRow[] {
  return buildGateRows(
    [
      ep(EP_A, "MedTech Event 13", [
        result(),
        result({ gate: "D4", sub_gate: "", verdict: "pass", span_id: "ranking:none", why: "chosen was not worse", id: "0e9e0000-0000-4000-8000-0000000d0004", grader: { kind: "deterministic" } }),
        result({ gate: "B1", sub_gate: "", verdict: "fail", why: "the event was misread", id: "0e9e0000-0000-4000-8000-0000000d0005", evidence_refs: ["activity:a1"], grader: { kind: "deterministic" } }),
      ]),
      ep(EP_B, "EcoLite", []),
    ],
    CATALOG,
  );
}

describe("bucketOfGate", () => {
  it("maps B to context, D to decision, S to system", () => {
    expect([bucketOfGate("B3"), bucketOfGate("D10"), bucketOfGate("S2")]).toEqual(["context", "decision", "system"]);
  });
  it("rejects anything else", () => {
    expect(bucketOfGate("X1")).toBeNull();
  });
});

describe("buildGateRows", () => {
  it("makes one row per stored result and one not-measured row per catalog gate with no result", () => {
    const r = rows();
    expect(r.filter((x) => x.episodeId === EP_A && x.measured)).toHaveLength(3);
    // EP_A: B1, D2, D4 stored, S1 missing. EP_B: all four missing.
    expect(r.filter((x) => !x.measured)).toHaveLength(5);
    expect(r).toHaveLength(8);
  });

  it("never turns a missing result into a pass and says nothing about it", () => {
    const missing = rows().find((x) => x.episodeId === EP_B && x.gate === "D2")!;
    expect(missing.verdict).toBeNull();
    expect(missing.observed).toBeNull();
    expect(missing.evidence).toEqual([]);
    expect(missing.calibrated).toBe(false);
  });

  it("reads a pass that cites no evidence as unknown (rule R1)", () => {
    const [row] = buildGateRows([ep(EP_A, "A", [result({ verdict: "pass", evidence_refs: [] })])], [CATALOG[1]!]);
    expect(row!.verdict).toBe("unknown");
  });

  it("reads an unrecognised verdict as unknown, never a pass", () => {
    const [row] = buildGateRows([ep(EP_A, "A", [result({ verdict: "abstain" })])], [CATALOG[1]!]);
    expect(row!.verdict).toBe("unknown");
  });

  it("takes the time from the judged span, and null when the span has none", () => {
    const r = rows();
    expect(r.find((x) => x.gate === "D2" && x.measured)!.time).toBe("2026-10-05T10:00:00Z");
    expect(r.find((x) => x.gate === "D4" && x.measured)!.time).toBeNull();
  });

  it("carries the grader kind, model and prompt version, and calibrated is always false", () => {
    const d2 = rows().find((x) => x.gate === "D2" && x.measured)!;
    expect(d2.graderKind).toBe("model");
    expect(d2.graderModel).toBe("judge-1");
    expect(d2.promptVersion).toBe("v1");
    expect(rows().every((x) => x.calibrated === false)).toBe(true);
  });

  it("does not mutate its inputs", () => {
    const results = [result()];
    const frozen = Object.freeze([...results]);
    expect(() => buildGateRows([ep(EP_A, "A", frozen as never)], CATALOG)).not.toThrow();
  });
});

describe("sort", () => {
  it("puts failures first, then warnings, unknown, passes and not measured last", () => {
    const order = [...rows()].sort(compareRows).map((x) => x.verdict);
    expect(order.slice(0, 3)).toEqual(["fail", "warn", "pass"]);
    expect(order.slice(3).every((v) => v === null)).toBe(true);
  });

  it("breaks ties by flow order (B, D, S), then episode label, so the order is stable", () => {
    const sorted = [...rows()].sort(compareRows).filter((x) => !x.measured);
    expect(sorted.map((x) => `${x.gate}/${x.episodeLabel}`)).toEqual(["B1/EcoLite", "D2/EcoLite", "D4/EcoLite", "S1/EcoLite", "S1/MedTech Event 13"]);
  });
});

describe("filterRows", () => {
  it("returns every row for the empty filter", () => {
    expect(filterRows(rows(), EMPTY_FILTERS)).toHaveLength(8);
  });
  it("filters by bucket", () => {
    expect(filterRows(rows(), { ...EMPTY_FILTERS, buckets: ["context"] }).every((x) => x.bucket === "context")).toBe(true);
  });
  it("filters by gate", () => {
    expect(new Set(filterRows(rows(), { ...EMPTY_FILTERS, gates: ["D4"] }).map((x) => x.gate))).toEqual(new Set(["D4"]));
  });
  it("filters by verdict and never matches a not-measured row", () => {
    const out = filterRows(rows(), { ...EMPTY_FILTERS, verdicts: ["fail", "warn"] });
    expect(out.map((x) => x.verdict).sort()).toEqual(["fail", "warn"]);
  });
  it("filters by episode", () => {
    expect(filterRows(rows(), { ...EMPTY_FILTERS, episodes: [EP_B] })).toHaveLength(4);
  });
  it("filters by grader kind", () => {
    const out = filterRows(rows(), { ...EMPTY_FILTERS, graders: ["model"] });
    expect(out).toHaveLength(1);
    expect(out[0]!.gate).toBe("D2");
  });
  it("shows only not-measured rows with the not measured chip", () => {
    const out = filterRows(rows(), { ...EMPTY_FILTERS, notMeasured: true });
    expect(out).toHaveLength(5);
    expect(out.every((x) => !x.measured)).toBe(true);
  });
  it("combines filters with AND", () => {
    expect(filterRows(rows(), { ...EMPTY_FILTERS, buckets: ["decision"], verdicts: ["warn"], graders: ["model"] })).toHaveLength(1);
    expect(filterRows(rows(), { ...EMPTY_FILTERS, buckets: ["system"], verdicts: ["warn"] })).toHaveLength(0);
  });
  it("searches question, observed, why, episode and gate case-insensitively, ignoring surrounding spaces", () => {
    expect(filterRows(rows(), { ...EMPTY_FILTERS, query: "  STRONGER than " })).toHaveLength(1);
    expect(filterRows(rows(), { ...EMPTY_FILTERS, query: "ecolite" })).toHaveLength(4);
    expect(filterRows(rows(), { ...EMPTY_FILTERS, query: "d4" }).every((x) => x.gate === "D4")).toBe(true);
  });
  it("treats regex and SQL characters in the search as plain text", () => {
    expect(filterRows(rows(), { ...EMPTY_FILTERS, query: ".*" })).toHaveLength(0);
    expect(filterRows(rows(), { ...EMPTY_FILTERS, query: "'; DROP TABLE--" })).toHaveLength(0);
  });
  it("handles unicode and emoji in the search", () => {
    expect(filterRows(rows(), { ...EMPTY_FILTERS, query: "Fatoumata Touré 🙂" })).toHaveLength(0);
  });
  it("does not mutate the input", () => {
    const r = rows();
    const copy = [...r];
    filterRows(r, { ...EMPTY_FILTERS, query: "d4" });
    expect(r).toEqual(copy);
  });
});

describe("saved views", () => {
  it("lists Default, Failures & warnings, Unknown and Model-graded in that order", () => {
    expect(VIEWS.map((v) => v.label)).toEqual(["Default", "Failures & warnings", "Unknown", "Model-graded"]);
  });
  it("Default shows everything, failures first", () => {
    const out = applyView(rows(), "default");
    expect(out).toHaveLength(8);
    expect(out[0]!.verdict).toBe("fail");
  });
  it("Failures & warnings keeps only those, failures first", () => {
    expect(applyView(rows(), "failures").map((x) => x.verdict)).toEqual(["fail", "warn"]);
  });
  it("Unknown keeps measured unknown rows only", () => {
    const withUnknown = buildGateRows([ep(EP_A, "A", [result({ verdict: "unknown", evidence_refs: [] })])], CATALOG);
    expect(applyView(withUnknown, "unknown").map((x) => x.verdict)).toEqual(["unknown"]);
  });
  it("Model-graded keeps rows a model graded", () => {
    expect(applyView(rows(), "model").map((x) => x.graderKind)).toEqual(["model"]);
  });
  it("an unknown view id falls back to Default", () => {
    expect(applyView(rows(), "nope")).toHaveLength(8);
  });
});

describe("toggleIn", () => {
  it("adds and removes without mutating", () => {
    const base = ["a"] as const;
    expect(toggleIn(base, "b")).toEqual(["a", "b"]);
    expect(toggleIn(base, "a")).toEqual([]);
    expect(base).toEqual(["a"]);
  });
});

describe("bucketSummaries", () => {
  it("counts pass, warn, fail, unknown and not measured per bucket, with no percentage", () => {
    const s = bucketSummaries(rows(), new Map());
    const decision = s.find((x) => x.bucket === "decision")!;
    expect(decision.counts).toEqual({ pass: 1, warn: 1, fail: 0, unknown: 0, notMeasured: 2, notTriggered: 0 });
    expect(JSON.stringify(s)).not.toMatch(/percent|%/);
  });
  it("says first run when the same episode has no previous run", () => {
    const s = bucketSummaries(rows(), new Map());
    expect(s.every((x) => x.delta === null)).toBe(true);
  });
  it("shows a delta only against a previous run of the same episode", () => {
    const prev = new Map([[EP_A, { pass: 0, warn: 1, fail: 1, unknown: 0, notMeasured: 0, notTriggered: 0 }]]);
    const s = bucketSummaries(rows(), new Map([["decision", prev]]));
    expect(s.find((x) => x.bucket === "decision")!.delta).toEqual({ pass: 1, warn: 0, fail: -1, unknown: 0, notMeasured: 0, notTriggered: 0 });
  });
});

describe("collapseNotMeasured", () => {
  it("leads with measured rows and reports how many not-measured rows it folded", () => {
    const out = collapseNotMeasured(applyView(rows(), "default"), false);
    expect(out.rows.every((x) => x.measured)).toBe(true);
    expect(out.rows).toHaveLength(3);
    expect(out.hidden).toBe(5);
  });
  it("keeps every row, measured first, when expanded", () => {
    const out = collapseNotMeasured(applyView(rows(), "default"), true);
    expect(out.rows).toHaveLength(8);
    expect(out.hidden).toBe(0);
  });
});

describe("execution modes (HAR-97): not triggered vs not measured vs offline", () => {
  const modal: GateCatalogEntry[] = [
    { id: "B1", question: "q1", improves: "i", mode: "live_required", grader: "hybrid" },
    { id: "D5", question: "q5", improves: "i", mode: "live_conditional", notTriggered: "no human edit" },
    { id: "S2", question: "q", improves: "i", mode: "offline_benchmark" },
    { id: "S6", question: "q", improves: "i", mode: "continuous_aggregate" },
  ];
  const built = () => buildGateRows([ep(EP_A, "A", [])], modal);

  it("a live_required gate with no result is not measured", () => {
    const b1 = built().find((r) => r.gate === "B1")!;
    expect(b1.verdict).toBeNull();
    expect(b1.notTriggered).toBeNull();
  });
  it("a live_conditional gate with no result is not triggered, never a pass and never not measured", () => {
    const d5 = built().find((r) => r.gate === "D5")!;
    expect(d5.verdict).toBeNull();
    expect(d5.notTriggered).toBe("no human edit");
    expect(d5.measured).toBe(false);
  });
  it("offline and continuous gates get no per-episode row at all", () => {
    expect(built().map((r) => r.gate).sort()).toEqual(["B1", "D5"]);
  });
  it("the Not measured filter excludes not-triggered rows, and Not triggered selects only them", () => {
    expect(filterRows(built(), { ...EMPTY_FILTERS, notMeasured: true }).map((r) => r.gate)).toEqual(["B1"]);
    expect(filterRows(built(), { ...EMPTY_FILTERS, notTriggered: true }).map((r) => r.gate)).toEqual(["D5"]);
  });
  it("counts them apart in the summary", () => {
    const s = bucketSummaries(built(), new Map());
    expect(s.find((x) => x.bucket === "context")!.counts).toMatchObject({ notMeasured: 1, notTriggered: 0 });
    expect(s.find((x) => x.bucket === "decision")!.counts).toMatchObject({ notMeasured: 0, notTriggered: 1 });
  });
  it("takes the grader from the registry: a hybrid gate stored as deterministic is hybrid, uncalibrated and in the Model-graded view", () => {
    const rs = buildGateRows([ep(EP_A, "A", [result({ gate: "B1", grader: { kind: "deterministic" } })])], modal);
    const b1 = rs.find((r) => r.gate === "B1" && r.measured)!;
    expect(b1.graderKind).toBe("hybrid");
    expect(b1.uncalibrated).toBe(true);
    expect(applyView(rs, "model").map((r) => r.gate)).toEqual(["B1"]);
  });
  it("a deterministic gate is not marked uncalibrated", () => {
    const rs = buildGateRows([ep(EP_A, "A", [result({ gate: "D4", grader: { kind: "deterministic" } })])], CATALOG);
    expect(rs.find((r) => r.gate === "D4" && r.measured)!.uncalibrated).toBe(false);
  });
});
