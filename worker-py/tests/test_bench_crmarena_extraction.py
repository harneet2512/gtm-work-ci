"""bench/data/crmarena_extraction_metrics.py: the pure parts of the CRMArena extraction report."""
from __future__ import annotations

import json
import random
import sys
from pathlib import Path

import pytest

ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT / "bench" / "data"))

import crmarena_extraction_metrics as m  # noqa: E402


def test_percentiles_interpolate_and_handle_empty() -> None:
    assert m.percentile([], 50) is None
    assert m.percentile([10, 20, 30, 40], 50) == 25
    assert m.percentile([1, 2, 3, 4, 5], 95) == pytest.approx(4.8)
    assert m.summarize([2, 4])["mean"] == 3 and m.summarize([])["p50"] is None


def test_rate_guards_zero_denominator() -> None:
    assert m.rate(1, 4) == 0.25 and m.rate(0, 0) is None


@pytest.mark.parametrize("text,expected", [
    ("The deal is $1,200,000 for the year", [1_200_000.0]),
    ("about $1.2M or $50K", [1_200_000.0, 50_000.0]),
    ("$2 million total", [2_000_000.0]),
    ("no money here", []),
])
def test_parse_amounts(text: str, expected: list[float]) -> None:
    assert m.parse_amounts(text) == expected


def test_amount_agreement_uses_relative_tolerance() -> None:
    assert m.amount_agrees([99_500.0], 100_000.0)
    assert not m.amount_agrees([90_000.0], 100_000.0)
    assert not m.amount_agrees([1.0], 0.0)


def test_stage_agreement_is_normalised_and_containment_based() -> None:
    assert m.stage_agrees("closed won", "Closed Won")
    assert m.stage_agrees("Negotiation", "Negotiation/Review")
    assert not m.stage_agrees("Prospecting", "Closed Lost")
    assert not m.stage_agrees("", "Closed Lost")


def test_title_overlap_ignores_seniority_words() -> None:
    assert m.title_overlap("technical evaluator, chip design", "Senior Design Engineer")
    assert not m.title_overlap("purchasing", "Senior Design Engineer")
    assert not m.title_overlap("the senior", "Senior Engineer")


def test_names_agree_on_partial_names() -> None:
    assert m.names_agree("Dana Kim", "Dana A. Kim")
    assert not m.names_agree("Dana Kim", "Priya Shah")


def test_worker_log_aggregation() -> None:
    lines = [
        json.dumps({"event": "extract complete", "elapsed_ms": 2000, "model": "m1",
                    "usage": {"prompt_tokens": 100, "completion_tokens": 10, "cost": 0.5}}),
        json.dumps({"event": "extract complete", "elapsed_ms": 4000, "model": "m1",
                    "usage": str({"prompt_tokens": 300, "completion_tokens": 30, "cost": 0.25, "prompt_tokens_details": {"cached_tokens": 50}})}),
        json.dumps({"event": "worker error", "error_type": "ProviderError"}),
        json.dumps({"event": "model call hung, retrying once"}),
        json.dumps({"event": "primary model failed, using fallback"}),
        "not json", json.dumps([1]),
    ]
    out = m.parse_worker_log(lines)
    assert out["latency_s"] == [2.0, 4.0]
    assert (out["prompt_tokens"], out["completion_tokens"], out["cached_tokens"]) == (400, 40, 50)
    assert out["reported_cost_usd"] == 0.75
    assert out["events"] == {"extract_complete": 2, "worker_error": 1, "hung_retry": 1, "fallback_used": 1}
    assert out["error_types"] == {"ProviderError": 1} and out["models"] == {"m1": 2}


def test_token_cost() -> None:
    assert m.token_cost(1_000_000, 1_000_000, 0.028e-6, 0.056e-6) == pytest.approx(0.084)


def test_stratified_sample_is_seeded_proportional_and_never_empty() -> None:
    strata = {"a": [f"a{i}" for i in range(100)], "b": [f"b{i}" for i in range(3)], "c": []}
    first = m.stratified_sample(strata, 0.05, random.Random(1))
    again = m.stratified_sample(strata, 0.05, random.Random(1))
    assert first == again
    assert (len(first["a"]), len(first["b"]), len(first["c"])) == (5, 1, 0)


def test_fill_table_counts_known_and_winning_standing() -> None:
    states = [
        {"fields": {"stage": {"known": True, "standing": "crm_explicit"}, "health": {"known": False}}},
        {"fields": {"stage": {"known": True, "standing": None}, "health": {"known": False}}},
    ]
    table = m.fill_table(states, ["stage", "health", "owner"])
    assert table["stage"] == {"known": 2, "unknown": 0, "by_standing": {"crm_explicit": 1, "derived": 1}}
    assert table["health"]["unknown"] == 2 and table["owner"]["unknown"] == 2
