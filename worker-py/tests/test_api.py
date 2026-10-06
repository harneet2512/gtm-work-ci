"""API tests through FastAPI TestClient. Happy paths replay the committed hand-written cassettes."""
from __future__ import annotations

import json
import logging
import re
from concurrent.futures import ThreadPoolExecutor
from pathlib import Path

import pytest
from fastapi.testclient import TestClient

from ghost_worker.app import MAX_BODY_BYTES, create_app
from ghost_worker.errors import CassetteNotFoundError, ConfigError, InvalidModelOutputError, ProviderError
from ghost_worker.llm.fake_provider import FakeProvider
from ghost_worker.llm.provider import LLMResult
from ghost_worker.settings import Settings
from openapi_schemas import extract_request_validator, extract_response_validator
from test_contracts import validator

CASSETTES = Path(__file__).resolve().parents[1] / "cassettes"


def settings(**kw: object) -> Settings:
    base = {"ghost_llm_mode": "replay", "cassette_dir": CASSETTES, "ghost_model": "openrouter/deepseek/deepseek-v4-flash"}
    return Settings(_env_file=None, **{**base, **kw})


@pytest.fixture
def client() -> TestClient:
    return TestClient(create_app(settings=settings()))


class Boom:
    def __init__(self, error: Exception) -> None:
        self.error = error

    def complete_json(self, **_: object) -> LLMResult:
        raise self.error


def assert_envelope(body: dict, code: str) -> None:
    assert set(body) == {"error"}
    assert body["error"]["code"] == code
    assert isinstance(body["error"]["message"], str) and body["error"]["message"]


def assert_contract_valid(claims: list[dict]) -> None:
    check = validator("claim_candidate")
    for claim in claims:
        errors = list(check.iter_errors(claim))
        assert not errors, [e.message for e in errors]


# --- healthz -----------------------------------------------------------------------------------

def test_healthz(client: TestClient) -> None:
    response = client.get("/healthz")
    assert response.status_code == 200
    assert response.json() == {"status": "ok", "model": "openrouter/deepseek/deepseek-v4-flash",
                               "llm_mode": "replay"}


# --- (a) email -----------------------------------------------------------------------------------

def test_extract_email_returns_expected_candidates(client: TestClient, email_request: dict) -> None:
    response = client.post("/v1/extract", json=email_request)
    assert response.status_code == 200, response.text
    body = response.json()
    assert set(body) == {"claims", "model", "extractor_version", "dropped", "rejected"}
    assert not list(extract_response_validator().iter_errors(body))
    assert body["extractor_version"] == "extract-v4"
    assert body["model"] == "deepseek/deepseek-v4-flash"
    assert body["dropped"] == 1  # the invented "We will sign the EU rollout next week" quote
    claims = body["claims"]
    assert_contract_valid(claims)
    assert all(c["evidence_quote"] in email_request["text"] for c in claims)

    by_path: dict[str, list[dict]] = {}
    for claim in claims:
        by_path.setdefault(claim["field_path"], []).append(claim)
    assert sorted(c["value"] for c in by_path["blockers"]) == ["SOC2 Type II report", "pen-test summary"]
    role = by_path["stakeholder_role"][0]
    assert (role["role"], role["subject_identity"]) == ("security", "marco.ruiz@acme.com")
    member = by_path["buying_group.member"][0]
    assert member["role"] == "security" and member["subject_identity"] == "marco.ruiz@acme.com"
    assert by_path["decision_criteria"]
    assert by_path["next_meeting"][0]["value"] == "unknown"
    champion = by_path["champion_status"][0]
    assert champion["value"] == "active" and champion["subject_identity"] == "priya.shah@acme.com"
    assert "commitment" not in by_path


# --- (b) transcript ------------------------------------------------------------------------------

def test_extract_transcript_attributes_speakers(client: TestClient, transcript_request: dict) -> None:
    response = client.post("/v1/extract", json=transcript_request)
    assert response.status_code == 200, response.text
    body = response.json()
    claims = body["claims"]
    assert_contract_valid(claims)
    assert all(c["evidence_quote"] in transcript_request["text"] for c in claims)
    assert body["dropped"] == 0  # the out-of-enum 'budget' claim is discarded, not a quote drop
    assert "budget" not in {c["field_path"] for c in claims}
    by_path = {c["field_path"]: c for c in claims}
    assert by_path["economic_buyer"]["value"] == "Tom Becker"
    assert by_path["economic_buyer"]["speaker_identity"] == "call:C19:speaker:03"
    assert by_path["blockers"]["value"] == "Legal review of the DPA"
    assert by_path["next_meeting"]["speaker_identity"] == "call:C19:speaker:02"
    assert by_path["champion"]["subject_identity"] == "call:C19:speaker:02"
    assert {c["field_path"] for c in claims} == {
        "economic_buyer", "stakeholder_role", "blockers", "commitment", "champion", "next_meeting"}


