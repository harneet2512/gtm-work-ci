"""HAR-132 (WP33): bench/metrics harness. Parsers are pure; the report recomputes every measured registry value
from the repo and reports the rest with their blocker; docs/metrics.md is generated from the registry."""
from __future__ import annotations

import json
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT / "bench" / "metrics"))

import metrics_collect as mc  # noqa: E402
import metrics_docs  # noqa: E402
import metrics_slices as ms  # noqa: E402
import metrics_sources as src  # noqa: E402
import report  # noqa: E402

REGISTRY = src.load_registry()
BY_ID = {m["id"]: m for m in REGISTRY["metrics"]}

DATE = "2026-10-03"  # report date (HAR-140 status refresh)
GO_DATE = "2026-10-02"  # the go-acceptance artifact the WP5/WP6 numbers are read from
GO_FILE = ROOT / src.go_artifact(GO_DATE)
GO_TEXT = GO_FILE.read_text(encoding="utf-8")


def test_committed_go_output_parses_to_identity_edges_and_state_accuracy() -> None:
    parsed = src.parse_go_output(GO_TEXT)
    assert parsed["identity"] == src.PR(1.0, 1.0, 111)
    assert parsed["about"] == src.PR(1.0, 1.0, 110)
    assert parsed["involves"] == src.PR(1.0, 1.0, 232)
    assert parsed["knowable"] == (110, 110) and parsed["all_fields"] == (136, 216)
    assert parsed["required_fields"] == (103, 132) and parsed["buying_group"] == (169, 182)
    assert "--- PASS: TestGoldAcceptance" in GO_TEXT and "\ufeff" not in GO_TEXT and "\r" not in GO_TEXT


def test_go_output_parser_fails_loudly_when_a_line_is_missing() -> None:
    try:
        src.parse_go_output("ok  pkg 1s\n")
    except src.SourceError as err:
        assert "identity" in str(err)
    else:
        raise AssertionError("expected SourceError")


def test_wp5_doc_table_agrees_with_the_go_run() -> None:
    wp5 = (ROOT / "docs" / "traceability" / "wp5.md").read_text(encoding="utf-8")
    parsed = src.parse_go_output(GO_TEXT)
    for edge in ("about", "involves"):
        n = parsed[edge].n
        assert f"| {n} | {n} | 1.0000 | 1.0000 |" in wp5.split(f"`{edge}`", 1)[1].splitlines()[0], edge


def test_run_go_writes_the_artifact_the_report_then_reads(tmp_path: Path) -> None:
    calls = []

    def runner(cmd: list[str], cwd: Path) -> str:
        calls.append((cmd, cwd))
        return GO_TEXT
    (tmp_path / "bench" / "reports").mkdir(parents=True)
    rel = src.run_go("2030-01-01", runner, tmp_path)
    assert rel == "bench/reports/go-acceptance-2030-01-01.txt"
    assert (tmp_path / rel).read_text(encoding="utf-8") == GO_TEXT
    assert src.read_go_artifact(rel, tmp_path)["identity"].n == 111
    cmd, cwd = calls[0]
    assert cmd[:3] == ["go", "test", "-p"] and "-v" in cmd and cwd == tmp_path / "core-go"


def test_run_go_refuses_to_write_output_it_cannot_parse(tmp_path: Path) -> None:
    try:
        src.run_go("2030-01-01", lambda cmd, cwd: "FAIL\n", tmp_path)
    except src.SourceError:
        assert not list(tmp_path.rglob("*.txt"))
    else:
        raise AssertionError("expected SourceError")


def test_a_missing_go_artifact_is_an_error() -> None:
    try:
        src.read_go_artifact(src.go_artifact("1999-01-01"))
    except src.SourceError as err:
        assert "--run-go" in str(err)
    else:
        raise AssertionError("expected SourceError")


