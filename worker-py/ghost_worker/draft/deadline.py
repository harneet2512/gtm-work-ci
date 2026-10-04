"""Overall wall-clock budget for one /v1/draft call (agent turns, core pulls and the skill together)."""
from __future__ import annotations

import time
from collections.abc import Callable

from ..errors import DraftDeadlineError


class Deadline:
    def __init__(self, budget_s: float, clock: Callable[[], float] = time.monotonic) -> None:
        self._clock = clock
        self._end = clock() + budget_s

    def remaining(self) -> float:
        return self._end - self._clock()

    def check(self, step: str) -> float:
        """Seconds left; raises DraftDeadlineError (before `step` starts) when none are."""
        left = self.remaining()
        if left <= 0:
            raise DraftDeadlineError(f"draft deadline exceeded before {step}")
        return left
