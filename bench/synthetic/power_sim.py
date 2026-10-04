"""Power simulation of the ACTUAL generator kernel (forward.py) on a stand-in base world.

Features are dependent the way the generator makes them: replies depend on health, push rules
need a reorg and a named owner, stage gates wait for the economic buyer. The split is by time,
as in WP31: deals closed before the cutoff train, the rest test. Stdlib only (CI has no numpy).

Null gate: accounts span the cutoff and carry an unobserved offset, so any account-level null
(industry) inherits it. The principled control is the account's own history: the null gate is the
AUC a null-feature model adds ON TOP OF the account's previous-deal win rate, not raw null AUC.

  Sweeps, sweep gates and the committed report: power_sweep.py.
"""
from __future__ import annotations

import math
from collections.abc import Mapping, Sequence
from dataclasses import dataclass, replace
from typing import Any

from . import rules as R
from .forward import DealResult, sigmoid, simulate
from .seeding import stream
from .world import WorldDeal, build_world

Row = list[tuple[int, float]]  # sparse feature row


@dataclass(frozen=True)
class SimReport:
    seed: int
    win_rate: float
    oracle_auc: float
    learner_auc: float
    hidden_learner_auc: float
    null_only_auc: float
    null_incremental_auc: float
    prevalence: tuple[tuple[str, float], ...]
    coef: tuple[tuple[str, float], ...]
    cc_share: float = 0.0  # deals with any colleague cc (P3 or neutral noise)
    cc_only_auc: float = 0.5  # 'has a colleague cc' alone
    cc_incremental_auc: float = 0.0  # what 'has a colleague cc' adds on top of account history (null gate)


def auc(scores: Sequence[float], labels: Sequence[bool]) -> float:
    pairs = sorted(zip(scores, labels))
    pos = sum(labels)
    neg = len(labels) - pos
    if pos == 0 or neg == 0:
        return 0.5
    i, total = 0, 0.0
    while i < len(pairs):  # rank-sum with ties averaged
        j = i
        while j < len(pairs) and pairs[j][0] == pairs[i][0]:
            j += 1
        total += (i + j + 1) / 2.0 * sum(1 for k in range(i, j) if pairs[k][1])
        i = j
    return (total - pos * (pos + 1) / 2.0) / (pos * neg)


def fit_logistic(rows: Sequence[Row], y: Sequence[bool], dim: int, l2: float = 1.0, iters: int = 12) -> list[float]:
    """L2 logistic regression by Newton steps on sparse rows; index 0 is the intercept (unpenalised)."""
    w = [0.0] * dim
    for _ in range(iters):
        grad = [0.0] * dim
        hess = [[0.0] * dim for _ in range(dim)]
        for row, label in zip(rows, y):
            p = sigmoid(sum(w[i] * v for i, v in row))
            g, h = p - (1.0 if label else 0.0), p * (1.0 - p)
            for i, vi in row:
                grad[i] += g * vi
                for j, vj in row:
                    hess[i][j] += h * vi * vj
        for i in range(1, dim):
            grad[i] += l2 * w[i]
            hess[i][i] += l2
        hess[0][0] += 1e-6
        step = _solve(hess, grad)
        w = [wi - si for wi, si in zip(w, step)]
    return w


def _solve(a: list[list[float]], b: list[float]) -> list[float]:
    n = len(b)
    m = [row[:] + [b[i]] for i, row in enumerate(a)]
    for c in range(n):
        piv = max(range(c, n), key=lambda r: abs(m[r][c]))
        m[c], m[piv] = m[piv], m[c]
        if abs(m[c][c]) < 1e-12:
            continue
        for r in range(n):
            if r != c and m[r][c]:
                f = m[r][c] / m[c][c]
                m[r] = [x - f * yv for x, yv in zip(m[r], m[c])]
    return [m[i][n] / m[i][i] if abs(m[i][i]) > 1e-12 else 0.0 for i in range(n)]


def _null_index(world: Sequence[WorldDeal]) -> dict[tuple[str, int], int]:
    keys = sorted({kv for d in world for kv in d.nulls})
    return {kv: i for i, kv in enumerate(keys)}


