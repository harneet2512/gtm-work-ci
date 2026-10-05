"""Deterministic seeding API of the synthetic layer generator.

Every random decision draws from its own named stream, `stream(root_seed, *path)`, e.g.
`stream(seed, "account", account_id, "reorg")`. Streams are independent: adding a draw to one
stream (a new feature, a new account) never shifts another, so regenerating with the same root
seed and rule file reproduces every unchanged record byte for byte.

Only `random.Random(int).random()` is used: it is the one sequence CPython promises to keep
across versions ("the generator's random() method will continue to produce the same sequence
when the compatible seeder is given the same seed"). Every helper is built on it.
"""
from __future__ import annotations

import hashlib
import math
import random
from collections.abc import Sequence
from typing import TypeVar

T = TypeVar("T")

DEFAULT_ROOT_SEED = 20261002


def derive_seed(root: int, *path: str) -> int:
    """64-bit seed from the root seed and a path. Parts are length-prefixed, so ("ab","c") != ("a","bc")."""
    if not isinstance(root, int) or isinstance(root, bool):
        raise TypeError(f"root seed must be an int, got {type(root).__name__}")
    h = hashlib.sha256(str(root).encode("ascii"))
    for part in path:
        data = str(part).encode("utf-8")
        h.update(len(data).to_bytes(4, "big"))
        h.update(data)
    return int.from_bytes(h.digest()[:8], "big")


class Stream:
    """A named random stream. Helpers consume only uniform draws."""

    def __init__(self, seed: int) -> None:
        self._rng = random.Random(seed)

    def uniform(self) -> float:
        return self._rng.random()

    def bernoulli(self, p: float) -> bool:
        if not 0.0 <= p <= 1.0:
            raise ValueError(f"probability {p} outside [0, 1]")
        return self.uniform() < p

    def integer(self, lo: int, hi: int) -> int:
        """Uniform integer in [lo, hi] inclusive."""
        if hi < lo:
            raise ValueError(f"empty range [{lo}, {hi}]")
        return lo + min(int(self.uniform() * (hi - lo + 1)), hi - lo)

    def choice(self, items: Sequence[T]) -> T:
        if not items:
            raise ValueError("choice from an empty sequence")
        return items[self.integer(0, len(items) - 1)]

    def normal(self, mu: float, sd: float) -> float:
        """Box-Muller on two uniform draws (one value per call, no cached second value).

        Rounded to 9 decimals: libm log/cos may differ in the last bit across platforms."""
        u1 = 1.0 - self.uniform()  # (0, 1], keeps log finite
        u2 = self.uniform()
        return round(mu + sd * math.sqrt(-2.0 * math.log(u1)) * math.cos(2.0 * math.pi * u2), 9)

    def shuffled(self, items: Sequence[T]) -> list[T]:
        """Fisher-Yates on a copy; the input is not modified."""
        out = list(items)
        for i in range(len(out) - 1, 0, -1):
            j = self.integer(0, i)
            out[i], out[j] = out[j], out[i]
        return out


def stream(root: int, *path: str) -> Stream:
    return Stream(derive_seed(root, *path))
