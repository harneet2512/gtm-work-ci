"""Pydantic mirrors of the draft contracts: parity with the JSON Schemas and the worker.yaml bodies."""
from __future__ import annotations

import copy

import pytest
from pydantic import ValidationError

from ghost_worker.models.draft import (
    QUIET_ACTIONS,
    SKILL_ACTIONS,
    AgentRunOutput,
    DecisionGuidance,
    DraftRequest,
    DraftResponse,
)
from openapi_schemas import draft_request_validator, draft_response_validator
from test_contracts import SCHEMAS, example, load_json, validator

RUN_TOKEN = "t" * 40


def request_body(**overrides: object) -> dict:
    body = {
        "run_id": "0f0a0000-0000-4000-8000-000000000601",
        "account_id": "0a0c0000-0000-4000-8000-000000000001",
        "workflow": "post_interaction_followup",
        "trigger_context": {"trigger_activity_ids": ["0ac70000-0000-4000-8000-000000000101"],
                            "signal_types": ["new_stakeholder_entered"],
                            "reason_codes": ["eligible_stakeholder_change"],
                            "rep_person_id": "0b0e0000-0000-4000-8000-000000000001"},
        "state_header": "Acme Corp: Technical evaluation; champion Priya (active).",
        "run_token": RUN_TOKEN,
    }
    return {**body, **overrides}


def test_request_body_conforms_to_openapi_and_model() -> None:
    body = request_body(decision_guidance=example("decision_guidance"), max_tool_calls=4)
    assert not list(draft_request_validator().iter_errors(body))
    parsed = DraftRequest.model_validate(body)
    assert parsed.max_tool_calls == 4 and parsed.decision_guidance is not None


def test_request_defaults_and_optional_guidance() -> None:
    parsed = DraftRequest.model_validate(request_body())
    assert parsed.max_tool_calls == 6 and parsed.decision_guidance is None


@pytest.mark.parametrize("override", [
    {"run_token": "short"}, {"run_token": "t" * 31 + "\n"}, {"run_token": "t" * 40 + " x"},
    {"max_tool_calls": 0}, {"max_tool_calls": 13}, {"state_header": "x" * 2001},
    {"workflow": "other"}, {"core_url": "http://evil.example"}, {"account_id": "nope"},
    {"trigger_context": {"trigger_activity_ids": [], "signal_types": [], "reason_codes": []}},
])
def test_request_rejects_invalid(override: dict) -> None:
    with pytest.raises(ValidationError):
        DraftRequest.model_validate(request_body(**override))


def test_guidance_model_accepts_the_example_and_round_trips() -> None:
    guidance = DecisionGuidance.model_validate(example("decision_guidance"))
    dumped = guidance.model_dump(mode="json", exclude_none=True)
    assert not list(validator("decision_guidance").iter_errors(dumped))
    assert guidance.supporting_knowledge[0].applies is True


def test_output_model_round_trips_the_example() -> None:
    output = AgentRunOutput.model_validate(example("agent_run_output"))
    assert not list(validator("agent_run_output").iter_errors(output.model_dump(mode="json", exclude_none=True)))


def test_output_enforces_email_rule() -> None:
    body = copy.deepcopy(example("agent_run_output"))
    body["recipients"] = []
    with pytest.raises(ValidationError, match="recipient"):
        AgentRunOutput.model_validate(body)
    body = copy.deepcopy(example("agent_run_output"))
    body["finished_artifact"]["channel"] = "slack"
    with pytest.raises(ValidationError, match="email"):
        AgentRunOutput.model_validate(body)


def test_output_requires_evidence_unless_wait_or_no_action() -> None:
    body = copy.deepcopy(example("agent_run_output"))
    body["evidence_refs"] = []
    with pytest.raises(ValidationError, match="evidence"):
        AgentRunOutput.model_validate(body)
    for action in ("wait", "no_action"):
        quiet = {**body, "proposed_action_type": action, "recipients": [],
                 "finished_artifact": {"channel": "none", "body": ""}}
        assert AgentRunOutput.model_validate(quiet).evidence_refs == ()