def _rows(observed: Sequence[set[str]], world: Sequence[WorldDeal], rule_keys: Sequence[str],
          nulls: dict[tuple[str, int], int], use_rules: bool, use_nulls: bool, history: dict[str, float] | None) -> list[Row]:
    out: list[Row] = []
    for held, wd in zip(observed, world):
        row: Row = [(0, 1.0)]
        if use_rules:
            row += [(1 + i, 1.0) for i, k in enumerate(rule_keys) if k in held]
        if use_nulls:
            row += [(1 + len(rule_keys) + nulls[kv], 1.0) for kv in wd.nulls]
        if history is not None:
            row.append((1 + len(rule_keys) + len(nulls), history.get(wd.deal.account_id, 0.0)))
        out.append(row)
    return out


def observe(results: Sequence[DealResult], world: Sequence[WorldDeal], test: set[int], seed: int,
            noise: Mapping[str, Any] | None) -> list[set[str]]:
    """Features a learner sees. Clean mode (noise None): every feature, as of the close. Noisy mode: features
    pass through the platform's extraction error for their observation class (miss / false positive), and
    current (test) deals are seen at a decision time inside the deal, not at the close."""
    out: list[set[str]] = []
    keys = sorted(noise["observed_via"]) if noise else []
    for i, (res, wd) in enumerate(zip(results, world)):
        held = dict(res.features)
        if noise is None:
            out.append(set(held))
            continue
        s = stream(seed, "observe", wd.deal.deal_id)
        lo, hi = noise["decision_window"]
        cut = wd.deal.days * (lo + (hi - lo) * s.uniform()) if i in test else math.inf
        seen = set()
        for key in keys:
            cls = noise["classes"][noise["observed_via"][key]]
            u = s.uniform()
            if key in held and held[key] <= cut:
                if u >= cls["miss"]:
                    seen.add(key)
            elif u < cls["false_positive"]:
                seen.add(key)
        out.append(seen)
    return out


def _history(results: Sequence[DealResult], world: Sequence[WorldDeal], train: set[int]) -> dict[str, float]:
    """Log-odds of each account's smoothed win rate on its previous (training) deals."""
    wins: dict[str, list[int]] = {}
    for i in train:
        wins.setdefault(world[i].deal.account_id, []).append(int(results[i].won))
    return {a: math.log((sum(v) + 1) / (len(v) - sum(v) + 1)) for a, v in wins.items()}


def _fit_auc(rows: list[Row], y: list[bool], train: list[int], test: list[int], dim: int) -> tuple[float, list[float]]:
    w = fit_logistic([rows[i] for i in train], [y[i] for i in train], dim)
    return auc([sum(w[j] * v for j, v in rows[i]) for i in test], [y[i] for i in test]), w


def simulate_world(world: Sequence[WorldDeal], rule_set: R.RuleSet, proc: dict[str, float], seed: int) -> list[DealResult]:
    """Each account's deals in creation order. A deal sees the account's other quotes: the REAL base quotes of
    its other deals (exogenous, visible as QuoteCreated) and the synthetic Quote entries of its earlier deals
    (syn1_second_quote_within_30d). The kernel reads only quotes dated before its own Quote entry."""
    quotes: dict[str, list[float]] = {}
    real: dict[str, list[tuple[int, float]]] = {}
    for k, wd in enumerate(world):
        if wd.base_quote is not None:
            real.setdefault(wd.deal.account_id, []).append((k, wd.base_quote))
    out: dict[int, DealResult] = {}
    for i in sorted(range(len(world)), key=lambda k: (world[k].deal.account_id, world[k].start)):
        wd = world[i]
        seen = quotes.setdefault(wd.deal.account_id, [])
        others = seen + [q for k, q in real.get(wd.deal.account_id, []) if k != i]
        deal = replace(wd.deal, account_quote_days=tuple(sorted(q - wd.start for q in others)))
        res = simulate(deal, rule_set, proc, seed)
        if res.quote_t is not None:
            seen.append(wd.start + res.quote_t)
        out[i] = res
    return [out[i] for i in range(len(world))]


def run(seed: int, rule_set: R.RuleSet, proc: dict[str, float], noise: Mapping[str, Any] | None = None) -> SimReport:
    """One seed. noise None: clean mode (features at close, no error). Otherwise noisy mode (see observe)."""
    world = build_world(seed, proc, float(rule_set.outcome["account_effect_sd"]))
    return evaluate(seed, rule_set, world, simulate_world(world, rule_set, proc, seed), noise)


