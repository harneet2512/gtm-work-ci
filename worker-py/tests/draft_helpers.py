"""Shared helpers for the account-agent tests: run a draft against a fake core, replay a situation's cassettes."""
from __future__ import annotations

from pathlib import Path

import draft_doubles as dbl
import draft_situations as sit
from ghost_worker.draft.core_client import CoreContextClient
from ghost_worker.draft.deadline import Deadline
from ghost_worker.draft.service import draft_action
from ghost_worker.llm.fake_provider import FakeProvider
from ghost_worker.models.draft import DraftRequest, DraftResponse
from openapi_schemas import draft_response_validator
from test_contracts import validator

CASSETTES = Path(__file__).resolve().parents[1] / "cassettes" / "draft"
FAMILY = "deepseek-v4-flash"


def run(provider, core: dbl.FakeCore, body: dict, *, deadline: Deadline | None = None) -> DraftResponse:
    request = DraftRequest.model_validate(body)
    with CoreContextClient(dbl.CORE_URL, request.run_token, timeout_s=2.0, max_bytes=16384,
                           transport=core.transport) as client:
        return draft_action(request, provider, client, deadline=deadline or Deadline(30))


def replay(situation: sit.Situation, *, body: dict | None = None) -> tuple[DraftResponse, dbl.FakeCore]:
    core = dbl.FakeCore(situation.packets)
    return run(FakeProvider(CASSETTES, FAMILY), core, body or situation.request_copy()), core


def assert_contract_valid(response: DraftResponse) -> dict:
    dumped = response.model_dump(mode="json", exclude_none=True)
    assert not list(draft_response_validator().iter_errors(dumped))
    assert not list(validator("agent_run_output").iter_errors(dumped["output"]))
    return dumped


def recipients(response: DraftResponse) -> list[tuple[str, str]]:
    return [(r.person_id, r.role) for r in response.output.recipients]


def acme_core() -> dbl.FakeCore:
    return dbl.FakeCore(sit.ACME_PACKETS)


def acme_provider(skill: dict | None, decision=None) -> dbl.ScriptedProvider:
    decision = decision or dbl.decision_turn("send_email", "Marco needs the package.",
                                             involve=((sit.MARCO, "Requested the documents"),),
                                             evidence=sit.ACME_EVIDENCE)
    return dbl.ScriptedProvider([dbl.tools_turn(dbl.call(1, "state"), dbl.call(2, "people"),
                                                dbl.call(3, "evidence"), dbl.call(4, "activities")), decision], skill)
