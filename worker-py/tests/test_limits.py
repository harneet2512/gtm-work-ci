"""Input bounds: model field limits and the byte-counting ASGI body guard (chunked requests included)."""
from __future__ import annotations

import asyncio
import json

import pytest
from fastapi.testclient import TestClient
from pydantic import ValidationError

from ghost_worker.app import MAX_BODY_BYTES, create_app
from ghost_worker.body_limit import BodySizeLimitMiddleware, BodyTooLarge
from ghost_worker.llm.provider import LLMResult
from ghost_worker.models import ExtractRequest
from ghost_worker.settings import Settings


class Empty:
    def complete_json(self, **_: object) -> LLMResult:
        return LLMResult(content={"claims": []}, model="m", usage={})


@pytest.fixture
def client() -> TestClient:
    return TestClient(create_app(settings=Settings(_env_file=None, ghost_llm_mode="replay"), provider=Empty()))


# --- model bounds --------------------------------------------------------------------------------

def participant(i: int) -> dict:
    return {"raw_identity": f"p{i}@acme.com", "display_name": f"P {i}", "role": "to"}


def test_participants_bounded_at_100(email_request: dict) -> None:
    email_request["activity"]["participants"] = [participant(i) for i in range(100)]
    ExtractRequest.model_validate(email_request)
    email_request["activity"]["participants"].append(participant(100))
    with pytest.raises(ValidationError):
        ExtractRequest.model_validate(email_request)


def test_known_people_bounded_at_100(email_request: dict) -> None:
    people = [{"raw_identity": f"k{i}@acme.com", "display_name": f"K {i}"} for i in range(100)]
    email_request["known_people"] = people
    ExtractRequest.model_validate(email_request)
    email_request["known_people"] = [*people, {"raw_identity": "x@acme.com", "display_name": "X"}]
    with pytest.raises(ValidationError):
        ExtractRequest.model_validate(email_request)


@pytest.mark.parametrize("mutate", [
    lambda r: r["activity"]["participants"][0].update(raw_identity="a" * 513),
    lambda r: r["activity"]["participants"][0].update(display_name="a" * 513),
    lambda r: r["known_people"][0].update(raw_identity="a" * 513),
    lambda r: r["known_people"][0].update(display_name="a" * 513),
    lambda r: r["known_people"][0].update(title="a" * 513),
    lambda r: r["activity"].update(summary="a" * 1001),
    lambda r: r["activity"].update(account_hint="a" * 513),
])
def test_string_fields_are_bounded(email_request: dict, mutate) -> None:
    mutate(email_request)
    with pytest.raises(ValidationError):
        ExtractRequest.model_validate(email_request)


def test_string_fields_at_the_limit_are_accepted(email_request: dict) -> None:
    email_request["activity"]["participants"][0].update(raw_identity="a" * 512, display_name="b" * 512)
    email_request["known_people"][0].update(raw_identity="c" * 512, display_name="d" * 512, title="e" * 512)
    email_request["activity"]["summary"] = "s" * 1000
    ExtractRequest.model_validate(email_request)


# --- body guard through the app ------------------------------------------------------------------

def test_declared_oversize_body_rejected_before_reading(client: TestClient, email_request: dict) -> None:
    email_request["text"] = "x" * 100
    email_request["known_people"] = [{"raw_identity": "a", "display_name": "b" * (MAX_BODY_BYTES + 1)}]
    response = client.post("/v1/extract", json=email_request)
    assert response.status_code == 422
    assert response.json()["error"]["code"] == "request_too_large"


def test_chunked_oversize_body_without_content_length_is_rejected(client: TestClient, email_request: dict) -> None:
    payload = json.dumps(email_request).encode()
    padding = b" " * (MAX_BODY_BYTES + 10)

    def chunks():
        yield payload[:-1]
        for i in range(0, len(padding), 65536):
            yield padding[i:i + 65536]
        yield payload[-1:]

    response = client.post("/v1/extract", content=chunks(), headers={"content-type": "application/json"})
    assert "content-length" not in response.request.headers
    assert response.status_code == 422
    assert response.json()["error"]["code"] == "request_too_large"


def test_chunked_body_under_the_limit_is_served(client: TestClient, email_request: dict) -> None:
    payload = json.dumps(email_request).encode()

    def chunks():
        for i in range(0, len(payload), 1000):
            yield payload[i:i + 1000]

    response = client.post("/v1/extract", content=chunks(), headers={"content-type": "application/json"})
    assert response.status_code == 200


# --- the ASGI wrapper itself ---------------------------------------------------------------------

def run_asgi(middleware: BodySizeLimitMiddleware, chunks: list[bytes], headers: list | None = None):
    sent: list[dict] = []
    queue = [{"type": "http.request", "body": c, "more_body": i < len(chunks) - 1} for i, c in enumerate(chunks)]

    async def receive() -> dict:
        return queue.pop(0)

    async def send(message: dict) -> None:
        sent.append(message)

    scope = {"type": "http", "method": "POST", "path": "/x", "headers": headers or []}
    asyncio.run(middleware(scope, receive, send))
    return sent


def drain_app(consumed: list[int]):
    async def app(scope, receive, send) -> None:
        more = True
        while more:
            message = await receive()
            consumed.append(len(message["body"]))
            more = message.get("more_body", False)
        await send({"type": "http.response.start", "status": 204, "headers": []})
        await send({"type": "http.response.body", "body": b""})
    return app


def test_wrapper_counts_chunks_and_raises_once_over_the_limit() -> None:
    consumed: list[int] = []
    middleware = BodySizeLimitMiddleware(drain_app(consumed), max_bytes=10)
    with pytest.raises(BodyTooLarge):
        run_asgi(middleware, [b"123456", b"78901"])
    assert consumed == [6]  # the second chunk tripped the guard before the app saw it


def test_wrapper_allows_exactly_the_limit() -> None:
    consumed: list[int] = []
    sent = run_asgi(BodySizeLimitMiddleware(drain_app(consumed), max_bytes=10), [b"12345", b"67890"])
    assert sum(consumed) == 10
    assert sent[0]["status"] == 204


def test_wrapper_rejects_declared_length_immediately() -> None:
    consumed: list[int] = []
    middleware = BodySizeLimitMiddleware(drain_app(consumed), max_bytes=10)
    sent = run_asgi(middleware, [b"x"], headers=[(b"content-length", b"11")])
    assert consumed == []
    assert sent[0]["status"] == 422
    assert json.loads(sent[1]["body"])["error"]["code"] == "request_too_large"


def test_wrapper_ignores_garbage_content_length_and_non_http_scopes() -> None:
    consumed: list[int] = []
    middleware = BodySizeLimitMiddleware(drain_app(consumed), max_bytes=10)
    sent = run_asgi(middleware, [b"12345"], headers=[(b"content-length", b"abc")])
    assert sent[0]["status"] == 204

    calls: list[str] = []

    async def lifespan_app(scope, receive, send) -> None:
        calls.append(scope["type"])

    asyncio.run(BodySizeLimitMiddleware(lifespan_app, max_bytes=1)({"type": "lifespan"}, None, None))
    assert calls == ["lifespan"]
