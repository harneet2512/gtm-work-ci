"""Worker-side provider circuit breaker (HAR-135): after N consecutive non-retryable provider failures every
model call is refused locally until a cool-down passes, so a dead key is not hammered."""
from __future__ import annotations

import logging
import threading
import time
from collections.abc import Callable
from typing import Any

from ..errors import DailyCapError, ProviderUnavailableError
from .provider import LLMProvider, LLMResult, TurnResult

log = logging.getLogger(__name__)

DEFAULT_THRESHOLD = 5
DEFAULT_COOLDOWN_S = 300.0


class CircuitBreaker:
    """closed -> open after `threshold` consecutive failures -> half-open after `cooldown_s` (calls allowed;
    one failure re-opens, one success closes). Thread-safe."""

    def __init__(self, threshold: int = DEFAULT_THRESHOLD, cooldown_s: float = DEFAULT_COOLDOWN_S,
                 clock: Callable[[], float] = time.monotonic) -> None:
        if threshold < 1 or cooldown_s <= 0:
            raise ValueError("threshold must be >= 1 and cooldown_s > 0")
        self._threshold, self._cooldown_s, self._clock = threshold, cooldown_s, clock
        self._lock = threading.Lock()
        self._failures = 0
        self._opened_at: float | None = None
        self._cooldown_override: float | None = None  # a daily cap holds the breaker open until the cap resets
        self._probing = False  # half-open: one probe call in flight at a time
        self._trips = 0

    @property
    def state(self) -> str:
        with self._lock:
            return self._state_locked()

    @property
    def trips(self) -> int:
        return self._trips

    def _state_locked(self) -> str:
        if self._opened_at is None:
            return "closed"
        cooldown = self._cooldown_override or self._cooldown_s
        return "open" if self._clock() - self._opened_at < cooldown else "half_open"

    def allow(self) -> bool:
        """May a call go out? While half-open exactly one caller gets True (the probe) until it reports back."""
        with self._lock:
            state = self._state_locked()
            if state == "open":
                return False
            if state == "half_open":
                if self._probing:
                    return False
                self._probing = True
            return True

    def release_probe(self) -> None:
        """The probe ended without a verdict (a non-counting error): let the next caller probe."""
        with self._lock:
            self._probing = False

    def record_success(self) -> None:
        with self._lock:
            self._failures, self._opened_at, self._cooldown_override = 0, None, None
            self._probing = False

    def trip(self, reason: str, cooldown_s: float) -> None:
        """Open at once for `cooldown_s` (a daily cap: retrying cannot help until it resets)."""
        with self._lock:
            self._opened_at, self._cooldown_override = self._clock(), cooldown_s
            self._probing = False
            self._failures += 1
            self._trips += 1
            log.error("provider circuit breaker OPEN: model calls paused", extra={
                "reason": reason, "cooldown_s": cooldown_s, "trips": self._trips})

    def record_failure(self) -> None:
        with self._lock:
            half_open = self._state_locked() == "half_open"
            self._probing = False
            self._failures += 1
            if half_open or (self._opened_at is None and self._failures >= self._threshold):
                self._opened_at = self._clock()
                self._trips += 1
                log.error("provider circuit breaker OPEN: model calls paused", extra={
                    "consecutive_failures": self._failures, "cooldown_s": self._cooldown_s, "trips": self._trips})

    def reset(self) -> None:
        self.record_success()


class BreakerProvider:
    """Counts non-retryable provider failures and refuses calls while the breaker is open."""

    def __init__(self, inner: LLMProvider, breaker: CircuitBreaker) -> None:
        self._inner, self.breaker = inner, breaker

    @property
    def inner(self) -> LLMProvider:
        return self._inner

    @property
    def usage_source(self) -> str:
        """Where the wrapped provider's answers come from (live or replay); metering reads it."""
        return getattr(self._inner, "usage_source", "live")

    def _guard(self, call: Callable[[], Any]) -> Any:
        if not self.breaker.allow():
            raise ProviderUnavailableError("provider circuit breaker is open")
        reported = False
        try:
            result = call()
            reported = True
            self.breaker.record_success()
            return result
        except DailyCapError as exc:
            reported = True
            self.breaker.trip(str(exc), exc.resume_in_s)
            raise
        except ProviderUnavailableError:
            reported = True
            self.breaker.record_failure()
            raise
        finally:
            if not reported:
                self.breaker.release_probe()  # e.g. unusable JSON or a recoverable 5xx: no verdict on the provider

    def complete_json(self, *, system: str, user: str, schema: dict[str, Any], schema_name: str) -> LLMResult:
        return self._guard(lambda: self._inner.complete_json(system=system, user=user, schema=schema,
                                                             schema_name=schema_name))

    def complete_turn(self, *, system: str, messages: list[dict[str, Any]], tools: list[dict[str, Any]],
                      schema: dict[str, Any], schema_name: str) -> TurnResult:
        return self._guard(lambda: self._inner.complete_turn(system=system, messages=messages, tools=tools,
                                                             schema=schema, schema_name=schema_name))
