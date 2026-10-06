// The Play strip view model (HAR-145): stages come from the progress document the core serves, one to one. A finished
// stage that is not the eval stage is "completed" (never PASS); only the evals stage may read passed or warning; a
// transport failure reads "backend unavailable" and is never an eval FAIL.
import { describe, expect, it } from "vitest";
import type { PipelineProgress, PipelineStageRow } from "@/lib/api/types";
import { isFinalOverall, overallLine, stagesFromProgress, STAGE_IDS, unavailableStages, waitingStages } from "@/lib/view/pipeline";

const MANIFEST = "0d3a0000-0000-4000-8000-000000000501";
const ACCOUNT = "0a0c0000-0000-4000-8000-000000000001";

const row = (stage: PipelineStageRow["stage"], over: Partial<PipelineStageRow> = {}): PipelineStageRow => ({
  stage,
  status: "waiting",
  attempt: 0,
  started_at: null,
  ended_at: null,
  duration_ms: null,
  refs: {},
  eval_result_ids: [],
  failure_kind: null,
  detail: null,
  seq: null,
  updated_at: null,
  ...over,
});

const progress = (over: Partial<PipelineProgress> = {}, rows: Partial<Record<PipelineStageRow["stage"], Partial<PipelineStageRow>>> = {}): PipelineProgress => ({
  scope: "manifest",
  manifest_id: MANIFEST,
  run_id: null,
  account_id: ACCOUNT,
  overall: "running",
  stages: STAGE_IDS.map((id) => row(id, rows[id])),
  generated_at: "2026-10-05T10:00:00Z",
  ...over,
});

describe("waiting and unavailable strips", () => {
  it("lists the seven stages in canonical order, all waiting", () => {
    const s = waitingStages();
    expect(s.map((x) => x.id)).toEqual(["ingest", "resolve", "graph", "state", "decide", "evals", "cliff"]);
    expect(s.every((x) => x.status === "waiting" && x.word === "waiting")).toBe(true);
  });

  it("reads an unreachable backend as unavailable on every stage, not as a failure", () => {
    const s = unavailableStages();
    expect(s).toHaveLength(7);
    expect(s.every((x) => x.status === "unavailable" && x.word === "backend unavailable")).toBe(true);
    expect(s.some((x) => x.mark === "✗")).toBe(false);
  });
});

describe("stagesFromProgress", () => {
  it("shows a finished non-eval stage as completed, with a neutral mark, never passed", () => {
    const s = stagesFromProgress(progress({}, { ingest: { status: "completed", ended_at: "2026-10-05T10:00:01Z", duration_ms: 1200 } }));
    const ingest = s.find((x) => x.id === "ingest")!;
    expect(ingest.status).toBe("completed");
    expect(ingest.word).toBe("completed");
    expect(ingest.mark).not.toBe("✓");
    expect(ingest.duration).toBe("1.2 s");
  });

  it("reserves the check mark for the evals stage when it passed", () => {
    const s = stagesFromProgress(progress({}, { evals: { status: "passed", ended_at: "2026-10-05T10:00:01Z" } }));
    const evals = s.find((x) => x.id === "evals")!;
    expect(evals.mark).toBe("✓");
    expect(evals.word).toBe("passed");
    expect(s.filter((x) => x.mark === "✓")).toHaveLength(1);
  });

  it("reads an eval warning as a warning and unknown as not observed", () => {
    const s = stagesFromProgress(progress({}, { evals: { status: "warning" }, cliff: { status: "unknown", detail: "outcome cannot be established" } }));
    expect(s.find((x) => x.id === "evals")!.word).toBe("warning");
    const cliff = s.find((x) => x.id === "cliff")!;
    expect(cliff.word).toBe("not observed");
    expect(cliff.detail).toBe("outcome cannot be established");
  });

  it("reads a transport failure as backend unavailable and keeps its failure kind", () => {
    const s = stagesFromProgress(progress({ overall: "failed" }, { decide: { status: "failed", failure_kind: "transport", detail: "provider timeout" } }));
    const decide = s.find((x) => x.id === "decide")!;
    expect(decide.word).toBe("backend unavailable");
    expect(decide.failureKind).toBe("transport");
    expect(decide.mark).not.toBe("✗");
    expect(decide.detail).toBe("provider timeout");
  });

  it("reads an unknown stage with a transport kind (no heartbeat) as backend unavailable too", () => {
    const s = stagesFromProgress(progress({}, { state: { status: "unknown", failure_kind: "transport", detail: "no heartbeat" } }));
    expect(s.find((x) => x.id === "state")!.word).toBe("backend unavailable");
  });

  it("shows an internal or contract failure as failed with its kind", () => {
    const s = stagesFromProgress(progress({ overall: "failed" }, { graph: { status: "failed", failure_kind: "internal" } }));
    const graph = s.find((x) => x.id === "graph")!;
    expect(graph.word).toBe("failed");
    expect(graph.failureKind).toBe("internal");
  });

  it("shows a judged eval failure as failed with no failure kind", () => {
    const s = stagesFromProgress(progress({}, { evals: { status: "failed", eval_result_ids: ["0e000000-0000-4000-8000-000000000001"] } }));
    const evals = s.find((x) => x.id === "evals")!;
    expect(evals.word).toBe("failed");
    expect(evals.failureKind).toBeNull();
  });

  it("shows a skipped stage and a running stage honestly", () => {
    const s = stagesFromProgress(progress({}, { cliff: { status: "skipped", detail: "no material change" }, decide: { status: "running", started_at: "2026-10-05T10:00:00Z" } }));
    expect(s.find((x) => x.id === "cliff")!.word).toBe("skipped");
    expect(s.find((x) => x.id === "decide")!.word).toBe("running");
  });

  it("falls back to waiting for a stage the document omits", () => {
    const partial = progress();
    partial.stages = partial.stages.slice(0, 3);
    const s = stagesFromProgress(partial);
    expect(s).toHaveLength(7);
    expect(s[6]!.status).toBe("waiting");
  });

  it("formats durations under a second, over a minute and when unrecorded", () => {
    const s = stagesFromProgress(progress({}, { ingest: { status: "completed", duration_ms: 480 }, resolve: { status: "completed", duration_ms: 75_000 }, graph: { status: "completed", duration_ms: null } }));
    expect(s[0]!.duration).toBe("480 ms");
    expect(s[1]!.duration).toBe("1 min 15 s");
    expect(s[2]!.duration).toBeNull();
  });
});

describe("overall", () => {
  it("is final only when complete or failed", () => {
    expect(isFinalOverall("complete")).toBe(true);
    expect(isFinalOverall("failed")).toBe(true);
    expect(isFinalOverall("running")).toBe(false);
    expect(isFinalOverall("not_started")).toBe(false);
    expect(isFinalOverall(null)).toBe(false);
  });

  it("words each state without claiming success it cannot prove", () => {
    expect(overallLine(progress({ overall: "not_started" }))).toBe("Not started");
    expect(overallLine(progress({ overall: "running" }))).toBe("Running");
    expect(overallLine(progress({ overall: "complete" }))).toBe("Pipeline complete");
  });

  it("names the failure kind of a failed pipeline, and calls a transport failure backend unavailable", () => {
    expect(overallLine(progress({ overall: "failed" }, { graph: { status: "failed", failure_kind: "internal" } }))).toBe("Pipeline failed (internal)");
    expect(overallLine(progress({ overall: "failed" }, { decide: { status: "failed", failure_kind: "transport" } }))).toBe("Backend unavailable (transport)");
    expect(overallLine(progress({ overall: "failed" }))).toBe("Pipeline failed");
  });
});
