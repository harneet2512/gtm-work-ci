"""WP18 (HAR-116), HAR-97 §7: route only the relevant judges from workflow + state + trigger + proposed action."""
from __future__ import annotations

import pytest

from eval_cases_lib import CASES
from judge_doubles import CATALOG, K_1, PERSON_A, PERSON_B, RUBRICS, context

from ghost_worker.errors import InvalidRequestError
from ghost_worker.judges import JudgeContext, revision_subset, route
from ghost_worker.judges import predicates as P

CRM_ONLY = {"proposed_action_type": "internal_note", "recipients": [],
            "finished_artifact": {"channel": "crm_note", "subject": None, "body": "Logged the security request.",
                                  "attachments": []}}
WAIT = {"proposed_action_type": "wait", "recipients": [], "evidence_refs": [],
        "finished_artifact": {"channel": "none", "subject": None, "body": "", "attachments": []},
        "wait_until": "2026-10-05T09:00:00Z"}


def selected(**kwargs) -> tuple[str, ...]:
    return route(context(**kwargs), RUBRICS, CATALOG).selected


def test_crm_only_update_skips_recipient_breadth_and_writing_style() -> None:
    """HAR-97 §7: 'a CRM-only update does not need recipient-breadth or writing-style evals'."""
    decision = route(context(action=CRM_ONLY, rep_profile={"style": "short"}), RUBRICS, CATALOG)
    for name in ("stakeholder_coverage", "stakeholder_selection", "champion_continuity", "rep_style", "cta_calibration"):
        assert name not in decision.selected and decision.skipped[name]
    assert {"next_action_quality", "grounding", "channel_appropriateness"} <= set(decision.selected)
    assert len(decision.selected) <= 6


def test_wait_routes_the_abstention_judges_but_no_recipient_judges() -> None:
    chosen = selected(action=WAIT)
    assert {"next_action_quality", "evidence_sufficiency"} <= set(chosen)
    assert not {"stakeholder_coverage", "cta_calibration", "rep_style", "channel_appropriateness"} & set(chosen)
    assert "timing_cadence" not in chosen, "a wait is not a timing question unless the customer went quiet"


ALWAYS_ON = {"buyer_readiness", "cta_calibration", "grounding", "next_action_quality"}


def test_external_email_runs_core_and_conditional_judges_in_catalog_order() -> None:
    chosen = selected()
    assert chosen == CATALOG.order(set(chosen))
    assert ALWAYS_ON <= set(chosen)
    assert {"stakeholder_coverage", "decision_process"} <= set(chosen)  # two recipients; a signature ask
    assert not {"knowledge_applicability", "rep_style", "channel_appropriateness", "champion_continuity"} & set(chosen)
    assert len(chosen) <= 12


def test_only_the_always_relevant_judges_run_on_every_external_action() -> None:
    """HAR-97 section 7: not 15 model calls on every trivial action. A plain informational reply routes the
    always-on four plus what its inputs make relevant."""
    plain = {"finished_artifact": {"channel": "email", "subject": "Re: notes", "body": "Thanks, noted.",
                                   "attachments": []}, "recipients": [{"person_id": PERSON_B, "role": "to", "why": "x"}]}
    chosen = set(selected(action=plain, fields={"blockers": []}, commitments=[]))
    assert ALWAYS_ON <= chosen and len(chosen) <= 8, sorted(chosen)


def test_open_security_blocker_pushed_through_routes_customer_risk() -> None:
    """HAR-116 review: customer_risk_sensitivity was skipped on a go-live push through an open security blocker."""
    assert "customer_risk_sensitivity" in selected()  # neutral: signature ask while the security review is open
    quiet_ask = {"finished_artifact": {"channel": "email", "subject": "Re", "body": "Answers attached.",
                                       "attachments": []}}
    assert "customer_risk_sensitivity" not in selected(action=quiet_ask)
    closed = [{"text": "Security review of the questionnaire", "status": "resolved"}]
    assert "customer_risk_sensitivity" not in selected(fields={"blockers": closed})


def test_email_that_answers_an_invite_request_is_judged_for_channel() -> None:
    ask = {"trigger": {**context().trigger, "text": "Please send an invite for Tuesday or Wednesday."}}
    assert "channel_appropriateness" in selected(**ask)
    assert "channel_appropriateness" not in selected()


