"""HAR-135: non-retryable provider errors (402 storm) and the worker's provider circuit breaker. No network."""
from __future__ import annotations

import threading
from pathlib import Path

import litellm
import pytest
from fastapi.testclient import TestClient

from ghost_worker.app import create_app
from ghost_worker.errors import ProviderError, ProviderUnavailableError
from ghost_worker.llm.breaker import BreakerProvider, CircuitBreaker
from ghost_worker.llm.classify import is_nonretryable_provider_failure
from ghost_worker.llm.litellm_provider import LiteLLMProvider
from ghost_worker.llm.provider import LLMResult
from ghost_worker.settings import Settings

CASSETTES = Path(__file__).resolve().parents[1] / "cassettes"
KW = {"system": "s", "user": "u", "schema": {}, "schema_name": "n"}


class Upstream(Exception):
    def __init__(self, status: int, message: str = "boom") -> None:
        super().__init__(message)
        self.status_code = status


@pytest.mark.parametrize("exc", [Upstream(401), Upstream(402), Upstream(403),
                                 Upstream(400, "Insufficient credits: add more"), Upstream(429, "insufficient_quota: add credits"),
                                 Upstream(400, "Invalid API key provided"), Upstream(400, "authentication failed")])
def test_credit_quota_and_auth_failures_are_nonretryable(exc: Exception) -> None:
    assert is_nonretryable_provider_failure(exc)


@pytest.mark.parametrize("exc", [Upstream(500), Upstream(502), Upstream(503), Upstream(429, "slow down"), TimeoutError("t"),
                                 Upstream(429, "quota exceeded for this minute"), Upstream(503, "authentication upstream hiccup")])
def test_recoverable_failures_are_not_nonretryable(exc: Exception) -> None:
    assert not is_nonretryable_provider_failure(exc)


def test_a_402_from_the_provider_is_one_call_no_fallback(monkeypatch: pytest.MonkeyPatch) -> None:
    calls: list[object] = []

    def completion(**kwargs: object) -> object:
        calls.append(kwargs)
        raise Upstream(402, "Payment Required")

    monkeypatch.setattr(litellm, "completion", completion)
    provider = LiteLLMProvider(model="openrouter/a/primary", fallback_model="openrouter/b/fallback",
                               api_key="sk-test", timeout_s=5)
    with pytest.raises(ProviderUnavailableError):
        provider.complete_json(**KW)
    assert len(calls) == 1


def test_a_500_is_still_a_retryable_provider_error(monkeypatch: pytest.MonkeyPatch) -> None:
    def completion(**_: object) -> object:
        raise Upstream(500)

    monkeypatch.setattr(litellm, "completion", completion)
    provider = LiteLLMProvider(model="openrouter/a/primary", fallback_model=None, api_key="sk-test", timeout_s=5)
    with pytest.raises(ProviderError) as info:
        provider.complete_json(**KW)
    assert not isinstance(info.value, ProviderUnavailableError) and info.value.retryable


class Counting:
    def __init__(self, error: Exception | None) -> None:
        self.error, self.calls = error, 0

    def complete_json(self, **_: object) -> LLMResult:
        self.calls += 1
        if self.error:
            raise self.error
        return LLMResult(content={"claims": []}, model="m", usage={})


class Clock:
    def __init__(self) -> None:
        self.now = 0.0

    def __call__(self) -> float:
        return self.now


def test_breaker_trips_after_threshold_then_refuses_without_calling_the_provider() -> None:
    inner = Counting(ProviderUnavailableError("402"))
    breaker = CircuitBreaker(threshold=3, cooldown_s=60, clock=Clock())
    provider = BreakerProvider(inner, breaker)
    for _ in range(10):
        with pytest.raises(ProviderUnavailableError):
            provider.complete_json(**KW)
    assert inner.calls == 3 and breaker.state == "open" and breaker.trips == 1


def test_breaker_resets_after_cooldown_and_a_failed_probe_reopens_it() -> None:
    clock = Clock()
    inner = Counting(ProviderUnavailableError("402"))
    breaker = CircuitBreaker(threshold=2, cooldown_s=60, clock=clock)
    provider = BreakerProvider(inner, breaker)
    for _ in range(2):
        with pytest.raises(ProviderUnavailableError):
            provider.complete_json(**KW)
    clock.now = 61
    assert breaker.state == "half_open"
    with pytest.raises(ProviderUnavailableError):
        provider.complete_json(**KW)
    assert inner.calls == 3 and breaker.state == "open" and breaker.trips == 2
    clock.now = 130
    inner.error = None
    provider.complete_json(**KW)
    assert breaker.state == "closed"


