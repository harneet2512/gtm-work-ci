// The fixture core's eval disputes (POST /eval-results/{id}/disputes), kept apart from fixture-core.mjs so a
// vitest suite can hold it to the contract. It mirrors core-go/internal/evaldispute: the dispute snapshots the
// fixture EvalResult, an identical repeat returns the stored one, and the same requests are refused.
const SURFACES = new Set(["web", "slack", "mcp", "api"]);
const EXPECTED = new Set(["pass", "warn", "fail", "abstain", "not_relevant"]);
const FIELDS = new Set(["reason", "expected_verdict", "surface", "actor_person_id", "actor_label"]);
const UUID = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;
const MAX_REASON = 2000;
const MAX_LABEL = 200;

const error = (status, code, message) => ({ status, body: { error: { code, message } } });
const chars = (s) => [...s].length;

/** Every EvalResult of the fixture strategies, by id. */
function resultsById(strategies) {
  const out = new Map();
  for (const bundle of strategies.eval_bundles) for (const item of bundle.items) if (item.result) out.set(item.result.id, item.result);
  return out;
}

/** Every person the fixture world knows: buying groups and activity participants. */
function knownPeople(trace) {
  const ids = new Set();
  for (const state of [trace.state_before, trace.state_at_run]) for (const m of state?.buying_group ?? []) ids.add(m.person_id);
  for (const a of [...trace.trigger_activities, ...(trace.correlated_activities ?? [])]) for (const p of a.participants) if (p.person_id) ids.add(p.person_id);
  return ids;
}

/** The core's request checks, in its order; null when the body is acceptable. */
function refusal(body) {
  if (body === null || typeof body !== "object" || Array.isArray(body)) return error(400, "bad_request", "request body is not a valid eval dispute request");
  const unknown = Object.keys(body).find((k) => !FIELDS.has(k));
  if (unknown) return error(400, "bad_request", `request body has an unknown field "${unknown}"`);
  const reason = typeof body.reason === "string" ? body.reason.trim() : "";
  if (reason === "") return error(422, "invalid_request", "reason must say what the eval got wrong");
  if (chars(reason) > MAX_REASON) return error(422, "invalid_request", `reason is limited to ${MAX_REASON} characters`);
  if (!SURFACES.has(body.surface)) return error(422, "invalid_request", "surface must be web, slack, mcp or api");
  if (typeof body.actor_label !== "string" || body.actor_label === "" || chars(body.actor_label) > MAX_LABEL) {
    return error(422, "invalid_request", `actor_label must be 1 to ${MAX_LABEL} characters`);
  }
  if (body.actor_person_id != null && !UUID.test(body.actor_person_id)) return error(422, "invalid_request", "actor_person_id must be a uuid");
  if (body.expected_verdict != null && !EXPECTED.has(body.expected_verdict)) {
    return error(422, "invalid_request", "expected_verdict must be pass, warn, fail, abstain or not_relevant");
  }
  return null;
}

export function createDisputeStore(strategies, trace) {
  const results = resultsById(strategies);
  const people = knownPeople(trace);
  const stored = [];

  function dispute(resultId, body) {
    // Same order as the Go core: body decoding (400), id shape (404), request checks (422), the result (404).
    const refused = refusal(body);
    if (refused?.status === 400) return refused;
    if (!UUID.test(resultId ?? "")) return error(404, "not_found", "not found");
    if (refused) return refused;
    const result = results.get(resultId);
    if (!result) return error(404, "not_found", "not found");
    const expected = body.expected_verdict ?? null;
    if (expected === result.verdict) {
      return error(422, "expected_equals_verdict", `the eval already says ${result.verdict}; to dispute only its reasoning, leave expected_verdict out`);
    }
    const person = body.actor_person_id ?? null;
    if (person !== null && !people.has(person)) return error(422, "unknown_person", "actor_person_id names no known person");

    const reason = body.reason.trim();
    const same = stored.find(
      (d) => d.eval_result_id === resultId && d.surface === body.surface && d.actor_label === body.actor_label && d.reason === reason && d.expected_verdict === expected,
    );
    if (same) return { status: 200, body: same };
    const n = stored.length + 1;
    const record = {
      id: `0d15e000-0000-4000-8000-${String(n).padStart(12, "0")}`,
      eval_result_id: resultId,
      agent_run_id: result.agent_run_id,
      draft_index: result.draft_index,
      eval_type: result.eval_type,
      eval_version: result.eval_version,
      disputed_verdict: result.verdict,
      disputed_blocking: result.blocking,
      expected_verdict: expected,
      reason,
      surface: body.surface,
      actor_person_id: person,
      actor_label: body.actor_label,
      created_at: new Date(Date.UTC(2026, 9, 4, 6, 10, n)).toISOString().replace(".000Z", "Z"),
    };
    stored.push(record);
    return { status: 201, body: record };
  }

  return { dispute };
}
