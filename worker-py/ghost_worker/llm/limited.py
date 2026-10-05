"""Concurrency cap around any provider (the sync endpoint runs in a thread pool)."""
from __future__ import annotations

import copy
import threading
from collections.abc import Iterator
from contextlib import contextmanager
from typing import Any

from ..errors import ProviderError
from .provider import LLMProvider, LLMResult, TurnResult


class ConcurrencyLimitedProvider:
    def __init__(self, inner: LLMProvider, *, max_concurrent: int, acquire_timeout_s: float) -> None:
        if max_concurrent < 1:
            raise ValueError("max_concurrent must be >= 1")
        self._inner = inner
        self._slots = threading.BoundedSemaphore(max_concurrent)
        self._acquire_timeout_s = acquire_timeout_s

    @property
    def inner(self) -> LLMProvider:
        return self._inner

    @property
    def acquire_timeout_s(self) -> float:
        return self._acquire_timeout_s

    def share(self, inner: LLMProvider, *, acquire_timeout_s: float) -> ConcurrencyLimitedProvider:
        """A new limiter around `inner` drawing from the same slots: one cap for the whole worker, while each
        path waits for a slot no longer than its own deadline."""
        twin = copy.copy(self)
        twin._inner = inner
        twin._acquire_timeout_s = acquire_timeout_s
        return twin

    def shares_slots_with(self, other: ConcurrencyLimitedProvider) -> bool:
        return self._slots is other._slots

    @contextmanager
    def _slot(self) -> Iterator[None]:
        if not self._slots.acquire(timeout=self._acquire_timeout_s):
            raise ProviderError("provider busy: concurrency limit reached", retryable=False)
        try:
            yield
        finally:
            self._slots.release()

    def complete_json(self, *, system: str, user: str, schema: dict[str, Any], schema_name: str) -> LLMResult:
        with self._slot():
            return self._inner.complete_json(system=system, user=user, schema=schema, schema_name=schema_name)

    def complete_turn(self, *, system: str, messages: list[dict[str, Any]], tools: list[dict[str, Any]],
                      schema: dict[str, Any], schema_name: str) -> TurnResult:
        with self._slot():
            return self._inner.complete_turn(system=system, messages=messages, tools=tools,
                                             schema=schema, schema_name=schema_name)
