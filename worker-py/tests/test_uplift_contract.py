"""contracts/har129/abc_report.v1.json and the blind-pair schemas: well-formed, strict, and the example conforms."""
from __future__ import annotations

import copy
import json
from pathlib import Path

import pytest
from jsonschema import Draft202012Validator, FormatChecker
from referencing import Registry, Resource

CONTRACTS = Path(__file__).resolve().parents[2] / "contracts"
HAR129 = CONTRACTS / "har129"


def load(path: Path) -> dict:
    return json.loads(path.read_text(encoding="utf-8"))


def registry() -> Registry:
    docs = [load(p) for p in (CONTRACTS / "schemas").glob("*.json")]
    return Registry().with_resources([(d["$id"], Resource.from_contents(d)) for d in docs])


def report_validator() -> Draft202012Validator:
    return Draft202012Validator(load(HAR129 / "abc_report.v1.json"), registry=registry(), format_checker=FormatChecker())


EXAMPLE = load(HAR129 / "abc_report.example.json")


@pytest.mark.parametrize("name", ["abc_report.v1.json", "abc_pairs_blind.v1.json", "abc_pairs_key.v1.json"])
def test_the_schemas_are_valid_draft_2020_12_with_a_ghost_id(name: str) -> None:
    doc = load(HAR129 / name)
    Draft202012Validator.check_schema(doc)
    assert doc["$id"] == f"https://ghost.local/contracts/har129/{name}"


def test_the_example_conforms() -> None:
    assert list(report_validator().iter_errors(EXAMPLE)) == []


@pytest.mark.parametrize("key", ["experiment", "provenance", "caveats", "learning", "situations", "paired", "negative_transfer",
                                 "corrections", "exceptions", "headline"])
def test_every_top_level_section_is_required(key: str) -> None:
    broken = copy.deepcopy(EXAMPLE)
    del broken[key]
    assert list(report_validator().iter_errors(broken))


def test_unknown_fields_are_refused_at_every_level() -> None:
    for path in (("headline",), ("provenance", "data"), ("learning", "guard"), ("situations", 0), ("situations", 0, "arms", "B"),
                 ("paired", "B_vs_A")):
        broken = copy.deepcopy(EXAMPLE)
        node = broken
        for step in path:
            node = node[step]
        node["unexpected"] = 1
        assert list(report_validator().iter_errors(broken)), path


def test_the_report_must_say_how_it_was_scored_and_carry_the_caveats() -> None:
    broken = copy.deepcopy(EXAMPLE)
    broken["caveats"] = broken["caveats"][:4]
    assert list(report_validator().iter_errors(broken))
    assert "PROXY" in load(HAR129 / "abc_report.v1.json")["description"]
    assert "No LLM judge" in load(HAR129 / "abc_report.v1.json")["description"]


def test_the_arm_decision_carries_score_regret_corrections_and_knowledge_ids() -> None:
    required = set(load(HAR129 / "abc_report.v1.json")["$defs"]["arm"]["required"])
    assert {"decision_score", "regret", "corrections_needed", "knowledge_retrieved", "knowledge_applicable",
            "knowledge_exception_blocked", "knowledge_cited", "realized_option", "followed_recommendation"} <= required


def test_provenance_pins_model_cassettes_seed_and_every_data_manifest() -> None:
    props = load(HAR129 / "abc_report.v1.json")["properties"]["provenance"]["properties"]
    assert {"runtime_model", "cassettes", "seed", "data"} <= set(props)
    assert {"synthetic_manifest_sha256", "base_export_sha256", "rules_sha256", "split_sha256", "pack_sha256",
            "learning_sha256"} <= set(props["data"]["properties"])


def test_a_bad_sha256_or_unknown_arm_status_is_refused() -> None:
    broken = copy.deepcopy(EXAMPLE)
    broken["provenance"]["cassettes"]["sha256"] = "xyz"
    assert list(report_validator().iter_errors(broken))
    broken = copy.deepcopy(EXAMPLE)
    broken["learning"]["knowledge"][0]["status"] = "great"
    assert list(report_validator().iter_errors(broken))