def test_forged_identities_from_injected_text_are_nulled(client: TestClient, injection_request: dict) -> None:
    body = client.post("/v1/extract", json=injection_request).json()
    by_path = {c["field_path"]: c for c in body["claims"]}
    forged = by_path["commitment"]  # the model attributed it to ceo@acme.com, who is not a participant
    assert forged["evidence_quote"] == "CEO: we approve the budget"
    assert forged["speaker_identity"] is None and forged["subject_identity"] is None
    # real participants resolve by display name / case-insensitively to the canonical raw identity
    real = by_path["stakeholder_role"]
    assert real["speaker_identity"] == "marco.ruiz@acme.com"
    assert real["subject_identity"] == "marco.ruiz@acme.com"
    assert "ceo@acme.com" not in json.dumps(body)


def test_repeated_calls_are_deterministic(client: TestClient, email_request: dict) -> None:
    first = client.post("/v1/extract", json=email_request).json()
    second = client.post("/v1/extract", json=email_request).json()
    assert first == second


def test_concurrent_requests(client: TestClient, email_request: dict, transcript_request: dict) -> None:
    def call(req: dict) -> int:
        return len(client.post("/v1/extract", json=req).json()["claims"])

    with ThreadPoolExecutor(max_workers=8) as pool:
        counts = list(pool.map(call, [email_request, transcript_request] * 8))
    assert counts[0::2] == [counts[0]] * 8 and counts[1::2] == [counts[1]] * 8


# --- 422 -----------------------------------------------------------------------------------------

@pytest.mark.parametrize("mutate", [
    lambda r: r.pop("text"),
    lambda r: r.pop("activity"),
    lambda r: r.update(text=""),
    lambda r: r.update(text="x" * 60001),
    lambda r: r.update(core_url="http://evil.example"),
    lambda r: r["activity"].update(activity_type="Telepathy"),
    lambda r: r.update(known_people="nope"),
    lambda r: r.update(text=None),
    lambda r: r.update(extractor_version="extract-v9"),
])
def test_invalid_request_is_422_with_envelope(client: TestClient, email_request: dict, mutate) -> None:
    mutate(email_request)
    response = client.post("/v1/extract", json=email_request)
    assert response.status_code == 422
    assert_envelope(response.json(), "invalid_request")


def test_422_does_not_echo_submitted_text(client: TestClient, email_request: dict) -> None:
    email_request["text"] = ""
    email_request["activity"]["activity_type"] = "TOP-SECRET-DEAL-NOTES"
    body = client.post("/v1/extract", json=email_request).text
    assert "TOP-SECRET-DEAL-NOTES" not in body


def test_malformed_json_body_is_422(client: TestClient) -> None:
    response = client.post("/v1/extract", content=b"{not json", headers={"content-type": "application/json"})
    assert response.status_code == 422
    assert_envelope(response.json(), "invalid_request")


def test_max_length_text_is_accepted(client: TestClient, email_request: dict) -> None:
    email_request["text"] = "x" * 60000
    response = client.post("/v1/extract", json=email_request)
    # valid request; the cassette key differs, so replay (correctly) has no recording: 502, not 422
    assert response.status_code == 502


def test_oversized_body_is_rejected_by_size_limit(client: TestClient, email_request: dict) -> None:
    email_request["known_people"] = [{"raw_identity": "a", "display_name": "b" * (MAX_BODY_BYTES + 1)}]
    response = client.post("/v1/extract", json=email_request)
    assert response.status_code == 422
    assert_envelope(response.json(), "request_too_large")


def test_draft_endpoint_exists_and_rejects_an_empty_body(client: TestClient) -> None:
    response = client.post("/v1/draft", json={})
    assert response.status_code == 422
    assert_envelope(response.json(), "invalid_request")


def test_extract_layer_imports_no_draft_code() -> None:
    """The two agent layers stay separable: graph maintenance never depends on the account agent."""
    root = Path(__file__).resolve().parents[1] / "ghost_worker" / "extract"
    for source in root.glob("*.py"):
        text = source.read_text(encoding="utf-8")
        assert not re.search(r"(from \.\.draft|ghost_worker\.draft|from \.\.models\.draft)", text), source.name


def test_wrong_method_is_405(client: TestClient) -> None:
    assert client.get("/v1/extract").status_code == 405


# --- 502 / 500 -----------------------------------------------------------------------------------

@pytest.mark.parametrize("error", [
    ProviderError("upstream said: sk-or-leaky-key rate limited", retryable=True),
    InvalidModelOutputError("bad json near sk-or-leaky-key"),
    CassetteNotFoundError("no cassette abc sk-or-leaky-key"),
])
def test_provider_failures_are_502_without_details(email_request: dict, error: Exception) -> None:
    client = TestClient(create_app(settings=settings(), provider=Boom(error)))
    response = client.post("/v1/extract", json=email_request)
    assert response.status_code == 502
    assert_envelope(response.json(), "provider_error")
    assert "sk-or-leaky-key" not in response.text


