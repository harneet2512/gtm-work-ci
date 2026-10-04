"""Distinctness: the shared fixture pins the Python and Go implementations to the same verdicts."""
from __future__ import annotations

import json
from pathlib import Path

import pytest

from ghost_worker.strategies.distinct import SIMILARITY_LIMIT, Shape, violation

FIXTURE = Path(__file__).resolve().parents[2] / "fixtures" / "orchestrator" / "distinctness_cases.json"
DATA = json.loads(FIXTURE.read_text(encoding="utf-8"))


def shape(raw: dict) -> Shape:
    return Shape(raw["strategy_type"], raw["action_type"], frozenset(raw["people"]), raw["body"])


def test_the_fixture_limit_is_the_implementation_limit() -> None:
    assert DATA["similarity_limit"] == SIMILARITY_LIMIT


@pytest.mark.parametrize("case", DATA["cases"], ids=[c["name"] for c in DATA["cases"]])
def test_candidates_are_distinct_exactly_as_the_fixture_says(case: dict) -> None:
    reason = violation(shape(case["a"]), shape(case["b"]))
    assert (reason is None) is case["distinct"], reason
