"""WP18 (HAR-116): every semantic catalog type has a data rubric consistent with the catalog; loaders fail loudly."""
from __future__ import annotations

import json
import re
import shutil
from pathlib import Path

import pytest

from judge_doubles import CATALOG, RUBRICS

from ghost_worker.errors import ConfigError
from ghost_worker.judges.catalog import load_catalog
from ghost_worker.judges.output import output_schema
from ghost_worker.judges.rubric import RUBRIC_DIR, load_rubric, load_rubrics

# HAR-97 L2 spellings of diagnostics that the EvalResult contract names differently (documented in wp18.md; the
# judges do not accept them).
L2_SPELLINGS = {"missing_decision_maker", "no_business_case", "support_risk_conflicts_with_expansion"}


def test_one_rubric_per_semantic_catalog_type_in_catalog_order() -> None:
    assert tuple(RUBRICS) == CATALOG.semantic_types() and len(RUBRICS) == 25


@pytest.mark.parametrize("name", list(RUBRICS))
def test_rubric_labels_blocking_and_class_follow_the_catalog(name: str) -> None:
    rubric, entry = RUBRICS[name], CATALOG.entry(name)
    assert sorted(g.label for g in rubric.labels) == sorted(entry.labels)
    assert (rubric.pass_label in entry.labels) if entry.labels else rubric.pass_label is None
    assert entry.blocking_rule, "blocking comes from the catalog (HAR-116 review)"
    assert entry.evidence_class in {"deal_data", "methodology", "cs_ops"}
    assert rubric.eval_version == f"{name}:v{rubric.version}"


def test_diagnostic_guides_cover_the_contract_vocabulary_exactly() -> None:
    guided = {d.name for r in RUBRICS.values() for d in r.diagnostics}
    assert guided == set(CATALOG.diagnostics)
    assert not guided & L2_SPELLINGS


@pytest.mark.parametrize("name", list(RUBRICS))
def test_output_schema_diagnostic_enum_is_the_contract_enum_so_l2_spellings_are_unreachable(name: str) -> None:
    schema = output_schema(RUBRICS[name], CATALOG.entry(name), CATALOG)
    assert set(schema["properties"]["diagnostics"]["items"]["enum"]) == set(CATALOG.diagnostics)


def test_only_rep_style_sees_the_rep_profile_and_reports_direction_magnitude() -> None:
    assert [n for n, r in RUBRICS.items() if r.uses_rep_profile] == ["rep_style"]
    style = RUBRICS["rep_style"]
    assert {c.id for c in style.criteria} == {"brevity", "formality", "directness", "warmth", "pressure_level"}
    assert all(c.output == "direction_magnitude" and c.before and c.after for c in style.criteria)
    assert any("fake personality scores" in p for p in style.principles)
    others = [c for n, r in RUBRICS.items() if n != "rep_style" for c in r.criteria]
    assert all(c.output == "finding" for c in others)


def test_wait_and_no_action_are_legitimate_next_action_labels() -> None:
    labels = {g.label for g in RUBRICS["next_action_quality"].labels}
    assert {"WAIT", "NO_ACTION"} <= labels
    assert "no_action" in RUBRICS["next_action_quality"].routing.actions


# ---------- loader errors ----------
def _copy_rubrics(tmp_path: Path) -> Path:
    target = tmp_path / "rubrics"
    shutil.copytree(RUBRIC_DIR, target)
    return target


def _edit(path: Path, **changes: object) -> None:
    doc = json.loads(path.read_text(encoding="utf-8"))
    doc.update(changes)
    path.write_text(json.dumps(doc), encoding="utf-8")


@pytest.mark.parametrize(("file", "changes", "fragment"), [
    ("cta_calibration", {"labels": [{"label": "TOO_WEAK", "when": "x"}]}, "label guides"),
    ("cta_calibration", {"pass_label": "READY"}, "pass_label"),
    ("grounding", {"pass_label": "READY"}, "no pass_label"),
    ("grounding", {"blocking_guide": "blocks sometimes"}, "malformed"),
    ("momentum", {"blocking_guide": None}, "malformed"),
    ("grounding", {"diagnostics": [{"name": "invented", "when": "x"}]}, "not in the EvalResult contract"),
    ("grounding", {"routing": {"actions": ["send_email"], "when_any": ["no_such_predicate"]}}, "unknown routing"),
    ("grounding", {"eval_type": "momentum"}, "declares eval_type"),
    ("grounding", {"version": 0}, "malformed"),
])
def test_inconsistent_rubrics_are_rejected(tmp_path: Path, file: str, changes: dict, fragment: str) -> None:
    directory = _copy_rubrics(tmp_path)
    _edit(directory / f"{file}.json", **changes)
    with pytest.raises(ConfigError, match=fragment):
        load_rubrics(CATALOG, directory)