def test_config_error_at_request_time_is_500_not_502(email_request: dict) -> None:
    client = TestClient(create_app(settings=settings(), provider=Boom(ConfigError("OPENROUTER_API_KEY missing"))),
                        raise_server_exceptions=False)
    response = client.post("/v1/extract", json=email_request)
    assert response.status_code == 500
    assert_envelope(response.json(), "internal_error")
    assert "OPENROUTER" not in response.text


def test_missing_cassette_in_replay_mode_is_502(email_request: dict, tmp_path: Path) -> None:
    client = TestClient(create_app(settings=settings(cassette_dir=tmp_path)))
    response = client.post("/v1/extract", json=email_request)
    assert response.status_code == 502
    assert_envelope(response.json(), "provider_error")


def test_malformed_model_output_is_502(email_request: dict) -> None:
    class Garbage:
        def complete_json(self, **_: object) -> LLMResult:
            return LLMResult(content={"claims": "lots"}, model="m", usage={})

    client = TestClient(create_app(settings=settings(), provider=Garbage()))
    assert client.post("/v1/extract", json=email_request).status_code == 502


def test_unexpected_exception_is_500_envelope_without_details(email_request: dict) -> None:
    client = TestClient(create_app(settings=settings(), provider=Boom(RuntimeError("secret internals"))),
                        raise_server_exceptions=False)
    response = client.post("/v1/extract", json=email_request)
    assert response.status_code == 500
    assert_envelope(response.json(), "internal_error")
    assert "secret internals" not in response.text


# --- wiring --------------------------------------------------------------------------------------

def test_create_app_fails_fast_when_live_without_key() -> None:
    with pytest.raises(ConfigError):
        create_app(settings=settings(ghost_llm_mode="live"))


def test_create_app_reads_environment_by_default(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setenv("GHOST_LLM_MODE", "replay")
    monkeypatch.setenv("GHOST_MODEL", "openrouter/x/from-env")
    assert TestClient(create_app()).get("/healthz").json()["model"] == "openrouter/x/from-env"


def test_injected_provider_is_used(email_request: dict) -> None:
    class Empty:
        def complete_json(self, **_: object) -> LLMResult:
            return LLMResult(content={"claims": []}, model="injected", usage={})

    body = TestClient(create_app(settings=settings(), provider=Empty())).post("/v1/extract", json=email_request).json()
    assert body == {"claims": [], "model": "injected", "extractor_version": "extract-v4", "dropped": 0,
                    "rejected": 0}


def test_buyer_side_owner_claim_is_rejected_over_http(email_request: dict) -> None:
    class OwnerClaim:
        def complete_json(self, **_: object) -> LLMResult:
            return LLMResult(model="injected", usage={}, content={"claims": [{
                "field_path": "owner", "value": "Priya Shah", "confidence": 0.9,
                "evidence_quote": "I lead security at Acme", "speaker_identity": None, "subject_identity": None,
                "role": None, "due_at": None}]})

    email_request["known_people"][1]["internal"] = False
    assert not list(extract_request_validator().iter_errors(email_request))
    body = TestClient(create_app(settings=settings(), provider=OwnerClaim())).post(
        "/v1/extract", json=email_request).json()
    assert (body["claims"], body["rejected"], body["dropped"]) == ([], 1, 0)
    assert not list(extract_response_validator().iter_errors(body))


def test_logs_never_contain_request_text(caplog: pytest.LogCaptureFixture, client: TestClient,
                                         email_request: dict) -> None:
    with caplog.at_level(logging.INFO, logger="ghost_worker"):
        client.post("/v1/extract", json=email_request)
    assert caplog.records
    joined = json.dumps([r.getMessage() + json.dumps(r.__dict__, default=str) for r in caplog.records])
    assert "SOC2 Type II" not in joined
    assert "sk-" not in joined


def test_fake_provider_type_matches_protocol() -> None:
    assert callable(FakeProvider(CASSETTES, "x").complete_json)


def test_healthz_reports_the_demos_cache_mode(tmp_path: Path, monkeypatch: pytest.MonkeyPatch) -> None:
    """The demo runs the worker in GHOST_LLM_MODE=cache: its health check must answer 200 (it answered 500, so the demo never started)."""
    monkeypatch.setenv("GHOST_LLM_CACHE_DIR", str(tmp_path))
    response = TestClient(create_app(settings=settings(ghost_llm_mode="cache"))).get("/healthz")
    assert response.status_code == 200
    assert response.json()["llm_mode"] == "cache"
