"""POST /v1/strategies and /v1/revise through FastAPI with a scripted model and a fake core (no network)."""
from __future__ import annotations

import copy
from pathlib import Path

from fastapi.testclient import TestClient

import draft_doubles as dbl
import strategy_doubles as sd
from ghost_worker.app import create_app
from ghost_worker.errors import ProviderUnavailableError
from ghost_worker.llm.provider import TurnResult
from ghost_worker.settings import Settings
from openapi_schemas import request_validator, response_validator
from test_contracts import example

CASSETTES = Path(__file__).resolve().parents[1] / "cassettes"


def client(provider, core: dbl.FakeCore | None = None) -> TestClient:
    cfg = Settings(_env_file=None, ghost_llm_mode="replay", cassette_dir=CASSETTES / "draft",
                   ghost_model="openrouter/deepseek/deepseek-v4-flash", core_url=dbl.CORE_URL)
    app = create_app(settings=cfg, provider=provider, core_transport=(core or sd.fake_core()).transport)
    return TestClient(app, raise_server_exceptions=False)


def pulls() -> list[TurnResult]:
    return [dbl.tools_turn(dbl.call(1, "state"), dbl.call(2, "people")), dbl.tools_turn(dbl.call(3, "activities"))]


def scripted(plan: TurnResult | None = None, artifacts=None) -> sd.ScriptedStrategyProvider:
    return sd.ScriptedStrategyProvider([*pulls(), plan or sd.default_plan()],
                                       {"strategy_artifacts_v1": artifacts or sd.artifacts_json()})


def error_code(response) -> str:
    return response.json()["error"]["code"]


def test_three_distinct_complete_candidates_that_match_the_contract() -> None:
    provider = scripted()
    request = sd.strategies_request()
    assert not list(request_validator("/v1/strategies").iter_errors(request))
    response = client(provider).post("/v1/strategies", json=request)
    assert response.status_code == 200, response.text
    body = response.json()
    assert not list(response_validator("/v1/strategies").iter_errors(body))
    cands = body["candidates"]
    assert [c["ranking"] for c in cands] == [1, 2, 3]
    assert [c["preferred_by_agent"] for c in cands] == [True, False, False]
    assert len({c["strategy_type"] for c in cands}) == 3
    first = cands[0]
    assert [r["person_id"] for r in first["to"]] == [sd.SECURITY] and [r["person_id"] for r in first["cc"]] == [sd.CHAMPION]
    assert first["subject"] == first["full_action_artifact"]["subject"] and first["preview"]
    assert "draft_index" not in first and "eval_bundle_ref" not in first
    assert body["tool_calls"] == 3 and len(provider.json_calls) == 1


def test_style_hints_reach_only_the_drafting_step() -> None:
    provider = scripted()
    request = sd.strategies_request()
    request["rep_profile"] = {"brevity": "very short", "examples": ["REP-STYLE-MARKER"]}
    assert client(provider).post("/v1/strategies", json=request).status_code == 200
    planner_text = " ".join(str(c["messages"]) + c["system"] for c in provider.turn_calls)
    assert "REP-STYLE-MARKER" not in planner_text
    assert "REP-STYLE-MARKER" in provider.json_calls[0]["user"]


def test_knowledge_is_recorded_only_when_it_applies_and_no_exception_fired() -> None:
    guidance = copy.deepcopy(example("decision_guidance"))
    guidance.update({"account_id": sd.ACCOUNT, "supporting_knowledge": [
        {"knowledge_id": sd.K_APPLIED, "applies": True, "matched_conditions": ["motion eq expansion"],
         "exceptions_checked": []},
        {"knowledge_id": sd.K_EXCEPTION, "applies": False, "matched_conditions": ["motion eq expansion"],
         "exceptions_checked": [{"exception": "delegated", "triggered": True}]}]})
    plan = sd.plan_turn(sd.planned("send_package_and_wait", knowledge=[sd.K_APPLIED, sd.K_EXCEPTION]),
                        sd.planned("add_security_lead", knowledge=[sd.K_EXCEPTION]),
                        sd.planned("clarify_scope_first", cc=()))
    body = client(scripted(plan)).post("/v1/strategies", json=sd.strategies_request(guidance)).json()
    assert [c["knowledge_refs"] for c in body["candidates"]] == [[sd.K_APPLIED], [], []]


