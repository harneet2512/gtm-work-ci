"""POST /v1/draft through FastAPI: envelope, size, concurrency and deadline patterns of /v1/extract."""
from __future__ import annotations

import copy
import logging
import time
from concurrent.futures import ThreadPoolExecutor
from pathlib import Path

import pytest
from fastapi.testclient import TestClient

import draft_doubles as dbl
import draft_situations as sit
from ghost_worker.app import MAX_BODY_BYTES, create_app
from ghost_worker.llm.provider import LLMResult, TurnResult
from ghost_worker.settings import Settings
from openapi_schemas import draft_response_validator
from test_offline_replay import _block_network

CASSETTES = Path(__file__).resolve().parents[1] / "cassettes"


def settings(**kw: object) -> Settings:
    base = {"ghost_llm_mode": "replay", "cassette_dir": CASSETTES / "draft",
            "ghost_model": "openrouter/deepseek/deepseek-v4-flash", "core_url": dbl.CORE_URL}
    return Settings(_env_file=None, **{**base, **kw})


def make_client(situation: sit.Situation = sit.ACME_GUIDED, *, core: dbl.FakeCore | None = None, provider=None,
                **cfg: object) -> tuple[TestClient, dbl.FakeCore]:
    core = core or dbl.FakeCore(situation.packets)
    app = create_app(settings=settings(**cfg), provider=provider, core_transport=core.transport)
    return TestClient(app, raise_server_exceptions=False), core


def assert_envelope(body: dict, code: str) -> None:
    assert set(body) == {"error"} and body["error"]["code"] == code and body["error"]["message"]


def test_draft_replays_the_guided_acme_cassettes_and_matches_the_contract() -> None:
    client, core = make_client()
    response = client.post("/v1/draft", json=sit.ACME_GUIDED.request_copy())
    assert response.status_code == 200, response.text
    body = response.json()
    assert not list(draft_response_validator().iter_errors(body))
    assert body["draft_index"] == 1
    assert body["decision"]["action"] == "send_email" and body["decision"]["used_guidance"] is True
    assert [p["person_id"] for p in body["decision"]["who_to_involve"]] == [sit.MARCO, sit.PRIYA]
    assert body["decision"]["who_not_to_involve"] == []
    assert [r["person_id"] for r in body["output"]["recipients"]] == [sit.MARCO, sit.PRIYA]
    assert body["output"]["knowledge_refs_used"] == [sit.K17]
    assert body["tool_calls"] == 4 and body["cited_access_ids"] == [101, 103, 104]
    assert "context_refs" not in body
    assert len(core.requests) == 4


def test_draft_without_guidance_replays_the_unguided_story() -> None:
    client, _ = make_client(sit.ACME_UNGUIDED)
    body = client.post("/v1/draft", json=sit.ACME_UNGUIDED.request_copy()).json()
    assert body["output"]["proposed_action_type"] == "schedule_meeting" and body["decision"]["used_guidance"] is False


def test_draft_wait_response_has_wait_until_in_decision_and_output() -> None:
    until = "2026-10-06T09:00:00Z"
    provider = dbl.ScriptedProvider([dbl.tools_turn(dbl.call(1, "state")),
                                    dbl.decision_turn("wait", "Account reviewing.", wait_until=until)])
    client, _ = make_client(sit.ACME_UNGUIDED, provider=provider)
    body = client.post("/v1/draft", json=sit.ACME_UNGUIDED.request_copy()).json()
    assert body["decision"]["wait_until"] == until and body["output"]["wait_until"] == until
    assert not list(draft_response_validator().iter_errors(body))


def test_replay_draft_never_touches_a_non_loopback_network(monkeypatch: pytest.MonkeyPatch) -> None:
    attempts = _block_network(monkeypatch)
    client, _ = make_client()
    assert client.post("/v1/draft", json=sit.ACME_GUIDED.request_copy()).status_code == 200
    assert attempts == []


def test_core_is_called_at_settings_core_url_only() -> None:
    client, core = make_client(core_url="http://core.internal:9000")
    core.host = "core.internal:9000"
    assert client.post("/v1/draft", json=sit.ACME_GUIDED.request_copy()).status_code == 200
    assert {r.url.netloc.decode() for r in core.requests} == {"core.internal:9000"}


# --- client errors (422) -------------------------------------------------------------------------

def test_core_url_in_the_request_is_rejected() -> None:
    client, core = make_client()
    body = sit.ACME_GUIDED.request_copy() | {"core_url": "http://evil.example"}
    response = client.post("/v1/draft", json=body)
    assert response.status_code == 422 and core.requests == []
    assert_envelope(response.json(), "invalid_request")
    assert "evil.example" not in response.text


