"""Seed sweep of the power simulation, its gates, and the committed report.

    python -m synthetic.power_sweep        (from bench/; writes bench/reports/synthetic-power-v1.json)

The sweep covers the frozen seed and the next sweep_seeds-1 seeds, in both modes (clean, noisy).
Gates on the CLEAN sweep (rules.v1.json#learnability_gates):
  - the share of seeds passing every per-seed gate >= seed_pass_rate_min (not just the frozen seed);
  - each eval_known rule's sign recovered in >= sign_recovery_min of seeds, each hidden rule's in
    >= hidden_sign_recovery_min.
The noisy sweep is reported, not gated: it is the realistic expectation, not a design target.
The sweep reports variance; it never chooses the generation seed (rules.v1.json#seed_policy).
"""
from __future__ import annotations

import json
import math
from collections.abc import Mapping, Sequence
from dataclasses import asdict
from pathlib import Path
from typing import Any

from . import rules as R
from .power_sim import SimReport, gate_failures, run

REPORT_PATH = Path(__file__).resolve().parents[1] / "reports" / "synthetic-power-v1.json"
REAL_REPORT_PATH = Path(__file__).resolve().parents[1] / "reports" / "synthetic-power-real-v1.json"
METRICS = ("win_rate", "oracle_auc", "learner_auc", "hidden_learner_auc", "null_only_auc", "null_incremental_auc",
           "cc_share", "cc_only_auc", "cc_incremental_auc")


def sweep(rule_set: R.RuleSet, doc: Mapping[str, Any], noisy: bool, export: Path | None = None) -> list[SimReport]:
    """sweep_seeds seeds from the frozen seed. export None: the stand-in world; else the real base (the export)."""
    n = int(rule_set.gates["sweep_seeds"])
    noise = doc["power_sim_noise"] if noisy else None
    if export is not None:
        from .pipeline import evaluate_real  # lazy: the stand-in sweep needs no base loader

        return [evaluate_real(export, rule_set, dict(doc["process"]), rule_set.generation_seed + k, noise) for k in range(n)]
    return [run(rule_set.generation_seed + k, rule_set, dict(doc["process"]), noise) for k in range(n)]


def sign_rates(reports: Sequence[SimReport], rule_set: R.RuleSet) -> dict[str, float]:
    out = {}
    for r in rule_set.rules:
        if r.kind != "null":
            hits = [math.copysign(1, dict(rep.coef)[r.key]) == math.copysign(1, r.log_odds) for rep in reports]
            out[r.key] = sum(hits) / len(hits)
    return out


def sweep_failures(reports: Sequence[SimReport], rule_set: R.RuleSet) -> list[str]:
    g, fails = rule_set.gates, []
    passing = sum(1 for rep in reports if not gate_failures(rep, rule_set)) / len(reports)
    if passing < g["seed_pass_rate_min"]:
        fails.append(f"seed pass rate {passing:.2f} < {g['seed_pass_rate_min']}")
    for metric in ("null_incremental_auc", "cc_incremental_auc"):  # the null gates also hold on the sweep mean
        mean = sum(getattr(rep, metric) for rep in reports) / len(reports)
        if mean > g["null_incremental_auc_max"]:
            fails.append(f"mean {metric} {mean:.3f} > {g['null_incremental_auc_max']}")
    groups = {r.key: r.group for r in rule_set.rules}
    for key, rate in sign_rates(reports, rule_set).items():
        floor = g["hidden_sign_recovery_min"] if groups[key] == "hidden_company_specific" else g["sign_recovery_min"]
        if rate < floor:
            fails.append(f"{key} sign recovered in {rate:.2f} of seeds < {floor}")
    return fails


def _band(values: Sequence[float]) -> dict[str, float]:
    v = sorted(values)
    return {"mean": round(sum(v) / len(v), 3), "p5": round(v[int(0.05 * (len(v) - 1))], 3),
            "p95": round(v[int(0.95 * (len(v) - 1))], 3)}


def summary(reports: Sequence[SimReport], rule_set: R.RuleSet) -> dict[str, Any]:
    failing = {rep.seed: gate_failures(rep, rule_set) for rep in reports}
    return {
        "metrics": {m: _band([getattr(rep, m) for rep in reports]) for m in METRICS},
        "seed_pass_rate": round(sum(1 for f in failing.values() if not f) / len(reports), 3),
        "failing_seeds": {str(s): f for s, f in failing.items() if f},
        "sign_recovery": {k: round(v, 3) for k, v in sign_rates(reports, rule_set).items()},
        "prevalence": {k: round(sum(dict(rep.prevalence)[k] for rep in reports) / len(reports), 3)
                       for k, _ in reports[0].prevalence},
        "sweep_failures": sweep_failures(reports, rule_set),
        "frozen_seed": asdict(reports[0]),
    }


def build_report(export: Path | None = None) -> dict[str, Any]:
    rule_set, doc = R.load_rules(), R.load_doc()
    what = ("Power simulation of the synthetic layer kernel on the stand-in base (world.py, WP31 profile mixes). Not generated data. "
            if export is None else
            "Power simulation of the kernel on the REAL base (the CRMArena-Pro snapshot, base.py), one load per seed; "
            "this is the gating sweep. ")
    return {
        "base": "stand_in" if export is None else "real_snapshot",
        "description": what + "clean: features at close, no error (gated). noisy: extraction error per "
                       "observation class and current deals seen at a decision time (reported).",
        "rules_sha256": R.content_hash(doc),
        "generation_seed": rule_set.generation_seed,
        "sweep_seeds": rule_set.gates["sweep_seeds"],
        "clean": summary(sweep(rule_set, doc, False, export), rule_set),
        "noisy": summary(sweep(rule_set, doc, True, export), rule_set),
    }


def main(argv: Sequence[str] | None = None) -> int:
    import argparse

    ap = argparse.ArgumentParser(prog="synthetic.power_sweep")
    ap.add_argument("--base", help="CRMArena export: sweep the real base (writes synthetic-power-real-v1.json)")
    args = ap.parse_args(None if argv is None else list(argv))
    export = Path(args.base) if args.base else None
    report = build_report(export)
    (REAL_REPORT_PATH if export else REPORT_PATH).write_text(json.dumps(report, indent=1) + "\n", encoding="utf-8")
    for mode in ("clean", "noisy"):
        m = report[mode]
        print(mode, "pass rate", m["seed_pass_rate"], "sweep failures", m["sweep_failures"])
        for name, band in m["metrics"].items():
            print(f"  {name}: {band['mean']:.3f} [{band['p5']:.3f}, {band['p95']:.3f}]")
        print("  failing seeds:", m["failing_seeds"])
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