def test_a_wait_strategy_has_no_artifact_to_write() -> None:
    plan = sd.plan_turn(sd.planned("send_package_and_wait"), sd.planned("wait_for_buyer", "wait", to=(), cc=()),
                        sd.planned("clarify_scope_first", cc=()))
    artifacts = sd.artifacts_json({k: v for k, v in sd.DISTINCT_BODIES.items() if k != "add_security_lead"})
    body = client(scripted(plan, artifacts)).post("/v1/strategies", json=sd.strategies_request()).json()
    quiet = body["candidates"][1]
    assert quiet["action_type"] == "wait" and quiet["full_action_artifact"]["channel"] == "none"
    assert quiet["to"] == [] and quiet["subject"] is None and quiet["preview"]


def test_near_duplicate_bodies_are_rejected_after_one_corrected_retry() -> None:
    same = sd.DISTINCT_BODIES["send_package_and_wait"]
    dup = sd.artifacts_json({"send_package_and_wait": same, "add_security_lead": same + " Thanks.",
                             "clarify_scope_first": same})
    plan = sd.plan_turn(sd.planned("send_package_and_wait"), sd.planned("add_security_lead"),
                        sd.planned("clarify_scope_first"))
    provider = sd.ScriptedStrategyProvider([*pulls(), plan], {"strategy_artifacts_v1": [dup, dup]})
    response = client(provider).post("/v1/strategies", json=sd.strategies_request())
    assert response.status_code == 502 and error_code(response) == "invalid_strategies"
    assert len(provider.json_calls) == 2 and "Correction" in provider.json_calls[1]["user"]


def test_a_retry_that_fixes_the_duplicates_succeeds() -> None:
    same = sd.DISTINCT_BODIES["send_package_and_wait"]
    dup = sd.artifacts_json({"send_package_and_wait": same, "add_security_lead": same, "clarify_scope_first": same})
    provider = sd.ScriptedStrategyProvider([*pulls(), sd.default_plan()],
                                           {"strategy_artifacts_v1": [dup, sd.artifacts_json()]})
    assert client(provider).post("/v1/strategies", json=sd.strategies_request()).status_code == 200


def test_a_repeated_strategy_type_gets_one_replan_then_fails() -> None:
    twice = sd.plan_turn(sd.planned("same_move"), sd.planned("same_move"), sd.planned("other_move", cc=()))
    provider = sd.ScriptedStrategyProvider([*pulls(), twice, twice], {})
    response = client(provider).post("/v1/strategies", json=sd.strategies_request())
    assert response.status_code == 502 and error_code(response) == "invalid_strategies"


def test_a_person_no_pull_returned_is_ungrounded() -> None:
    plan = sd.plan_turn(sd.planned("send_package_and_wait", to=((sd.OUTSIDER, "stranger"),)),
                        sd.planned("add_security_lead"), sd.planned("clarify_scope_first", cc=()))
    provider = scripted(plan)
    provider.turns.append(plan)  # the one corrective turn gets the same answer
    response = client(provider).post("/v1/strategies", json=sd.strategies_request())
    assert response.status_code == 502 and error_code(response) == "ungrounded_proposal"


def test_evidence_that_no_pull_returned_is_dropped_and_none_left_is_ungrounded() -> None:
    bad = sd.planned("send_package_and_wait")
    bad["evidence_refs"] = [dbl.ev("0d000000-0000-4000-8000-0000000009ff", None, "invented")]
    plan = sd.plan_turn(bad, sd.planned("add_security_lead"), sd.planned("clarify_scope_first", cc=()))
    response = client(scripted(plan)).post("/v1/strategies", json=sd.strategies_request())
    assert response.status_code == 502 and error_code(response) == "ungrounded_proposal"


def test_an_open_provider_breaker_is_a_non_retryable_424() -> None:
    class Down:
        def complete_turn(self, **_):
            raise ProviderUnavailableError("breaker open")

    response = client(Down()).post("/v1/strategies", json=sd.strategies_request())
    assert response.status_code == 424 and error_code(response) == "provider_unavailable_nonretryable"


def test_the_request_must_ask_for_exactly_three_candidates() -> None:
    request = sd.strategies_request()
    request["candidate_count"] = 4
    assert client(scripted()).post("/v1/strategies", json=request).status_code == 422


def revise_request() -> dict:
    return {"run_id": sd.RUN_ID, "account_id": sd.ACCOUNT, "draft_index": 1, "candidate": sd.candidate_json(),
            "feedback": [{"eval_result_id": "0e1a0000-0000-4000-8000-000000000911",
                          "instruction": "Remove the signing ask."}], "run_token": dbl.RUN_TOKEN}


