"""WP32 (HAR-131) step 2, precondition 6: the real-vs-synthetic distinguishability classifier.

The classifier is checked on constructed data (a planted separating feature gives AUC near 1, identical
distributions give AUC near 0.5) and runs end to end on fixtures/crmarena_sample. The full-export numbers are
in bench/reports/synthetic-distinguish-v1.json (aggregates only)."""
from __future__ import annotations

import sys
from datetime import datetime, timezone
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT / "bench"))

from synthetic import distinguish, pipeline, rules  # noqa: E402
from synthetic.seeding import stream  # noqa: E402

SAMPLE = ROOT / "fixtures" / "crmarena_sample"
AT = datetime(2024, 3, 4, 10, 30, tzinfo=timezone.utc)


def _rows(n: int, tag: str, cc: bool, s) -> list[tuple[str, dict[str, float]]]:
    out = []
    for i in range(n):
        body = "Hi Sam,\n\n" + " ".join("word" for _ in range(20 + int(40 * s.uniform()))) + "\n\nBest regards,\nAl"
        out.append((f"{tag}{i % 10}", distinguish.features("Re: plan", body, AT, s.uniform() < 0.5, cc)))
    return out


def test_a_planted_tell_is_found_and_identical_distributions_are_not() -> None:
    s = stream(1, "test")
    real, synth = _rows(60, "a", False, s), _rows(60, "a", True, s)  # same accounts, as in the layer
    labels = [False] * 60 + [True] * 60
    assert distinguish.cv_auc(real + synth, labels, distinguish.SETS["text_timing_thread"], 7) > 0.95
    same = _rows(60, "a", False, s)
    assert abs(distinguish.cv_auc(real + same, labels, distinguish.SETS["text"], 7) - 0.5) < 0.2


def test_report_on_the_sample_is_aggregate_only(tmp_path) -> None:
    pipeline.generate_to(SAMPLE, tmp_path, rules.load_rules(), rules.load_doc())
    rep = distinguish.report(SAMPLE, tmp_path, 20261002)
    assert rep["real_emails"] > 0 and rep["synthetic_emails"] > 0
    assert set(rep["auc"]) == set(distinguish.SETS) and all(0.0 <= v <= 1.0 for v in rep["auc"].values())
    assert rep["single_feature_tells"]["has_cc"]["real"] == 0.0  # the base has no cc at all
    flat = repr(rep)
    assert "@" not in flat and "006Wt" not in flat  # no addresses or record ids leave the module