def test_knowledge_and_style_judges_need_their_inputs() -> None:
    chosen = selected(offered_knowledge=[{"id": K_1}], rep_profile={"style": "short"})
    assert {"knowledge_applicability", "exception_awareness", "rep_style"} <= set(chosen)


def test_unknown_workflow_is_an_explicit_error() -> None:
    ctx = context().model_copy(update={"workflow": "unknown_workflow"})
    with pytest.raises(InvalidRequestError, match="unknown workflow"):
        route(ctx, RUBRICS, CATALOG)


def test_revision_reevaluates_affected_and_blocking_dimensions() -> None:
    routed = ("buyer_readiness", "momentum", "business_case", "grounding")
    previous = [{"eval_type": "momentum", "verdict": "warn"}, {"eval_type": "business_case", "verdict": "pass"},
                {"eval_type": "buyer_readiness", "verdict": "pass"}]
    assert revision_subset(previous, routed, CATALOG) == ("buyer_readiness", "momentum", "grounding")


@pytest.mark.parametrize(("predicate", "kwargs", "expected"), [
    ("commercial_ask", {}, True),
    ("commercial_ask", {"action": {"finished_artifact": {"channel": "email", "subject": None,
                                                         "body": "The answers are attached.", "attachments": []}}}, False),
    ("meeting_ask", {"action": {"proposed_action_type": "schedule_meeting"}}, True),
    ("value_claim", {}, False),
    ("early_stage", {"fields": {"stage": "Discovery"}}, True),
    ("risk_elevated", {"fields": {"relationship_risk": "high"}}, True),
    ("risk_elevated", {}, False),
    ("open_blocker", {}, True),
    ("champion_in_question", {"fields": {"champion_status": "weakening"}}, True),
    ("champion_known", {"fields": {"champion": "unknown"}}, False),
    ("exception_in_state", {"fields": {"champion_status": "delegated"}}, True),
    ("eb_in_recipients", {"fields": {"economic_buyer": PERSON_A}}, True),
    ("process_gap", {}, False),  # an unknown economic buyer is the economic-buyer judge's gap, not a process gap
    ("process_gap", {"fields": {"decision_process": "unknown"}}, True),
    ("awaiting_reply", {"timeline_facts": {"last_inbound_at": "2026-09-20T10:00:00Z",
                                           "last_outbound_at": "2026-09-25T10:00:00Z"}}, True),
    ("engagement_shift", {"timeline_facts": {"last_inbound_at": "2026-09-20T10:00:00Z"}}, True),
    ("engagement_shift", {}, False),
    ("evidence_thin", {}, False),  # a commercial ask with an unknown economic buyer is not thin evidence
    ("evidence_thin", {"fields": {"stage": "unknown"}}, True),
    ("evidence_thin", {"recent_changes": {"summary": "x", "material_diff_fields": [],
                                          "signals": ["field_contradicted"]}}, True),
    ("channel_choice", {}, False),
    ("proactive_outreach", {}, False),
    ("proactive_outreach", {"trigger": {**context().trigger, "text": ""}}, True),
    ("customer_quiet", {"timeline_facts": {"last_inbound_at": "2026-09-20T10:00:00Z"}}, True),
    ("customer_quiet", {}, False),
    ("risk_elevated", {"fields": {"health": "at_risk"}}, False),  # a health rating alone is not an incident
    ("open_blocker", {}, True),
    ("open_blocker", {"fields": {"blockers": [{"text": "Pricing sign-off", "status": "open"}]}}, False),
    ("commitment_in_play", {}, True),  # the customer says they cannot commit to a review date
    ("commitment_in_play", {"trigger": {**context().trigger, "text": "Thanks, got it."}}, False),
    ("commitment_in_play", {"action": {"finished_artifact": {"channel": "email", "subject": None,
                                                             "body": "I will follow up on the commitment.",
                                                             "attachments": []}}}, True),
    ("commitment_pending", {}, False),
    ("commitment_pending", {"timeline_facts": {"last_inbound_at": "2026-09-20T10:00:00Z"}}, True),
    ("breadth_in_question", {}, True),
    ("addressee_in_question", {}, False),
    ("addressee_in_question", {"action": {"recipients": [{"person_id": PERSON_A, "role": "to", "why": "x"}]}}, True),
    ("champion_off_thread", {}, False),
    ("state_moved", {}, False),
    ("state_moved", {"recent_changes": {"summary": "x", "material_diff_fields": ["stage"], "signals": []}}, True),
    ("written_message", {"action": CRM_ONLY}, False),
])
def test_predicates(predicate: str, kwargs: dict, expected: bool) -> None:
    assert P.PREDICATES[predicate](context(**kwargs)) is expected


