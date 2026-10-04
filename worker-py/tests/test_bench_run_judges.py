"""WP18 (HAR-116): bench/evals/run_judges.py produces the §20 metrics and the L5 table over the legacy gold,
offline (baseline and replay modes; no network)."""
from __future__ import annotations

import json
import sys
from pathlib import Path

import pytest

BENCH = Path(__file__).resolve().parents[2] / "bench" / "evals"
sys.path.insert(0, str(BENCH))

import run_judges  # noqa: E402

from ghost_worker.judges.meta import GOLD_PROVENANCE, HUMAN_BLOCKED  # noqa: E402


@pytest.fixture(scope="module")
def baseline() -> dict:
    return run_judges.run(mode="baseline", model="unused", trials=2, selection="gold")


def test_baseline_report_has_every_section20_metric(baseline: dict) -> None:
    o = baseline["overall"]
    assert baseline["gold"] == GOLD_PROVENANCE and baseline["n_cases"] == 87
    assert o["n_gold"] == o["n_judged"] == 2 * 329 and o["coverage"] == 1.0
    for key in ("verdict_agreement", "label_agreement", "fail_precision", "fail_recall", "false_pass_rate",
                "false_block_rate", "verdict_kappa"):
        assert key in o
    assert o["false_pass_rate"] == 1.0 and o["false_block_rate"] == 0.0 and o["verdict_kappa"] == 0.0
    assert baseline["consistency"]["unanimous_rate"] == 1.0 and baseline["consistency"]["n_repeated"] == 329
    assert set(baseline["per_eval"]) == set(baseline["rubric_versions"]) and len(baseline["per_eval"]) == 25
    assert set(baseline["by_evidence_class"]) == {"deal_data", "methodology", "cs_ops"}
    assert {"case_type", "motion", "risk", "champion_status"} <= set(baseline["slices"])


def test_l5_table_reports_gold_now_and_human_blocked(baseline: dict) -> None:
    rows = {r["dimension"]: r for r in baseline["l5"]}
    assert len(rows) == 10 and rows["readiness"]["gold_n"] == 56
    assert all(r["human_agreement"] is None for r in rows.values())
    assert rows["decision process"]["status"] == HUMAN_BLOCKED


def test_routing_report_needs_no_model(baseline: dict) -> None:
    routing = baseline["routing"]
    assert routing["recall_fail"] >= 0.93 and routing["recall_block"] == 1.0 and routing["semantic_types"] == 25
    assert routing["by_action"]["send_email"]["mean_selected"] <= 12
    assert routing["by_action"]["internal_note"]["mean_selected"] < routing["by_action"]["send_email"]["mean_selected"]
    assert all(m["eval_type"] for m in routing["missed_fails"])


def test_replay_without_cassettes_records_errors_not_crashes(tmp_path: Path) -> None:
    report = run_judges.run(mode="replay", model="openrouter/deepseek/deepseek-v4-flash", trials=1, selection="routed",
                            only=["acme_cp4_review_date_ask_too_soon"], cassettes=tmp_path)
    routed = report["routing"]["by_action"]["send_email"]["max_selected"]
    assert report["errors"] == {"CassetteNotFoundError": routed}
    assert report["overall"]["n_judged"] == 0 and report["overall"]["coverage"] == 0.0


def test_report_with_zero_judged_rows_says_insufficient_data_and_prints_no_rates(tmp_path: Path) -> None:
    report = run_judges.run(mode="replay", model="openrouter/deepseek/deepseek-v4-flash", trials=1, selection="routed",
                            only=["acme_cp4_review_date_ask_too_soon"], cassettes=tmp_path)
    assert report["overall"]["insufficient_data"] and report["case_level"]["insufficient_data"]
    md = run_judges.render(report)
    assert md.startswith("# INSUFFICIENT DATA") and "no judged rows" in md
    for forbidden in ("false-pass rate", "false_pass_rate", "## Per eval", "## L5", "Case level"):
        assert forbidden not in md
    assert "## §7 routing" in md and "CassetteNotFoundError" in md


def test_error_reasons_group_by_cause_without_ids() -> None:
    reason = run_judges.error_reason
    assert reason("InvalidModelOutputError: judge cited an activity that is not in its context") == (
        "InvalidModelOutputError: judge cited an activity that is not in its context")
    key = "a" * 64
    assert reason(f"CassetteNotFoundError: no cassette {key} for 0d000000-0000-4000-8000-000000000001") == (
        "CassetteNotFoundError: no cassette <id> for <id>")
    assert reason("ProviderError") == "ProviderError" and reason(None) == ""


def test_main_writes_json_and_markdown(tmp_path: Path) -> None:
    reports = run_judges.main(["--mode", "baseline", "--case", "too_soon", "--out", str(tmp_path), "--tag", "t"])
    expected = [c for c in run_judges.load_cases() if "too_soon" in c["id"]]
    assert len(reports) == 1 and reports[0]["n_cases"] == len(expected) > 0
    md = (tmp_path / "judges-t-baseline-always-pass.md").read_text(encoding="utf-8")
    assert GOLD_PROVENANCE in md and "## L5 grader agreement" in md and "## §7 routing" in md
    data = json.loads((tmp_path / "judges-t-baseline-always-pass.json").read_text(encoding="utf-8"))
    assert data["mode"] == "baseline" and data["selection"] == "gold"


def test_live_modes_build_providers_without_fallback(monkeypatch: pytest.MonkeyPatch, tmp_path: Path) -> None:
    built = []
    monkeypatch.setattr(run_judges, "build_judge_provider", lambda settings: built.append(settings) or object())
    factory = run_judges.provider_factory("record", "openrouter/some/model", {}, tmp_path)
    factory(2)
    assert built[0].ghost_fallback_model is None and built[0].ghost_llm_mode == "record"
    assert built[0].cassette_dir == tmp_path / "trial-2" and built[0].ghost_model == "openrouter/some/model"


def test_replay_report_lists_rows_without_a_cassette_and_takes_a_title(tmp_path: Path) -> None:
    report = run_judges.run(mode="replay", model="openrouter/deepseek/deepseek-v4-flash", trials=1, selection="gold",
                            only=["acme_cp4_review_date_ask_too_soon"], cassettes=tmp_path)
    assert sum(report["missing_cassettes"].values()) == report["overall"]["error_count"] > 0
    path = run_judges.write({**report, "title": "LEGACY FIXTURE GOLD: t"}, tmp_path, "t", "stem")
    assert path.name == "stem.md" and path.read_text(encoding="utf-8").startswith("# INSUFFICIENT DATA")
    assert "LEGACY FIXTURE GOLD: t" in path.read_text(encoding="utf-8")
    assert "## Rows without a cassette" in path.read_text(encoding="utf-8")
