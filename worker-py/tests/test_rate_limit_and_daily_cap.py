"""HAR-135 addendum: process-wide RPM pacing, 429 backoff with Retry-After, fallback after repeated 429s, and the
free-models-per-day cap tripping the breaker. Fakes only: no network, no real sleeping."""
from __future__ import annotations

import threading
import time
from datetime import datetime, timezone
from types import SimpleNamespace

import litellm
import pytest

from ghost_worker.errors import DailyCapError, ProviderError, ProviderUnavailableError, RateLimitedError
from ghost_worker.llm.breaker import BreakerProvider, CircuitBreaker
from ghost_worker.llm.classify import is_daily_cap, retry_after_seconds, seconds_until_utc_midnight
from ghost_worker.llm.factory import build_provider
from ghost_worker.llm.litellm_provider import LiteLLMProvider
from ghost_worker.llm.ratelimit import RateLimiter, shared_limiter
from ghost_worker.settings import Settings

KW = {"system": "s", "user": "u", "schema": {}, "schema_name": "n"}
DAILY = "Rate limit exceeded: free-models-per-day. Add 10 credits to unlock 1000 free model requests per day"


class Upstream(Exception):
    def __init__(self, status: int, message: str = "slow down", retry_after: str | None = None) -> None:
        super().__init__(message)
        self.status_code = status
        self.response = SimpleNamespace(headers={"retry-after": retry_after} if retry_after else {})


def ok(model: str = "m") -> SimpleNamespace:
    return SimpleNamespace(choices=[SimpleNamespace(message=SimpleNamespace(content='{"a": 1}'))], model=model, usage=None)


class Script:
    def __init__(self, *outcomes: object) -> None:
        self.outcomes, self.calls = list(outcomes), []

    def __call__(self, **kwargs: object) -> object:
        self.calls.append(kwargs["model"])
        outcome = self.outcomes.pop(0)
        if isinstance(outcome, BaseException):
            raise outcome
        return outcome


def provider(monkeypatch: pytest.MonkeyPatch, *outcomes: object, sleeps: list[float] | None = None,
             fallback: str | None = "openrouter/b/fallback", **kw: object) -> tuple[LiteLLMProvider, Script]:
    script = Script(*outcomes)
    monkeypatch.setattr(litellm, "completion", script)
    rec = sleeps if sleeps is not None else []
    llm = LiteLLMProvider(model="openrouter/a/primary", fallback_model=fallback, api_key="k", timeout_s=60,
                          sleep=rec.append, rng=lambda: 1.0, **kw)  # type: ignore[arg-type]
    return llm, script


# ---- pacing ----------------------------------------------------------------------------------------------

class FakeTime:
    def __init__(self) -> None:
        self.now = 0.0

    def clock(self) -> float:
        return self.now

    def sleep(self, seconds: float) -> None:
        self.now += seconds


def test_pacing_honours_the_rpm_limit() -> None:
    t = FakeTime()
    limiter = RateLimiter(15, clock=t.clock, sleep=t.sleep)  # one request every 4 s
    stamps = []
    for _ in range(6):
        limiter.acquire(1000)
        stamps.append(t.now)
    assert stamps == [0, 4, 8, 12, 16, 20]
    assert 60 / (stamps[-1] / 5) == pytest.approx(15)


def test_burst_is_spent_then_paced_and_idle_time_refills() -> None:
    t = FakeTime()
    limiter = RateLimiter(60, burst=3, clock=t.clock, sleep=t.sleep)
    for _ in range(3):
        assert limiter.acquire(10) == 0
    assert limiter.acquire(10) == pytest.approx(1.0)
    t.now += 100
    assert limiter.acquire(10) == 0


def test_a_wait_longer_than_the_deadline_is_refused_without_taking_a_slot() -> None:
    t = FakeTime()
    limiter = RateLimiter(15, clock=t.clock, sleep=t.sleep)
    limiter.acquire(10)
    with pytest.raises(ProviderError, match="deadline"):
        limiter.acquire(1)  # needs 4 s
    assert limiter.acquire(10) == pytest.approx(4.0), "the refused call must not have consumed a slot"