def test_has_open_commitments_means_our_side_owes_something_open() -> None:
    assert P.has_open_commitments(context())
    assert not P.has_open_commitments(context(commitments=[]))
    customer_owned = [{"text": "Review the answers", "owner_person_id": PERSON_B, "due_at": None, "status": "open"}]
    assert not P.has_open_commitments(context(commitments=customer_owned))


def test_unknown_or_malformed_state_values_do_not_break_predicates() -> None:
    ctx = context(fields={"blockers": "unknown", "current_commitments": "unknown", "decision_process": "unknown"},
                  commitments=[], timeline_facts={"last_inbound_at": "not a time"})
    assert not P.open_blocker(ctx) and not P.has_open_commitments(ctx) and P.process_gap(ctx)
    assert not P.commitment_pending(ctx) and not P.state_moved(ctx)
    assert not P.engagement_shift(ctx) and not P.awaiting_reply(ctx)


def _gold_routing() -> tuple[list[tuple[str, str, bool]], dict[str, list[int]]]:
    """(case id, eval type, blocking) of every skipped gold fail, and judges per proposed action."""
    skipped, per_action = [], {}
    for case in CASES.values():
        ctx = JudgeContext.from_eval_case(case)
        chosen = route(ctx, RUBRICS, CATALOG).selected
        per_action.setdefault(ctx.action_type, []).append(len(chosen))
        skipped += [(case["id"], e["eval_type"], bool(e["blocking"])) for e in case["expected"]
                    if e["eval_type"] in RUBRICS and e["verdict"] == "fail" and e["eval_type"] not in chosen]
    return skipped, per_action


def test_router_skips_no_gold_block_on_the_legacy_benchmark() -> None:
    """Routing must not silently skip a judge where the legacy gold says the candidate is blocked (a skipped judge
    is a false pass). Skipped non-blocking fails are allowed and listed in docs/traceability/wp18.md."""
    skipped, _ = _gold_routing()
    assert [s for s in skipped if s[2]] == []
    fails = sum(e["verdict"] == "fail" for c in CASES.values() for e in c["expected"] if e["eval_type"] in RUBRICS)
    assert len(skipped) / fails <= 0.07, skipped


def test_the_cases_the_review_found_are_routed() -> None:
    by_id = {c["id"]: c for c in CASES.values()}
    for case_id, judge in (("northstar_cp4_go_live_before_security_signoff", "customer_risk_sensitivity"),
                           ("northstar_cp4_email_asks_which_day_instead_of_invite", "channel_appropriateness"),
                           ("northstar_cp3_handoff_drops_final_signoff", "commitment_consistency")):
        assert judge in route(JudgeContext.from_eval_case(by_id[case_id]), RUBRICS, CATALOG).selected, case_id


def test_send_email_averages_at_most_twelve_judges_on_the_legacy_gold() -> None:
    _, per_action = _gold_routing()
    assert sum(per_action["send_email"]) / len(per_action["send_email"]) <= 12


def test_every_registered_predicate_is_used_by_a_rubric() -> None:
    used = {n for r in RUBRICS.values() for n in (*r.routing.when_all, *r.routing.when_any)}
    assert set(P.PREDICATES) == used


def test_unknown_proposed_action_type_is_an_explicit_error() -> None:
    """HAR-116 re-review: an action type no rubric lists used to route to nothing, silently skipping every judge."""
    ctx = context().model_copy(update={"candidate_action": {**context().candidate_action,
                                                            "proposed_action_type": "launch_rocket"}})
    with pytest.raises(InvalidRequestError, match="unknown proposed action"):
        route(ctx, RUBRICS, CATALOG)
