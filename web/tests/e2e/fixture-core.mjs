// A stand-in for the Go core that replays recorded responses (tests/fixtures/core and the contract
// examples). CI never needs a live core: the web server under test talks to this over HTTP exactly as it
// would to the real one, including the bearer token, the error envelope and world-time reads (ADR-0019).
// Two worlds are served: Acme (the synthetic contract world) and MedTech Advances (the real CRMArena-Pro
// demo case). Accounts, runs and the replay cursor live in their own modules; this file only routes.
import { readFileSync } from "node:fs";
import { createServer } from "node:http";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { ACCOUNTS, accountList, diffForEvent, graphWorldAsOf, stateWorldAsOf, timelinePage } from "./fixture-accounts.mjs";
import { controlReply, paged } from "./fixture-control.mjs";
import { createDisputeStore } from "./fixture-disputes.mjs";
import { handleReplay, replayRoute } from "./fixture-replay.mjs";
import { inferenceForEpisode, REPLAY_BI, RUNS, runDocument, runList, runPart, surfaceMessage } from "./fixture-runs.mjs";

const here = path.dirname(fileURLToPath(import.meta.url));
const read = (file) => JSON.parse(readFileSync(path.resolve(here, file), "utf8"));
const RUN_STATUSES = new Set(["pending", "context_built", "drafted", "awaiting_human", "approved", "edited", "rejected", "ignored", "executed", "recorded", "failed", "cancelled"]);
const KNOWLEDGE_STATUSES = new Set(["candidate", "provisional", "supported", "confirmed", "disputed", "stale"]);
const TOKEN = process.env.FIXTURE_CORE_TOKEN ?? "e2e-token";
const PORT = Number(process.env.FIXTURE_CORE_PORT ?? 18080);

const knowledgeItems = [read("../../../contracts/examples/knowledge.example.json"), read("../fixtures/core/knowledge.extra.json")];
// "This eval is wrong" (HAR-97 E19): disputes of every fixture run's EvalResults, held in memory like the replay cursor.
const [first, ...rest] = [...RUNS.values()];
const disputes = createDisputeStore(first.strategies, first.trace, rest);

function send(res, status, body) {
  res.writeHead(status, { "content-type": "application/json" });
  res.end(JSON.stringify(body));
}

const fail = (res, status, code, message) => send(res, status, { error: { code, message } });

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

const ok = (body, status = 200) => ({ status, body });
const err = (status, code, message) => ({ status, body: { error: { code, message } } });
const NOT_READY = { strategies: "strategies_not_ready", "strategy-decision": "no_decision", trace: "not_found" };

/** The run chain (WP24): the run document plus its trace, strategy set + eval bundles, and the human's decision. */
function routeRuns(url) {
  if (url.pathname === "/runs") {
    const [account, status] = [url.searchParams.get("account_id"), url.searchParams.get("status")];
    if (account !== null && !/^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/.test(account)) return err(400, "bad_request", "account_id is not a uuid");
    if (status !== null && !RUN_STATUSES.has(status)) return err(400, "bad_request", "status is not a run status");
    const items = runList().items.filter((r) => (!account || r.account_id === account) && (!status || r.status === status));
    const page = paged(url, items);
    return page.error ? err(page.status, page.error.code, page.error.message) : ok(page.body);
  }
  const sub = /^\/runs\/([^/]+)\/(trace|strategies|strategy-decision)$/.exec(url.pathname);
  if (sub) {
    const part = runPart(sub[1], sub[2]);
    return part ? ok(part) : err(404, NOT_READY[sub[2]], `no ${sub[2]} for that run`);
  }
  const doc = /^\/runs\/([^/]+)$/.exec(url.pathname);
  if (doc) {
    const run = runDocument(doc[1]);
    return run ? ok(run) : err(404, "not_found", "no such run");
  }
  const inference = /^\/episodes\/([^/]+)\/judgment-inference$/.exec(url.pathname);
  if (!inference) return null;
  const inf = inferenceForEpisode(inference[1]);
  return inf ? ok(inf) : err(404, "inference_not_ready", "no judgment inference for that episode");
}

/** The knowledge reads (WP24): list with the lifecycle-status filter, and the detail by id. */
function routeKnowledge(url) {
  if (url.pathname === "/knowledge") {
    const status = url.searchParams.get("status");
    if (status !== null && status !== "" && !KNOWLEDGE_STATUSES.has(status)) {
      return err(400, "bad_request", "status must be one of candidate, provisional, supported, confirmed, disputed, stale");
    }
    const limit = Number(url.searchParams.get("limit") ?? 200);
    return ok({ items: (status ? knowledgeItems.filter((k) => k.status === status) : knowledgeItems).slice(0, limit) });
  }
  const one = /^\/knowledge\/([^/]+)$/.exec(url.pathname);
  if (!one) return null;
  const item = knowledgeItems.find((k) => k.id === one[1]);
  return item ? ok(item) : err(404, "not_found", "knowledge not found");
}

