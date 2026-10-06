"""The committed parity cases (fixtures/transitions/parity_cases.json) are exactly what the reference evaluator produces
today. The Go detector's parity test (core-go/internal/transitions/parity_test.go) reads the same file, so the two
implementations cannot drift apart without one of the two tests failing."""
from __future__ import annotations

import json
from collections import Counter

from transition_parity import OUT, build, render


def test_committed_parity_cases_are_current() -> None:
    assert OUT.read_text(encoding="utf-8") == render(), "run: python worker-py/tests/transition_parity.py --write"


def test_parity_cases_cover_every_status_and_both_targets() -> None:
    cases = json.loads(OUT.read_text(encoding="utf-8"))["cases"]
    seen = Counter((c["expected"]["status"], c["expected"]["to_state"]) for c in cases)
    for status in ("CONFIRMED", "CANDIDATE", "UNRESOLVED", "REJECTED"):
        assert any(s == status for s, _ in seen), status
    assert ("CONFIRMED", "EXPANSION") in seen and ("CONFIRMED", "REORG") in seen
    assert any(c["expected"]["closed"] for c in cases), "a stale UNRESOLVED is closed in at least one case"


def test_parity_generation_is_deterministic() -> None:
    assert build() == build()
