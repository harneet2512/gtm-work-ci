// The fixture core's accounts: the recorded graph, state and timeline of each account, read as of world time
// exactly as the Go core does (ADR-0019). Acme is the synthetic contract world; MedTech Advances is the real
// CRMArena-Pro demo case (tests/fixtures/medtech). Each account's Play event N splits its world: strictly
// before N is the N-1 world, at or after it the current one.
import { readFileSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const here = path.dirname(fileURLToPath(import.meta.url));
const fixtures = path.resolve(here, "../fixtures/core");
const examples = path.resolve(here, "../../../contracts/examples");
const read = (dir, file) => JSON.parse(readFileSync(path.join(dir, file), "utf8"));
const fx = (file) => read(fixtures, file);

/**
 * An instant as epoch microseconds, so `occurred_at(N)` and `occurred_at(N)+1µs` stay distinct
 * (Date.parse truncates to milliseconds and would collapse them). NaN for a malformed value.
 */
export function toMicros(raw) {
  const m = /^(\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2})(?:\.(\d{1,9}))?Z$/.exec(raw ?? "");
  if (!m) return NaN;
  const ms = Date.parse(`${m[1]}Z`);
  if (Number.isNaN(ms)) return NaN;
  const micros = (m[2] ?? "").padEnd(6, "0").slice(0, 6);
  return BigInt(ms) * 1000n + BigInt(micros);
}

/** The parsed instant of a world_as_of query value: null (absent), NaN (malformed) or epoch µs. */
const worldAt = (raw) => (raw ? toMicros(raw) : null);

function account({ graphAfter, graphBefore, stateAfter, stateBefore, timeline, eventId, diff, eventAt, firstStateAt }) {
  return { graphAfter, graphBefore, stateAfter, stateBefore, timeline, eventId, diff, eventMicros: toMicros(eventAt), firstStateMicros: toMicros(firstStateAt) };
}

export const ACCOUNTS = new Map([
  [
    "0a0c0000-0000-4000-8000-000000000001",
    account({
      graphAfter: fx("acme.graph.json"),
      graphBefore: fx("acme.graph-before.json"),
      stateAfter: read(examples, "account_state.example.json"),
      stateBefore: fx("acme.state-before.json"),
      timeline: fx("acme.timeline.json"),
      eventId: "05e00000-0000-4000-8000-000000000101",
      diff: fx("acme.graph-diff.json"),
      eventAt: "2026-09-29T15:42:00Z",
      firstStateAt: "2026-09-20T10:00:00Z",
    }),
  ],
  [
    "0a0cad00-0000-4000-8000-00000000ad01",
    account({
      graphAfter: fx("medtech.graph.json"),
      graphBefore: fx("medtech.graph-before.json"),
      stateAfter: fx("medtech.state.json"),
      stateBefore: fx("medtech.state-before.json"),
      timeline: fx("medtech.timeline.json"),
      eventId: "05e0ad00-0000-4000-8000-00000000ad0d",
      diff: fx("medtech.graph-diff.json"),
      eventAt: "2023-11-09T09:30:00Z",
      firstStateAt: "2023-05-25T11:23:41Z",
    }),
  ],
]);

/** GET /accounts: Acme first (the contract world), then the MedTech demo case. */
export const accountList = () => {
  const list = fx("accounts.json");
  return { items: [...list.items, fx("medtech.account.json")] };
};

/** The graph as of world time: strictly before N is the N-1 world, at or after it the current graph. */
export function graphWorldAsOf(acc, raw) {
  const at = worldAt(raw);
  if (at === null) return acc.graphAfter;
  if (Number.isNaN(at)) return null;
  return at <= acc.eventMicros ? acc.graphBefore : acc.graphAfter;
}

/**
 * The state as of world time, strict-before (ADR-0019): the highest version whose as_of is earlier.
 * Before the first version the core answers 404 `state_not_computed_before` (never a later version).
 */
export function stateWorldAsOf(acc, raw) {
  const at = worldAt(raw);
  if (at === null) return { status: 200, body: acc.stateAfter };
  if (Number.isNaN(at)) return { status: 400, code: "bad_request" };
  if (at <= acc.firstStateMicros) return { status: 404, code: "state_not_computed_before" };
  if (at <= acc.eventMicros) return { status: 200, body: acc.stateBefore };
  return { status: 200, body: acc.stateAfter };
}

/** The timeline keyset page: `before` is strict, `before_id` is the tie-break cursor (ADR-0019). */
export function timelinePage(acc, before, beforeId) {
  let items = acc.timeline.items;
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

/** GET /events/{id}/graph-diff for any account's Play event. */
export const diffForEvent = (eventId) => [...ACCOUNTS.values()].find((a) => a.eventId === eventId)?.diff ?? null;
