// The fixture core's decision runs: each run document with its trace, strategy set + eval bundles, the
// human's strategy decision and the judgment inference of its episode. Acme's run ...0601 is the contract
// example world; MedTech's run ...ad60 is the real CRMArena-Pro demo case. Any other run answers 404, so
// the pages' empty states are exercised honestly.
import { readFileSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const here = path.dirname(fileURLToPath(import.meta.url));
const read = (dir, file) => JSON.parse(readFileSync(path.resolve(here, dir, file), "utf8"));
const fx = (file) => read("../fixtures/core", file);
const ex = (name) => read("../../../contracts/examples", `${name}.example.json`);

const acmeRun = ex("agent_run");
const medtechRun = fx("medtech.agent-run.json");

/** A second Acme run still evaluating: it has a document but nothing else yet. */
export const PENDING_RUN = "0a120000-0000-4000-8000-000000000002";
const pending = () => {
  const second = { ...acmeRun, id: PENDING_RUN, status: "awaiting_human" };
  if (acmeRun.generation) second.generation = { ...acmeRun.generation, phase: "evaluating" };
  return second;
};

/**
 * The run the replay manifest's next advance opens: episode 0de5…0202 is released by Play event 3, and
 * this published run is what /control's strip then finds — the demo walks the real chain end to end.
 */
const REPLAY_RUN_ID = "0a120000-0000-4000-8000-0000000000e5";
const REPLAY_EPISODE = "0de50000-0000-4000-8000-000000000202";

/**
 * The BusinessIntelligenceUpdate Play event 3 writes (its account_change_id is episode 3's 0a1c…0302) —
 * Message 1's surface subject, per HAR-136. Served as the account's latest BI once the demo world has it.
 */
export const REPLAY_BI = { ...ex("business_intelligence_update"), account_change_id: "0a1c0000-0000-4000-8000-000000000302" };
const replayRun = () => ({
  ...acmeRun,
  id: REPLAY_RUN_ID,
  generation: { ...acmeRun.generation, decision_episode_id: REPLAY_EPISODE, phase: "published" },
  created_at: "2026-10-04T06:00:30Z",
});

export const RUNS = new Map([
  [
    acmeRun.id,
    {
      run: acmeRun,
      trace: fx("acme.run-trace.json"),
      strategies: fx("acme.run-strategies.json"),
      decision: ex("human_strategy_decision"),
      episodeId: "0e9e0000-0000-4000-8000-000000000a01",
      inference: ex("judgment_inference"),
    },
  ],
  [
    medtechRun.id,
    {
      run: medtechRun,
      trace: fx("medtech.run-trace.json"),
      strategies: fx("medtech.run-strategies.json"),
      decision: fx("medtech.strategy-decision.json"),
      episodeId: medtechRun.generation.decision_episode_id,
      inference: fx("medtech.judgment-inference.json"),
    },
  ],
  [
    REPLAY_RUN_ID,
    {
      run: replayRun(),
      trace: fx("acme.run-trace.json"),
      strategies: fx("acme.run-strategies.json"),
      decision: ex("human_strategy_decision"),
      episodeId: REPLAY_EPISODE,
      inference: ex("judgment_inference"),
    },
  ],
]);

/** GET /runs: newest first — the replay run, the MedTech demo run, the pending Acme run, then Acme's decided run. */
export const runList = () => ({ items: [replayRun(), medtechRun, pending(), acmeRun] });

/** GET /runs/{id}: a known run, the pending one, or null. */
export function runDocument(id) {
  if (RUNS.has(id)) return RUNS.get(id).run;
  return id === PENDING_RUN ? pending() : null;
}

/** GET /runs/{id}/{trace|strategies|strategy-decision}; null when the run has none. */
export function runPart(id, part) {
  const r = RUNS.get(id);
  if (!r) return null;
  return part === "trace" ? r.trace : part === "strategies" ? r.strategies : r.decision;
}

export const inferenceForEpisode = (episodeId) => [...RUNS.values()].find((r) => r.episodeId === episodeId)?.inference ?? null;

/**
 * GET /surface-messages/{subject}/slack/{kind} (HAR-136): the subjects that have Cliff messages on the
 * wire. Message 1 keys on the BusinessIntelligenceUpdate, chooser/judgment on the DecisionEpisode.
 * Anything else answers 404 — the page shows "not observable"/"nothing sent yet" off real answers.
 */
const SURFACE = new Map([
  // Message 1's subject is the BusinessIntelligenceUpdate; chooser/judgment key on the DecisionEpisode.
  [REPLAY_BI.id, { bi: "1730000000.000100" }],
  [medtechRun.generation.decision_episode_id, { chooser: "1730000000.000200", judgment: null }],
  ["0de50000-0000-4000-8000-000000000201", { chooser: "1730000000.000300", judgment: null }],
  [REPLAY_EPISODE, { chooser: "1730000000.000400", judgment: null }],
]);

export function surfaceMessage(subjectId, surface, kind) {
  if (surface !== "slack" || (kind !== "bi" && kind !== "chooser" && kind !== "judgment")) return null;
  const entry = SURFACE.get(subjectId);
  if (entry === undefined || !(kind in entry)) return null;
  return {
    subject_id: subjectId,
    surface,
    kind,
    channel: "#ghost-demo",
    ts: entry[kind],
    reserved_at: "2026-10-04T05:59:00Z",
  };
}
