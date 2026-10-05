"""The projection barrier: core answers 503 projection_pending until the account's graph projection is complete."""
from __future__ import annotations

import httpx
import pytest

from ghost_worker.draft.core_client import CoreContextClient, ToolParams
from ghost_worker.errors import CoreContextError

BASE = "http://core.internal:8080"
TOKEN = "run-token-" + "x" * 30


def packet() -> dict:
    return {"access_id": 7, "tool": "people", "items": [], "truncated": False, "bytes": 2}


def pending(retry_after: str | None = "1") -> httpx.Response:
    headers = {"Retry-After": retry_after} if retry_after else {}
    return httpx.Response(503, headers=headers, json={"error": {"code": "projection_pending", "message": "wait"}})


class Clock:
    def __init__(self) -> None:
        self.now = 0.0
        self.sleeps: list[float] = []

    def __call__(self) -> float:
        return self.now

    def sleep(self, seconds: float) -> None:
        self.sleeps.append(seconds)
        self.now += seconds


def client(clock: Clock, handler, timeout_s: float = 30.0) -> CoreContextClient:
    return CoreContextClient(BASE, TOKEN, timeout_s=timeout_s, max_bytes=4096, clock=clock, sleep=clock.sleep,
                             transport=httpx.MockTransport(handler))


def test_pull_waits_out_the_barrier_honouring_retry_after() -> None:
    answers = [pending("2"), pending("1"), httpx.Response(200, json=packet())]
    clock = Clock()
    assert client(clock, lambda r: answers.pop(0)).pull("people", ToolParams(), timeout_s=30.0).access_id == 7
    assert clock.sleeps == [2.0, 1.0] and not answers


def test_pull_defaults_and_caps_the_wait() -> None:
    answers = [pending(None), pending("999"), httpx.Response(200, json=packet())]
    clock = Clock()
    client(clock, lambda r: answers.pop(0)).pull("people", ToolParams(), timeout_s=30.0)
    assert clock.sleeps == [1.0, 5.0]


def test_pull_gives_up_when_the_projection_outlasts_the_deadline() -> None:
    clock = Clock()
    with pytest.raises(CoreContextError, match="not complete"):
        client(clock, lambda r: pending("1"), timeout_s=3.0).pull("people", ToolParams(), timeout_s=3.0)
    assert clock.now <= 3.0


def test_other_503_answers_are_not_retried() -> None:
    seen: list[httpx.Request] = []

    def handler(request: httpx.Request) -> httpx.Response:
        seen.append(request)
        return httpx.Response(503, json={"error": {"code": "internal", "message": "x"}})

    with pytest.raises(CoreContextError, match="HTTP 503"):
        client(Clock(), handler).pull("people", ToolParams(), timeout_s=1.0)
    assert len(seen) == 1