/** The account map reads, as of world time, the latest business-intelligence update, and graph diffs. */
function routeAccounts(url) {
  if (url.pathname === "/accounts") {
    const page = paged(url, accountList().items);
    return page.error ? err(page.status, page.error.code, page.error.message) : ok(page.body);
  }
  const bi = /^\/accounts\/([^/]+)\/business-intelligence\/latest$/.exec(url.pathname);
  if (bi) {
    const acc = ACCOUNTS.get(bi[1]);
    if (!acc) return err(404, "not_found", "no such account");
    return bi[1] === REPLAY_BI.account_id ? ok(REPLAY_BI) : err(404, "not_found", "no business intelligence yet");
  }
  const m = /^\/accounts\/([^/]+)\/(graph|state|timeline)$/.exec(url.pathname);
  if (m) {
    const acc = ACCOUNTS.get(m[1]);
    if (!acc) return err(404, "not_found", "no such account");
    const asOf = url.searchParams.get("world_as_of");
    if (m[2] === "timeline") return ok(timelinePage(acc, url.searchParams.get("before"), url.searchParams.get("before_id")));
    if (m[2] === "graph") {
      const graph = graphWorldAsOf(acc, asOf);
      return graph ? ok(graph) : err(400, "bad_request", "world_as_of is not a time");
    }
    const state = stateWorldAsOf(acc, asOf);
    return state.status === 200 ? ok(state.body) : err(state.status, state.code, "no state existed strictly before that world time");
  }
  const event = /^\/events\/([^/]+)\/graph-diff$/.exec(url.pathname);
  if (!event) return null;
  const diff = diffForEvent(event[1]);
  return diff ? ok(diff) : err(404, "not_found", "no such event");
}

const server = createServer((req, res) => {
  const url = new URL(req.url ?? "/", `http://${req.headers.host}`);
  if (url.pathname === "/healthz") return send(res, 200, { status: "ok" });
  if (req.headers.authorization !== `Bearer ${TOKEN}`) return fail(res, 401, "unauthorized", "a valid bearer token is required");

  // The replay routes accept POST (advance/reset); everything else is read-only.
  if (replayRoute(url.pathname)) {
    const respond = (body) => handleReplay(req, url, body, { send: (s, b) => send(res, s, b), fail: (s, c, m) => fail(res, s, c, m) });
    return req.method === "GET" ? respond(undefined) : void readBody(req).then(respond);
  }
  const dispute = /^\/eval-results\/([^/]+)\/disputes$/.exec(url.pathname);
  if (dispute) {
    if (req.method !== "POST") return fail(res, 405, "method_not_allowed", "method not allowed; use POST");
    return void readBody(req).then((body) => {
      const reply = disputes.dispute(dispute[1], body);
      send(res, reply.status, reply.body);
    });
  }
  const surface = /^\/surface-messages\/([^/]+)\/([^/]+)\/([^/]+)$/.exec(url.pathname);
  if (req.method === "GET" && surface) {
    const msg = surfaceMessage(surface[1], surface[2], surface[3]);
    return send(res, msg ? 200 : 404, msg ?? { error: { code: "not_found", message: "no such surface message" } });
  }
  if (req.method !== "GET") return fail(res, 405, "method_not_allowed", "read-only");
  // HAR-135/HAR-117 system reads: a healthy breaker and a drained slack queue — the demo's steady state.
  if (url.pathname === "/provider-breaker") {
    return send(res, 200, { state: "closed", consecutive_failures: 0, threshold: 3, cooldown_seconds: 30, trips_total: 0, rejected_total: 0, opened_at: null, last_reason: "" });
  }
  if (url.pathname === "/outbox/events") {
    const consumer = url.searchParams.get("consumer");
    if (!consumer) return fail(res, 400, "bad_request", "consumer is required");
    return send(res, 200, { items: [] });
  }
  const control = controlReply(url);
  const reply = (control && (control.error ? err(control.status, control.error.code, control.error.message) : control)) ?? routeRuns(url) ?? routeKnowledge(url) ?? routeAccounts(url) ?? err(404, "not_found", "no such endpoint");
  return send(res, reply.status, reply.body);
});

server.listen(PORT, "127.0.0.1", () => console.log(`fixture core on http://127.0.0.1:${PORT}`));
