"""Account agent behaviour on the named situations (replayed cassettes) and guidance/rep-profile bookkeeping."""
from __future__ import annotations

import pytest

import draft_doubles as dbl
import draft_situations as sit
from draft_helpers import acme_core, acme_provider, assert_contract_valid, recipients, replay, run
from ghost_worker.draft.prompt import build_agent_user_prompt, render_guidance
from ghost_worker.errors import CassetteNotFoundError, InvalidRequestError
from ghost_worker.models.draft import DraftRequest
from test_contracts import example


# --- (a) Acme without guidance: the "bad" Draft 1 ------------------------------------------------

def test_acme_without_guidance_pushes_a_meeting_straight_to_marco() -> None:
    response, core = replay(sit.ACME_UNGUIDED)
    assert_contract_valid(response)
    assert response.output.proposed_action_type == "schedule_meeting"
    assert recipients(response) == [(sit.MARCO, "to")]  # Priya (champion) is dropped: champion_continuity fails
    assert sit.PRIYA not in {r.person_id for r in response.output.recipients}
    assert response.decision.action == "schedule_meeting"
    assert [p.person_id for p in response.decision.who_to_involve] == [sit.MARCO]
    assert response.decision.used_guidance is False and response.output.knowledge_refs_used == ()
    assert response.draft_index == 1
    assert response.tool_calls == 4 and len(core.requests) == 4


# --- (b) same trigger WITH K17 guidance: HAR-97 §16 causality proof -------------------------------

def test_har97_s16_guidance_changes_draft_1_to_champion_inclusive_send_email() -> None:
    guided, _ = replay(sit.ACME_GUIDED)
    unguided, _ = replay(sit.ACME_UNGUIDED)
    assert_contract_valid(guided)

    assert guided.output.proposed_action_type == "send_email"
    assert recipients(guided) == [(sit.MARCO, "to"), (sit.PRIYA, "cc")]
    assert guided.output.finished_artifact.channel == "email"
    assert guided.output.knowledge_refs_used == (sit.K17,)
    assert guided.decision.used_guidance is True
    assert [p.person_id for p in guided.decision.who_to_involve] == [sit.MARCO, sit.PRIYA]  # the champion choice
    assert guided.output.evidence_refs and guided.output.evidence_refs[0].activity_id == sit.ACME_EMAIL_ACT

    # the only input that differs is the guidance, and the outcome differs on exactly the guided dimensions
    assert (guided.output.proposed_action_type, recipients(guided)) != (
        unguided.output.proposed_action_type, recipients(unguided))
    assert unguided.output.knowledge_refs_used == () and unguided.decision.used_guidance is False


def test_guidance_is_the_only_difference_between_the_two_acme_prompts() -> None:
    guided = DraftRequest.model_validate(sit.ACME_GUIDED.request_copy())
    unguided = DraftRequest.model_validate(sit.ACME_UNGUIDED.request_copy())
    assert guided.model_copy(update={"decision_guidance": None}) == unguided
    prompt = build_agent_user_prompt(guided)
    assert sit.K17 in prompt and sit.K17 not in build_agent_user_prompt(unguided)
    assert prompt.replace(render_guidance(guided.decision_guidance), render_guidance(None)) == \
        build_agent_user_prompt(unguided)


def test_guided_cassettes_do_not_answer_an_unguided_request_and_vice_versa() -> None:
    """Replay is keyed on the whole prompt: guidance cannot be silently ignored or injected."""
    body = sit.ACME_GUIDED.request_copy()
    body.pop("decision_guidance")
    unguided_again, _ = replay(sit.ACME_GUIDED, body=body)
    assert unguided_again.output.proposed_action_type == "schedule_meeting"
    body = sit.ACME_UNGUIDED.request_copy()
    body["decision_guidance"] = example("decision_guidance")
    guided_again, _ = replay(sit.ACME_UNGUIDED, body=body)
    assert guided_again.decision.used_guidance is True
    body["decision_guidance"]["why_now"] = "A different why_now changes the prompt."
    with pytest.raises(CassetteNotFoundError):
        replay(sit.ACME_UNGUIDED, body=body)


# --- guided WAIT: knowledge refs survive a quiet decision (HAR-97 §16) ---------------------------

def test_guided_wait_until_tuesday_keeps_its_knowledge_refs_and_sends_nothing() -> None:
    response, core = replay(sit.ACME_GUIDED_WAIT)
    dumped = assert_contract_valid(response)
    assert response.output.proposed_action_type == "wait"
    assert response.output.wait_until == sit.TUESDAY == response.decision.wait_until
    assert response.output.knowledge_refs_used == (sit.K21,) and response.decision.used_guidance is True
    assert response.output.recipients == () and response.output.finished_artifact.channel == "none"
    assert response.output.finished_artifact.body == ""  # no outbound CTA today
    assert [p.person_id for p in response.decision.who_not_to_involve] == [sit.MARCO]
    assert dumped["output"]["knowledge_refs_used"] == [sit.K21]
    assert len(core.requests) == 2  # no drafting skill was invoked: the cassettes hold no skill call