def test_live_reports_give_extraction_recall_completion_and_check_totals() -> None:
    extract = src.extract_summary(ROOT / "bench/reports/live-extract-2026-10-02-v4.json")
    assert extract["fact_recall"] == 0.805 and extract["facts"] == 41
    agent = src.agent_report(ROOT / "bench/reports/live-agent-2026-10-02-v3.json")
    assert src.completion(agent) == (19, 25)
    assert src.check_totals(agent, src.RECIPIENT_CHECKS) == (22, 22)
    assert src.check_totals(agent, src.ABSTENTION_CHECKS) == (7, 8)
    assert src.runs_at_tool_cap(agent) == (15, 19)
    before = src.agent_report(ROOT / "bench/reports/live-agent-2026-10-02.json")
    assert src.completion(before) == (15, 25)


def test_ratio_strings_are_validated() -> None:
    assert src.ratio_of("4/5") == (4, 5)
    for bad in ("4", "a/b", "6/5"):
        try:
            src.ratio_of(bad)
        except src.SourceError:
            continue
        raise AssertionError(bad)


def test_every_measured_registry_value_is_recomputed_with_value_n_and_source() -> None:
    sources = mc.Sources.from_repo(GO_DATE)
    measured = [m for m in REGISTRY["metrics"] if m["status"] == "measured"]
    assert {m["id"] for m in measured} == set(mc.COLLECTORS)
    for m in measured:
        got = mc.COLLECTORS[m["id"]](sources)
        assert mc.matches(got, m["current_value"]), (m["id"], got)


def test_matches_rejects_a_different_sample_size_or_source() -> None:
    current = {"value": 1.0, "n": 109, "source": "a"}
    assert mc.matches(mc.Collected(1.0, "", "a", 109), current)
    assert not mc.matches(mc.Collected(1.0, "", "a", 110), current)
    assert not mc.matches(mc.Collected(1.0, "", "b", 109), current)


def test_section_20_case_type_counts_cover_every_gold_case() -> None:
    sources = mc.Sources.from_repo(GO_DATE)
    counts = {i: mc.COLLECTORS[i](sources).value for i in mc.COLLECTORS if i.startswith("bench.")}
    assert len(counts) == 17 and sum(counts.values()) == 87
    assert counts["bench.too_soon_cta"] == 8 and counts["bench.correct_timing"] == 9


def test_same_value_compares_rounded_numbers_and_dicts() -> None:
    assert mc.same_value(0.92857, 0.929) and mc.same_value({"p": 1.0}, {"p": 1})
    assert not mc.same_value(0.8, 0.805) and not mc.same_value({"p": 1.0}, {"q": 1.0}) and not mc.same_value(True, 1)


def test_gold_counts_per_eval_type_and_case_type() -> None:
    cases = ms.load_cases()
    assert len(cases) == 87
    counts = ms.eval_type_counts(cases)
    assert counts["champion_continuity"] == 36 and counts["buyer_readiness"] == 28
    assert ms.case_summary(cases) == {"cases": 87, "should_pass": 28, "flawed": 59, "blocking_fails": 71,
                                      "with_expected_best_action": 87, "contested_judgments": 1, "cases_without_contested": 86,
                                      "should_pass_without_contested": 28, "blocking_fails_without_contested": 70}


def test_slices_assign_every_gold_case_one_value_per_derivable_dimension() -> None:
    cases = ms.load_cases()
    coverage = ms.slice_coverage(cases)
    assert set(coverage) == {f"slices.{k}" for k in ms.SLICE_RULES}
    for slice_id, values in coverage.items():
        assert sum(values.values()) == 87, slice_id
    assert coverage["slices.motion"] == {"expansion": 79, "new_business": 4, "renewal": 4}
    assert coverage["slices.knowledge_available"] == {"no_knowledge": 70, "knowledge_available": 17}
    assert coverage["slices.stage"]["early"] == 11
    computable = {m["id"] for m in REGISTRY["metrics"] if m["layer"] == "slices" and m["status"] == "computable"}
    assert computable == set(coverage)


