// The Play-Event pipeline strip (HAR-145): six stages, each derived from real backend artifacts — the
// advance result's own fields, the linked run's generation phase and steps, and observed outbox kinds.
// No timers: a stage lights only when its artifact exists, and "skipped" is honest (contract vocabulary
// for the run's own steps) for a non-material event's downstream stages.
import type { AdvanceResult, AgentRun } from "@/lib/api/types";

type RunStep = NonNullable<AgentRun["steps"]>[number];

/** `unavailable`: the backend could not be reached for this stage's read. It is neither a failure nor "still running". */
export type StageStatus = "waiting" | "running" | "passed" | "warning" | "failed" | "skipped" | "unknown" | "unavailable";

export type StageId = "ingest" | "resolve" | "graph" | "state" | "decide" | "cliff";

export interface PipelineStage {
  id: StageId;
  label: string;
  status: StageStatus;
  /** The artifact or reason the status rests on — rendered small. */
  detail: string | null;
}

const stage = (id: StageId, label: string, status: StageStatus, detail: string | null = null): PipelineStage => ({
  id,
  label,
  status,
  detail,
});

/** The strip before Play: everything waits on the held-out event. */
export function waitingPipeline(): PipelineStage[] {
  return [
    stage("ingest", "Ingest", "waiting"),
    stage("resolve", "Resolve", "waiting"),
    stage("graph", "Graph", "waiting"),
    stage("state", "State", "waiting"),
    stage("decide", "Decide", "waiting"),
    stage("cliff", "Cliff", "waiting"),
  ];
}

const runPhaseStatus = (phase: string | undefined): StageStatus => {
  switch (phase) {
    case "published":
      return "passed";
    case "generating":
    case "evaluating":
    case "queued":
      return "running";
    case "paused":
      return "warning";
    case "failed":
      return "failed";
    default:
      return "unknown";
  }
};

const stepDetail = (steps: RunStep[] | undefined): string | null => {
  if (!steps || steps.length === 0) return null;
  const running = steps.find((s) => s.status === "running");
  if (running) return `step ${running.seq}: ${running.step} running`;
  const failed = steps.find((s) => s.status === "failed");
  if (failed) return `step ${failed.seq}: ${failed.step} failed`;
  const done = steps.filter((s) => s.status === "succeeded" || s.status === "recorded").length;
  return `${done}/${steps.length} steps done`;
};

/**
 * Stages after an advance. Ingest/resolve/graph/state come from the advance result's own artifacts;
 * decide follows the linked run's generation phase and steps; cliff follows the episode's posted
 * surface-message kinds (`bi`/`chooser`/`judgment` — a ref with ts means the message truly posted,
 * per HAR-136). A non-material event skips the downstream stages honestly rather than pretending
 * they ran.
 */
export function pipelineFromAdvance(
  res: AdvanceResult,
  run: AgentRun | null,
  cliffKinds: readonly string[] | null,
  unavailable = false,
): PipelineStage[] {
  const stages: PipelineStage[] = [];
  stages.push(stage("ingest", "Ingest", "passed", res.released.source_system));

  if (!res.material) {
    const why = res.no_action_reason ?? "no material change";
    stages.push(stage("resolve", "Resolve", "passed", res.account_change_id ? "linked to the account" : null));
    for (const [id, label] of [
      ["graph", "Graph"],
      ["state", "State"],
      ["decide", "Decide"],
      ["cliff", "Cliff"],
    ] as const) {
      stages.push(stage(id, label, "skipped", why));
    }
    return stages;
  }

  stages.push(
    stage("resolve", "Resolve", res.account_change_id ? "passed" : "warning", res.account_change_id ? "account change recorded" : "no account change"),
    stage("graph", "Graph", res.graph_diff_id != null ? "passed" : "warning", res.graph_diff_id != null ? `graph diff ${res.graph_diff_id}` : "no graph diff"),
    stage("state", "State", res.state_version != null ? "passed" : "warning", res.state_version != null ? `state v${res.state_version}` : "no new state"),
  );

  if (!res.decision_episode_id) {
    stages.push(stage("decide", "Decide", "skipped", res.no_action_reason ?? "no decision was owed"), stage("cliff", "Cliff", "skipped"));
    return stages;
  }
  if (unavailable) {
    stages.push(stage("decide", "Decide", "unavailable", "backend unavailable"), stage("cliff", "Cliff", "unavailable", "backend unavailable"));
    return stages;
  }
  if (!run) {
    stages.push(stage("decide", "Decide", "running", "episode opened"), stage("cliff", "Cliff", "waiting"));
    return stages;
  }
  const decide = runPhaseStatus(run.generation?.phase);
  stages.push(stage("decide", "Decide", decide, stepDetail(run.steps) ?? run.generation?.phase ?? null));

  if (decide === "failed") {
    stages.push(stage("cliff", "Cliff", "skipped", "the run failed before Cliff"));
    return stages;
  }
  if (cliffKinds === null) {
    stages.push(stage("cliff", "Cliff", decide === "passed" ? "unknown" : "waiting", decide === "passed" ? "surface refs unreadable" : null));
    return stages;
  }
  const posted = cliffKinds.length;
  stages.push(stage("cliff", "Cliff", posted > 0 ? "passed" : "waiting", posted > 0 ? `${cliffKinds.join(" + ")} posted` : null));
  return stages;
}
