"""bench/uplift/stats: the exact sign test, the seeded bootstrap and the paired summary."""
from __future__ import annotations

import math
import sys
from pathlib import Path

import pytest

ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT))

from bench.uplift import stats as S  # noqa: E402


def test_sign_test_is_one_when_nothing_differs() -> None:
    assert S.sign_test_p(0, 0) == 1.0


def test_sign_test_matches_the_exact_binomial() -> None:
    # 9 of 10 non-tied pairs favour the first arm: 2 * (C(10,0) + C(10,1)) / 1024
    assert S.sign_test_p(9, 1) == pytest.approx(2 * 11 / 1024)
    assert S.sign_test_p(1, 9) == pytest.approx(2 * 11 / 1024)


def test_sign_test_is_capped_at_one_for_a_split() -> None:
    assert S.sign_test_p(5, 5) == 1.0
    assert S.sign_test_p(3, 2) == pytest.approx(1.0)
    assert S.sign_test_p(10, 0) == pytest.approx(2 / 1024)


def test_count_pairs_treats_float_noise_as_a_tie() -> None:
    assert S.count_pairs([0.9, 0.0, 1.0], [0.9 + 1e-12, 0.9, 0.0]) == (1, 1, 1)


def test_count_pairs_rejects_unequal_lengths() -> None:
    with pytest.raises(ValueError):
        S.count_pairs([1.0], [1.0, 2.0])


def test_bootstrap_is_deterministic_for_a_seed_and_brackets_the_mean() -> None:
    diffs = [0.9, 0.9, 0.0, 0.9, 0.0, 0.9, 0.9, -0.9, 0.9, 0.9]
    lo, hi = S.bootstrap_ci(diffs, seed=7)
    assert (lo, hi) == S.bootstrap_ci(diffs, seed=7)
    assert lo <= sum(diffs) / len(diffs) <= hi
    assert S.bootstrap_ci(diffs, seed=8) != (lo, hi)


def test_bootstrap_of_nothing_is_zero() -> None:
    assert S.bootstrap_ci([], seed=1) == (0.0, 0.0)


def test_bootstrap_of_a_constant_collapses() -> None:
    lo, hi = S.bootstrap_ci([0.5] * 8, seed=3, resamples=200)
    assert math.isclose(lo, 0.5) and math.isclose(hi, 0.5)


def test_paired_stat_reports_wins_losses_ties_and_same_decision_share() -> None:
    stat = S.paired_stat(first=[0.9, 0.9, 0.0, 0.0], second=[0.0, 0.9, 0.9, 0.0],
                         first_acted=[True, True, False, False], second_acted=[False, True, True, False], seed=1)
    assert (stat["n"], stat["wins"], stat["losses"], stat["ties"]) == (4, 1, 1, 2)
    assert stat["mean_difference"] == 0.0
    assert stat["share_same_decision"] == 0.5
    assert stat["sign_test_p"] == 1.0
    assert len(stat["ci95"]) == 2


def test_paired_stat_of_no_situations_is_inert() -> None:
    stat = S.paired_stat([], [], [], [], seed=1)
    assert stat["n"] == 0 and stat["sign_test_p"] == 1.0 and stat["share_same_decision"] == 1.0