def evaluate(seed: int, rule_set: R.RuleSet, world: Sequence[WorldDeal], results: Sequence[DealResult],
             noise: Mapping[str, Any] | None = None) -> SimReport:
    """The gated metrics of simulated deals: the stand-in world (run) or the realised data (generation qa_gates)."""
    y = [r.won for r in results]
    close = [wd.start + r.close_t for wd, r in zip(world, results)]
    cutoff = sorted(close)[int(0.67 * len(close))]
    train = [i for i, c in enumerate(close) if c < cutoff]
    # Test (scoring) population: current deals whose outcome is synthetic. Deals with a visible real terminal
    # event keep their real outcome (spec Q2): they stay in training as label noise, never in scoring.
    test = [i for i, c in enumerate(close) if c >= cutoff and results[i].outcome_source == "synthetic"]
    synthetic = [r.won for r in results if r.outcome_source == "synthetic"]
    keys = [r.key for r in rule_set.rules if r.kind != "null"]
    hidden = [r.key for r in rule_set.rules if r.group == "hidden_company_specific"]
    nulls, hist = _null_index(world), _history(results, world, set(train))
    dim = 2 + len(keys) + len(nulls)
    seen = observe(results, world, set(test), seed, noise)
    oracle = auc([results[i].final_log_odds for i in test], [y[i] for i in test])
    learner, w = _fit_auc(_rows(seen, world, keys, nulls, True, True, None), y, train, test, dim)
    hidden_rows = [[(0, 1.0)] + [(1 + keys.index(k), 1.0) for k in sorted(held) if k in hidden] for held in seen]
    hidden_auc, _ = _fit_auc(hidden_rows, y, train, test, dim)
    null_only, _ = _fit_auc(_rows(seen, world, keys, nulls, False, True, None), y, train, test, dim)
    base, _ = _fit_auc(_rows(seen, world, keys, nulls, False, False, hist), y, train, test, dim)
    with_nulls, _ = _fit_auc(_rows(seen, world, keys, nulls, False, True, hist), y, train, test, dim)
    prev = tuple((k, sum(1 for r in results if k in dict(r.features)) / len(results)) for k in keys)
    cc = [has_colleague_cc(r) for r in results]
    cc_rows = [[(0, 1.0)] + ([(1, 1.0)] if c else []) for c in cc]
    cc_only, _ = _fit_auc(cc_rows, y, train, test, dim)
    base_rows = _rows(seen, world, keys, nulls, False, False, hist)
    cc_hist, _ = _fit_auc([r + ([(1, 1.0)] if c else []) for r, c in zip(base_rows, cc)], y, train, test, dim)
    return SimReport(seed, sum(synthetic) / len(synthetic), oracle, learner, hidden_auc, null_only, with_nulls - base, prev,
                     tuple((k, w[1 + i]) for i, k in enumerate(keys)), sum(cc) / len(cc), cc_only, cc_hist - base)


def has_colleague_cc(res: DealResult) -> bool:
    """Any synthetic seller email or outreach of the deal with a seller colleague in cc (P3 or neutral noise)."""
    return any(r.kind in ("seller_email", "seller_outreach") and dict(r.data).get("cc_colleague") for r in res.records)


def gate_failures(rep: SimReport, rule_set: R.RuleSet) -> list[str]:
    g, fails = rule_set.gates, []
    checks = (("win_rate", rep.win_rate, rule_set.outcome["target_win_rate"]),
              ("oracle_auc", rep.oracle_auc, g["oracle_auc"]), ("learner_auc", rep.learner_auc, g["learner_auc"]),
              ("hidden_learner_auc", rep.hidden_learner_auc, g["hidden_learner_auc"]))
    for name, value, (lo, hi) in checks:
        if not lo <= value <= hi:
            fails.append(f"{name} {value:.3f} outside [{lo}, {hi}]")
    if rep.null_incremental_auc > g["null_incremental_auc_max"]:
        fails.append(f"null_incremental_auc {rep.null_incremental_auc:.3f} > {g['null_incremental_auc_max']}")
    if rep.cc_incremental_auc > g["null_incremental_auc_max"]:
        fails.append(f"cc_incremental_auc {rep.cc_incremental_auc:.3f} > {g['null_incremental_auc_max']} ('has a cc' predicts)")
    sign = {r.key: r.log_odds for r in rule_set.rules if r.group == "hidden_company_specific"}
    for key, c in rep.coef:
        if key in sign and math.copysign(1, c) != math.copysign(1, sign[key]):
            fails.append(f"hidden rule {key} recovered with the wrong sign ({c:+.2f})")
    return fails
