// A stand-in for the Go core that replays recorded responses (tests/fixtures/core and the contract
// examples). CI never needs a live core: the web server under test talks to this over HTTP exactly as it
// would to the real one, including the bearer token, the error envelope and world-time reads (ADR-0019).
import { readFileSync } from "node:fs";
import { createServer } from "node:http";
import path from "node:path";
import { fileURLToPath } from "node:url";

const here = path.dirname(fileURLToPath(import.meta.url));
const fixtures = path.resolve(here, "../fixtures/core");
const examples = path.resolve(here, "../../../contracts/examples");
const read = (file) => JSON.parse(readFileSync(file, "utf8"));

const ACCOUNT = "0a0c0000-0000-4000-8000-000000000001";
const EVENT = "05e00000-0000-4000-8000-000000000101";
const RUN = "0f0a0000-0000-4000-8000-000000000601"; // agent_run.example.json's id — the run chain page
const EPISODE = "0e9e0000-0000-4000-8000-000000000a01"; // the run's decision episode
const KNOWLEDGE_STATUSES = new Set(["candidate", "provisional", "supported", "confirmed", "disputed", "stale"]);
// The Play event's instant and the as_of of the first state version (the world starts here).
const EVENT_AT = "2026-09-29T15:42:00Z";
const FIRST_STATE_AT = "2026-09-20T10:00:00Z";
const TOKEN = process.env.FIXTURE_CORE_TOKEN ?? "e2e-token";
const PORT = Number(process.env.FIXTURE_CORE_PORT ?? 18080);

const accounts = read(path.join(fixtures, "accounts.json"));
const graphAfter = read(path.join(fixtures, "acme.graph.json"));
const graphBefore = read(path.join(fixtures, "acme.graph-before.json"));
const diff = read(path.join(fixtures, "acme.graph-diff.json"));
const timeline = read(path.join(fixtures, "acme.timeline.json"));
const stateAfter = read(path.join(examples, "account_state.example.json"));
const stateBefore = read(path.join(fixtures, "acme.state-before.json"));
const replayWorld = read(path.join(fixtures, "replay.events.json"));
const agentRun = read(path.join(examples, "agent_run.example.json"));
const runTrace = read(path.join(fixtures, "acme.run-trace.json"));
const runStrategies = read(path.join(fixtures, "acme.run-strategies.json"));
const strategyDecision = read(path.join(examples, "human_strategy_decision.example.json"));
const judgmentInference = read(path.join(examples, "judgment_inference.example.json"));
const knowledgeExample = read(path.join(examples, "knowledge.example.json"));
const knowledgeExtra = read(path.join(fixtures, "knowledge.extra.json"));
const knowledgeItems = [knowledgeExample, knowledgeExtra];

function send(res, status, body) {
  res.writeHead(status, { "content-type": "application/json" });
  res.end(JSON.stringify(body));
}

const fail = (res, status, code, message) => send(res, status, { error: { code, message } });

/**
 * An instant as epoch microseconds, so `occurred_at(N)` and `occurred_at(N)+1µs` stay distinct
 * (Date.parse truncates to milliseconds and would collapse them). NaN for a malformed value.
 */
function toMicros(raw) {
  const m = /^(\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2})(?:\.(\d{1,9}))?Z$/.exec(raw ?? "");
  if (!m) return NaN;
  const ms = Date.parse(`${m[1]}Z`);
  if (Number.isNaN(ms)) return NaN;
  const micros = (m[2] ?? "").padEnd(6, "0").slice(0, 6);
  return BigInt(ms) * 1000n + BigInt(micros);
}

const EVENT_MICROS = toMicros(EVENT_AT);
const FIRST_STATE_MICROS = toMicros(FIRST_STATE_AT);

/** The parsed instant of a world_as_of query value: null (absent), NaN (malformed) or epoch µs. */
function worldAt(raw) {
  return raw ? toMicros(raw) : null;
}

/** The graph as of world time: strictly before N is the N-1 world, at or after it the current graph. */
function graphWorldAsOf(raw) {
  const at = worldAt(raw);
  if (at === null) return graphAfter;
  if (Number.isNaN(at)) return null;
  return at <= EVENT_MICROS ? graphBefore : graphAfter;
}

/**
 * The state as of world time, strict-before (ADR-0019): the highest version whose as_of is earlier.
 * Before the first version the core answers 404 `state_not_computed_before` (never a later version).
 */
