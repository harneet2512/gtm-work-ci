"""Process-wide request pacing for rate-limited (e.g. OpenRouter `:free`) models (HAR-135 addendum).

`GHOST_LLM_MAX_RPM` sets a token bucket shared by every provider in the process, so concurrent judge,
extract and draft calls draw from one budget. Unset means unlimited."""
from __future__ import annotations

import threading
import time
from collections.abc import Callable

from ..errors import ProviderError


class RateLimiter:
    """Token bucket: refills at `max_rpm / 60` tokens per second up to `burst` (default 1: strict pacing).
    A caller that must wait reserves its slot first, so concurrent callers queue behind each other."""

    def __init__(self, max_rpm: float, *, burst: float = 1.0, clock: Callable[[], float] = time.monotonic,
                 sleep: Callable[[float], None] = time.sleep) -> None:
        if max_rpm <= 0 or burst < 1:
            raise ValueError("max_rpm must be > 0 and burst >= 1")
        self.max_rpm = max_rpm
        self._rate = max_rpm / 60.0
        self._burst = burst
        self._clock, self._sleep = clock, sleep
        self._lock = threading.Lock()
        self._tokens = burst
        self._stamp = clock()

    def acquire(self, max_wait_s: float) -> float:
        """Block until a request may be sent; returns the seconds waited. Raises ProviderError (not
        reserving anything) when the wait would exceed `max_wait_s`, the call's remaining deadline."""
        with self._lock:
            now = self._clock()
            self._tokens = min(self._burst, self._tokens + (now - self._stamp) * self._rate)
            self._stamp = now
            wait = 0.0 if self._tokens >= 1 else (1 - self._tokens) / self._rate
            if wait > max_wait_s:
                raise ProviderError(f"rate limiter: waiting {wait:.0f}s for a free slot would exceed the call deadline",
                                    retryable=False)
            self._tokens -= 1
        if wait > 0:
            self._sleep(wait)
        return wait


_shared: RateLimiter | None = None
_shared_lock = threading.Lock()


def shared_limiter(max_rpm: float | None) -> RateLimiter | None:
    """The one limiter of this process for `max_rpm` (None or 0: unlimited). Every provider built with the
    same setting gets the same object, which is what makes the budget process-wide."""
    global _shared
    if not max_rpm:
        return None
    with _shared_lock:
        if _shared is None or _shared.max_rpm != max_rpm:
            _shared = RateLimiter(max_rpm)
        return _shared
