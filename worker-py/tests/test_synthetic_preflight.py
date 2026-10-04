"""WP32 (HAR-131) generation preconditions 1-2: a recorded gate exception waives ONLY the exact failure the
user accepted; any other sweep failure, a stale report or a changed frozen file still blocks. The changelog is
outside the hashed content, so notes never stale the report or the freeze."""
from __future__ import annotations

import copy
import json
import sys
from pathlib import Path

import pytest

ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT / "bench"))

from synthetic import generate, rules  # noqa: E402

DOC = rules.load_doc()
EXCEPTED = DOC["gate_exceptions"][0]["failure"]


def frozen_copy(tmp: Path, mutate=None) -> Path:
    doc = copy.deepcopy(DOC)
    doc["status"] = "frozen"
    if mutate:
        mutate(doc)
    doc["frozen_sha256"] = rules.content_hash(doc)
    path = tmp / "rules.v1.json"
    path.write_text(json.dumps(doc), encoding="utf-8")
    return path


def report(tmp: Path, rules_path: Path, failures: list[str], base: str = "real_snapshot") -> Path:
    doc = json.loads(rules_path.read_text(encoding="utf-8"))
    path = tmp / "report.json"
    path.write_text(json.dumps({"base": base, "rules_sha256": rules.content_hash(doc), "clean": {"sweep_failures": failures}}),
                    encoding="utf-8")
    return path


def test_the_changelog_never_changes_the_hash() -> None:
    doc = copy.deepcopy(DOC)
    before = rules.content_hash(doc)
    doc["changelog"].append({"date": "2026-10-04", "spec": "1.1", "change": "a note only, nothing else", "why": "test"})
    assert rules.content_hash(doc) == before
    doc["process"]["cc_noise_share"] = 0.51
    assert rules.content_hash(doc) != before


def test_the_exact_recorded_failure_is_waived(tmp_path: Path) -> None:
    r = frozen_copy(tmp_path)
    assert generate.preflight(report(tmp_path, r, [EXCEPTED], "stand_in"), r) == []


def test_any_other_sweep_failure_still_blocks(tmp_path: Path) -> None:
    r = frozen_copy(tmp_path)
    other = "syn1_second_quote_within_30d sign recovered in 0.93 of seeds < 0.95"
    reasons = generate.preflight(report(tmp_path, r, [EXCEPTED, other], "stand_in"), r)
    assert reasons == [f"sweep gate: {other}"]
    worse = generate.preflight(report(tmp_path, r, ["seed pass rate 0.80 < 0.9"], "stand_in"), r)
    assert worse == ["sweep gate: seed pass rate 0.80 < 0.9"]


def test_a_stale_report_or_a_changed_frozen_file_blocks(tmp_path: Path) -> None:
    r = frozen_copy(tmp_path)
    rep = report(tmp_path, r, [EXCEPTED], "stand_in")
    doc = json.loads(r.read_text(encoding="utf-8"))
    doc["process"]["reprice_share"] = 0.5  # edited after freezing
    r.write_text(json.dumps(doc), encoding="utf-8")
    reasons = generate.preflight(rep, r)
    assert any("frozen_sha256 mismatch" in x for x in reasons) and any("stale" in x for x in reasons)


def test_the_exception_does_not_lower_any_gate() -> None:
    assert DOC["learnability_gates"]["seed_pass_rate_min"] == 0.9
    assert [e["decided_by"] for e in DOC["gate_exceptions"]] == ["user", "user"] and REAL_EXC["reconfirmed"] == "2026-10-03"


REAL_EXC = DOC["gate_exceptions"][1]
REAL_FAILURES = list(REAL_EXC["failures"])


def test_the_real_base_exception_covers_exactly_the_four_recorded_failures(tmp_path: Path) -> None:
    assert REAL_EXC["base"] == "real_snapshot" and REAL_EXC["date"] == "2026-10-03" and REAL_EXC["decided_by"] == "user"
    assert sorted(f.split(" ")[0] for f in REAL_FAILURES) == sorted(
        ["seed", "syn1_written_handover_exempts", "syn1_quote_before_economic_buyer", "syn1_requested_pause_ignored"])
    r = frozen_copy(tmp_path)
    assert generate.preflight(report(tmp_path, r, REAL_FAILURES), r) == []
    for f in REAL_FAILURES:  # drop none: each is individually recognised and nothing nearby is
        assert generate.accepted(f, DOC, "real_snapshot", "clean")
    assert not generate.accepted(f"{REAL_FAILURES[2][:-9]}0.04 of seeds < 0.8", DOC, "real_snapshot", "clean")  # other rate
    assert not generate.accepted("seed pass rate 0.41 < 0.9", DOC, "real_snapshot", "clean")


