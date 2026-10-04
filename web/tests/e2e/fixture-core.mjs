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

const server = createServer((req, res) => {
  const url = new URL(req.url ?? "/", `http://${req.headers.host}`);
  if (url.pathname === "/healthz") return send(res, 200, { status: "ok" });
  if (req.headers.authorization !== `Bearer ${TOKEN}`) return fail(res, 401, "unauthorized", "a valid bearer token is required");
  if (req.method !== "GET") return fail(res, 405, "method_not_allowed", "read-only");

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