def test_concurrent_callers_share_one_budget() -> None:
    limiter = RateLimiter(3000)  # real clock, one request per 20 ms
    stamps: list[float] = []
    lock = threading.Lock()

    def worker() -> None:
        for _ in range(4):
            limiter.acquire(30)
            with lock:
                stamps.append(time.monotonic())

    threads = [threading.Thread(target=worker) for _ in range(5)]
    [t.start() for t in threads]
    [t.join() for t in threads]
    stamps.sort()
    assert len(stamps) == 20
    assert (stamps[-1] - stamps[0]) / 19 >= 0.0185  # 20 requests spread over >= 19 intervals, not a burst


def test_the_limiter_is_one_object_per_process_and_off_by_default() -> None:
    assert shared_limiter(None) is None and shared_limiter(0) is None
    assert shared_limiter(15) is shared_limiter(15)


def test_factory_gives_extract_and_judge_providers_the_same_limiter() -> None:
    cfg = Settings(_env_file=None, ghost_llm_mode="live", openrouter_api_key="sk-test", ghost_llm_max_rpm=15)
    a, b = build_provider(cfg), build_provider(cfg, deadline_s=180)
    assert a._limiter is b._limiter and a._limiter is not None  # type: ignore[attr-defined]
    assert build_provider(Settings(_env_file=None, openrouter_api_key="sk-test"))._limiter is None  # type: ignore[attr-defined]


def test_every_upstream_attempt_is_paced_including_retries(monkeypatch: pytest.MonkeyPatch) -> None:
    t = FakeTime()
    limiter = RateLimiter(15, clock=t.clock, sleep=t.sleep)
    llm, script = provider(monkeypatch, Upstream(429), ok(), limiter=limiter, rate_limit_switch_after=3)
    llm.complete_json(**KW)
    assert len(script.calls) == 2 and t.now == pytest.approx(4.0)


# ---- 429 handling ----------------------------------------------------------------------------------------

def test_retry_after_is_honoured(monkeypatch: pytest.MonkeyPatch) -> None:
    sleeps: list[float] = []
    llm, script = provider(monkeypatch, Upstream(429, retry_after="7"), ok(), sleeps=sleeps, rate_limit_switch_after=3)
    assert llm.complete_json(**KW).content == {"a": 1}
    assert sleeps == [7.0] and script.calls == ["openrouter/a/primary"] * 2


def test_without_retry_after_backoff_is_exponential_with_jitter(monkeypatch: pytest.MonkeyPatch) -> None:
    sleeps: list[float] = []
    llm, _ = provider(monkeypatch, Upstream(429), Upstream(429), ok(), sleeps=sleeps, rate_limit_switch_after=3,
                      backoff_base_s=2.0)
    llm.rng = None  # type: ignore[attr-defined]
    llm._rng = lambda: 0.0  # lowest jitter: half the step
    llm.complete_json(**KW)
    assert sleeps == [1.0, 2.0]  # base 2 s: steps 2 and 4, jittered to 50%
    assert RateLimitedError("x").retry_after_s is None


def test_backoff_is_capped(monkeypatch: pytest.MonkeyPatch) -> None:
    sleeps: list[float] = []
    llm, _ = provider(monkeypatch, *[Upstream(429)] * 4, ok(), sleeps=sleeps, rate_limit_switch_after=5,
                      backoff_base_s=10, backoff_cap_s=15)
    llm.complete_json(**KW)
    assert max(sleeps) == 15.0


