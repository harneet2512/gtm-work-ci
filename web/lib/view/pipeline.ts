// The Play-Event strip (HAR-145): one stage per row of the progress document the core serves, in canonical order. The
// web adds no stage of its own and no timer. A finished stage that is not `evals` is "completed" and says nothing about
// quality; the check mark and "passed" belong to the eval verdict of the `evals` stage alone; a transport failure reads
// "backend unavailable" and is never an eval FAIL.
import type { PipelineProgress, PipelineStageRow } from "@/lib/api/types";

export type StageId = PipelineStageRow["stage"];
/** The contract's stage statuses, plus `unavailable`: the poll itself could not reach the backend. */
export type StageStatus = PipelineStageRow["status"] | "unavailable";
export type Overall = PipelineProgress["overall"];

export const STAGE_IDS: readonly StageId[] = ["ingest", "resolve", "graph", "state", "decide", "evals", "cliff"];

const LABEL: Record<StageId, string> = { ingest: "Ingest", resolve: "Resolve", graph: "Graph", state: "State", decide: "Decide", evals: "Evals", cliff: "Cliff" };

/** The four ways a stage can go wrong, kept apart: only `eval_fail` is a verdict about the work. */
export type ProblemKind = "backend_unavailable" | "trace_incomplete" | "eval_failed_to_run" | "eval_fail";

export const PROBLEM_LABEL: Record<ProblemKind, string> = {
  backend_unavailable: "Backend unavailable",
  trace_incomplete: "Trace incomplete",
  eval_failed_to_run: "Eval failed to run",
  eval_fail: "Eval FAIL",
};

export interface PipelineStage {
  id: StageId;
  label: string;
  status: StageStatus;
  /** What the stage reads on screen, beside the glyph. */
  word: string;
  /** A neutral glyph; "✓" only for a passed eval verdict. */
  mark: string;
  /** Why the stage did not complete: transport (outcome unknown, a retry may succeed), contract or internal. */
  failureKind: PipelineStageRow["failure_kind"];
  detail: string | null;
  duration: string | null;
  /** Which of the four problems this stage shows, or null when it shows none. */
  problem: ProblemKind | null;
}

const MARK: Record<StageStatus, string> = {
  waiting: "○",
  running: "◐",
  completed: "●",
  passed: "✓",
  warning: "!",
  failed: "✗",
  skipped: "—",
  unknown: "?",
  unavailable: "⊘",
};

const WORD: Record<StageStatus, string> = {
  waiting: "waiting",
  running: "running",
  completed: "completed",
  passed: "passed",
  warning: "warning",
  failed: "failed",
  skipped: "skipped",
  unknown: "not observed",
  unavailable: "backend unavailable",
};

const BACKEND_UNAVAILABLE = "backend unavailable";

function duration(ms: number | null): string | null {
  if (ms === null) return null;
  if (ms < 1000) return `${ms} ms`;
  if (ms < 60_000) return `${(ms / 1000).toFixed(1)} s`;
  return `${Math.floor(ms / 60_000)} min ${Math.round((ms % 60_000) / 1000)} s`;
}

const blank = (id: StageId, status: StageStatus): PipelineStage => ({ id, label: LABEL[id], status, word: WORD[status], mark: MARK[status], failureKind: null, detail: null, duration: null, problem: null });

/** A transport problem is never a verdict; the judging stage fails only as a judgment; an unrecorded stage is an incomplete trace. */
function problemOf(id: StageId, status: StageStatus, kind: PipelineStageRow["failure_kind"]): ProblemKind | null {
  if (status === "unavailable" || kind === "transport") return "backend_unavailable";
  if (id === "evals") {
    if (status === "failed" && kind === null) return "eval_fail";
    if (status === "unknown" || kind !== null) return "eval_failed_to_run";
    return null;
  }
  return status === "unknown" ? "trace_incomplete" : null;
}

export const waitingStages = (): PipelineStage[] => STAGE_IDS.map((id) => blank(id, "waiting"));
export const unavailableStages = (): PipelineStage[] => STAGE_IDS.map((id) => ({ ...blank(id, "unavailable"), detail: BACKEND_UNAVAILABLE, problem: "backend_unavailable" as const }));

function fromRow(r: PipelineStageRow): PipelineStage {
  // A transport failure (a timeout, an unreachable dependency) is not a verdict: whatever status it carries, it reads unavailable.
  const transport = r.failure_kind === "transport";
  const status: StageStatus = transport ? "unavailable" : r.status;
  return { ...blank(r.stage, status), failureKind: r.failure_kind, detail: r.detail, duration: duration(r.duration_ms), problem: problemOf(r.stage, status, r.failure_kind) };
}

/** The seven stages of a progress document; a stage the document omits is waiting, never invented as done. */
export function stagesFromProgress(p: PipelineProgress): PipelineStage[] {
  const byId = new Map(p.stages.map((s) => [s.stage, s]));
  return STAGE_IDS.map((id) => {
    const r = byId.get(id);
    return r ? fromRow(r) : blank(id, "waiting");
  });
}

/** `complete` and `failed` are final: the server never takes them back. */
export const isFinalOverall = (overall: Overall | null | undefined): boolean => overall === "complete" || overall === "failed";

export function overallLine(p: PipelineProgress): string {
  switch (p.overall) {
    case "not_started":
      return "Not started";
    case "running":
      return "Running";
    case "complete":
      return "Pipeline complete";
    case "failed": {
      const kind = p.stages.find((s) => s.failure_kind !== null)?.failure_kind ?? null;
      if (kind === "transport") return "Backend unavailable (transport)";
      return kind ? `Pipeline failed (${kind})` : "Pipeline failed";
    }
  }
}