def test_output_rejects_bad_timestamp_and_uuid() -> None:
    body = copy.deepcopy(example("agent_run_output"))
    for bad in ("tomorrow", "2026-10-06T09:00:00"):  # not a timestamp / no timezone
        with pytest.raises(ValidationError):
            AgentRunOutput.model_validate({**body, "wait_until": bad})
    body = copy.deepcopy(example("agent_run_output"))
    body["recipients"][0]["person_id"] = "marco"
    with pytest.raises(ValidationError):
        AgentRunOutput.model_validate(body)


DECISION = {"action": "send_email", "why_now": "Marco asked for SOC2.", "used_guidance": False,
            "who_to_involve": [{"person_id": "0b0e0000-0000-4000-8000-000000000018", "why": "asked"}],
            "who_not_to_involve": []}


def make_response(**overrides: object) -> DraftResponse:
    base = {"output": AgentRunOutput.model_validate(example("agent_run_output")), "decision": DECISION,
            "model": "deepseek/deepseek-v4-flash", "tool_calls": 1, "cited_access_ids": [101, 103]}
    return DraftResponse(**{**base, **overrides})


def test_response_conforms_to_openapi() -> None:
    dumped = make_response().model_dump(mode="json", exclude_none=True)
    assert dumped["draft_index"] == 1 and dumped["cited_access_ids"] == [101, 103]
    assert "context_refs" not in dumped  # AgentRun.input_context_refs comes only from core's access log
    assert not list(draft_response_validator().iter_errors(dumped)), dumped
    assert list(draft_response_validator().iter_errors({**dumped, "draft_index": 2}))


def test_response_decision_is_the_agent_run_draft_decision_definition() -> None:
    decision = make_response().model_dump(mode="json", exclude_none=True)["decision"]
    definition = load_json(SCHEMAS / "agent_run_draft.v1.json")["$defs"]["decision"]
    assert set(decision) <= set(definition["properties"]) and set(definition["required"]) <= set(decision)
    draft = {**example("agent_run_draft"), "decision": decision}
    assert not list(validator("agent_run_draft").iter_errors(draft))
    assert list(validator("agent_run_draft").iter_errors({**draft, "decision": {**decision, "action": "spam"}}))


def test_decision_must_name_who_to_involve_and_not_involve() -> None:
    for missing in ("who_to_involve", "who_not_to_involve"):
        with pytest.raises(ValidationError):
            make_response(decision={k: v for k, v in DECISION.items() if k != missing})


def test_rep_profile_is_optional_style_hints() -> None:
    body = request_body(rep_profile={"brevity": "short", "examples": ["Hi - quick one."]})
    assert not list(draft_request_validator().iter_errors(body))
    parsed = DraftRequest.model_validate(body)
    assert parsed.rep_profile.brevity == "short" and parsed.rep_profile.examples == ("Hi - quick one.",)
    assert DraftRequest.model_validate(request_body()).rep_profile is None


@pytest.mark.parametrize("profile", [{"tone": "x"}, {"brevity": "x" * 201}, {"examples": ["x"] * 6},
                                     {"examples": ["x" * 2001]}])
def test_rep_profile_is_bounded(profile: dict) -> None:
    assert list(draft_request_validator().iter_errors(request_body(rep_profile=profile)))
    with pytest.raises(ValidationError):
        DraftRequest.model_validate(request_body(rep_profile=profile))


def test_quiet_and_action_decisions_partition_the_action_vocabulary() -> None:
    vocabulary = set(load_json(SCHEMAS / "agent_run_output.v1.json")["properties"]["proposed_action_type"]["enum"])
    assert QUIET_ACTIONS | SKILL_ACTIONS == vocabulary and not QUIET_ACTIONS & SKILL_ACTIONS
    assert QUIET_ACTIONS == {"wait", "no_action"}
    needs_evidence = set(load_json(SCHEMAS / "agent_run_output.v1.json")["allOf"][1]["if"]["properties"][
        "proposed_action_type"]["not"]["enum"])
    assert needs_evidence == QUIET_ACTIONS  # the schema's "any action except wait/no_action" rule
