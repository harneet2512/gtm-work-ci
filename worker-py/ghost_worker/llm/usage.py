"""Per-request usage metering (HAR-145): a MeteredProvider wraps the request's provider and a UsageMeter sums what every
completion reported. Metering is explicit per request (the judge runner calls the provider from a thread pool, so a
context variable would not follow it).

Retries are not visible from outside the provider, so the litellm provider notes each one on a thread-local counter
(`note_retry`) and the metered call drains it (`take_retries`) on the thread that made the call.
"""
from __future__ import annotations

import threading
import time
from collections.abc import Callable, Mapping
from typing import Any

from ..models.usage import UsageSummary
from .provider import LLMProvider, LLMResult, TurnResult

_local = threading.local()


def note_retry() -> None:
    """Count one extra provider attempt on the calling thread."""
    _local.retries = getattr(_local, "retries", 0) + 1


def take_retries() -> int:
    """The retries noted on this thread since the last take; resets the counter."""
    n = getattr(_local, "retries", 0)
    _local.retries = 0
    return n


def _reported(usage: Mapping[str, Any], *keys: str) -> int | None:
    """Like _count, but None when the provider reported none of the keys: a missing figure is not a measured 0."""
    for key in keys:
        value = usage.get(key)
        if isinstance(value, (int, float)) and not isinstance(value, bool) and value >= 0:
            return int(value)
    return None


def _count(usage: Mapping[str, Any], *keys: str) -> int:
    """The first reported, non-negative number among keys (a provider names the same figure differently)."""
    for key in keys:
        value = usage.get(key)
        if isinstance(value, (int, float)) and not isinstance(value, bool) and value >= 0:
            return int(value)
    return 0


class UsageMeter:
    """Thread-safe sums of the completions of one request."""

    def __init__(self) -> None:
        self._lock = threading.Lock()
        self._source = "live"
        self._cached_calls = self._reasoning_calls = 0
        self._calls = self._input = self._output = self._cached = self._reasoning = 0
        self._tool_calls = self._retries = self._model_ms = self._cost_calls = 0
        self._cost = 0.0
        self._models: list[str] = []

    def set_source(self, usage_source: str) -> None:
        """Where the answers come from: "live" (a real provider) or "replay" (recorded cassettes, no spend)."""
        self._source = usage_source

    def record_call(self, usage: Mapping[str, Any], model: str, *, elapsed_s: float, retries: int, tool_calls: int) -> None:
        """One completion that returned a response. In replay mode nothing is recorded: nothing was spent."""
        if self._source == "replay":
            return
        inp = _count(usage, "prompt_tokens", "input_tokens")
        out = _count(usage, "completion_tokens", "output_tokens")
        cached = _reported(usage, "cached_tokens", "cache_read_input_tokens")
        reasoning = _reported(usage, "reasoning_tokens")
        cost = usage.get("cost")
        with self._lock:
            self._calls += 1
            self._input += inp
            self._output += out
            if cached is not None:  # a part of the input, never more
                self._cached_calls += 1
                self._cached += min(cached, inp)
            if reasoning is not None:
                self._reasoning_calls += 1
                self._reasoning += min(reasoning, out)
            self._tool_calls += tool_calls
            self._retries += retries
            self._model_ms += max(0, round(elapsed_s * 1000))
            if isinstance(cost, (int, float)) and not isinstance(cost, bool) and cost >= 0:
                self._cost_calls += 1
                self._cost += float(cost)
            if model and model not in self._models:
                self._models.append(model)

    def record_failure(self, *, elapsed_s: float, retries: int) -> None:
        """A request that raised: its retries and time were really spent, but no completion returned usage."""
        if self._source == "replay":
            return
        with self._lock:
            self._retries += retries
            self._model_ms += max(0, round(elapsed_s * 1000))

    def summary(self) -> UsageSummary:
        with self._lock:
            # The cost is the sum only when every call reported one: a partial sum would understate it.
            cost = round(self._cost, 6) if self._calls > 0 and self._cost_calls == self._calls else None
            # Likewise cached and reasoning tokens: a sum over calls that did not all report them would be a floor
            # presented as a figure, so it is null instead.
            every = self._calls > 0
            cached = self._cached if every and self._cached_calls == self._calls else None
            reasoning = self._reasoning if every and self._reasoning_calls == self._calls else None
            return UsageSummary(
                model_calls=self._calls, input_tokens=self._input, output_tokens=self._output,
                cached_input_tokens=cached, reasoning_tokens=reasoning, tool_calls=self._tool_calls,
                retries=self._retries, model_ms=self._model_ms, cost_usd=cost, models=tuple(self._models),
                usage_source=self._source)


class MeteredProvider:
    """Delegates to `inner` and records every completion on `meter`."""

    def __init__(self, inner: LLMProvider, meter: UsageMeter, *, clock: Callable[[], float] = time.monotonic) -> None:
        self._inner = inner
        self._meter = meter
        meter.set_source(getattr(inner, "usage_source", "live"))
        self._clock = clock

    def complete_json(self, *, system: str, user: str, schema: dict[str, Any], schema_name: str) -> LLMResult:
        started = self._begin()
        try:
            result = self._inner.complete_json(system=system, user=user, schema=schema, schema_name=schema_name)
        except BaseException:
            self._meter.record_failure(elapsed_s=self._clock() - started, retries=take_retries())
            raise
        self._meter.record_call(result.usage, result.model, elapsed_s=self._clock() - started, retries=take_retries(),
                                tool_calls=0)
        return result

    def complete_turn(self, *, system: str, messages: list[dict[str, Any]], tools: list[dict[str, Any]],
                      schema: dict[str, Any], schema_name: str) -> TurnResult:
        started = self._begin()
        try:
            result = self._inner.complete_turn(system=system, messages=messages, tools=tools, schema=schema,
                                               schema_name=schema_name)
        except BaseException:
            self._meter.record_failure(elapsed_s=self._clock() - started, retries=take_retries())
            raise
        self._meter.record_call(result.usage, result.model, elapsed_s=self._clock() - started, retries=take_retries(),
                                tool_calls=len(result.tool_calls))
        return result

    def _begin(self) -> float:
        take_retries()  # a retry noted before this call belongs to nobody
        return self._clock()
