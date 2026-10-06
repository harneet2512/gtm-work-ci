"""A request deadline that follows the provider call it bounds.

Ask Cliff runs under a wall-clock deadline (120 s). A deadline that is only checked between model turns lets one slow
turn run past it, so the loop publishes its Deadline here, in a context variable, for the duration of each turn; the
provider stack reads the time left (`remaining_s`) and never waits longer for a slot, a first byte or a retry. The
loop also guards the turn itself (ask/loop.py), so a provider that ignores the bound still cannot overrun it.

A context variable (not a parameter) keeps the `LLMProvider` protocol, and with it the Play path's provider calls and
cassette keys, exactly as they were."""
from __future__ import annotations

from collections.abc import Iterator
from contextlib import contextmanager
from contextvars import ContextVar
from typing import Protocol


class _HasRemaining(Protocol):
    def remaining(self) -> float: ...


_CURRENT: ContextVar[_HasRemaining | None] = ContextVar("ghost_call_deadline", default=None)


@contextmanager
def bounded_by(deadline: _HasRemaining) -> Iterator[None]:
    """Make `deadline` the bound of provider calls made inside the block (in this context)."""
    token = _CURRENT.set(deadline)
    try:
        yield
    finally:
        _CURRENT.reset(token)


def remaining_s(default: float | None = None) -> float | None:
    """Seconds left of the enclosing deadline (never negative), or `default` when no deadline is set."""
    current = _CURRENT.get()
    if current is None:
        return default
    return max(0.0, current.remaining())