def test_explicit_reset_closes_an_open_breaker_and_a_success_clears_the_streak() -> None:
    breaker = CircuitBreaker(threshold=2, cooldown_s=60, clock=Clock())
    breaker.record_failure()
    breaker.record_failure()
    assert breaker.state == "open"
    breaker.reset()
    assert breaker.allow()
    breaker.record_failure()
    breaker.record_success()
    breaker.record_failure()
    assert breaker.state == "closed"


def _client(provider: object, breaker: CircuitBreaker | None = None, **kw: object) -> TestClient:
    cfg = Settings(_env_file=None, ghost_llm_mode="replay", cassette_dir=CASSETTES, **kw)
    return TestClient(create_app(settings=cfg, provider=provider, breaker=breaker))  # type: ignore[arg-type]


def _extract(client: TestClient, email_request: dict) -> object:
    return client.post("/v1/extract", json=email_request)


def test_402_storm_is_424_nonretryable_and_stops_hitting_the_provider(email_request: dict) -> None:
    inner = Counting(ProviderUnavailableError("402"))
    client = _client(inner, provider_breaker_threshold=3)
    statuses = [_extract(client, email_request).status_code for _ in range(12)]
    body = _extract(client, email_request).json()
    assert set(statuses) == {424} and body["error"]["code"] == "provider_unavailable_nonretryable"
    assert inner.calls == 3, "the open breaker must short-circuit every later call"
    assert client.app.state.provider_breaker.state == "open"


def test_a_recoverable_provider_failure_stays_502_and_never_trips_the_breaker(email_request: dict) -> None:
    inner = Counting(ProviderError("upstream 503", retryable=True))
    client = _client(inner, provider_breaker_threshold=2)
    codes = [(r.status_code, r.json()["error"]["code"]) for r in (_extract(client, email_request) for _ in range(6))]
    assert set(codes) == {(502, "provider_error")} and inner.calls == 6
    assert client.app.state.provider_breaker.state == "closed"


def test_app_breaker_resets_after_its_cooldown(email_request: dict) -> None:
    inner = Counting(ProviderUnavailableError("402"))
    clock = Clock()
    client = _client(inner, breaker=CircuitBreaker(threshold=1, cooldown_s=60, clock=clock))
    assert _extract(client, email_request).status_code == 424 and inner.calls == 1
    assert _extract(client, email_request).status_code == 424 and inner.calls == 1
    clock.now = 61
    assert _extract(client, email_request).status_code == 424 and inner.calls == 2


def test_a_429_mentioning_quota_is_a_retryable_502_not_a_nonretryable_424(
        monkeypatch: pytest.MonkeyPatch, email_request: dict) -> None:
    def completion(**_: object) -> object:
        raise Upstream(429, "Rate limit exceeded: quota for this minute, retry shortly")

    monkeypatch.setattr(litellm, "completion", completion)
    llm = LiteLLMProvider(model="openrouter/a/primary", fallback_model=None, api_key="sk-test", timeout_s=5)
    client = _client(llm, provider_breaker_threshold=1)
    r = _extract(client, email_request)
    assert r.status_code == 502 and r.json()["error"]["code"] == "provider_error"
    assert client.app.state.provider_breaker.state == "closed"


def test_half_open_lets_exactly_one_concurrent_probe_through() -> None:
    clock = Clock()
    breaker = CircuitBreaker(threshold=1, cooldown_s=60, clock=clock)
    breaker.record_failure()
    clock.now = 61
    entered, release = threading.Event(), threading.Event()
    calls: list[int] = []

    class Slow:
        def complete_json(self, **_: object) -> LLMResult:
            calls.append(1)
            entered.set()
            release.wait(5)
            return LLMResult(content={"claims": []}, model="m", usage={})

    provider = BreakerProvider(Slow(), breaker)
    outcomes: list[str] = []
    lock = threading.Lock()

    def attempt() -> None:
        try:
            provider.complete_json(**KW)
            result = "ok"
        except ProviderUnavailableError:
            result = "refused"
        with lock:
            outcomes.append(result)

    probe = threading.Thread(target=attempt)
    probe.start()
    assert entered.wait(5)
    rest = [threading.Thread(target=attempt) for _ in range(7)]
    [t.start() for t in rest]
    [t.join() for t in rest]
    assert len(calls) == 1 and outcomes.count("refused") == 7, "only the probe may reach the provider"
    release.set()
    probe.join()
    assert breaker.state == "closed" and outcomes.count("ok") == 1


def test_a_probe_without_a_verdict_hands_the_probe_to_the_next_caller() -> None:
    clock = Clock()
    breaker = CircuitBreaker(threshold=1, cooldown_s=60, clock=clock)
    breaker.record_failure()
    clock.now = 61
    inner = Counting(ProviderError("bad json or 5xx", retryable=True))
    provider = BreakerProvider(inner, breaker)
    for _ in range(3):
        with pytest.raises(ProviderError):
            provider.complete_json(**KW)
    assert inner.calls == 3 and breaker.state == "half_open"
