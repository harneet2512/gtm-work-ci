// ---- Episode replay (HAR-129 B/C) -------------------------------------------------
// The replay surface keeps a released cursor and a max-released mark (once released, an
// episode keeps its bookkeeping even after a reset). Views are derived from
// replay.events.json: prior episodes get their bookkeeping back, the next event is
// withheld, and positions beyond it are not even named.
import { readFileSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const here = path.dirname(fileURLToPath(import.meta.url));
const replayWorld = JSON.parse(readFileSync(path.resolve(here, "../fixtures/core/replay.events.json"), "utf8"));

import { progressDocument } from "./fixture-progress.mjs";

const REPLAY = { released: 2, maxReleased: 2 };
/** The Play in flight (or last finished): when it started and which event it released. Progress reads follow its real clock. */
const PLAY = { startedAt: null, event: null };
/** How long the Play request stays open, like the real pipeline run it stands in for (ms). */
const PLAY_MS = Number(process.env.FIXTURE_PLAY_MS ?? 2600);
const replayN = () => replayWorld.events.length;

/**
 * The hidden handoff to the next account (the demo's second Play): while core restarts on the next case every replay read
 * fails, afterwards the same fixture world is served under the next manifest id at Event N-1.
 */
const HANDOFF = { switching: false, served: null };
export const demoManifestId = () => replayWorld.manifest_id;
export const beginHandoff = () => {
  HANDOFF.switching = true;
};
export const finishHandoff = (manifestId) => {
  HANDOFF.served = manifestId;
  HANDOFF.switching = false;
  REPLAY.released = 2;
  REPLAY.maxReleased = 2;
  PLAY.startedAt = null;
  PLAY.event = null;
};
export const resetHandoff = () => {
  HANDOFF.switching = false;
  HANDOFF.served = null;
};
export const releaseEverything = () => {
  REPLAY.released = replayN();
  REPLAY.maxReleased = replayN();
  // the account was just played to its end: its progress reads as a finished pipeline, as the real one does
  PLAY.startedAt = Date.now() - PLAY_MS * 4;
  PLAY.event = replayWorld.events[replayN() - 1];
};

const publicEvent = (e) => ({
  position: e.position,
  event_id: e.event_id,
  occurred_at: e.occurred_at,
  source_system: e.source_system,
  provenance_origin: e.provenance_origin,
  provenance: e.provenance,
});

const releasedEvent = (e) => ({ ...publicEvent(e), ...e.release, released: true, held_out: e.held_out });

const withheldEvent = (e) => ({
  ...publicEvent(e),
  released: false,
  held_out: e.held_out,
  material: null,
  account_change_id: null,
  decision_episode_id: null,
  state_version: null,
  graph_diff_id: null,
  no_action_reason: null,
  coalesced: null,
});

const replayWindow = (b, k) => (k === 0 ? "none" : k <= b.historical_end ? "historical" : "live");

function replayView(k) {
  const world = replayWorld;
  const N = replayN();
  const bound = k === 0 ? null : world.events[k - 1];
  return {
    manifest_id: world.manifest_id,
    account_id: world.account_id,
    opportunity_id: world.opportunity_id,
    episode: k,
    total: N,
    boundary: world.boundary,
    window: replayWindow(world.boundary, k),
    state: bound?.state ?? null,
    knowledge: { as_of: bound ? `${bound.occurred_at.slice(0, -1)}.000001Z` : null, items: k === 0 ? world.knowledge_at_zero : bound.knowledge },
    prior_episodes: world.events.slice(0, k).map(releasedEvent),
    next_event: k < N ? withheldEvent(world.events[k]) : null,
    can_previous: k > 0,
    can_play_next: REPLAY.released < N,
    computed_at: "2026-10-04T06:00:00Z",
  };
}

function advanceResult() {
  const e = replayWorld.events[REPLAY.released - 1];
  return {
    manifest_id: replayWorld.manifest_id,
    account_id: replayWorld.account_id,
    episode: e.position,
    total: replayN(),
    released: releasedEvent(e),
    material: e.release.material,
    no_action_reason: e.release.no_action_reason,
    state_version: e.release.state_version,
    state_digest: e.state.digest,
    graph_diff_id: e.release.graph_diff_id,
    account_change_id: e.release.account_change_id,
    decision_episode_id: e.release.decision_episode_id,
    coalesced: e.release.coalesced,
    advanced_at: "2026-10-04T06:00:01Z",
  };
}

export const replayRoute = (pathname) => /^\/replay\/manifests\/([^/]+)\/(episodes|episodes\/next|reset|invisibility|progress)$/.exec(pathname);

const ok = (status, body) => ({ status, body });
const err = (status, code, message) => ({ status, error: { code, message } });

/** The reply to one replay request: { status, body } or { status, error }. Mutates the cursor on POST. */
function replayReply(method, manifestId, which, url, body) {
  if (HANDOFF.switching) return err(503, "core_restarting", "core is restarting on the next account");
  if (manifestId !== (HANDOFF.served ?? replayWorld.manifest_id)) return err(404, "manifest_not_found", "no such demo manifest");
  if (method === "GET" && which === "episodes") {
    const at = url.searchParams.get("at");
    if (at === null) return ok(200, replayView(REPLAY.released));
    const k = Number(at);
    if (!/^\d+$/.test(at) || !Number.isSafeInteger(k)) return err(400, "bad_request", "at is not a position");
    if (k > REPLAY.released) return err(422, "invalid_episode", `${at} is not a released episode (0..${REPLAY.released})`);
    return ok(200, replayView(k));
  }
  if (method === "GET" && which === "invisibility") {
    // HAR-129 §B: while events remain withheld the report covers the next withheld event — inspected
    // stores listed, leaks empty (the fixture never leaks). Once everything is released the withheld
    // event no longer exists and the report answers `released` for the last one.
    const withheld = REPLAY.released < replayN() ? replayWorld.events[REPLAY.released] : null;
    return ok(
      200,
      withheld
        ? { manifest_id: replayWorld.manifest_id, held_out_event_id: withheld.event_id, status: "withheld", checked: ["postgres", "neo4j"], leaks: [] }
        : { manifest_id: replayWorld.manifest_id, held_out_event_id: replayWorld.events[replayN() - 1].event_id, status: "released", checked: [], leaks: [] },
    );
  }
  if (method === "GET" && which === "progress") return ok(200, progressDocument({ play: PLAY, world: replayWorld, playMs: PLAY_MS }));
  if (method === "POST" && which === "episodes/next") {
    if (REPLAY.released >= replayN()) return err(409, "replay_complete", "every event of the manifest is released");
    REPLAY.released += 1;
    REPLAY.maxReleased = Math.max(REPLAY.maxReleased, REPLAY.released);
    PLAY.startedAt = Date.now();
    PLAY.event = replayWorld.events[REPLAY.released - 1];
    // The request stays open for the pipeline's duration; the web app polls progress meanwhile.
    return { ...ok(200, advanceResult()), delayMs: PLAY_MS };
  }
  if (method === "POST" && which === "reset") {
    const episode = body?.episode;
    if (!Number.isInteger(episode) || episode < 0 || episode > REPLAY.maxReleased) {
      return err(422, "invalid_episode", `the target ${JSON.stringify(episode)} was never released or is out of range`);
    }
    REPLAY.released = episode;
    PLAY.startedAt = null;
    PLAY.event = null;
    return ok(200, {
      manifest_id: replayWorld.manifest_id,
      account_id: replayWorld.account_id,
      episode,
      released_events: replayWorld.events.slice(0, episode).map((e) => e.event_id),
      reset_at: "2026-10-04T06:00:02Z",
      digest: "0".repeat(63) + "0",
    });
  }
  return err(405, "method_not_allowed", "unsupported method");
}

/** Answers one replay request; false when the path is not a replay route. */
export function handleReplay(req, url, body, { send, fail }) {
  const m = replayRoute(url.pathname);
  if (!m) return false;
  const reply = replayReply(req.method, m[1], m[2], url, body);
  if (reply.error) fail(reply.status, reply.error.code, reply.error.message);
  else {
    const body = HANDOFF.served && reply.body?.manifest_id ? { ...reply.body, manifest_id: HANDOFF.served } : reply.body;
    if (reply.delayMs) setTimeout(() => send(reply.status, body), reply.delayMs);
    else send(reply.status, body);
  }
  return true;
}
