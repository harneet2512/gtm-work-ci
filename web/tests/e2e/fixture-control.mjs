// The control-plane reads of the fixture core (HAR-145): the episode summary and trace, knowledge mutations, operational
// metrics, the run recomputation and the eval-run roll-ups. Every document is DERIVED from the recorded run fixtures
// (their strategy sets, eval bundles, trace and human decision) so counts, ids and statuses agree with what the run and
// eval pages show; tests/fixtures-control.contract.test.ts validates each one against the contract schemas.
import { readFileSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { RUNS, REPLAY_BI, surfaceMessage } from "./fixture-runs.mjs";

const here = path.dirname(fileURLToPath(import.meta.url));
const read = (rel) => JSON.parse(readFileSync(path.resolve(here, rel), "utf8"));
const ex = (name) => read(`../../../contracts/examples/${name}.example.json`);
const areasDoc = read("../../../contracts/evals/eval_areas.json");
const registry = read("../../../contracts/evals/eval_registry.json");

const ACCOUNT_NAMES = { "0a0c0000-0000-4000-8000-000000000001": "Acme Corp", "0a0cad00-0000-4000-8000-00000000ad01": "MedTech Advances" };
/** Each recorded run's account change: the replay run's is the one Message 1 (REPLAY_BI) was written for. */
const CHANGE_IDS = { default: "0a1c0000-0000-4000-8000-000000000301", replay: REPLAY_BI.account_change_id, medtech: "0a1cad00-0000-4000-8000-00000000ad31" };
const GRAPH_DIFF_IDS = { default: "41", replay: "302", medtech: "4101" };
const LIVE_MODELS = { "0f0aad00-0000-4000-8000-00000000ad60": ["qwen/qwen3.8-flash"] };

const entries = () => [...RUNS.values()];
const entryOfEpisode = (id) => entries().find((e) => e.episodeId === id) ?? null;
const entryOfRun = (id) => entries().find((e) => e.run.id === id) ?? null;
const variant = (e) => (e.run.account_id === "0a0cad00-0000-4000-8000-00000000ad01" ? "medtech" : e.episodeId.startsWith("0de5") ? "replay" : "default");

const familyOf = (type) => areasDoc.eval_types[type];
const areaOfFamily = (family) => areasDoc.areas.find((a) => a.families.includes(family))?.id;
const familyName = (id) => registry.families.find((f) => f.id === id)?.name ?? id;
const results = (e) => e.strategies.eval_bundles.flatMap((b) => b.items.map((i) => i.result).filter(Boolean));
const bucket = (v) => (v === "abstain" ? "unknown" : v);

// ---- counts -----------------------------------------------------------------------------------------------------
const blank = () => ({ pass: 0, warn: 0, fail: 0, unknown: 0, blocking_fail: 0, total: 0 });
function tally(list) {
  const c = blank();
  for (const r of list) {
    c[bucket(r.verdict)] += 1;
    c.total += 1;
    if (r.verdict === "fail" && r.blocking) c.blocking_fail += 1;
  }
  return c;
}
const delta = (now, before) => Object.fromEntries(Object.keys(blank()).map((k) => [k, now[k] - before[k]]));

/** The previous run of the same account by creation time, or null. */
function previousOf(e) {
  const prior = entries().filter((o) => o.run.account_id === e.run.account_id && o.run.created_at < e.run.created_at && results(o).length > 0);
  return prior.sort((a, b) => b.run.created_at.localeCompare(a.run.created_at))[0] ?? null;
}

const inArea = (list, area) => list.filter((r) => areaOfFamily(familyOf(r.eval_type)) === area);

// ---- eval runs ----------------------------------------------------------------------------------------------------
function evalRun(e) {
  const list = results(e);
  const prev = previousOf(e);
  return {
    id: e.run.id,
    account_id: e.run.account_id,
    account_name: ACCOUNT_NAMES[e.run.account_id],
    decision_episode_id: e.episodeId,
    run_status: e.run.status,
    evaluated_at: list.map((r) => r.created_at).sort().at(-1),
    result_count: list.length,
    counts: tally(list),
    areas: areasDoc.areas.map((a) => {
      const counts = tally(inArea(list, a.id));
      return { area: a.id, label: a.label, order: a.order, measured: counts.total > 0, counts, delta: prev ? delta(counts, tally(inArea(results(prev), a.id))) : null };
    }),
    previous_eval_run_id: prev ? prev.run.id : null,
  };
}

const evalRunsNewestFirst = () => entries().filter((e) => results(e).length > 0).sort((a, b) => b.run.created_at.localeCompare(a.run.created_at) || a.run.id.localeCompare(b.run.id));

/** Keyset paging by offset cursor ("o<n>"); a malformed cursor is a 400, like the core. */
export function paged(url, items) {
  const limit = Math.min(Math.max(Number(url.searchParams.get("limit") ?? 50) || 50, 1), 200);
  const raw = url.searchParams.get("cursor");
  let offset = 0;
  if (raw !== null && raw !== "") {
    if (!/^o\d+$/.test(raw)) return { status: 400, error: { code: "bad_cursor", message: "the cursor is not one this list issued" } };
    offset = Number(raw.slice(1));
  }
  const next = offset + limit < items.length ? `o${offset + limit}` : null;
  return { status: 200, body: { items: items.slice(offset, offset + limit), next_cursor: next } };
}

function families(e) {
  const list = results(e);
  const prev = previousOf(e);
  const prevList = prev ? results(prev) : [];
  const area = (a) => {
    const here_ = inArea(list, a.id);
    const counts = tally(here_);
    const famIds = [...new Set([...here_, ...inArea(prevList, a.id)].map((r) => familyOf(r.eval_type)))].sort((x, y) => Number(x.slice(1)) - Number(y.slice(1)));
    return {
      area: a.id,
      label: a.label,
      order: a.order,
      measured: counts.total > 0,
      counts,
      delta: prev ? delta(counts, tally(inArea(prevList, a.id))) : null,
      families: famIds.map((f) => {
        const mine = here_.filter((r) => familyOf(r.eval_type) === f);
        const before = prevList.filter((r) => familyOf(r.eval_type) === f);
        const types = [...new Set([...mine, ...before].map((r) => r.eval_type))].sort();
        return {
          family_id: f,
          name: familyName(f),
          counts: tally(mine),
          delta: prev ? delta(tally(mine), tally(before)) : null,
          eval_types: types.map((t) => ({ eval_type: t, counts: tally(mine.filter((r) => r.eval_type === t)), delta: prev ? delta(tally(mine.filter((r) => r.eval_type === t)), tally(before.filter((r) => r.eval_type === t))) : null, result_ids: mine.filter((r) => r.eval_type === t).map((r) => r.id) })),
        };
      }),
    };
  };
  return { eval_run_id: e.run.id, previous_eval_run_id: prev ? prev.run.id : null, areas: areasDoc.areas.map(area) };
}

const RANK = { fail: 0, warn: 1, pass: 2 };
const worst = (list) => list.map((r) => bucket(r.verdict)).sort((a, b) => (RANK[a] ?? 1.5) - (RANK[b] ?? 1.5))[0] ?? null;

function compare(a, b) {
  const types = [...new Set([...results(a), ...results(b)].map((r) => r.eval_type))].sort();
  const rows = types.map((t) => {
    const ra = results(a).filter((r) => r.eval_type === t);
    const rb = results(b).filter((r) => r.eval_type === t);
    const [va, vb] = [worst(ra), worst(rb)];
    let change = "unchanged";
    if (va === null) change = "added";
    else if (vb === null) change = "removed";
    else if (va !== vb) change = va === "unknown" || vb === "unknown" ? "inconclusive" : RANK[vb] > RANK[va] ? "improved" : "regressed";
    const blocking = (l) => l.some((r) => r.verdict === "fail" && r.blocking);
    return { eval_type: t, family_id: familyOf(t), area: areaOfFamily(familyOf(t)), a: va, b: vb, a_blocking: blocking(ra), b_blocking: blocking(rb), change, a_result_ids: ra.map((r) => r.id), b_result_ids: rb.map((r) => r.id) };
  });
  const n = (c) => rows.filter((r) => r.change === c).length;
  const side = (e) => ({ eval_run_id: e.run.id, decision_episode_id: e.episodeId, account_id: e.run.account_id, account_name: ACCOUNT_NAMES[e.run.account_id], evaluated_at: evalRun(e).evaluated_at, counts: tally(results(e)) });
  const overall = n("regressed") > 0 ? "regressed" : n("improved") > 0 ? "improved" : n("inconclusive") > 0 ? "inconclusive" : "unchanged";
  return {
    a: side(a),
    b: side(b),
    rows,
    overall: { change: overall, improved: n("improved"), regressed: n("regressed"), unchanged: n("unchanged"), added: n("added"), removed: n("removed"), inconclusive: n("inconclusive"), added_fail: rows.filter((r) => r.change === "added" && r.b === "fail").length, delta: delta(tally(results(b)), tally(results(a))) },
  };
}

const sameTrigger = (a, b) => JSON.stringify([...a.run.trigger_activity_ids].sort()) === JSON.stringify([...b.run.trigger_activity_ids].sort());

// ---- episode ------------------------------------------------------------------------------------------------------
const ACTION_OF_DECISION = { approve: "APPROVE_UNCHANGED", edit: "APPROVE_WITH_EDIT", reject: "REJECT", ignore: "IGNORE" };

const actionRef = (c) => c && { candidate_id: c.candidate_id, ranking: c.ranking, strategy_type: c.strategy_type, title: c.title ?? c.strategy_type, action_type: c.action_type ?? c.action_class ?? "REPLY", preferred_by_agent: Boolean(c.preferred_by_agent) };

const FINAL = { pending: "awaiting_send", send: "send_recorded", discard: "discarded" };

function summary(e) {
  const cands = e.strategies.strategy_set.candidates;
  const d = e.decision;
  const trig = e.trace.trigger_activities[0];
  const judged = e.inference && e.inference.human_verdict !== "pending";
  return {
    id: e.episodeId,
    agent_run_id: e.run.id,
    account_id: e.run.account_id,
    account_name: ACCOUNT_NAMES[e.run.account_id],
    opportunity_id: e.run.opportunity_id ?? null,
    strategy_set_id: e.strategies.strategy_set.id ?? e.strategies.strategy_set.strategy_set_id ?? null,
    status: !d ? "awaiting_choice" : judged ? "judged" : "decided",
    final_status: !d ? "awaiting_choice" : FINAL[d.send_decision] ?? "decided",
    triggering_event: { activity_id: trig.id, source_event_id: trig.source_event_id, activity_type: trig.activity_type, source_system: trig.source_system, occurred_at: trig.occurred_at, summary: trig.summary ?? null, trigger_activity_count: e.trace.trigger_activities.length },
    run: { id: e.run.id, status: e.run.status, phase: e.run.generation.phase, state_version: e.run.state_version ?? null, model: e.run.model ?? null },
    recommended_action: actionRef(cands.find((c) => c.preferred_by_agent)) ?? null,
    selected_action: d ? actionRef(cands.find((c) => c.candidate_id === d.selected_candidate_id)) ?? null : null,
    human_outcome: d && { agreement: e.inference?.agreement ?? (d.selected_candidate_id === d.original_agent_preference ? "agreed" : "overrode"), human_action: ACTION_OF_DECISION[e.trace.decisions?.[0]?.decision] ?? null, send_decision: d.send_decision, edited: (d.edits ?? []).length > 0, actor_label: d.actor_label, chosen_at: d.chosen_at, send_decided_at: d.send_decided_at ?? null },
    judgment_status: !e.inference ? "none" : e.inference.human_verdict ?? "pending",
    replay: null,
    created_at: e.run.created_at,
  };
}

// ---- trace --------------------------------------------------------------------------------------------------------
const span = (kind, key, title, status, summaryText, extra = {}) => ({ id: `${kind}:${key}`, kind, title, status, occurred_at: null, summary: summaryText, refs: [], eval_result_ids: [], attributes: {}, ...extra });

function knowledgeSpans(e) {
  const refs = [...new Set(e.strategies.strategy_set.candidates.flatMap((c) => c.knowledge_refs ?? []))];
  const titles = { knowledge_retrieved: "Knowledge retrieved", knowledge_applicable: "Knowledge applicable", knowledge_used: "Knowledge cited" };
  return Object.entries(titles).map(([kind, title]) =>
    refs.length === 0
      ? span(kind, "0", title, "not_recorded", "No knowledge attribution is recorded for this run", { attributes: { reason: "no_attribution_recorded" } })
      : span(kind, "0", title, "recorded", `${refs.length} knowledge ${refs.length === 1 ? "object" : "objects"} ${kind === "knowledge_used" ? "cited by a candidate" : kind === "knowledge_applicable" ? "applied" : "retrieved"}`, { refs: refs.map((id) => ({ kind: "knowledge", id })), attributes: { knowledge_ids: refs, ...(kind === "knowledge_used" ? { influence_measured: false } : {}) } }),
  );
}

function cliffSpans(e) {
  const subjects = { bi: variant(e) === "replay" ? REPLAY_BI.id : null, chooser: e.episodeId, judgment: e.episodeId };
  return ["bi", "chooser", "judgment"].map((kind) => {
    const msg = subjects[kind] && surfaceMessage(subjects[kind], "slack", kind);
    const title = `Cliff message: ${kind}`;
    if (!msg) return kind === "judgment" && !e.decision ? span("cliff_message", kind, title, "pending", "Message 3 (judgment) is posted after the human decides", { attributes: { message_kind: kind } }) : span("cliff_message", kind, title, "not_recorded", `Message ${["bi", "chooser", "judgment"].indexOf(kind) + 1} (${kind}) has no reservation or post recorded`, { attributes: { message_kind: kind } });
    const posted = msg.ts !== null;
    return span("cliff_message", kind, title, "recorded", `Message ${["bi", "chooser", "judgment"].indexOf(kind) + 1} (${kind}) ${posted ? "posted" : "reserved, not yet confirmed posted"} on slack`, {
      occurred_at: msg.reserved_at,
      refs: [{ kind: "surface_message", id: `${msg.subject_id}/slack/${kind}` }],
      attributes: { message_kind: kind, posted, surfaces: [{ surface: "slack", channel: msg.channel, ts: msg.ts, posted, reserved_at: msg.reserved_at }] },
    });
  });
}

const SPAN_OF_FAMILY = { ...areasDoc.family_spans };

function trace(e) {
  const v = variant(e);
  const t = e.trace;
  const trig = t.trigger_activities[0];
  const d = e.decision;
  const diff = t.state_diff;
  const set = e.strategies.strategy_set;
  const setId = set.id ?? set.strategy_set_id;
  const mutated = mutations(e).length > 0;
  const spans = [
    span("source_event", trig.id, "Source event", "recorded", `${trig.source_system} ${trig.activity_type} at ${trig.occurred_at}${trig.summary ? `: ${trig.summary}` : ""}`.slice(0, 600), { occurred_at: trig.occurred_at, refs: [{ kind: "activity", id: trig.id }, { kind: "source_event", id: trig.source_event_id }], attributes: { trigger_activity_count: t.trigger_activities.length, activity_ids: t.trigger_activities.map((a) => a.id) } }),
    span("evidence", CHANGE_IDS[v], "Evidence", "recorded", `${t.correlated_activities.length} evidence references behind the account change`, { occurred_at: trig.occurred_at, refs: [{ kind: "account_change", id: CHANGE_IDS[v] }], attributes: { material_change: true } }),
    span("resolution", trig.id, "Resolution", "recorded", `Attached to ${ACCOUNT_NAMES[e.run.account_id]}; ${(trig.participants ?? []).length} participants`, { occurred_at: trig.occurred_at, refs: [{ kind: "activity", id: trig.id }], attributes: { participants_total: (trig.participants ?? []).length } }),
    span("graph_mutation", GRAPH_DIFF_IDS[v], "Graph mutation", "recorded", `The account graph changed for this event`, { occurred_at: trig.occurred_at, refs: [{ kind: "graph_diff", id: GRAPH_DIFF_IDS[v] }], attributes: { change_count: diff.changes.length } }),
    span("state", String(diff.to_version), "State", "recorded", `State v${diff.from_version} to v${diff.to_version}: ${diff.changes.length} fields changed (${diff.is_material ? "material" : "not material"})`, { occurred_at: diff.created_at ?? trig.occurred_at, refs: [{ kind: "state_diff", id: diff.id }], attributes: { from_version: diff.from_version, to_version: diff.to_version, is_material: diff.is_material, changed_fields: diff.changes.map((c) => c.field), state_version: diff.to_version } }),
    span("precedents", "0", "Precedents", "not_recorded", "Precedent retrieval is not recorded yet", { attributes: { reason: "not_persisted" } }),
    ...knowledgeSpans(e),
    span("candidates", setId, "Candidates", "recorded", `${set.candidates.length} candidates in the strategy set`, { refs: [{ kind: "strategy_set", id: setId }, ...set.candidates.map((c) => ({ kind: "strategy_candidate", id: c.candidate_id }))], attributes: { candidate_ids: set.candidates.map((c) => c.candidate_id) } }),
    span("ranking", setId, "Ranking", "recorded", `gtm_ai preferred ${set.candidates.find((c) => c.preferred_by_agent)?.title ?? "none of them"}`, { refs: [{ kind: "strategy_set", id: setId }], attributes: { preferred_candidate_id: set.candidates.find((c) => c.preferred_by_agent)?.candidate_id ?? null } }),
    ...cliffSpans(e),
    d ? span("human_interaction", d.id, "Human interaction", "recorded", `${d.actor_label} chose ${set.candidates.find((c) => c.candidate_id === d.selected_candidate_id)?.title ?? "a candidate"}`, { occurred_at: d.chosen_at, refs: [{ kind: "human_strategy_decision", id: d.id }], attributes: { agreement: e.inference?.agreement ?? null, edited: (d.edits ?? []).length > 0, send_decision: d.send_decision } }) : span("human_interaction", "0", "Human interaction", "pending", "No human choice has been recorded yet"),
    d ? span("recomputed_action", d.id, "Recomputed action", "recorded", (d.edits ?? []).length > 0 ? "The human edited the draft; the final action was re-evaluated at send time" : "The chosen action was sent as drafted", { occurred_at: d.send_decided_at ?? d.chosen_at, refs: [{ kind: "human_strategy_decision", id: d.id }], attributes: { edited: (d.edits ?? []).length > 0, send_decision: d.send_decision } }) : span("recomputed_action", "0", "Recomputed action", "pending", "No human choice yet, so no action was recomputed"),
    mutated ? span("knowledge_mutation", mutations(e)[0].knowledge_id, "Knowledge mutation", "recorded", `${mutations(e).length} change to company knowledge`, { refs: mutations(e).map((m) => ({ kind: "knowledge", id: m.knowledge_id })), attributes: { operations: mutations(e).map((m) => m.operation), mutation_ids: mutations(e).map((m) => m.id) } }) : d ? span("knowledge_mutation", "0", "Knowledge mutation", "not_recorded", "This episode changed no company knowledge") : span("knowledge_mutation", "0", "Knowledge mutation", "pending", "Knowledge changes are recorded after the human decides"),
  ].map((s, i) => ({ ...s, seq: i + 1 }));

  const unassigned = [];
  for (const r of results(e)) {
    const kind = SPAN_OF_FAMILY[familyOf(r.eval_type)];
    const target = kind && spans.find((s) => s.kind === kind);
    if (target) target.eval_result_ids.push(r.id);
    else unassigned.push(r.id);
  }
  return { episode_id: e.episodeId, agent_run_id: e.run.id, account_id: e.run.account_id, spans, unassigned_eval_result_ids: unassigned };
}

// ---- knowledge mutations, metrics, recomputation --------------------------------------------------------------------
function mutations(e) {
  if (variant(e) !== "default") return [];
  const base = ex("knowledge_mutation");
  return [
    { ...base, evidence_episode_id: e.episodeId, evidence: { ...base.evidence, ref_id: e.episodeId } },
    { ...base, id: "0c1a0000-0000-4000-8000-000000000f02", operation: "WEAKEN", status_before: "supported", status: "provisional", version: 3, occurred_at: "2026-09-29T16:12:00Z", evidence_episode_id: e.episodeId, evidence: { kind: "counterexample", ref_id: e.episodeId, note: "The buyer did not respond to the extra stakeholder" } },
  ];
}

function metrics(e) {
  const base = { classification: "metric", agent_run_id: e.run.id, decision_episode_id: e.episodeId };
  const zero = { model_calls: 0, input_tokens: 0, output_tokens: 0, cached_input_tokens: null, reasoning_tokens: null, tool_calls: 0, context_pulls: 0, retries: 0, cost_usd: null, latency: { worker_call_ms: 0, model_ms: 0 }, models: [], stages: [] };
  const v = variant(e);
  if (v === "medtech") return { ...ex("operational_metrics"), ...base, models: LIVE_MODELS[e.run.id] };
  return { ...base, measured: false, usage_source: v === "default" ? "replay" : null, ...zero };
}

const EDIT_FIELD = { recipients: "recipients", subject: "subject", body: "body" };

function recomputation(e) {
  const d = e.decision;
  const head = { run_id: e.run.id, decision_episode_id: e.episodeId, human_delta_id: null, semantic_labels: [], generated_at: "2026-10-05T10:00:00Z" };
  const state = { account_id: e.run.account_id, version_before: e.run.state_version ?? null, version_after: e.run.state_version ?? null, preserved: true };
  if (!d) return { ...head, status: "not_decided", edited: false, account_state: { ...state, version_after: null, preserved: null }, entries: [], preserved_overall: [] };
  if ((d.edits ?? []).length === 0) return { ...head, status: "unedited", edited: false, account_state: state, entries: [], preserved_overall: [] };
  const cand = e.strategies.strategy_set.candidates.find((c) => c.candidate_id === d.selected_candidate_id);
  const bundle = e.strategies.eval_bundles.find((b) => b.strategy_candidate_id === d.selected_candidate_id);
  const evals = bundle.items.map((i) => i.result).filter(Boolean).slice(0, 2);
  const ref = (kind, id, field, label, reason, verdict = null, replaces = null) => ({ kind, ref_id: id, field, label, reason, verdict, replaces });
  const stateRef = ref("account_state", e.run.account_id, null, `Account state v${state.version_before}`, "An edit never writes account state; the send-time evaluation read the same version and content the run read.");
  const entries = d.edits.map((edit, index) => ({
    index,
    edit: { field: EDIT_FIELD.body, kind: "paragraph_edited", before: edit.before ?? null, after: edit.after ?? null, semantic_class: "content_change" },
    invalidated: [ref("artifact_field", cand.candidate_id, "body", "Body of the chosen action", "The human edited this part of the draft."), ...evals.map((r) => ref("eval_result", r.id, "body", r.eval_type, `${r.eval_type} reads the body, which this edit changed: its verdict on the old draft no longer applies.`, r.verdict)), ref("ranking_rationale", cand.candidate_id, null, `Why this candidate ranked ${cand.ranking}`, "The ranking rested on verdicts of the old draft that this edit invalidated.")],
    recomputed: [ref("final_artifact", d.id, null, "Final artifact as sent", "Re-evaluated at send time at the run's replay clock.")],
    not_recomputed: [...evals.map((r) => ref("eval_result", r.id, "body", r.eval_type, "Semantic evals are not re-run at send time; the old verdict is stale and no new one exists.", r.verdict)), ref("ranking_rationale", cand.candidate_id, null, `Why this candidate ranked ${cand.ranking}`, "The strategy set is not re-ranked after a send.")],
    preserved: [stateRef],
  }));
  return { ...head, status: "reevaluated", edited: true, semantic_labels: ["content_change"], account_state: state, entries, preserved_overall: [stateRef] };
}

// ---- routing --------------------------------------------------------------------------------------------------------
const ok = (body) => ({ status: 200, body });
const missing = (what) => ({ status: 404, error: { code: "not_found", message: `no such ${what}` } });

/** The control-plane GETs; null when the path is not one of them. */
export function controlReply(url) {
  if (url.pathname === "/eval-runs") {
    const wanted = url.searchParams.get("account_id");
    return paged(url, evalRunsNewestFirst().filter((e) => !wanted || e.run.account_id === wanted).map(evalRun));
  }
  if (url.pathname === "/eval-runs/compare") {
    const [ida, idb] = [url.searchParams.get("a"), url.searchParams.get("b")];
    if (!ida || !idb) return { status: 400, error: { code: "bad_request", message: "a and b are required" } };
    const [a, b] = [entryOfRun(ida), entryOfRun(idb)];
    if (!a || !b || results(a).length === 0 || results(b).length === 0) return missing("eval run");
    return sameTrigger(a, b) ? ok(compare(a, b)) : { status: 422, error: { code: "not_comparable", message: "the runs were not triggered by the same activities" } };
  }
  const fam = /^\/eval-runs\/([^/]+)\/families$/.exec(url.pathname);
  if (fam) {
    const e = entryOfRun(fam[1]);
    return e && results(e).length > 0 ? ok(families(e)) : missing("eval run");
  }
  const rec = /^\/runs\/([^/]+)\/recomputation$/.exec(url.pathname);
  if (rec) {
    const e = entryOfRun(rec[1]);
    return e ? ok(recomputation(e)) : missing("run");
  }
  const ep = /^\/episodes\/([^/]+)(?:\/(trace|knowledge-mutations|metrics|gate-results))?$/.exec(url.pathname);
  if (!ep) return null;
  const e = entryOfEpisode(ep[1]);
  if (!e) return missing("episode");
  if (ep[2] === "trace") return ok(trace(e));
  if (ep[2] === "knowledge-mutations") return ok({ episode_id: e.episodeId, items: mutations(e) });
  if (ep[2] === "metrics") return ok(metrics(e));
  if (ep[2] === "gate-results") return ok({ episode_id: e.episodeId, items: gateResults(e) });
  return ok(summary(e));
}

/**
 * Stored Bucket 2 gate results (gate_result.v1.json). Only the Acme episode has any, as a run that went through the real
 * path would: the intent judge's candidate warn, the person's choice read as different but defensible, and an eval gap.
 * Every other episode has none, which the page reads as "Not measured".
 */
function gateResults(e) {
  if (variant(e) === "medtech") return medtechGateResults(e);
  if (!e.episodeId.startsWith("0e9e0000")) return [];
  const cand = e.strategies.strategy_set.candidates[0].candidate_id;
  const row = (n, gate, sub, label, verdict, question, observed, why, refs, improves, grader) => ({
    id: `0e9e0000-0000-4000-8000-0000000d${String(n).padStart(4, "0")}`, gate, sub_gate: sub, label,
    judged_object: { type: "StrategyCandidate", id: cand }, span_id: `candidates:${e.strategies.strategy_set.id}`,
    verdict, question, observed, why, evidence_refs: refs, improves, grader, calibrated: false });
  return [
    row(2, "D2", "candidate", "", "warn", "Are the three complete actions each a plausible execution of a defensible strategy, and do they differ in substance?",
      "fit=pass, grounding=pass, cta=warn", "cta: the ask is stronger than the buyer's stated timing", [`candidate:${cand}`],
      "Makes the person's choice a real judgment among meaningful options.", { kind: "model", model: "fixture-judge", prompt_version: "decision_judge:v1" }),
    row(4, "D4", "", "DIFFERENT_BUT_DEFENSIBLE", "pass", "What does the person's choice tell us about gtm_ai's decision?",
      "the person chose option B over the preferred option A", "the chosen candidate was not worse evaluated than the preferred one", [`candidate:${cand}`],
      "Turns a click into structured supervision without treating the person as automatically right.", { kind: "deterministic" }),
    row(6, "D6", "cta", "MISSING_CRITERION", "warn", "Did the existing checks already identify the reason for the person's correction?",
      "no stored eval covers the cta dimension the person corrected", "no current evaluator represents this semantic correction", [`candidate:${cand}`],
      "Lets real use improve the eval system itself, not only produce another feedback row.", { kind: "deterministic" }),
  ];
}

/**
 * Bucket 1 results of the MedTech episode, as the post-publish runner would store them: B1 (the event read faithfully, cites
 * the trigger activity) and B2 (linkage warns). Every other Bucket 1 gate has no result, which the page reads as "not measured".
 */
function medtechGateResults(e) {
  const trig = e.trace.trigger_activities[0];
  const row = (n, gate, verdict, question, observed, why, improves) => ({
    id: `0e9e0000-0000-4000-8000-0000000d01${String(n).padStart(2, "0")}`, gate, sub_gate: "", label: "",
    judged_object: { type: "Activity", id: trig.id }, span_id: `source_event:${trig.id}`, verdict, question, observed, why,
    evidence_refs: [`activity:${trig.id}`], improves, grader: { kind: "deterministic" }, calibrated: false });
  return [
    row(1, "B1", "pass", "Did gtm_ai understand the new event correctly?", "the stored event matches the source email", "the email's sender, date and subject match the source record", "Stops corrupted raw context from becoming company knowledge or account state."),
    row(2, "B2", "warn", "Did gtm_ai attach the event to the correct people, account, opportunity and relationships?", "one participant is not linked to a person", "an unlinked participant weakens the relationship graph", "Makes sure the organizational world is about the right entities before any reasoning happens."),
  ];
}

/** Everything the fixture serves for one entry, for the contract test. */
export const documentsOf = (e) => ({ gateResults: gateResults(e), summary: summary(e), trace: trace(e), mutations: mutations(e), metrics: metrics(e), recomputation: recomputation(e), evalRun: results(e).length ? evalRun(e) : null, families: results(e).length ? families(e) : null });
export const allEntries = entries;
export { compare as compareEntries };
