"""WP32 (HAR-131) leakage guard: the planted rules must never reach the agent, the evals or the
knowledge store. Knowledge may only come from learning on previous deals.

Fails if any rule key, rule statement or 8-word run of a statement appears in the worker
prompts/code, the recorded LLM cassettes, the eval catalog, the contract examples (knowledge,
guidance) or the eval fixtures (offered knowledge)."""
from __future__ import annotations

import json
import sys
from pathlib import Path

import pytest

ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT / "bench"))

from synthetic import leakcheck, rules  # noqa: E402

SCANNED = [
    ROOT / "worker-py" / "ghost_worker",          # prompts and agent code
    ROOT / "worker-py" / "cassettes",             # recorded LLM prompts
    ROOT / "contracts",                           # eval catalog, rubrics, knowledge examples, schemas
    ROOT / "fixtures" / "evals",
    ROOT / "fixtures" / "world",
    ROOT / "fixtures" / "gold",
    ROOT / "fixtures" / "live",
    ROOT / "bench" / "live",
    ROOT / "docs" / "adr",
    ROOT / "core-go",                             # full source text, not just path names
    ROOT / "bench" / "synthetic" / "templates",   # reply templates the generator writes from
] + [p for p in (ROOT / "worker-py" / "tests" / "knowledge_lessons.py",) if p.exists()]  # PR #23 lessons, once merged


def test_scanned_locations_exist_and_contain_text() -> None:
    files = [p for p, _ in leakcheck.text_files(SCANNED)]
    assert all(root.exists() for root in SCANNED)
    assert len(files) > 300
    names = {p.name for p in files}
    assert {"prompt.py", "knowledge.example.json", "replies.v1.json", "normalize.go"} <= names


def test_no_planted_rule_text_reaches_prompts_evals_or_knowledge() -> None:
    markers = leakcheck.markers(rules.load_rules())
    hits = leakcheck.scan(SCANNED, markers)
    assert not hits, "\n".join(f"{p}: {m!r}" for p, m in hits)


def test_markers_cover_keys_statements_and_shingles() -> None:
    rs = rules.load_rules()
    markers = leakcheck.markers(rs)
    first = rs.rules[0]
    assert leakcheck.normalize(first.key) in markers
    assert leakcheck.normalize(first.statement) in markers
    assert len(markers) > 2 * len(rs.rules)


def test_detector_catches_verbatim_reworded_case_and_partial_copies(tmp_path: Path) -> None:
    rs = rules.load_rules()
    stmt = rs.rules[0].statement
    words = stmt.split()
    (tmp_path / "prompt.py").write_text(f'SYSTEM = """{stmt.upper()}"""', encoding="utf-8")
    (tmp_path / "k.json").write_text('{"summary": "' + " ".join(words[1:10]) + '"}', encoding="utf-8")
    (tmp_path / "id.md").write_text(f"see {rs.rules[1].key}", encoding="utf-8")
    hit_files = {p.name for p, _ in leakcheck.scan([tmp_path], leakcheck.markers(rs))}
    assert hit_files == {"prompt.py", "k.json", "id.md"}


def test_detector_sees_through_json_escapes_and_unusual_suffixes(tmp_path: Path) -> None:
    """A cassette stores prompts as JSON strings: a statement wrapped across \\n or quoted with \\" still hits."""
    words = rules.load_rules().rules[0].statement.split()
    wrapped = " ".join(words[:5]) + "\n" + " ".join(words[5:12])
    (tmp_path / "cassette.json").write_text(json.dumps({"messages": [{"content": wrapped}]}), encoding="utf-8")
    (tmp_path / "system.prompt").write_text(" ".join(words[:10]), encoding="utf-8")
    (tmp_path / "blob.bin").write_bytes(b"\x00\x01" + " ".join(words).encode())
    hit_files = {p.name for p, _ in leakcheck.scan([tmp_path], leakcheck.markers(rules.load_rules()))}
    assert hit_files == {"cassette.json", "system.prompt"}


def test_generated_output_is_scanned_for_rule_text_and_visible_markers(tmp_path: Path) -> None:
    rs = rules.load_rules()
    ok = {"origin": "synthetic", "provenance": "synthetic:v1", "payload": {"body_text": "Thanks, see you Monday."}}
    (tmp_path / "a.json").write_text(json.dumps(ok), encoding="utf-8")
    assert leakcheck.scan_generated(tmp_path, rs) == []
    bad_id = {**ok, "source_object_id": "<syn1-reply-1@acme.com>"}
    bad_text = {**ok, "payload": {"body_text": rs.rules[0].statement}}
    (tmp_path / "b.json").write_text(json.dumps(bad_id), encoding="utf-8")
    (tmp_path / "c.json").write_text(json.dumps(bad_text), encoding="utf-8")
    problems = leakcheck.scan_generated(tmp_path, rs)
    assert any("b.json" in p and "visible marker" in p for p in problems)
    assert any("c.json" in p and "rule text" in p for p in problems)


def test_detector_ignores_ordinary_gtm_language(tmp_path: Path) -> None:
    (tmp_path / "ok.txt").write_text("Keep the champion in the loop and confirm budget with finance.", encoding="utf-8")
    assert not leakcheck.scan([tmp_path], leakcheck.markers(rules.load_rules()))


@pytest.mark.parametrize("schema", ["activity", "context_packet", "account_state", "agent_run_output"])
def test_the_origin_marker_is_not_part_of_what_the_agent_reads(schema: str) -> None:
    """The synthetic marker lives on the SourceEvent (audit). If it were on the activity, packet or
    state, a learner could key on 'synthetic' instead of on what happened."""
    text = (ROOT / "contracts" / "schemas" / f"{schema}.v1.json").read_text(encoding="utf-8")
    assert '"origin"' not in text


def test_platform_code_never_reads_the_rule_file_or_the_labels() -> None:
    code = [ROOT / "worker-py" / "ghost_worker", ROOT / "core-go"]
    needles = ["bench/synthetic", "synthetic/v1/labels", "synthetic_rules:v1"]
    hits = leakcheck.scan(code, {leakcheck.normalize(n) for n in needles})
    assert not hits, hits