def test_guided_no_action_also_keeps_its_knowledge_refs() -> None:
    guidance = sit.wait_guidance() | {"recommended_action": "no_action", "wait_until": None}
    body = sit.ACME_GUIDED_WAIT.request_copy() | {"decision_guidance": guidance}
    provider = dbl.ScriptedProvider([dbl.decision_turn("no_action", "Nothing to do.", used_guidance=True,
                                                       knowledge=[sit.K21])])
    response = run(provider, acme_core(), body)
    assert response.output.proposed_action_type == "no_action"
    assert response.output.knowledge_refs_used == (sit.K21,) and response.decision.used_guidance is True


# --- (c) Beta non-material event -----------------------------------------------------------------

def test_beta_non_material_event_is_no_action_and_invokes_no_skill() -> None:
    response, core = replay(sit.BETA_NON_MATERIAL)
    dumped = assert_contract_valid(response)
    assert response.output.proposed_action_type == "no_action" and response.decision.action == "no_action"
    assert response.output.recipients == () and response.output.finished_artifact.channel == "none"
    assert response.output.reason == response.decision.why_now
    assert dumped["output"]["evidence_refs"] == [] and response.output.wait_until is None
    assert response.cited_access_ids == ()
    assert len(core.requests) == 2


# --- (d) Northstar delegation: guidance exception triggered ---------------------------------------

def test_northstar_delegation_exception_does_not_force_elena_back_in() -> None:
    response, _ = replay(sit.NORTHSTAR_DELEGATION)
    assert_contract_valid(response)
    guidance = DraftRequest.model_validate(sit.NORTHSTAR_DELEGATION.request_copy()).decision_guidance
    assert guidance.supporting_knowledge[0].applies is False
    assert guidance.supporting_knowledge[0].exceptions_checked[0].triggered is True

    assert recipients(response) == [(sit.SAM, "to")]
    assert sit.ELENA not in {r.person_id for r in response.output.recipients}
    assert response.decision.used_guidance is False and response.output.knowledge_refs_used == ()
    assert response.output.proposed_action_type == "send_email"


# --- decision recorded, cited access ids ---------------------------------------------------------

def test_wait_decision_carries_wait_until_and_skips_the_skill() -> None:
    provider = dbl.ScriptedProvider([
        dbl.tools_turn(dbl.call(1, "state")),
        dbl.decision_turn("wait", "Marco said he will review internally first.", wait_until=sit.TUESDAY,
                          evidence=[dbl.ev(sit.ACME_EMAIL_ACT), dbl.ev(sit.NS_ACT)])])
    response = run(provider, acme_core(), sit.ACME_UNGUIDED.request_copy())
    assert response.output.proposed_action_type == "wait" and response.decision.wait_until == sit.TUESDAY
    assert response.output.wait_until == sit.TUESDAY
    assert provider.json_calls == []
    # the state pull returned the email activity, so that reference survives; the other account's is dropped
    assert [r.activity_id for r in response.output.evidence_refs] == [sit.ACME_EMAIL_ACT]


def test_wait_until_is_only_reported_for_wait() -> None:
    provider = dbl.ScriptedProvider([dbl.decision_turn("no_action", "ok", wait_until=sit.TUESDAY)])
    response = run(provider, acme_core(), sit.ACME_UNGUIDED.request_copy())
    assert response.decision.wait_until is None and response.output.wait_until is None


def test_cited_access_ids_are_the_pulls_that_returned_the_cited_evidence() -> None:
    response, _ = replay(sit.ACME_UNGUIDED)
    # evidence cites the email activity + claim: returned by state (101), evidence (103) and activities (104)
    assert response.cited_access_ids == (101, 103, 104)
    assert not hasattr(response, "context_refs")  # the pull trail is core's context_access_log, not ours


# --- guidance bookkeeping ------------------------------------------------------------------------

def guided_run(decision, *, guidance: dict | None = None):
    body = sit.ACME_GUIDED.request_copy()
    body["decision_guidance"] = guidance if guidance is not None else body["decision_guidance"]
    provider = acme_provider(sit.ACME_GUIDED.skill, decision)
    return run(provider, acme_core(), body)


GUIDED_PEOPLE = ((sit.MARCO, sit.MARCO_WHY), (sit.PRIYA, sit.PRIYA_WHY))


def guided_decision(knowledge: list[str], *, used: bool = True):
    return dbl.decision_turn("send_email", "x", involve=GUIDED_PEOPLE, used_guidance=used, knowledge=knowledge,
                             evidence=sit.ACME_EVIDENCE)