def revision(**over) -> dict:
    body = {"to": dbl.people((sd.SECURITY, "asked")), "cc": [], "rationale": "Removed the signing ask.",
            "artifact": sd.artifact("Hi Person B,\n\nHere are the answers. No other ask.\n\nBest,\nDana")}
    body.update(over)
    return body


def test_revise_returns_the_same_candidate_with_a_corrected_action() -> None:
    provider = sd.ScriptedStrategyProvider([], {"strategy_revision_v1": revision()})
    request = revise_request()
    assert not list(request_validator("/v1/revise").iter_errors(request))
    response = client(provider).post("/v1/revise", json=request)
    assert response.status_code == 200, response.text
    body = response.json()
    assert not list(response_validator("/v1/revise").iter_errors(body))
    revised = body["candidate"]
    assert revised["candidate_id"] == request["candidate"]["candidate_id"] and revised["ranking"] == 1
    assert revised["cc"] == [] and "No other ask" in revised["full_action_artifact"]["body"]
    assert revised["evidence_refs"] == request["candidate"]["evidence_refs"]
    assert "Remove the signing ask." in provider.json_calls[0]["user"]


def test_revise_cannot_name_a_person_the_core_did_not_return() -> None:
    provider = sd.ScriptedStrategyProvider([], {"strategy_revision_v1": revision(to=dbl.people((sd.OUTSIDER, "x")))})
    response = client(provider).post("/v1/revise", json=revise_request())
    assert response.status_code == 502 and error_code(response) == "ungrounded_proposal"


def with_transition(status: str) -> dict:
    request = sd.strategies_request()
    transition = copy.deepcopy(example("state_transition"))
    transition.update({"status": status, "account_id": sd.ACCOUNT})
    request["state_transition"] = transition
    return request


def planner_prompt(provider) -> str:
    return " ".join(str(c["messages"]) for c in provider.turn_calls)


def test_the_transition_and_the_candidate_policy_reach_the_planner_while_it_is_open() -> None:
    request = with_transition("CANDIDATE")
    assert not list(request_validator("/v1/strategies").iter_errors(request)), "contract"
    provider = scripted()
    assert client(provider).post("/v1/strategies", json=request).status_code == 200
    text = planner_prompt(provider)
    assert "State transition (read it" in text and "Transition policy while the status is CANDIDATE" in text
    assert request["state_transition"]["id"] in text


def test_a_confirmed_transition_carries_no_candidate_policy() -> None:
    provider = scripted()
    assert client(provider).post("/v1/strategies", json=with_transition("CONFIRMED")).status_code == 200
    text = planner_prompt(provider)
    assert "State transition (read it" in text and "Transition policy" not in text


def test_without_a_transition_the_prompt_is_unchanged() -> None:
    provider = scripted()
    assert client(provider).post("/v1/strategies", json=sd.strategies_request()).status_code == 200
    assert "State transition" not in planner_prompt(provider)


def test_every_candidate_answers_the_five_questions() -> None:
    body = client(scripted()).post("/v1/strategies", json=sd.strategies_request()).json()
    for cand in body["candidates"]:
        assert set(cand["five_questions"]) == set(sd.FIVE) and all(cand["five_questions"].values())


def test_an_empty_answer_to_one_of_the_five_questions_is_not_a_usable_plan() -> None:
    bad = sd.planned("send_package_and_wait")
    bad["five_questions"] = {**sd.FIVE, "what_remains_unknown": ""}
    plan = sd.plan_turn(bad, sd.planned("add_security_lead"), sd.planned("clarify_scope_first", cc=()))
    response = client(scripted(plan)).post("/v1/strategies", json=sd.strategies_request())
    assert response.status_code == 502


def test_a_decision_class_its_action_cannot_carry_is_invalid() -> None:
    plan = sd.plan_turn(sd.planned("send_package_and_wait", action_class="WAIT"), sd.planned("add_security_lead"),
                        sd.planned("clarify_scope_first", cc=()))
    response = client(scripted(plan)).post("/v1/strategies", json=sd.strategies_request())
    assert response.status_code == 502 and error_code(response) == "invalid_strategies"


def test_the_draft_request_accepts_the_transition_too() -> None:
    from openapi_schemas import draft_request_validator
    import draft_situations as sit
    request = sit.ACME_GUIDED.request_copy()
    request["state_transition"] = with_transition("CANDIDATE")["state_transition"]
    assert not list(draft_request_validator().iter_errors(request))
