// The live Play progress of the fixture core (HAR-145): what GET /replay/manifests/{id}/progress answers while a Play is
// open and after it. The clock is real: each stage commits at its own offset from the moment Play started, so the web
// app's poll sees stages go waiting -> running -> completed one read at a time (no timers in the page, none faked in the
// tests). A finished stage other than evals is "completed"; the evals stage reports the real verdicts of the played run's
// results ("warning" when any result is not a pass); a non-material event skips everything after resolve.
import { RUNS } from "./fixture-runs.mjs";

export const STAGES = ["ingest", "resolve", "graph", "state", "decide", "evals", "cliff"];

/** Offsets (ms after Play started) at which each stage of a material event completes; cliff completes after Play returns. */
const offsets = (playMs) => ({ ingest: 300, resolve: 600, graph: 900, state: 1200, decide: 1700, evals: 2200, cliff: playMs + 500 });
const SKIP_AT = 700;

const iso = (ms) => new Date(ms).toISOString();

/** The played event's pipeline artifacts, by the ids the replay fixture releases. */
function runOf(event) {
  const id = event.release.decision_episode_id;
  return [...RUNS.values()].find((r) => r.episodeId === id) ?? null;
}

function evalsOutcome(entry) {
  const verdicts = entry.strategies.eval_bundles.flatMap((b) => b.items.map((i) => i.result).filter(Boolean));
  const bad = verdicts.filter((r) => r.verdict !== "pass").length;
  return { ids: verdicts.map((r) => r.id), status: verdicts.length === 0 ? "unknown" : bad === 0 ? "passed" : "warning", detail: `${verdicts.length} results: ${verdicts.length - bad} pass, ${bad} not pass` };
}

const waiting = (stage) => ({ stage, status: "waiting", attempt: 0, started_at: null, ended_at: null, duration_ms: null, refs: {}, eval_result_ids: [], failure_kind: null, detail: null, seq: null, updated_at: null });

function refsFor(stage, event, entry) {
  const r = event.release;
  const map = {
    ingest: { source_event_id: event.event_id },
    resolve: { account_change_id: r.account_change_id },
    graph: { graph_diff_id: r.graph_diff_id },
    state: { state_version: r.state_version },
    decide: { run_id: entry?.run.id, strategy_set_id: entry?.strategies.strategy_set.id, decision_episode_id: r.decision_episode_id },
    evals: { eval_bundle_ids: (entry?.strategies.eval_bundles ?? []).map((b) => b.id) },
    cliff: { decision_episode_id: r.decision_episode_id },
  };
  return Object.fromEntries(Object.entries(map[stage]).filter(([, v]) => v != null && !(Array.isArray(v) && v.length === 0)));
}

export function progressDocument({ play, world, playMs, now = Date.now() }) {
  const base = { scope: "manifest", manifest_id: world.manifest_id, run_id: null, account_id: world.account_id, generated_at: iso(now) };
  if (play.startedAt === null) return { ...base, overall: "not_started", stages: STAGES.map(waiting) };

  const event = play.event;
  const material = event.release.material;
  const entry = material ? runOf(event) : null;
  const evals = entry ? evalsOutcome(entry) : { ids: [], status: "unknown", detail: "No eval results were recorded" };
  const at = offsets(playMs);
  const elapsed = now - play.startedAt;
  let seq = 0;
  const stages = STAGES.map((stage, i) => {
    const skipped = !material && i >= 2;
    const doneAt = skipped ? SKIP_AT : at[stage];
    const startAt = skipped ? SKIP_AT : i === 0 ? 0 : (STAGES.slice(0, i).map((s) => (!material && STAGES.indexOf(s) >= 2 ? SKIP_AT : at[s])).at(-1) ?? 0);
    if (elapsed < startAt) return waiting(stage);
    seq += 1;
    const started = iso(play.startedAt + startAt);
    if (elapsed < doneAt) return { ...waiting(stage), status: "running", attempt: 1, started_at: started, seq, updated_at: started, refs: {} };
    const status = skipped ? "skipped" : stage === "evals" ? evals.status : "completed";
    return {
      stage,
      status,
      attempt: 1,
      started_at: started,
      ended_at: iso(play.startedAt + doneAt),
      duration_ms: doneAt - startAt,
      refs: skipped ? {} : refsFor(stage, event, entry),
      eval_result_ids: stage === "evals" && !skipped ? evals.ids : [],
      failure_kind: null,
      detail: skipped ? "The event did not change the account materially" : stage === "evals" ? evals.detail : null,
      seq,
      updated_at: iso(play.startedAt + doneAt),
    };
  });
  const finished = stages.every((s) => s.status !== "waiting" && s.status !== "running");
  return { ...base, overall: finished ? "complete" : "running", stages };
}
