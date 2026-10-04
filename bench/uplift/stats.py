"""Paired statistics of the A/B/C report: exact sign test and a seeded percentile bootstrap. Stdlib only."""
from __future__ import annotations

import math
import random
from collections.abc import Hashable, Sequence

BOOTSTRAP_RESAMPLES = 10_000
TOL = 1e-9  # scores are sums of a few planted log-odds; equal to within float noise is a tie


def sign_test_p(first_better: int, second_better: int) -> float:
    """Two-sided exact binomial sign test on the non-tied pairs; 1.0 when there are none."""
    n = first_better + second_better
    if n == 0:
        return 1.0
    k = min(first_better, second_better)
    tail = sum(math.comb(n, i) for i in range(k + 1)) / 2.0 ** n
    return min(1.0, 2.0 * tail)


def bootstrap_ci(diffs: Sequence[float], seed: int, resamples: int = BOOTSTRAP_RESAMPLES) -> tuple[float, float]:
    """95% percentile bootstrap interval of the mean of `diffs`; deterministic for a seed."""
    if not diffs:
        return (0.0, 0.0)
    rng = random.Random(seed)
    n = len(diffs)
    means = sorted(sum(diffs[rng.randrange(n)] for _ in range(n)) / n for _ in range(resamples))
    return (means[int(0.025 * resamples)], means[min(resamples - 1, int(0.975 * resamples))])


def count_pairs(first: Sequence[float], second: Sequence[float]) -> tuple[int, int, int]:
    """(first better, second better, ties) over paired scores."""
    if len(first) != len(second):
        raise ValueError("paired scores must have equal length")
    better = sum(1 for a, b in zip(first, second) if a - b > TOL)
    worse = sum(1 for a, b in zip(first, second) if b - a > TOL)
    return better, worse, len(first) - better - worse


def paired_stat(first: Sequence[float], second: Sequence[float], first_acted: Sequence[Hashable],
                second_acted: Sequence[Hashable], seed: int) -> dict:
    """The report's pairedStat for the scores of two arms over the same situations."""
    wins, losses, ties = count_pairs(first, second)
    n = len(first)
    diffs = [a - b for a, b in zip(first, second)]
    same = sum(1 for a, b in zip(first_acted, second_acted) if a == b)
    lo, hi = bootstrap_ci(diffs, seed)
    return {
        "n": n,
        "mean_score_first": round(sum(first) / n, 6) if n else 0.0,
        "mean_score_second": round(sum(second) / n, 6) if n else 0.0,
        "mean_difference": round(sum(diffs) / n, 6) if n else 0.0,
        "ci95": [round(lo, 6), round(hi, 6)],
        "wins": wins, "losses": losses, "ties": ties,
        "sign_test_p": sign_test_p(wins, losses),
        "share_same_decision": round(same / n, 6) if n else 1.0,
    }