@pytest.mark.parametrize("failure", [
    "syn1_ops_contact_before_quote sign recovered in 0.90 of seeds < 0.95",
    "syn1_reprice_after_quote_pushback sign recovered in 0.50 of seeds < 0.95",
    "syn1_cc_colleague_on_technical_reply sign recovered in 0.94 of seeds < 0.95",
    "syn1_second_quote_within_30d sign recovered in 0.85 of seeds < 0.95",
    "mean null_incremental_auc 0.040 > 0.03",
    "mean cc_incremental_auc 0.045 > 0.03",
    "syn1_push_while_owner_unstable sign recovered in 0.75 of seeds < 0.8",  # an eval-known rule NOT in the exception
])
def test_hidden_rule_null_and_cc_failures_still_block(tmp_path: Path, failure: str) -> None:
    r = frozen_copy(tmp_path)
    assert generate.preflight(report(tmp_path, r, REAL_FAILURES + [failure]), r) == [f"sweep gate: {failure}"]


def test_an_exception_naming_a_hidden_rule_is_refused_at_load(tmp_path: Path) -> None:
    import pytest as _pt

    def add(doc: dict) -> None:
        doc["gate_exceptions"].append({"failures": ["syn1_ops_contact_before_quote sign recovered in 0.90 of seeds < 0.95"],
                                       "mode": "clean", "decided_by": "user", "date": "2026-10-04", "reason": "x" * 50})

    path = frozen_copy(tmp_path, add)
    with _pt.raises(ValueError, match="hidden rule"):
        rules.load_rules(path)


def test_an_exception_only_applies_to_its_own_base_and_mode(tmp_path: Path) -> None:
    for f in REAL_FAILURES:
        assert not generate.accepted(f, DOC, "stand_in", "clean")  # the real-base waiver never covers the stand-in
        assert not generate.accepted(f, DOC, "real_snapshot", "noisy")
    assert not generate.accepted(EXCEPTED, DOC, "real_snapshot", "clean")  # nor the stand-in waiver the real base
    assert generate.accepted(EXCEPTED, DOC, "stand_in", "clean")
    r = frozen_copy(tmp_path)
    blocked = generate.preflight(report(tmp_path, r, REAL_FAILURES, "stand_in"), r)
    assert blocked == [f"sweep gate: {f}" for f in REAL_FAILURES]


@pytest.mark.parametrize("gate", ["null_incremental_auc", "cc_incremental_auc"])
def test_a_null_gate_waiver_is_refused_at_load(tmp_path: Path, gate: str) -> None:
    def add(doc: dict) -> None:
        doc["gate_exceptions"].append({"failures": [f"mean {gate} 0.040 > 0.03"], "mode": "clean", "base": "real_snapshot",
                                       "decided_by": "user", "date": "2026-10-04", "reason": "x" * 50})

    with pytest.raises(ValueError, match="null gate"):
        rules.load_rules(frozen_copy(tmp_path, add))


def test_the_recorded_reason_states_the_true_breakdown() -> None:
    reason = REAL_EXC["reason"]
    for needle in ("oracle AUC below 0.65 in 17 seeds", "learner AUC in 6", "cc_incremental_auc in 3", "win rate in 1",
                   "syn1_second_quote_within_30d", "NOT a per-seed gate", "re-confirmed", "2026-10-03",
                   "one per-seed hidden-sign failure and three per-seed cc failures"):
        assert needle in reason, needle
    real = json.loads((ROOT / "bench" / "reports" / "synthetic-power-real-v1.json").read_text(encoding="utf-8"))
    from collections import Counter

    why = Counter("hidden" if "hidden rule" in x else x.split(" ")[0] for fs in real["clean"]["failing_seeds"].values() for x in fs)
    assert (len(real["clean"]["failing_seeds"]), why["oracle_auc"], why["learner_auc"], why["cc_incremental_auc"],
            why["win_rate"], why["hidden"]) == (23, 17, 6, 3, 1, 1)
