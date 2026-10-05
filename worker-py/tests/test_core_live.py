"""The worker's real CoreContextClient and draft agent against the real Go core over HTTP (HAR-107 / D4).

Skipped unless the environment names a running core, a run token and the world it was started over. The Go
test internal/api/contracttest/worker_test.go starts the core on the ingested CRMArena sample, mints a run
token for one account and runs this file, so CI needs Postgres, Go and Python in one job. No model is called:
the agent's turns are scripted (draft_doubles.ScriptedProvider) from ids read back from the core.
"""
from __future__ import annotations

import os
from typing import Any

import pytest

import draft_doubles as dbl
from draft_helpers import assert_contract_valid
from ghost_worker.draft.core_client import TOOL_NAMES, ContextPacket, CoreContextClient, ToolParams
from ghost_worker.draft.deadline import Deadline
from ghost_worker.draft.grounding import PullLog
from ghost_worker.draft.service import draft_action
from ghost_worker.errors import CoreContextError
from ghost_worker.models.draft import DraftRequest
from test_contracts import validator

CORE_URL = os.environ.get("GHOST_CORE_URL", "")
RUN_TOKEN = os.environ.get("GHOST_RUN_TOKEN", "")
RUN_ID = os.environ.get("GHOST_RUN_ID", "")
ACCOUNT_ID = os.environ.get("GHOST_ACCOUNT_ID", "")
OTHER_ACCOUNT_ID = os.environ.get("GHOST_OTHER_ACCOUNT_ID", "")
TRIGGER_ID = os.environ.get("GHOST_TRIGGER_ACTIVITY_ID", "")

pytestmark = pytest.mark.skipif(not (CORE_URL and RUN_TOKEN and RUN_ID and ACCOUNT_ID and TRIGGER_ID),
                                reason="needs a running core: see internal/api/contracttest/worker_test.go")


def client(token: str = RUN_TOKEN) -> CoreContextClient:
    return CoreContextClient(CORE_URL, token, timeout_s=10.0, max_bytes=16384)


def pull(tool: str, **params: Any) -> ContextPacket:
    with client() as c:
        return c.pull(tool, ToolParams(**params), timeout_s=10.0)  # type: ignore[arg-type]


def test_every_tool_returns_a_packet_the_worker_and_the_contract_accept() -> None:
    check = validator("context_packet")
    for tool in TOOL_NAMES:
        params = {"field_path": "blockers"} if tool == "evidence" else {}
        packet = pull(tool, **params)
        assert packet.tool == tool and packet.access_id >= 1
        assert not list(check.iter_errors(packet.model_dump(mode="json", exclude_none=True)))
        assert packet.bytes <= 12 << 10


def test_state_is_the_real_account_state_of_the_runs_account() -> None:
    packet = pull("state", limit=20)
    header, *fields = (dict(item) for item in packet.items)
    assert header["kind"] == "state_header" and header["account_id"] == ACCOUNT_ID
    assert header["version"] >= 1 and fields
    known = [f for f in fields if f["known"]]
    assert known, "the sample account has no known state field"
    assert all(f["evidence_refs"] for f in known), "a known field without evidence"
    assert {f["field_path"] for f in fields} <= {"stage", "health", "owner", "motion", "champion",
                                                 "champion_status", "economic_buyer", "blockers", "objections",
                                                 "decision_criteria", "decision_process", "current_commitments",
                                                 "next_milestone", "next_meeting", "relationship_risk",
                                                 "product_use_case", "commercial_issue",
                                                 "last_customer_interaction", "last_meaningful_change", "summary"}
    log = PullLog().record(packet)
    assert log.activity_ids, "the agent could not cite any activity from the state pull"


def test_the_trigger_activity_comes_first_in_activities_and_nothing_of_another_account_leaks() -> None:
    packet = pull("activities", limit=20)
    first = dict(packet.items[0])
    assert first["activity_id"] == TRIGGER_ID and first["is_trigger"] is True
    if OTHER_ACCOUNT_ID:
        for tool in TOOL_NAMES:
            params = {"field_path": "stage"} if tool == "evidence" else {"limit": 20}
            assert OTHER_ACCOUNT_ID not in pull(tool, **params).model_dump_json()


def test_the_client_has_no_way_to_ask_for_another_account_and_a_bad_token_is_refused() -> None:
    with pytest.raises(ValueError):
        from ghost_worker.draft.core_client import tool_params
        tool_params({"account_id": OTHER_ACCOUNT_ID or ACCOUNT_ID})
    with client("rt1." + RUN_ID + ".9999999999." + "0" * 64) as c, pytest.raises(CoreContextError, match="401"):
        c.pull("state", ToolParams(), timeout_s=5.0)


def _first_evidence(packet: ContextPacket) -> dict[str, Any]:
    for item in packet.items:
        for ref in dict(item).get("evidence_refs") or []:
            if ref.get("activity_id"):
                return dbl.ev(ref["activity_id"], ref.get("claim_id"), None, None, ref.get("occurred_at"))
    raise AssertionError("no evidence reference in the state packet")


def test_the_real_draft_agent_decides_from_real_core_context() -> None:
    state, people = pull("state", limit=20), pull("people")
    person = next((dict(p)["person_id"] for p in people.items), None)
    assert person, "the sample account's buying group is empty"
    evidence = [_first_evidence(state)]
    why = "Real buying-group member pulled from core"
    provider = dbl.ScriptedProvider(
        [dbl.tools_turn(dbl.call(1, "state", limit=20), dbl.call(2, "people"), dbl.call(3, "activities", limit=5)),
         dbl.decision_turn("send_email", "Follow up with the buying-group member the core returned.",
                           involve=((person, why),), evidence=evidence)],
        dbl.skill_output(recipients=[(person, "to", why)], subject="Following up", body="Hi,\n\nFollowing up.\n\nBest",
                         next_step="Follow up", reason="Grounded in the account state the core returned.",
                         evidence=evidence))
    request = DraftRequest.model_validate({
        "run_id": RUN_ID, "account_id": ACCOUNT_ID, "workflow": "post_interaction_followup",
        "trigger_context": {"trigger_activity_ids": [TRIGGER_ID], "signal_types": ["customer_replied"],
                            "reason_codes": ["eligible_customer_replied"], "rep_person_id": dbl.uid("0b0e0000", 1)},
        "state_header": "Real account state is pulled from core.", "run_token": RUN_TOKEN})
    with client() as core:
        response = draft_action(request, provider, core, deadline=Deadline(30))
    assert response.tool_calls == 3
    assert [r.person_id for r in response.output.recipients] == [person]
    assert response.cited_access_ids, "the proposal cites no logged pull"
    assert_contract_valid(response)