def test_a_retry_after_beyond_the_deadline_is_not_slept(monkeypatch: pytest.MonkeyPatch) -> None:
    sleeps: list[float] = []
    llm, script = provider(monkeypatch, Upstream(429, retry_after="500"), sleeps=sleeps, fallback=None,
                           rate_limit_switch_after=3)
    with pytest.raises(RateLimitedError):
        llm.complete_json(**KW)  # 90 s deadline < 500 s
    assert sleeps == [] and len(script.calls) == 1


def test_fallback_is_used_after_n_consecutive_429s(monkeypatch: pytest.MonkeyPatch) -> None:
    sleeps: list[float] = []
    llm, script = provider(monkeypatch, Upstream(429), Upstream(429), Upstream(429), ok("fb"), sleeps=sleeps,
                           rate_limit_switch_after=3)
    result = llm.complete_json(**KW)
    assert result.model == "fb"
    assert script.calls == ["openrouter/a/primary"] * 3 + ["openrouter/b/fallback"]
    assert len(sleeps) == 2, "backed off between the three primary attempts, not after the last one"


def test_a_single_429_still_switches_at_once_by_default(monkeypatch: pytest.MonkeyPatch) -> None:
    llm, script = provider(monkeypatch, Upstream(429), ok())
    llm.complete_json(**KW)
    assert script.calls == ["openrouter/a/primary", "openrouter/b/fallback"]


def test_429_stays_a_retryable_provider_error_when_everything_is_exhausted(monkeypatch: pytest.MonkeyPatch) -> None:
    llm, _ = provider(monkeypatch, Upstream(429), Upstream(429), fallback=None, rate_limit_switch_after=2)
    with pytest.raises(ProviderError) as info:
        llm.complete_json(**KW)
    assert info.value.retryable and not isinstance(info.value, ProviderUnavailableError)


def test_retry_after_parsing() -> None:
    assert retry_after_seconds(Upstream(429, retry_after="12")) == 12.0
    assert retry_after_seconds(Upstream(429, retry_after="soon")) is None
    assert retry_after_seconds(Upstream(429)) is None


# ---- daily cap -------------------------------------------------------------------------------------------

def test_daily_cap_detection_and_reset_time() -> None:
    assert is_daily_cap(Upstream(429, DAILY))
    assert not is_daily_cap(Upstream(429, "Rate limit exceeded, retry shortly"))
    assert not is_daily_cap(Upstream(500, DAILY))
    assert seconds_until_utc_midnight(datetime(2026, 10, 3, 23, 0, tzinfo=timezone.utc)) == 3600.0


def test_daily_cap_is_not_retried_and_trips_the_breaker_with_a_clear_reason(monkeypatch: pytest.MonkeyPatch) -> None:
    # The breaker below uses a fake clock, but the daily-cap reset is read from the real wall clock when the
    # 429 is classified. Pin it so the test cannot depend on the time of day: near UTC midnight the real reset
    # is seconds away, and the "still open after an hour" assertion would fail.
    monkeypatch.setattr("ghost_worker.llm.litellm_provider.seconds_until_utc_midnight", lambda *_: 7200.0)
    sleeps: list[float] = []
    llm, script = provider(monkeypatch, Upstream(429, DAILY), ok(), sleeps=sleeps, rate_limit_switch_after=3)
    now = [0.0]
    breaker = CircuitBreaker(threshold=5, cooldown_s=60, clock=lambda: now[0])
    guarded = BreakerProvider(llm, breaker)

    with pytest.raises(DailyCapError, match="00:00 UTC"):
        guarded.complete_json(**KW)
    assert sleeps == [] and len(script.calls) == 1, "no backoff, no fallback: the cap applies to every free model"
    assert breaker.state == "open", "one daily-cap response trips the breaker; it does not wait for N failures"

    with pytest.raises(ProviderUnavailableError):
        guarded.complete_json(**KW)
    assert len(script.calls) == 1, "while open, no further upstream call"

    now[0] = 3600  # well past the generic 60 s cool-down: still open until the daily reset
    assert breaker.state == "open"
    now[0] = 86400
    assert breaker.state == "half_open"
