"""/v1/judge: the semantic evals of ONE candidate, read through the run token, returned as EvalBundle items.

The judge context is built from the same bounded pulls the draft agent uses (state, recent diffs, activities,
commitments, people). `now` is the trigger activity's time, never the wall clock, so a replayed run judges the
moment of the event. A judge that fails (provider or output) is returned as an `unknown` item that says so: an eval
that was selected is never silently dropped, and it cannot block.
"""
from __future__ import annotations

import logging
from collections.abc import Mapping, Sequence
from typing import Any

from ..draft.core_client import ContextPacket, CoreContextClient, ToolParams
from ..draft.deadline import Deadline
from ..judges import EvalCatalog, JudgeOutcome, Rubric, route, run_suite
from ..judges.context import JudgeContext
from ..judges.engine import JudgeRun, judged_object, result_id, span_id, utc_now
from ..llm.provider import LLMProvider
from ..models.strategies import JudgeRequest, JudgeResponse, StrategyCandidate

log = logging.getLogger(__name__)

PULL_LIMITS = {"state": 20, "recent_diffs": 5, "activities": 10, "commitments": 10, "people": 20}
ACTOR_ROLES = ("from", "organizer", "speaker", "actor")
INBOUND = frozenset({"EmailReply", "EmailReceived", "EmailInbound"})
OUTBOUND = frozenset({"EmailSent"})


def candidate_output(c: StrategyCandidate) -> dict[str, Any]:
    """The candidate as the AgentRunOutput the judges read (the CRM next step is the candidate's intent)."""
    return {
        "proposed_action_type": c.action_type,
        "recipients": [r.model_dump(mode="json", exclude_none=True) for r in (*c.to, *c.cc)],
        "finished_artifact": c.full_action_artifact.model_dump(mode="json"),
        "crm_next_step_intent": {"next_step": c.description[:500], "due_at": None, "stage_change": None},
        "reason": c.rationale, "evidence_refs": [e.model_dump(mode="json", exclude_none=True) for e in c.evidence_refs],
        "knowledge_refs_used": list(c.knowledge_refs), "wait_until": None}


def _items(packet: ContextPacket) -> list[dict[str, Any]]:
    return [dict(i) for i in packet.items]


def _state(packet: ContextPacket, members: Sequence[Mapping[str, Any]]) -> dict[str, Any]:
    header = next((i for i in _items(packet) if i.get("kind") == "state_header"), {})
    fields = {i["field_path"]: i.get("value") for i in _items(packet) if "field_path" in i}
    return {"account_id": header.get("account_id"), "account_name": header.get("account_name"),
            "opportunity_id": header.get("opportunity_id"), "fields": fields, "buying_group": list(members),
            "coverage_gaps": list(header.get("coverage_gaps") or [])}


def _actor(activity: Mapping[str, Any]) -> str | None:
    for role in ACTOR_ROLES:
        for p in activity.get("participants", ()):
            if p.get("role") == role and p.get("person_id"):
                return p["person_id"]
    return None


def _activity(a: Mapping[str, Any]) -> dict[str, Any]:
    text = a.get("excerpt") or a.get("summary") or ""
    return {"activity_id": a["activity_id"], "activity_type": a["activity_type"], "occurred_at": a["occurred_at"],
            "actor_person_id": _actor(a), "text": text}


def _latest(activities: Sequence[Mapping[str, Any]], types: frozenset[str]) -> str | None:
    times = [a["occurred_at"] for a in activities if a["activity_type"] in types]
    return max(times) if times else None