def test_knowledge_refs_are_limited_to_applied_knowledge_in_the_supplied_guidance() -> None:
    invented = "0c17c000-0000-4000-8000-0000000000ff"
    response = guided_run(guided_decision([sit.K17, invented]))
    assert response.output.knowledge_refs_used == (sit.K17,) and response.decision.used_guidance is True


def test_claiming_guidance_with_only_unknown_knowledge_is_not_used_guidance() -> None:
    response = guided_run(guided_decision(["0c17c000-0000-4000-8000-0000000000ff"]))
    assert response.output.knowledge_refs_used == () and response.decision.used_guidance is False


def test_knowledge_marked_not_applying_cannot_be_cited() -> None:
    response = guided_run(guided_decision([sit.K17]), guidance=sit.northstar_guidance() | {"account_id": sit.ACME})
    assert response.output.knowledge_refs_used == () and response.decision.used_guidance is False


def test_knowledge_with_a_triggered_exception_cannot_be_cited_even_when_applies_is_true() -> None:
    guidance = sit.northstar_guidance() | {"account_id": sit.ACME}
    guidance["supporting_knowledge"][0]["applies"] = True  # inconsistent guidance: exception fired, yet "applies"
    response = guided_run(guided_decision([sit.K17]), guidance=guidance)
    assert response.output.knowledge_refs_used == () and response.decision.used_guidance is False


def test_knowledge_with_only_untriggered_exceptions_can_be_cited() -> None:
    response = guided_run(guided_decision([sit.K17]))
    exceptions = DraftRequest.model_validate(sit.ACME_GUIDED.request_copy()).decision_guidance \
        .supporting_knowledge[0].exceptions_checked
    assert exceptions and not any(e.triggered for e in exceptions)
    assert response.output.knowledge_refs_used == (sit.K17,)


def test_the_model_saying_it_did_not_use_guidance_wins_over_cited_ids() -> None:
    response = guided_run(guided_decision([sit.K17], used=False))
    assert response.output.knowledge_refs_used == () and response.decision.used_guidance is False


def test_no_guidance_means_no_knowledge_refs_even_if_the_model_claims_some() -> None:
    decision = guided_decision([sit.K17])
    response = run(acme_provider(sit.ACME_GUIDED.skill, decision), acme_core(), sit.ACME_UNGUIDED.request_copy())
    assert response.output.knowledge_refs_used == () and response.decision.used_guidance is False


def test_guidance_for_another_account_is_rejected() -> None:
    body = sit.ACME_GUIDED.request_copy()
    body["decision_guidance"]["account_id"] = sit.BETA
    provider = dbl.ScriptedProvider([])
    with pytest.raises(InvalidRequestError, match="account"):
        run(provider, acme_core(), body)
    assert provider.turn_calls == []


# --- skill prompt: guidance, decision, who, rep profile ------------------------------------------

def test_skill_prompt_carries_the_guidance_decision_and_who_to_involve() -> None:
    provider = acme_provider(sit.ACME_GUIDED.skill, guided_decision([sit.K17]))
    run(provider, acme_core(), sit.ACME_GUIDED.request_copy())
    (skill,) = provider.json_calls
    assert skill["schema_name"] == dbl.SKILL_SCHEMA_NAME
    user = skill["user"]
    assert "action=send_email" in user and "Who to involve: " in user and sit.PRIYA in user
    assert "Who not to involve: none" in user
    assert "Marco (security) asks for the SOC2" in user  # the pulled context is what the skill drafts from


PROFILE = {"brevity": "very short", "formality": "casual", "directness": "blunt", "warmth": "high",
           "pressure": "none", "examples": ["Quick one: attached. Shout if anything is missing. - D"]}


def test_rep_profile_reaches_only_the_skill_never_the_decision_step() -> None:
    body = sit.ACME_GUIDED.request_copy() | {"rep_profile": PROFILE}
    provider = acme_provider(sit.ACME_GUIDED.skill, guided_decision([sit.K17]))
    run(provider, acme_core(), body)
    shown_to_decision = repr(provider.turn_calls)
    assert "very short" not in shown_to_decision and "Shout if anything" not in shown_to_decision
    assert "Rep profile" not in shown_to_decision
    (skill,) = provider.json_calls
    assert "very short" in skill["user"] and "Shout if anything is missing" in skill["user"]


def test_rep_profile_does_not_change_the_decision_prompt() -> None:
    plain = DraftRequest.model_validate(sit.ACME_GUIDED.request_copy())
    styled = DraftRequest.model_validate(sit.ACME_GUIDED.request_copy() | {"rep_profile": PROFILE})
    assert build_agent_user_prompt(plain) == build_agent_user_prompt(styled)


def test_without_a_rep_profile_the_skill_is_told_so() -> None:
    provider = acme_provider(sit.ACME_GUIDED.skill, guided_decision([sit.K17]))
    run(provider, acme_core(), sit.ACME_GUIDED.request_copy())
    assert "Rep profile: none supplied" in provider.json_calls[0]["user"]
