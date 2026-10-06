import { describe, expect, it } from "vitest";
import type { GateResult, TraceSpan } from "@/lib/api/types";
import { aggregateVerdicts, buildTraceTree, gapText, pickSpan } from "@/lib/view/trace-tree";

const span = (seq: number, kind: TraceSpan["kind"], at: string | null, id = `${kind}:x${seq}`): TraceSpan =>
  ({ id, seq, kind, title: kind, status: "recorded", occurred_at: at, summary: "", refs: [], eval_result_ids: [], attributes: {} }) as TraceSpan;
const gate = (span_id: string, verdict: string, evidence: string[] = ["activity:a"]) => ({ gate: "D2", span_id, verdict, evidence_refs: evidence }) as unknown as GateResult;

const SPANS = [
  span(3, "state", "2026-10-05T10:00:10Z"),
  span(1, "source_event", "2026-10-05T10:00:00Z"),
  span(2, "evidence", null),
  span(4, "candidates", "2026-10-05T10:02:10Z"),
];

describe("buildTraceTree", () => {
  it("orders phases along the causal chain and drops empty ones", () => {
    expect(buildTraceTree(SPANS, []).map((p) => p.id)).toEqual(["event", "evidence", "state", "candidates"]);
  });
  it("groups the three knowledge kinds and cliff messages under one phase each", () => {
    const t = buildTraceTree([span(1, "knowledge_retrieved", null), span(2, "knowledge_used", null), span(3, "cliff_message", null), span(4, "cliff_message", null, "cliff_message:m2")], []);
    expect(t.find((p) => p.id === "knowledge")!.spans).toHaveLength(2);
    expect(t.find((p) => p.id === "cliff")!.spans).toHaveLength(2);
  });
  it("gives a gap only between spans that both have a time", () => {
    const flat = buildTraceTree(SPANS, []).flatMap((p) => p.spans);
    expect(flat.map((s) => s.gapMs)).toEqual([null, null, 10_000, 120_000]);
  });
  it("attaches the gate results of a span and their verdicts", () => {
    const t = buildTraceTree(SPANS, [gate("candidates:x4", "warn"), gate("candidates:x4", "fail"), gate("nowhere:1", "pass")]);
    const c = t.find((p) => p.id === "candidates")!.spans[0]!;
    expect(c.verdicts).toEqual(["warn", "fail"]);
    expect(t.flatMap((p) => p.spans).flatMap((s) => s.gates)).toHaveLength(2);
  });
  it("reads a pass without evidence, or an odd verdict, as unknown", () => {
    const t = buildTraceTree(SPANS, [gate("state:x3", "pass", []), gate("state:x3", "abstain")]);
    expect(t.find((p) => p.id === "state")!.spans[0]!.verdicts).toEqual(["unknown", "unknown"]);
  });
  it("does not mutate its input", () => {
    const copy = [...SPANS];
    buildTraceTree(SPANS, []);
    expect(SPANS).toEqual(copy);
  });
});

describe("gapText", () => {
  it("shows nothing for null and scales units otherwise", () => {
    expect(gapText(null)).toBe("");
    expect(gapText(250)).toBe("+250 ms");
    expect(gapText(10_000)).toBe("+10 s");
    expect(gapText(120_000)).toBe("+2 min");
    expect(gapText(5_400_000)).toBe("+1.5 h");
    expect(gapText(172_800_000)).toBe("+2.0 d");
  });
});

describe("pickSpan", () => {
  it("returns the named span, else the first, else null", () => {
    expect(pickSpan(SPANS, "state:x3")!.seq).toBe(3);
    expect(pickSpan(SPANS, "nope")!.seq).toBe(1);
    expect(pickSpan(SPANS, null)!.seq).toBe(1);
    expect(pickSpan([], null)).toBeNull();
  });
});

describe("clock domains", () => {
  it("never computes a gap between world time (2023) and wall-clock time (2026), nor a negative one", () => {
    const t = buildTraceTree(
      [span(1, "source_event", "2023-11-09T09:30:00Z"), span(2, "state", "2023-11-09T09:30:11Z"), span(3, "cliff_message", "2026-10-05T10:00:00Z"), span(4, "human_interaction", "2026-10-05T10:00:30Z"), span(5, "recomputed_action", "2026-10-05T09:00:00Z")],
      [],
    );
    expect(t.flatMap((p) => p.spans).map((s) => s.gapMs)).toEqual([null, 11_000, null, 30_000, null]);
  });
});

describe("aggregateVerdicts", () => {
  it("is null with no gate, else the worst verdict and counts", () => {
    expect(aggregateVerdicts([])).toBeNull();
    expect(aggregateVerdicts(["pass", "warn"])).toEqual({ worst: "warn", text: "1 warn · 1 pass" });
    expect(aggregateVerdicts(["pass", "pass", "fail"])).toEqual({ worst: "fail", text: "1 fail · 2 pass" });
  });
});