function stateWorldAsOf(raw) {
  const at = worldAt(raw);
  if (at === null) return { status: 200, body: stateAfter };
  if (Number.isNaN(at)) return { status: 400, code: "bad_request" };
  if (at <= FIRST_STATE_MICROS) return { status: 404, code: "state_not_computed_before" };
  if (at <= EVENT_MICROS) return { status: 200, body: stateBefore };
  return { status: 200, body: stateAfter };
}

/** The timeline keyset page: `before` is strict, `before_id` is the tie-break cursor (ADR-0019). */
function timelinePage(before, beforeId) {
  let items = timeline.items;
  if (before) {
    const cut = toMicros(before);
    items = Number.isNaN(cut) ? [] : items.filter((a) => toMicros(a.occurred_at) < cut);
  }
  if (before && beforeId) {
    const idx = items.findIndex((a) => a.id === beforeId);
    if (idx >= 0) items = items.slice(idx + 1);
  }
  return { items, next_before: null, next_before_id: null };
}

// ---- Episode replay (HAR-129 B/C) -------------------------------------------------
// The replay surface keeps a released cursor and a max-released mark (once released, an
// episode keeps its bookkeeping even after a reset). Views are derived from
// replay.events.json: prior episodes get their bookkeeping back, the next event is
// withheld, and positions beyond it are not even named.
const REPLAY = { released: 2, maxReleased: 2 };
const replayN = () => replayWorld.events.length;

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

function readBody(req) {
  return new Promise((resolve) => {
    let data = "";
    req.on("data", (c) => (data += c));
    req.on("end", () => {
      try {
        resolve(JSON.parse(data || "{}"));
      } catch {
        resolve(null);
      }
    });
  });
}

const replayRoute = (pathname) => /^\/replay\/manifests\/([^/]+)\/(episodes|episodes\/next|reset)$/.exec(pathname);

function handleReplay(req, res, url, body) {
  const m = replayRoute(url.pathname);
  if (!m) return false;
  const [, manifestId, which] = m;
  if (manifestId !== replayWorld.manifest_id) {
    fail(res, 404, "manifest_not_found", "no such demo manifest");
    return true;
  }
  if (req.method === "GET" && which === "episodes") {
    const at = url.searchParams.get("at");
    if (at !== null) {
      const k = Number(at);
      if (!/^\d+$/.test(at) || !Number.isSafeInteger(k)) {
        fail(res, 400, "bad_request", "at is not a position");
        return true;
      }
      if (k > REPLAY.released) {
        fail(res, 422, "invalid_episode", `${at} is not a released episode (0..${REPLAY.released})`);
        return true;
      }
      send(res, 200, replayView(k));
      return true;
    }
    send(res, 200, replayView(REPLAY.released));
    return true;
  }
  if (req.method === "POST" && which === "episodes/next") {
    if (REPLAY.released >= replayN()) {
      fail(res, 409, "replay_complete", "every event of the manifest is released");
      return true;
    }
    REPLAY.released += 1;
    REPLAY.maxReleased = Math.max(REPLAY.maxReleased, REPLAY.released);
    send(res, 200, advanceResult());
    return true;
  }
  if (req.method === "POST" && which === "reset") {
    const episode = body?.episode;
    if (!Number.isInteger(episode) || episode < 0 || episode > REPLAY.maxReleased) {
      fail(res, 422, "invalid_episode", `the target ${JSON.stringify(episode)} was never released or is out of range`);
      return true;
    }
    REPLAY.released = episode;
    send(res, 200, {
      manifest_id: replayWorld.manifest_id,
      account_id: replayWorld.account_id,
      episode,
      released_events: replayWorld.events.slice(0, episode).map((e) => e.event_id),
      reset_at: "2026-10-04T06:00:02Z",
      digest: "0".repeat(63) + "0",
    });
    return true;
  }
  fail(res, 405, "method_not_allowed", "unsupported method");
  return true;
}