def build_context(request: JudgeRequest, packets: Mapping[str, ContextPacket], trigger_signals: Sequence[str],
                  offered: Sequence[Mapping[str, Any]]) -> JudgeContext:
    acts = _items(packets["activities"])
    triggers = [a for a in acts if a.get("is_trigger")] or acts[:1]
    if not triggers:
        raise ValueError("the run has no activity to judge against")
    support = [_activity(a) for a in acts if a not in triggers][:PULL_LIMITS["activities"]]
    diffs = _items(packets["recent_diffs"])
    material = [c for d in diffs for c in d.get("changes", ()) if c.get("material")]
    changed = sorted({c["field"] for c in material if c.get("field")})
    now = max(t["occurred_at"] for t in triggers)
    context = {
        "now": now, "state": _state(packets["state"], _items(packets["people"])),
        "recent_changes": {"summary": "; ".join(f"{c['field']} {c.get('op', 'changed')}" for c in material[:6])
                           or "no material change", "material_diff_fields": changed,
                           "signals": list(trigger_signals)},
        "trigger": _activity(max(triggers, key=lambda a: a["occurred_at"])), "supporting_activities": support,
        "commitments": _items(packets["commitments"]),
        "timeline_facts": {k: v for k, v in (("last_inbound_at", _latest(acts, INBOUND)),
                                             ("last_outbound_at", _latest(acts, OUTBOUND))) if v},
        "offered_knowledge": list(offered)}
    return JudgeContext.build(context, candidate_output(request.candidate))


def _abstain(rubric: Rubric, catalog: EvalCatalog, run: JudgeRun, outcome: JudgeOutcome) -> dict[str, Any]:
    entry = catalog.entry(rubric.eval_type)
    return {"id": result_id(run, rubric.eval_type), "agent_run_id": run.agent_run_id, "draft_index": run.draft_index,
            "eval_type": rubric.eval_type, "eval_version": rubric.eval_version, "kind": entry.kind,
            "verdict": "unknown", "label": None, "diagnostics": [], "score": None, "blocking": False,
            "reason": f"The judge did not return a usable answer ({(outcome.error or 'error').split(':')[0]}); "
                      "this dimension was not judged.",
            "state_refs": [], "activity_refs": [], "evidence_refs": [], "knowledge_refs": [],
            "suggested_correction": None, "confidence": None, "evidence_class": entry.evidence_class,
            "model": None, "created_at": utc_now().isoformat().replace("+00:00", "Z"),
            "judged_object": judged_object(run), "span_id": span_id(run)}


def judge_candidate(request: JudgeRequest, provider: LLMProvider, core: CoreContextClient, *, deadline: Deadline,
                    rubrics: Mapping[str, Rubric], catalog: EvalCatalog) -> JudgeResponse:
    packets = {tool: core.pull(tool, ToolParams(limit=limit), timeout_s=deadline.check(f"pulling {tool}"))
               for tool, limit in PULL_LIMITS.items()}
    signals = request.trigger_context.signal_types if request.trigger_context else ()
    context = build_context(request, packets, signals, request.offered_knowledge)
    routed = route(context, rubrics, catalog)
    excluded = {t: f"not in the {request.eval_suite.name} suite selected for this transition"
                for t in (request.eval_suite.excluded_eval_types if request.eval_suite else ()) if t in routed.selected}
    selected = [t for t in routed.selected if t not in excluded]
    suite = run_suite(context, request.run_id, lambda _trial: provider, rubrics, catalog,
                      draft_index=request.draft_index, eval_types=selected)
    run = JudgeRun(agent_run_id=request.run_id, draft_index=request.draft_index,
                   candidate_id=request.candidate.candidate_id)
    items: list[dict[str, Any]] = []
    models: list[str] = []
    for outcome in suite.outcomes:
        rubric = rubrics[outcome.eval_type]
        result = outcome.eval_result() if outcome.ok else _abstain(rubric, catalog, run, outcome)
        if outcome.ok and result.get("model"):
            models.append(result["model"])
        items.append({"eval_type": outcome.eval_type, "verdict": result["verdict"], "result": result,
                      "relevance_reason": f"{rubric.title}: routed for a {context.action_type} candidate."[:500]})
    items.extend({"eval_type": name, "verdict": "not_relevant", "result": None, "relevance_reason": reason[:500]}
                 for name, reason in {**suite.route.skipped, **excluded}.items())
    log.info("candidate judged", extra={"run_id": request.run_id, "draft_index": request.draft_index,
                                        "selected": len(suite.outcomes), "skipped": len(suite.route.skipped) + len(excluded)})
    return JudgeResponse(items=tuple(items), model=models[0] if models else "none")
