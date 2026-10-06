from __future__ import annotations

import threading
import time
from concurrent.futures import ThreadPoolExecutor

import pytest

from ghost_worker.errors import ProviderError
from ghost_worker.llm.limited import ConcurrencyLimitedProvider
from ghost_worker.llm.provider import LLMResult

KW = {"system": "s", "user": "u", "schema": {}, "schema_name": "n"}


class Tracking:
    def __init__(self, hold_s: float = 0.02) -> None:
        self.hold_s = hold_s
        self.active = 0
        self.peak = 0
        self.lock = threading.Lock()

    def complete_json(self, **_: object) -> LLMResult:
        with self.lock:
            self.active += 1
            self.peak = max(self.peak, self.active)
        time.sleep(self.hold_s)
        with self.lock:
            self.active -= 1
        return LLMResult(content={"claims": []}, model="m", usage={})


def test_never_exceeds_the_cap() -> None:
    inner = Tracking()
    limited = ConcurrencyLimitedProvider(inner, max_concurrent=3, acquire_timeout_s=30)
    with ThreadPoolExecutor(max_workers=16) as pool:
        results = list(pool.map(lambda _: limited.complete_json(**KW), range(32)))
    assert len(results) == 32
    assert 1 <= inner.peak <= 3


def test_waiters_time_out_with_provider_error() -> None:
    inner = Tracking(hold_s=0.5)
    limited = ConcurrencyLimitedProvider(inner, max_concurrent=1, acquire_timeout_s=0.05)
    with ThreadPoolExecutor(max_workers=2) as pool:
        first = pool.submit(limited.complete_json, **KW)
        time.sleep(0.1)
        second = pool.submit(limited.complete_json, **KW)
        with pytest.raises(ProviderError, match="busy"):
            second.result()
        assert first.result().model == "m"


def test_slot_is_released_when_the_provider_fails() -> None:
    class Failing:
        def complete_json(self, **_: object) -> LLMResult:
            raise ProviderError("down")

    limited = ConcurrencyLimitedProvider(Failing(), max_concurrent=1, acquire_timeout_s=0.1)
    for _ in range(3):
        with pytest.raises(ProviderError, match="down"):
            limited.complete_json(**KW)


def test_rejects_non_positive_cap() -> None:
    with pytest.raises(ValueError):
        ConcurrencyLimitedProvider(Tracking(), max_concurrent=0, acquire_timeout_s=1)


def test_shared_limiter_draws_from_the_same_slots_with_its_own_acquire_timeout() -> None:
    first, second = Tracking(hold_s=0.5), Tracking()
    limited = ConcurrencyLimitedProvider(first, max_concurrent=1, acquire_timeout_s=30)
    twin = limited.share(second, acquire_timeout_s=0.05)
    assert twin.inner is second and twin.acquire_timeout_s == 0.05 and limited.inner is first
    assert twin.shares_slots_with(limited)
    assert not ConcurrencyLimitedProvider(first, max_concurrent=1, acquire_timeout_s=1).shares_slots_with(limited)
    with ThreadPoolExecutor(max_workers=2) as pool:
        busy = pool.submit(limited.complete_json, **KW)
        time.sleep(0.1)
        with pytest.raises(ProviderError, match="busy"):
            twin.complete_json(**KW)  # the one slot is held through the other limiter
        assert busy.result().model == "m"
    assert twin.complete_json(**KW).model == "m"
