"""The cache provider's spend accounting: one global recording lock and a local count of what was spent since the usage was
read, so parallel misses cannot overrun the cap on a delayed usage figure. Retries and the fallback model count too."""
from __future__ import annotations

import threading
from pathlib import Path
from typing import Any

import pytest

from ghost_worker.errors import ProviderError, SpendCapError
from ghost_worker.llm.cache_provider import CachingProvider, SpendGuard
from ghost_worker.llm.provider import LLMResult, TurnResult
from ghost_worker.llm.usage import note_retry, take_retries

SCHEMA = {"type": "object", "properties": {"a": {"type": "string"}}}


class Billing:
    """An upstream that reports a cost (or none), notes retries like the litellm provider, and may fail."""

    def __init__(self, cost: float | None = 0.2, retries: int = 0, fail: bool = False) -> None:
        self.cost, self.retries, self.fail = cost, retries, fail
        self.calls = 0
        self._lock = threading.Lock()

    def complete_json(self, *, system: str, user: str, schema: dict[str, Any], schema_name: str) -> LLMResult:
        with self._lock:
            self.calls += 1
        for _ in range(self.retries):
            note_retry()
        if self.fail:
            raise ProviderError("provider down", retryable=False)
        usage = {} if self.cost is None else {"cost": self.cost}
        return LLMResult(content={"n": 1}, model="live/x", usage=usage)

    def complete_turn(self, **_: Any) -> TurnResult:  # pragma: no cover - the spend tests use complete_json only
        raise NotImplementedError


def ask(p: CachingProvider, user: str) -> LLMResult:
    return p.complete_json(system="s", user=user, schema=SCHEMA, schema_name="s")


def make(tmp_path: Path, up: Billing, usage: float = 7.0, cap: float = 7.5) -> tuple[CachingProvider, SpendGuard]:
    guard = SpendGuard(cap_usd=cap, reserve_usd=0.05, fetch=lambda: usage)  # a usage figure that never moves (it lags)
    return CachingProvider(up, tmp_path, "x", guard=guard), guard


def test_parallel_misses_cannot_overrun_the_cap_on_a_stale_usage_figure(tmp_path):
    up = Billing(cost=0.2)
    p, _ = make(tmp_path, up)
    outcomes: list[str] = []

    def one(i: int) -> None:
        try:
            ask(p, f"q{i}")
            outcomes.append("ok")
        except SpendCapError:
            outcomes.append("refused")

    threads = [threading.Thread(target=one, args=(i,)) for i in range(8)]
    for t in threads:
        t.start()
    for t in threads:
        t.join()
    # 7.00 + 3 x 0.20 = 7.60 > 7.50: a fourth call would pass the cap, so the guard stops after exactly three
    assert up.calls == 3 and outcomes.count("ok") == 3 and outcomes.count("refused") == 5


def test_the_local_count_is_the_reported_cost_or_the_reserve_when_none_is_reported(tmp_path):
    p, guard = make(tmp_path, Billing(cost=0.2))
    ask(p, "a")
    assert guard.local_spend_usd == pytest.approx(0.2)
    p2, guard2 = make(tmp_path / "b", Billing(cost=None))
    ask(p2, "a")
    assert guard2.local_spend_usd == pytest.approx(0.05)


def test_retries_and_the_fallback_attempt_count(tmp_path):
    p, guard = make(tmp_path, Billing(cost=0.01, retries=2))
    ask(p, "a")
    # the answer's 0.01 plus a reserve for each of the two extra attempts (a JSON retry, the fallback model)
    assert guard.local_spend_usd == pytest.approx(0.01 + 2 * 0.05)


def test_a_failed_call_still_counts_every_attempt_it_made(tmp_path):
    p, guard = make(tmp_path, Billing(retries=1, fail=True))
    with pytest.raises(ProviderError):
        ask(p, "a")
    assert guard.local_spend_usd == pytest.approx(2 * 0.05)


def test_the_retries_stay_visible_to_the_request_meter(tmp_path):
    take_retries()
    p, _ = make(tmp_path, Billing(retries=2))
    ask(p, "a")
    assert take_retries() == 2


def test_a_usage_figure_that_catches_up_wins_over_the_local_estimate(tmp_path):
    readings = iter([7.0, 7.46])
    guard = SpendGuard(cap_usd=7.5, reserve_usd=0.05, fetch=lambda: next(readings))
    p = CachingProvider(Billing(cost=0.01), tmp_path, "x", guard=guard)
    ask(p, "a")  # reads 7.00, spends about 0.01
    with pytest.raises(SpendCapError):
        ask(p, "b")  # reads 7.46: 7.46 + the 0.05 reserve passes the cap although the local estimate is only 7.01


def test_the_guard_never_records_for_a_replayed_answer(tmp_path):
    p, guard = make(tmp_path, Billing(cost=0.2))
    ask(p, "a")
    ask(p, "a")
    assert guard.local_spend_usd == pytest.approx(0.2)


def test_without_a_guard_nothing_is_counted_and_nothing_breaks(tmp_path):
    p = CachingProvider(Billing(retries=1), tmp_path, "x")
    ask(p, "a")
