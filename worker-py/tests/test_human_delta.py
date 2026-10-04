"""POST /v1/human-delta (HAR-139): the scripted provider labels the literal diff; contract rules are enforced
by the request model (explaining_evals empty exactly when unexplained) and by the service (vocabulary via
pydantic, candidate_criterion required iff unexplained, dropped when explained)."""
from __future__ import annotations

from typing import Any

import pytest
import strategy_doubles as sd
from draft_doubles import ScriptedProvider
from fastapi.testclient import TestClient
from ghost_worker.app import create_app
from ghost_worker.llm.provider import LLMResult

EVAL_RESULT = "0e7a1000-0000-4000-8000-000000000b01"
MODEL = "scripted-delta-v0"


def request_body(**over: Any) -> dict[str, Any]:
    body: dict[str, Any] = {
        "run_id": sd.RUN_ID, "decision_episode_id": sd.EPISODE, "account_id": sd.ACCOUNT,
        "selected_candidate": sd.candidate_json(),
        "final_action": {
            "to": [{"person_id": sd.SECURITY, "role": "to", "why": "asked"}],
            "cc": [{"person_id": sd.CHAMPION, "role": "cc", "why": "champion"}],
            "artifact": sd.artifact("Hi Person B,\n\nHere are the answers, no rush.\n\nBest,\nDana")},
        "literal_changes": [{"kind": "paragraph_edited",
                             "before": "Attached are the questionnaire answers.",
                             "after": "Here are the answers, no rush."}],
        "unexplained": False,
        "explaining_evals": [{"eval_result_id": EVAL_RESULT, "eval_type": "cta_calibration",
                              "reason": "the ask was flagged as stronger than the buyer's timing"}]}
    body.update(over)
    return body


def client_for(skill: dict[str, Any]) -> TestClient:
    from test_api import settings
    return TestClient(create_app(settings=settings(), provider=ScriptedProvider(turns=[], skill=skill)))


def labeled(labels: list[str], criterion: dict[str, Any] | None = None) -> dict[str, Any]:
    return {"semantic_labels": labels, "candidate_criterion": criterion}


CRITERION = {"statement": "The human softens the ask when the buyer signals they need time.",
             "suggested_eval_type": "cta_calibration", "knowledge_candidate_id": None}


def test_explained_delta_returns_labels_and_no_criterion() -> None:
    client = client_for(labeled(["smaller_ask", "deferred_to_buyer_timing"], CRITERION))
    resp = client.post("/v1/human-delta", json=request_body())
    assert resp.status_code == 200
    body = resp.json()
    assert body["semantic_labels"] == ["smaller_ask", "deferred_to_buyer_timing"]
    # An explained delta drops any criterion the model offered: the explaining evals account for it.
    assert body["candidate_criterion"] is None
    assert body["model"]


def test_unexplained_delta_returns_the_candidate_criterion() -> None:
    client = client_for(labeled(["changed_channel"], CRITERION))
    resp = client.post("/v1/human-delta", json=request_body(unexplained=True, explaining_evals=[]))
    assert resp.status_code == 200
    body = resp.json()
    assert body["candidate_criterion"]["suggested_eval_type"] == "cta_calibration"


def test_unexplained_delta_without_criterion_is_a_worker_error() -> None:
    client = client_for(labeled(["style_only"], None))
    resp = client.post("/v1/human-delta", json=request_body(unexplained=True, explaining_evals=[]))
    assert resp.status_code == 502
    assert resp.json()["error"]["code"] == "provider_error"


def test_a_label_outside_the_vocabulary_is_a_worker_error() -> None:
    client = client_for(labeled(["made_up_label"], None))
    resp = client.post("/v1/human-delta", json=request_body())
    assert resp.status_code == 502


def test_duplicate_labels_are_deduplicated() -> None:
    client = client_for(labeled(["smaller_ask", "smaller_ask"], None))
    resp = client.post("/v1/human-delta", json=request_body())
    assert resp.status_code == 200
    assert resp.json()["semantic_labels"] == ["smaller_ask"]


def test_unexplained_with_explainers_is_invalid() -> None:
    client = client_for(labeled([], None))
    body = request_body(unexplained=True)  # leaves explaining_evals populated
    resp = client.post("/v1/human-delta", json=body)
    assert resp.status_code == 422
    assert resp.json()["error"]["code"] == "invalid_request"


def test_explained_without_explainers_is_invalid() -> None:
    client = client_for(labeled([], None))
    resp = client.post("/v1/human-delta", json=request_body(explaining_evals=[]))
    assert resp.status_code == 422


def test_a_cc_role_in_to_is_invalid() -> None:
    client = client_for(labeled([], None))
    body = request_body()
    body["final_action"]["to"] = [{"person_id": sd.CHAMPION, "role": "cc", "why": "champion"}]
    resp = client.post("/v1/human-delta", json=body)
    assert resp.status_code == 422


def test_provider_failure_is_502() -> None:
    from test_api import Boom, settings
    client = TestClient(create_app(settings=settings(), provider=Boom(Exception("down"))))
    # Boom raises a bare Exception -> the catch-all is 500; a provider error raises ProviderError -> 502.
    from ghost_worker.errors import ProviderError
    client = TestClient(create_app(settings=settings(), provider=Boom(ProviderError("upstream", retryable=True))))
    resp = client.post("/v1/human-delta", json=request_body())
    assert resp.status_code == 502
    assert resp.json()["error"]["code"] == "provider_error"