@pytest.mark.parametrize("override", [{"run_token": "short"}, {"max_tool_calls": 13}, {"workflow": "nope"},
                                      {"account_id": "x"}, {"state_header": "h" * 2001}])
def test_invalid_bodies_are_422_envelopes_that_do_not_echo_values(override: dict) -> None:
    client, _ = make_client()
    response = client.post("/v1/draft", json=sit.ACME_GUIDED.request_copy() | override)
    assert response.status_code == 422
    assert_envelope(response.json(), "invalid_request")
    assert "nope" not in response.text


def test_guidance_for_a_different_account_is_422() -> None:
    client, core = make_client()
    body = sit.ACME_GUIDED.request_copy()
    body["decision_guidance"]["account_id"] = sit.BETA
    response = client.post("/v1/draft", json=body)
    assert response.status_code == 422 and core.requests == []
    assert_envelope(response.json(), "invalid_request")


def test_oversized_body_is_422_request_too_large() -> None:
    client, _ = make_client()
    response = client.post("/v1/draft", content=b"x" * (MAX_BODY_BYTES + 1),
                           headers={"content-type": "application/json"})
    assert response.status_code == 422
    assert_envelope(response.json(), "request_too_large")


# --- failures (502) ------------------------------------------------------------------------------

def test_core_403_is_a_502_envelope_without_leaking_the_token(caplog: pytest.LogCaptureFixture) -> None:
    caplog.set_level(logging.DEBUG)
    core = dbl.FakeCore(sit.ACME_PACKETS, token="a-different-token-" + "z" * 20)
    client, _ = make_client(core=core)
    response = client.post("/v1/draft", json=sit.ACME_GUIDED.request_copy())
    assert response.status_code == 502
    assert_envelope(response.json(), "core_unavailable")
    assert dbl.RUN_TOKEN not in response.text and dbl.RUN_TOKEN not in caplog.text
    assert "403" not in response.text  # internals are logged, not returned


def test_core_timeout_is_a_502_envelope() -> None:
    client, _ = make_client(core=dbl.FakeCore(sit.ACME_PACKETS, timeout=True))
    response = client.post("/v1/draft", json=sit.ACME_GUIDED.request_copy())
    assert response.status_code == 502
    assert_envelope(response.json(), "core_unavailable")


def test_missing_cassette_is_a_502_envelope() -> None:
    client, _ = make_client()
    body = sit.ACME_GUIDED.request_copy()
    body["state_header"] = "A header nobody recorded a cassette for."
    response = client.post("/v1/draft", json=body)
    assert response.status_code == 502
    assert_envelope(response.json(), "provider_error")


def test_ungrounded_proposal_is_a_502_never_a_draft() -> None:
    skill = copy.deepcopy(sit.ACME_UNGUIDED.skill)
    skill["evidence_refs"] = [dbl.ev(dbl.uid("0ac70000", 999), None, "invented")]
    provider = dbl.ScriptedProvider(
        [dbl.tools_turn(dbl.call(1, "state"), dbl.call(2, "people")),
         dbl.decision_turn("send_email", "x", involve=((sit.MARCO, "m"),), evidence=sit.ACME_EVIDENCE)], skill)
    client, _ = make_client(sit.ACME_UNGUIDED, provider=provider)
    response = client.post("/v1/draft", json=sit.ACME_UNGUIDED.request_copy())
    assert response.status_code == 502
    assert_envelope(response.json(), "ungrounded_proposal")
    assert "output" not in response.json()


def test_skill_that_leaves_out_an_involved_person_is_a_502_inconsistent_draft() -> None:
    """Live benchmark (2026-10-02): this surfaced as provider_error; it is a decision/skill consistency failure."""
    provider = dbl.ScriptedProvider(
        [dbl.tools_turn(dbl.call(1, "state"), dbl.call(2, "people")),
         dbl.decision_turn("send_email", "Keep the champion in.", evidence=sit.ACME_EVIDENCE,
                           involve=((sit.MARCO, sit.MARCO_WHY), (sit.PRIYA, sit.PRIYA_WHY)))],
        sit.ACME_UNGUIDED.skill)  # the skill only writes to Marco
    client, _ = make_client(sit.ACME_UNGUIDED, provider=provider)
    response = client.post("/v1/draft", json=sit.ACME_UNGUIDED.request_copy())
    assert response.status_code == 502
    assert_envelope(response.json(), "inconsistent_draft")
    assert "output" not in response.json() and "involve" not in response.text  # detail is logged, not returned


