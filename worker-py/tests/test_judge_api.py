"""POST /v1/judge: the semantic evals of one candidate as EvalBundle items (scripted judges, fake core)."""
from __future__ import annotations

import copy

import draft_doubles as dbl
import judge_doubles as jd
import strategy_doubles as sd
from ghost_worker.errors import InvalidModelOutputError
from openapi_schemas import request_validator, response_validator
from test_contracts import example, validator
from test_strategies_api import client


def judge_request(**over) -> dict:
    body = {"run_id": sd.RUN_ID, "account_id": sd.ACCOUNT, "draft_index": 2, "candidate": sd.candidate_json(),
            "run_token": dbl.RUN_TOKEN,
            "trigger_context": {"trigger_activity_ids": [sd.TRIGGER], "signal_types": ["customer_replied"],
                                "reason_codes": ["eligible_customer_replied"], "rep_person_id": sd.REP}}
    body.update(over)
    return body


def post(provider, **over):
    return client(provider).post("/v1/judge", json=judge_request(**over))


def by_type(body: dict) -> dict:
    return {i["eval_type"]: i for i in body["items"]}


def test_selected_evals_carry_results_and_the_rest_say_why_they_are_not_relevant() -> None:
    request = judge_request()
    assert not list(request_validator("/v1/judge").iter_errors(request))
    response = post(sd.ScriptedStrategyProvider([]))
    assert response.status_code == 200, response.text
    body = response.json()
    assert not list(response_validator("/v1/judge").iter_errors(body))
    items = by_type(body)
    ran = {t: i for t, i in items.items() if i["verdict"] != "not_relevant"}
    skipped = {t: i for t, i in items.items() if i["verdict"] == "not_relevant"}
    assert ran and skipped and len(items) == len(body["items"])
    assert all(i["result"]["draft_index"] == 2 and i["result"]["agent_run_id"] == sd.RUN_ID for i in ran.values())
    assert all(i["result"] is None and i["relevance_reason"] for i in skipped.values())
    bundle = {"id": sd.RUN_ID, "agent_run_id": sd.RUN_ID, "draft_index": 2,
              "strategy_candidate_id": sd.candidate_json()["candidate_id"], "items": body["items"],
              "generated_at": "2026-09-29T15:30:00Z"}
    assert not list(validator("eval_bundle").iter_errors(bundle))


def test_the_judges_see_the_event_time_not_the_wall_clock() -> None:
    provider = sd.ScriptedStrategyProvider([])
    assert post(provider).status_code == 200
    assert provider.json_calls and all('"now": "2026-09-29T15:00:00Z"' in c["user"] for c in provider.json_calls)


def test_a_blocking_semantic_failure_is_returned_as_blocking() -> None:
    br = jd.RUBRICS["buyer_readiness"]
    fail = jd.answer(br, verdict="fail", label="TOO_EARLY", diagnostics=["too_early"], blocks=True,
                     suggested_correction="Drop the signing ask.")
    items = by_type(post(sd.ScriptedStrategyProvider([], {br.schema_name: fail})).json())
    result = items["buyer_readiness"]["result"]
    assert items["buyer_readiness"]["verdict"] == "fail" and result["blocking"] is True
    assert result["suggested_correction"] == "Drop the signing ask."


def test_a_judge_that_fails_is_unknown_and_cannot_block() -> None:
    br = jd.RUBRICS["buyer_readiness"]
    items = by_type(post(sd.ScriptedStrategyProvider([], {br.schema_name: InvalidModelOutputError("bad json")})).json())
    item = items["buyer_readiness"]
    assert item["verdict"] == "unknown" and item["result"]["blocking"] is False
    assert item["result"]["judged_object"]["type"] == "StrategyCandidate" and item["result"]["span_id"].startswith("candidates:")
    assert item["result"]["model"] is None and "did not return a usable answer" in item["result"]["reason"]
    assert not list(validator("eval_result").iter_errors(item["result"]))


def test_offered_knowledge_routes_the_knowledge_evals() -> None:
    knowledge = copy.deepcopy(example("knowledge"))
    plain = by_type(post(sd.ScriptedStrategyProvider([])).json())
    assert plain["knowledge_applicability"]["verdict"] == "not_relevant"
    offered = by_type(post(sd.ScriptedStrategyProvider([]), offered_knowledge=[knowledge]).json())
    assert offered["knowledge_applicability"]["verdict"] != "not_relevant"


def test_a_core_that_refuses_the_token_is_a_core_error_not_a_verdict() -> None:
    core = sd.fake_core()
    core.status = 403
    response = client(sd.ScriptedStrategyProvider([]), core).post("/v1/judge", json=judge_request())
    assert response.status_code == 502 and response.json()["error"]["code"] == "core_unavailable"


def test_evals_outside_the_selected_suite_are_not_judged() -> None:
    everything = by_type(post(sd.ScriptedStrategyProvider([])).json())
    ran = sorted(t for t, i in everything.items() if i["verdict"] != "not_relevant")
    dropped = ran[0]
    suite = {"name": "expansion_candidate", "excluded_eval_types": [dropped]}
    request = judge_request(eval_suite=suite)
    assert not list(request_validator("/v1/judge").iter_errors(request))
    provider = sd.ScriptedStrategyProvider([])
    items = by_type(client(provider).post("/v1/judge", json=request).json())
    assert items[dropped]["verdict"] == "not_relevant" and items[dropped]["result"] is None
    assert "expansion_candidate suite" in items[dropped]["relevance_reason"]
    assert len(provider.json_calls) == len(ran) - 1
