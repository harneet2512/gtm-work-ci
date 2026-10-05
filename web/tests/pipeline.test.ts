// The Play-Event pipeline view model (HAR-145): every stage reads a real artifact — the advance
// result's fields, the linked run's generation phase, posted surface refs — or says waiting/skipped.
import { describe, expect, it } from "vitest";
import type { AdvanceResult, AgentRun } from "@/lib/api/types";
import { pipelineFromAdvance, waitingPipeline } from "@/lib/view/pipeline";

const advance = (over: Partial<AdvanceResult>): AdvanceResult => ({
  manifest_id: "0d3a0000-0000-4000-8000-000000000501",
  account_id: "0a0c0000-0000-4000-8000-000000000001",
  episode: 3,
  total: 4,
  released: {
    position: 3,
    event_id: "0e7e0000-0000-4000-8000-000000000013",
    occurred_at: "2026-10-01T09:00:00Z",
    source_system: "email",
    provenance_origin: "dataset",
    provenance: "crmarena-pro:b2b",
    released: true,
    held_out: true,
    material: true,
    account_change_id: "0a1c0000-0000-4000-8000-000000000302",
    decision_episode_id: "0de50000-0000-4000-8000-000000000202",
    state_version: 8,
    graph_diff_id: 302,
    no_action_reason: null,
    coalesced: false,
  },
  material: true,
  no_action_reason: null,
  state_version: 8,
  state_digest: "sha256:x",
  graph_diff_id: 302,
  account_change_id: "0a1c0000-0000-4000-8000-000000000302",
  decision_episode_id: "0de50000-0000-4000-8000-000000000202",
  coalesced: false,
  advanced_at: "2026-10-04T06:00:01Z",
  ...over,
});

const run = (phase: string, steps: { seq: number; step: string; status: string }[] = []): AgentRun =>
  ({ id: "0a120000-0000-4000-8000-0000000000e5", account_id: "0a0c0000-0000-4000-8000-000000000001", generation: { phase }, steps }) as AgentRun;

const byId = (stages: ReturnType<typeof pipelineFromAdvance>, id: string) => stages.find((s) => s.id === id);

describe("waitingPipeline", () => {
  it("has all six stages waiting in order", () => {
    const stages = waitingPipeline();
    expect(stages.map((s) => s.id)).toEqual(["ingest", "resolve", "graph", "state", "decide", "cliff"]);
    expect(stages.every((s) => s.status === "waiting")).toBe(true);
  });
});

describe("pipelineFromAdvance", () => {
  it("marks ingest passed and skips downstream stages for a non-material event", () => {
    const res = advance({ material: false, account_change_id: null, decision_episode_id: null, state_version: 7, graph_diff_id: null, no_action_reason: "deduped into the last fold" });
    const stages = pipelineFromAdvance(res, null, []);
    expect(byId(stages, "ingest")!.status).toBe("passed");
    expect(byId(stages, "resolve")!.status).toBe("passed");
    for (const id of ["graph", "state", "decide", "cliff"]) {
      expect(byId(stages, id)!.status).toBe("skipped");
      expect(byId(stages, id)!.detail).toContain("deduped");
    }
  });

  it("lights resolve/graph/state off the advance result's own artifacts", () => {
    const stages = pipelineFromAdvance(advance({}), null, []);
    expect(byId(stages, "resolve")!.detail).toBe("account change recorded");
    expect(byId(stages, "graph")!.detail).toContain("graph diff");
    expect(byId(stages, "state")!.detail).toBe("state v8");
  });

  it("warns instead of passing when a material event produced no graph diff", () => {
    const stages = pipelineFromAdvance(advance({ graph_diff_id: null }), null, []);
    expect(byId(stages, "graph")!.status).toBe("warning");
  });

  it("shows decide running on an opened episode before the run exists, cliff waiting", () => {
    const stages = pipelineFromAdvance(advance({}), null, []);
    expect(byId(stages, "decide")!.status).toBe("running");
    expect(byId(stages, "cliff")!.status).toBe("waiting");
  });

  it("follows the run's generation phase and step detail", () => {
    const r = run("evaluating", [
      { seq: 1, step: "retrieve", status: "succeeded" },
      { seq: 2, step: "judge", status: "running" },
    ]);
    const stages = pipelineFromAdvance(advance({}), r, []);
    expect(byId(stages, "decide")!.status).toBe("running");
    expect(byId(stages, "decide")!.detail).toContain("judge running");
  });

  it("marks cliff passed only on a posted surface ref, unknown when the read failed", () => {
    const published = run("published");
    const posted = pipelineFromAdvance(advance({}), published, ["bi", "chooser"]);
    expect(byId(posted, "cliff")!.status).toBe("passed");
    expect(byId(posted, "cliff")!.detail).toBe("bi + chooser posted");

    const unreadable = pipelineFromAdvance(advance({}), published, null);
    expect(byId(unreadable, "cliff")!.status).toBe("unknown");
  });

  it("reads 'backend unavailable' - not running or a failure - when the progress read could not reach the core", () => {
    const stages = pipelineFromAdvance(advance({}), null, null, true);
    expect(byId(stages, "decide")!.status).toBe("unavailable");
    expect(byId(stages, "decide")!.detail).toBe("backend unavailable");
    expect(byId(stages, "cliff")!.status).toBe("unavailable");
    // What the advance result itself proved stays as it was.
    expect(byId(stages, "ingest")!.status).toBe("passed");
    expect(byId(stages, "graph")!.status).toBe("passed");
  });

  it("ignores the unavailable flag for a non-material event that owes no downstream stage", () => {
    const stages = pipelineFromAdvance(advance({ material: false, decision_episode_id: null }), null, [], true);
    expect(byId(stages, "decide")!.status).toBe("skipped");
  });

  it("keeps cliff waiting while no message has posted", () => {
    const stages = pipelineFromAdvance(advance({}), run("published"), []);
    expect(byId(stages, "cliff")!.status).toBe("waiting");
  });

  it("skips cliff honestly when the run failed", () => {
    const stages = pipelineFromAdvance(advance({}), run("failed"), null);
    expect(byId(stages, "decide")!.status).toBe("failed");
    expect(byId(stages, "cliff")!.status).toBe("skipped");
  });

  it("skips decide+cliff when the material event owed no decision", () => {
    const stages = pipelineFromAdvance(advance({ decision_episode_id: null }), null, []);
    expect(byId(stages, "decide")!.status).toBe("skipped");
    expect(byId(stages, "cliff")!.status).toBe("skipped");
  });
});
