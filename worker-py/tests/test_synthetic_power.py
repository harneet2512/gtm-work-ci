"""WP32 (HAR-131) v1.1: power simulation of the actual kernel, its gates and the committed sweep report.

The sweep (40 seeds x 2 modes) is too slow for every CI run, so it is committed as
bench/reports/synthetic-power-v1.json and pinned to the rule file's content hash: this test fails if the
report is stale, re-runs the frozen seed live and checks it reproduces the report. The sweep gates are a
GENERATION precondition (generate.preflight), not a CI invariant: a failing sweep is disclosed, never
hidden by changing the seed (rules.v1.json#seed_policy)."""
from __future__ import annotations

import json
import math
import sys
from dataclasses import replace
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT / "bench"))

from synthetic import generate, power_sim, power_sweep, rules  # noqa: E402

RS, DOC = rules.load_rules(), rules.load_doc()
REPORT = json.loads(power_sweep.REPORT_PATH.read_text(encoding="utf-8"))
LIVE = power_sim.run(RS.generation_seed, RS, dict(DOC["process"]))


def test_committed_report_is_current_for_this_rule_file() -> None:
    assert REPORT["rules_sha256"] == rules.content_hash(DOC), "rerun: python -m synthetic.power_sweep (from bench/)"
    assert REPORT["sweep_seeds"] == RS.gates["sweep_seeds"] and {"clean", "noisy"} <= set(REPORT)


def test_frozen_seed_run_reproduces_the_report() -> None:
    frozen = REPORT["clean"]["frozen_seed"]
    for name in ("win_rate", "oracle_auc", "learner_auc", "hidden_learner_auc", "null_incremental_auc"):
        assert math.isclose(getattr(LIVE, name), frozen[name], abs_tol=1e-9), name


REAL = json.loads(power_sweep.REAL_REPORT_PATH.read_text(encoding="utf-8"))


def test_the_real_base_report_is_current_and_in_band_on_win_rate() -> None:
    assert REAL["base"] == "real_snapshot" and REAL["rules_sha256"] == rules.content_hash(DOC),         "rerun: python -m synthetic.power_sweep --base <export> (from bench/)"
    lo, hi = RS.outcome["target_win_rate"]
    assert lo <= REAL["clean"]["metrics"]["win_rate"]["mean"] <= hi


def test_report_lists_every_failing_seed_and_preflight_blocks_on_real_base_sweep_failures() -> None:
    clean = REAL["clean"]
    rate = 1 - len(clean["failing_seeds"]) / REAL["sweep_seeds"]
    assert math.isclose(rate, clean["seed_pass_rate"], abs_tol=1e-3)
    reasons = generate.preflight()
    # the gating sweep is the real base; only the exact recorded exception would be waived, nothing else is
    waived = [f for f in clean["sweep_failures"] if generate.accepted(f, DOC, "real_snapshot", "clean")]
    assert [f for f in clean["sweep_failures"] if f not in waived] == [r.removeprefix("sweep gate: ") for r in reasons]


def test_noisy_mode_is_less_optimistic_than_clean() -> None:
    assert REPORT["noisy"]["metrics"]["learner_auc"]["mean"] < REPORT["clean"]["metrics"]["learner_auc"]["mean"]
    noise = DOC["power_sim_noise"]
    assert set(noise["observed_via"]) == {r.key for r in RS.rules if r.kind != "null"}


def test_gate_failures_enforce_hidden_learner_auc() -> None:
    low = replace(LIVE, hidden_learner_auc=RS.gates["hidden_learner_auc"][0] - 0.01)
    assert any("hidden_learner_auc" in f for f in power_sim.gate_failures(low, RS))


def test_sweep_failures_enforce_pass_rate_and_sign_recovery() -> None:
    hidden = next(r for r in RS.rules if r.group == "hidden_company_specific")
    flipped = replace(LIVE, coef=tuple((k, -c if k == hidden.key else c) for k, c in LIVE.coef))
    fails = power_sweep.sweep_failures([LIVE] * 9 + [flipped] * 1, RS)
    assert any(hidden.key in f for f in fails)  # 0.9 < hidden_sign_recovery_min 0.95
    bad = replace(LIVE, oracle_auc=0.0)
    assert any("pass rate" in f for f in power_sweep.sweep_failures([LIVE] * 8 + [bad] * 2, RS))


def test_rule_file_prevalence_matches_the_clean_sweep() -> None:
    prev = REPORT["clean"]["prevalence"]
    for r in RS.rules:
        if r.kind != "null":
            assert abs(prev[r.key] - r.prevalence_target) <= 0.02, (r.key, prev[r.key], r.prevalence_target)


def test_cc_presence_alone_predicts_nothing_beyond_account_history() -> None:
    """User decision 2026-10-03: with neutral cc noise, 'has a colleague cc' adds nothing beyond account history
    (inside the null gate), at the frozen seed and on average over the clean sweep."""
    gate = RS.gates["null_incremental_auc_max"]
    assert LIVE.cc_share > 0.2 and LIVE.cc_incremental_auc <= gate
    assert REPORT["clean"]["metrics"]["cc_incremental_auc"]["mean"] <= gate
    # Seeds where cc presence still leaks past the gate are failures of the sweep gate (they block generation via
    # preflight); they must be reported, never hidden.
    leaking = [s for s, fs in REPORT["clean"]["failing_seeds"].items() if any(f.startswith("cc_incremental") for f in fs)]
    assert REPORT["clean"]["metrics"]["cc_incremental_auc"]["p95"] <= gate or leaking


def test_auc_and_logistic_helpers() -> None:
    assert power_sim.auc([0.1, 0.2, 0.8, 0.9], [False, False, True, True]) == 1.0
    assert power_sim.auc([0.5, 0.5], [False, True]) == 0.5
    rows = [[(0, 1.0), (1, 1.0)]] * 30 + [[(0, 1.0)]] * 30
    w = power_sim.fit_logistic(rows, [True] * 25 + [False] * 5 + [True] * 5 + [False] * 25, dim=2)
    assert w[1] > 1.0
