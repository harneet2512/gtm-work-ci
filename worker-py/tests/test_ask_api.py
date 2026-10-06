"""POST /v1/ask through FastAPI with a scripted model and a fake core."""
from __future__ import annotations

from pathlib import Path

import yaml
from fastapi.testclient import TestClient

import ask_doubles as d
from ghost_worker.app import create_app
from ghost_worker.errors import ProviderUnavailableError
from ghost_worker.settings import Settings
from openapi_schemas import _spec, _validator

CASSETTES = Path(__file__).resolve().parents[1] / "cassettes"
BODY = {"text": "What changed at MedTech on Nov 9?", "channel_kind": "dm", "ask_token": d.ASK_TOKEN}


def client(provider, core: d.FakeAskCore | None = None) -> TestClient:
    cfg = Settings(_env_file=None, ghost_llm_mode="replay", cassette_dir=CASSETTES / "draft",
                   ghost_model="openrouter/qwen/qwen3.8-flash", core_url=d.CORE_URL)
    app = create_app(settings=cfg, provider=provider, core_transport=(core or d.FakeAskCore()).transport)
    return TestClient(app, raise_server_exceptions=False)


def validators():
    op = _spec()["paths"]["/v1/ask"]["post"]
    return (_validator(op["requestBody"]["content"]["application/json"]["schema"]),
            _validator(op["responses"]["200"]["content"]["application/json"]["schema"]))


def test_a_cited_answer_matches_the_contract_and_reports_usage() -> None:
    provider = d.ScriptedAskProvider([d.tools_turn(d.call(1, "account_state", account="MedTech")),
                                      d.final_turn("MedTech is in security review [t1].", ["t1"])])
    req_v, resp_v = validators()
    assert not list(req_v.iter_errors(BODY))
    response = client(provider).post("/v1/ask", json=BODY)
    assert response.status_code == 200, response.text
    body = response.json()
    assert not list(resp_v.iter_errors(body)), body
    assert body["citations"][0]["url"] == d.EPISODE_URL and body["tool_calls"] == 1 and body["dont_know"] is False
    assert body["usage"]["tool_calls"] == 0 or "usage" in body


def test_a_proposed_action_matches_the_contract() -> None:
    provider = d.ScriptedAskProvider([d.final_turn("I can play it.", action={"kind": "play_next", "summary": "Release the next event"})])
    body = client(provider).post("/v1/ask", json=BODY).json()
    assert body["proposed_action"]["kind"] == "play_next"
    assert not list(validators()[1].iter_errors(body))


def test_a_bad_request_is_a_422_that_never_echoes_the_question() -> None:
    response = client(d.ScriptedAskProvider([])).post("/v1/ask", json={**BODY, "channel_kind": "sms", "text": "SECRET-TEXT"})
    assert response.status_code == 422 and "SECRET-TEXT" not in response.text


def test_more_than_twelve_tool_calls_or_a_longer_deadline_is_refused() -> None:
    c = client(d.ScriptedAskProvider([]))
    assert c.post("/v1/ask", json={**BODY, "max_tool_calls": 13}).status_code == 422
    assert c.post("/v1/ask", json={**BODY, "deadline_s": 121}).status_code == 422
    ok = client(d.ScriptedAskProvider([d.final_turn("I don't know.", dont_know=True)]))
    assert ok.post("/v1/ask", json={**BODY, "max_tool_calls": 12, "deadline_s": 120}).status_code == 200


def test_the_conversation_history_is_part_of_the_contract_and_bounded() -> None:
    req_v, _ = validators()
    turns = [{"role": "user", "text": "What stage is MedTech in?"}, {"role": "cliff", "text": "Negotiation."}]
    assert not list(req_v.iter_errors({**BODY, "history": turns}))
    assert list(req_v.iter_errors({**BODY, "history": [{"role": "robot", "text": "x"}]}))
    assert list(req_v.iter_errors({**BODY, "history": turns * 7}))
    provider = d.ScriptedAskProvider([d.final_turn("I don't know.", dont_know=True)])
    c = client(provider)
    assert c.post("/v1/ask", json={**BODY, "history": turns}).status_code == 200
    assert "Person: What stage is MedTech in?" in provider.calls[0]["messages"][0]["content"]
    assert c.post("/v1/ask", json={**BODY, "history": turns * 7}).status_code == 422  # more than 12 entries
    assert c.post("/v1/ask", json={**BODY, "history": [{"role": "user", "text": "x" * 2001}]}).status_code == 422


def test_the_default_deadline_of_the_ask_endpoint_is_two_minutes() -> None:
    assert Settings(_env_file=None).ask_deadline_s == 120.0


def test_a_refused_ask_token_is_a_502_core_unavailable() -> None:
    provider = d.ScriptedAskProvider([d.tools_turn(d.call(1, "list_accounts"))])
    response = client(provider, d.FakeAskCore(status=401)).post("/v1/ask", json=BODY)
    assert response.status_code == 502 and response.json()["error"]["code"] == "core_unavailable"


def test_an_open_provider_breaker_is_a_non_retryable_424() -> None:
    class Down:
        def complete_turn(self, **_):
            raise ProviderUnavailableError("no credits")

    response = client(Down()).post("/v1/ask", json=BODY)
    assert response.status_code == 424


def test_ask_has_its_own_small_lane_that_never_takes_a_play_slot() -> None:
    app = client(d.ScriptedAskProvider([])).app
    ask_llm, draft_llm = app.state.ask_provider, app.state.draft_provider
    assert not ask_llm.shares_slots_with(draft_llm)
    assert not ask_llm.shares_slots_with(app.state.extract_provider)
    # Hold every slot of the ask lane: Play's lane is untouched.
    held = [ask_llm._slots.acquire(timeout=0.1) for _ in range(app.state.settings.ask_max_concurrency)]
    try:
        assert all(held) and not ask_llm._slots.acquire(timeout=0.01)
        assert draft_llm._slots.acquire(timeout=0.1)
        draft_llm._slots.release()
    finally:
        for _ in held:
            ask_llm._slots.release()


def test_ask_never_records_into_the_demo_cassettes_or_cache(tmp_path) -> None:
    from ghost_worker.llm.cache_provider import CachingProvider
    from ghost_worker.llm.factory import build_ask_provider, build_provider
    from ghost_worker.llm.fake_provider import RecordingProvider

    common = dict(_env_file=None, openrouter_api_key="sk-test-not-real", ghost_model="openrouter/qwen/qwen3.8-flash",
                  cassette_dir=tmp_path / "cassettes", ghost_llm_cache_dir=tmp_path / "demo-cache",
                  ask_cache_dir=tmp_path / "ask-cache", ghost_llm_cache_strict=True)
    record = Settings(ghost_llm_mode="record", **common)
    assert isinstance(build_provider(record), RecordingProvider)
    assert not isinstance(build_ask_provider(record), RecordingProvider)
    cache = build_ask_provider(Settings(ghost_llm_mode="cache", **common))
    assert isinstance(cache, CachingProvider)
    assert cache.directory == tmp_path / "ask-cache" and cache.directory != tmp_path / "demo-cache"
    assert cache.strict is False  # the demo's strict verify cannot see, or be broken by, a question
    assert not (tmp_path / "cassettes").exists() and not (tmp_path / "demo-cache").exists()


def test_the_operation_is_in_the_worker_contract() -> None:
    spec = yaml.safe_load((Path(__file__).resolve().parents[2] / "contracts/openapi/worker.yaml").read_text(encoding="utf-8"))
    assert spec["paths"]["/v1/ask"]["post"]["operationId"] == "askCliff"