def test_tool_call_cap_over_http_is_a_502_after_at_most_the_cap_pulls() -> None:
    provider = dbl.ScriptedProvider([dbl.tools_turn(dbl.call(i, "people", limit=i)) for i in range(1, 15)])
    client, core = make_client(sit.ACME_UNGUIDED, provider=provider)
    response = client.post("/v1/draft", json=sit.ACME_UNGUIDED.request_copy() | {"max_tool_calls": 3})
    assert response.status_code == 502 and len(core.requests) == 3
    assert_envelope(response.json(), "tool_budget_exceeded")


# --- deadline and concurrency --------------------------------------------------------------------

def test_overall_deadline_over_http_is_a_502() -> None:
    class Slow(dbl.ScriptedProvider):
        def complete_turn(self, **kw: object) -> TurnResult:
            time.sleep(0.15)
            return super().complete_turn(**kw)

    provider = Slow([dbl.tools_turn(dbl.call(i, "people")) for i in range(1, 6)])
    client, core = make_client(sit.ACME_UNGUIDED, provider=provider, draft_deadline_s=0.1)
    response = client.post("/v1/draft", json=sit.ACME_UNGUIDED.request_copy())
    assert response.status_code == 502 and len(core.requests) <= 1
    assert_envelope(response.json(), "deadline_exceeded")
    assert "extract" not in response.json()["error"]["message"]


def test_concurrency_cap_returns_502_to_the_request_that_cannot_get_a_slot() -> None:
    class Slow(dbl.ScriptedProvider):
        def complete_turn(self, **kw: object) -> TurnResult:
            time.sleep(0.6)
            return TurnResult(content=dbl.decision_turn("no_action", "nothing").content, model=dbl.MODEL)

        def complete_json(self, **kw: object) -> LLMResult:  # pragma: no cover - never reached
            raise AssertionError

    client, _ = make_client(sit.ACME_UNGUIDED, provider=Slow([]), max_concurrency=1, llm_deadline_s=0.2)
    with ThreadPoolExecutor(max_workers=2) as pool:
        futures = [pool.submit(client.post, "/v1/draft", json=sit.ACME_UNGUIDED.request_copy()) for _ in range(2)]
        codes = sorted(f.result().status_code for f in futures)
    assert codes == [200, 502]


def test_concurrency_rejection_is_a_provider_error() -> None:
    class Hold(dbl.ScriptedProvider):
        def complete_turn(self, **kw: object) -> TurnResult:
            time.sleep(0.6)
            return TurnResult(content=dbl.decision_turn("no_action", "nothing").content, model=dbl.MODEL)

    client, _ = make_client(sit.ACME_UNGUIDED, provider=Hold([]), max_concurrency=1, llm_deadline_s=0.2)
    with ThreadPoolExecutor(max_workers=2) as pool:
        futures = [pool.submit(client.post, "/v1/draft", json=sit.ACME_UNGUIDED.request_copy()) for _ in range(2)]
        bodies = [f.result() for f in futures]
    (busy,) = [r for r in bodies if r.status_code == 502]
    assert_envelope(busy.json(), "provider_error")
    assert busy.json()["error"]["message"] == "model provider failed to produce a usable result"


def test_guided_wait_over_http_keeps_knowledge_refs() -> None:
    client, _ = make_client(sit.ACME_GUIDED_WAIT)
    body = client.post("/v1/draft", json=sit.ACME_GUIDED_WAIT.request_copy()).json()
    assert body["output"]["knowledge_refs_used"] == [sit.K21] and body["decision"]["wait_until"] == sit.TUESDAY
    assert not list(draft_response_validator().iter_errors(body))


def test_rep_profile_is_accepted_and_changes_only_the_drafting_prompt() -> None:
    provider = dbl.ScriptedProvider(
        [dbl.tools_turn(dbl.call(1, "state"), dbl.call(2, "people")),
         dbl.decision_turn("send_email", "x", involve=((sit.MARCO, "m"),), evidence=sit.ACME_EVIDENCE)],
        sit.ACME_UNGUIDED.skill)
    client, _ = make_client(sit.ACME_UNGUIDED, provider=provider)
    body = sit.ACME_UNGUIDED.request_copy() | {"rep_profile": {"brevity": "terse"}}
    assert client.post("/v1/draft", json=body).status_code == 200
    assert "terse" not in repr(provider.turn_calls) and "terse" in provider.json_calls[0]["user"]