const server = createServer((req, res) => {
  const url = new URL(req.url ?? "/", `http://${req.headers.host}`);
  if (url.pathname === "/healthz") return send(res, 200, { status: "ok" });
  if (req.headers.authorization !== `Bearer ${TOKEN}`) return fail(res, 401, "unauthorized", "a valid bearer token is required");

  // The replay routes accept POST (advance/reset); everything else is read-only.
  if (replayRoute(url.pathname)) {
    const respond = (body) => handleReplay(req, res, url, body);
    return req.method === "GET" ? respond(undefined) : void readBody(req).then(respond);
  }
  if (req.method !== "GET") return fail(res, 405, "method_not_allowed", "read-only");

  if (url.pathname === "/runs") {
    const second = { ...agentRun, id: "0a120000-0000-4000-8000-000000000002", status: "awaiting_human" };
    if (agentRun.generation) second.generation = { ...agentRun.generation, phase: "evaluating" };
    return send(res, 200, { items: [second, agentRun] });
  }

  // The run chain (WP24): the run document plus its trace, strategy set + eval bundles, and the
  // human's strategy decision. Run ...0601 is fully populated; any other run answers 404 so the
  // page's empty states are exercised honestly.
  const runSub = /^\/runs\/([^/]+)\/(trace|strategies|strategy-decision)$/.exec(url.pathname);
  if (runSub) {
    if (runSub[1] !== RUN) {
      const code = runSub[2] === "strategies" ? "strategies_not_ready" : runSub[2] === "strategy-decision" ? "no_decision" : "not_found";
      return fail(res, 404, code, `no ${runSub[2]} for that run`);
    }
    if (runSub[2] === "trace") return send(res, 200, runTrace);
    if (runSub[2] === "strategies") return send(res, 200, runStrategies);
    return send(res, 200, strategyDecision);
  }
  const runDoc = /^\/runs\/([^/]+)$/.exec(url.pathname);
  if (runDoc) {
    if (runDoc[1] === RUN) return send(res, 200, agentRun);
    if (runDoc[1] === "0a120000-0000-4000-8000-000000000002") return send(res, 200, { ...agentRun, id: runDoc[1], status: "awaiting_human" });
    return fail(res, 404, "not_found", "no such run");
  }
  const inference = /^\/episodes\/([^/]+)\/judgment-inference$/.exec(url.pathname);
  if (inference) {
    return inference[1] === EPISODE ? send(res, 200, judgmentInference) : fail(res, 404, "inference_not_ready", "no judgment inference for that episode");
  }

  // The knowledge reads (WP24): list with the lifecycle-status filter, and the detail by id.
  if (url.pathname === "/knowledge") {
    const status = url.searchParams.get("status");
    if (status !== null && status !== "" && !KNOWLEDGE_STATUSES.has(status)) {
      return fail(res, 400, "bad_request", "status must be one of candidate, provisional, supported, confirmed, disputed, stale");
    }
    const limit = Number(url.searchParams.get("limit") ?? 200);
    const items = (status ? knowledgeItems.filter((k) => k.status === status) : knowledgeItems).slice(0, limit);
    return send(res, 200, { items });
  }
  const knowledge = /^\/knowledge\/([^/]+)$/.exec(url.pathname);
  if (knowledge) {
    const item = knowledgeItems.find((k) => k.id === knowledge[1]);
    return item ? send(res, 200, item) : fail(res, 404, "not_found", "knowledge not found");
  }

  if (url.pathname === "/accounts") return send(res, 200, accounts);
  const account = /^\/accounts\/([^/]+)\/(graph|state|timeline)$/.exec(url.pathname);
  if (account) {
    if (account[1] !== ACCOUNT) return fail(res, 404, "not_found", "no such account");
    if (account[2] === "graph") {
      const graph = graphWorldAsOf(url.searchParams.get("world_as_of"));
      return graph ? send(res, 200, graph) : fail(res, 400, "bad_request", "world_as_of is not a time");
    }
    if (account[2] === "timeline") {
      return send(res, 200, timelinePage(url.searchParams.get("before"), url.searchParams.get("before_id")));
    }
    const state = stateWorldAsOf(url.searchParams.get("world_as_of"));
    if (state.status !== 200) return fail(res, state.status, state.code, "no state existed strictly before that world time");
    return send(res, 200, state.body);
  }
  const event = /^\/events\/([^/]+)\/graph-diff$/.exec(url.pathname);
  if (event) return event[1] === EVENT ? send(res, 200, diff) : fail(res, 404, "not_found", "no such event");
  return fail(res, 404, "not_found", "no such endpoint");
});

server.listen(PORT, "127.0.0.1", () => console.log(`fixture core on http://127.0.0.1:${PORT}`));