def test_report_has_a_row_per_metric_with_status_value_or_blocker() -> None:
    rep = report.build_report(REGISTRY, mc.Sources.from_repo(GO_DATE), DATE)
    rows = {r["id"]: r for r in rep["metrics"]}
    assert set(rows) == set(BY_ID)
    assert rows["online_product.workflow_completion"]["value"] == 0.76
    assert rows["online_product.workflow_completion"]["matches_registry"] is True
    assert rows["l2.grounding"]["status"] == "computable" and "judges-2026-10-03" in rows["l2.grounding"]["notes"]
    assert rows["routing.candidate.buyer_readiness"]["status"] == "blocked" and rows["routing.candidate.buyer_readiness"]["blocker"]
    assert rows["l2.grounding"]["evidence"]["gold_judgments"] > 0 and rows["l2.grounding"]["tracked_by"]
    assert rows["l1.entity_attachment"]["data_basis"] == "legacy_fixture_world"
    assert rows["l1.account_association"]["n"] == 110
    assert rows["eval_quality.agreement_with_gold"]["evidence"]["gold_cases"]["cases"] == 87
    assert rows["l3.no_unnecessary_tool_calls"]["evidence"]["runs_at_6_call_cap"] == "15/19"
    assert rows["slices.motion"]["evidence"]["gold_cases_per_value"]["expansion"] == 79
    total = sum(sum(v.values()) for v in rep["summary"]["by_layer_status"].values())
    assert total == len(BY_ID) and rep["summary"]["stale"] == []
    assert rep["go_acceptance_source"] == "bench/reports/go-acceptance-2026-10-02.txt"


def test_every_harness_evidence_key_has_a_lookup() -> None:
    keys = {k for m in REGISTRY["metrics"] for k in m.get("harness_evidence", [])}
    assert keys == set(report.EVIDENCE)


def test_markdown_report_lists_every_metric_and_the_blockers() -> None:
    rep = report.build_report(REGISTRY, mc.Sources.from_repo(GO_DATE), DATE)
    md = report.render_markdown(rep)
    for m in REGISTRY["metrics"]:
        assert f"`{m['id']}`" in md
    assert "19/25" in md and "blocked" in md and "## Summary" in md and "| data_basis |" in md


def test_rerun_reproduces_the_committed_report_byte_for_byte(tmp_path: Path) -> None:
    assert report.main(["--date", DATE, "--go-date", GO_DATE, "--out-dir", str(tmp_path)]) == 0
    for suffix in (".json", ".md"):
        committed = (ROOT / "bench" / "reports" / f"metrics-{DATE}{suffix}").read_bytes()
        assert (tmp_path / f"metrics-{DATE}{suffix}").read_bytes() == committed, suffix
    data = json.loads((tmp_path / f"metrics-{DATE}.json").read_text(encoding="utf-8"))
    assert data["date"] == DATE and data["go_acceptance_source"] == src.go_artifact(GO_DATE)


def test_date_is_required() -> None:
    try:
        report.main([])
    except SystemExit as exc:
        assert exc.code == 2
    else:
        raise AssertionError("expected argparse error")


def test_docs_metrics_md_is_generated_from_the_registry() -> None:
    docs = metrics_docs.render_docs(REGISTRY)
    assert list(docs) == ["docs/metrics.md", "docs/metrics-learning-outcomes.md"]
    for rel, text in docs.items():
        assert (ROOT / rel).read_text(encoding="utf-8") == text, f"regenerate {rel}: report.py --docs"
        assert len(text.splitlines()) < 400, rel
    combined = "\n".join(docs.values())
    assert "| kind | status | data_basis |" in combined
    assert all(f"`{m['id']}`" in combined for m in REGISTRY["metrics"])
    order = [combined.index(f"## {metrics_docs.LAYER_TITLES[layer]}") for layer in REGISTRY["layer_order"]]
    assert order == sorted(order), "groups run from process metrics to final state and outcome metrics"