def test_duplicate_criteria_and_missing_examples_are_rejected(tmp_path: Path) -> None:
    directory = _copy_rubrics(tmp_path)
    doc = json.loads((directory / "grounding.json").read_text(encoding="utf-8"))
    _edit(directory / "grounding.json", criteria=[doc["criteria"][0], doc["criteria"][0]])
    with pytest.raises(ConfigError, match="duplicate criterion"):
        load_rubric(directory / "grounding.json", CATALOG)
    style = json.loads((directory / "rep_style.json").read_text(encoding="utf-8"))
    style["criteria"][0]["before"] = None
    (directory / "rep_style.json").write_text(json.dumps(style), encoding="utf-8")
    with pytest.raises(ConfigError, match="before/after"):
        load_rubric(directory / "rep_style.json", CATALOG)


def test_missing_and_non_semantic_rubrics_are_rejected(tmp_path: Path) -> None:
    directory = _copy_rubrics(tmp_path)
    (directory / "momentum.json").unlink()
    with pytest.raises(ConfigError, match="no rubric for semantic eval types"):
        load_rubrics(CATALOG, directory)
    doc = json.loads((RUBRIC_DIR / "grounding.json").read_text(encoding="utf-8"))
    doc["eval_type"] = "recipient_correctness"
    (directory / "recipient_correctness.json").write_text(json.dumps(doc), encoding="utf-8")
    with pytest.raises(ConfigError, match="only semantic"):
        load_rubric(directory / "recipient_correctness.json", CATALOG)
    (directory / "broken.json").write_text("{not json", encoding="utf-8")
    with pytest.raises(ConfigError, match="unreadable"):
        load_rubric(directory / "broken.json", CATALOG)


def test_catalog_loader_errors(tmp_path: Path) -> None:
    with pytest.raises(ConfigError, match="cannot read"):
        load_catalog(tmp_path / "missing.json")
    bad = tmp_path / "catalog.json"
    bad.write_text(json.dumps({"catalog_version": 1}), encoding="utf-8")
    with pytest.raises(ConfigError, match="malformed"):
        load_catalog(bad)
    with pytest.raises(ConfigError, match="not in the eval catalog"):
        CATALOG.entry("no_such_eval")


def _strings(node: object):
    if isinstance(node, dict):
        for key, value in node.items():
            yield str(key)
            yield from _strings(value)
    elif isinstance(node, list):
        for value in node:
            yield from _strings(value)
    elif isinstance(node, str):
        yield node


BLOCKING_WORD = re.compile(r"\bblock(s|ed|ing)?(?![a-z])", re.IGNORECASE)  # "blocker" (a state noun) is fine


def blocking_hits(document: object) -> list[str]:
    return [text for text in _strings(document) if BLOCKING_WORD.search(text)]


@pytest.mark.parametrize("path", sorted(RUBRIC_DIR.glob("*.json")), ids=lambda p: p.stem)
def test_no_rubric_file_contains_blocking_logic(path: Path) -> None:
    """HAR-116 review: when a judge blocks is decided by the catalog's blocking_rule only, never by rubric data."""
    hits = blocking_hits(json.loads(path.read_text(encoding="utf-8")))
    assert not hits, f"{path.name} carries blocking logic: {hits}"


def test_the_blocking_check_is_not_vacuous() -> None:
    """The scan must fail on blocking text (it was once a literal backspace, matching nothing)."""
    assert BLOCKING_WORD.pattern.startswith(r"\b")
    clean = json.loads((RUBRIC_DIR / "grounding.json").read_text(encoding="utf-8"))
    assert blocking_hits(clean) == []
    for planted in ("Block when the ask is a signature.", "this blocks the send", "never blocked", "blocking_guide"):
        mutated = {**clean, "principles": [*clean.get("principles", []), planted]}
        assert blocking_hits(mutated), planted
    assert blocking_hits({"x": "a new blocker appeared"}) == []
