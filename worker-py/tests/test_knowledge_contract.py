"""WP20 (HAR-118) contract tests: Knowledge scope fields, condition grammar, lifecycle thresholds, SQL parity."""
from __future__ import annotations

import copy
import re

import pytest

from test_contract_parity import SQL
from test_contracts import CONTRACTS, SCHEMAS, example, load_json, validator

LIFECYCLE = CONTRACTS / "knowledge" / "lifecycle.v1.json"
EPISODE = "0e9e0000-0000-4000-8000-000000000a01"


def scoped_k17() -> dict:
    doc = copy.deepcopy(example("knowledge"))
    doc["applicability_conditions"] = [
        {"field": "transition.to_state", "op": "eq", "value": "EXPANSION"},
        {"field": "transition.status", "op": "in", "value": ["CANDIDATE"]},
        {"field": "relationship_state", "op": "eq", "value": "REORG"},
        {"field": "current_commitments", "op": "contains", "value": {"text": "residency", "status": ["open", "overdue"]}},
    ]
    doc["status_history"] = [
        {"from_status": None, "to_status": "candidate", "reason": "created from human delta", "changed_at": "2026-08-01T00:00:00Z"},
        {"from_status": "candidate", "to_status": "provisional", "reason": "1 decision, 1 positive reaction",
         "evidence_kind": "customer_reaction", "evidence_ref_id": EPISODE, "changed_at": "2026-08-03T00:00:00Z"},
    ]
    return doc


def errors(name: str, doc: dict) -> list[str]:
    return [f"{list(e.absolute_path)}: {e.message}" for e in validator(name).iter_errors(doc)]


def test_scoped_knowledge_with_history_validates() -> None:
    assert errors("knowledge", scoped_k17()) == []


BAD_CONDITIONS = [
    ({"field": "motion", "op": "eq"}, "eq needs a value"),
    ({"field": "motion", "op": "in", "value": []}, "in needs at least one value"),
    ({"field": "motion", "op": "in", "value": "expansion"}, "in needs a list"),
    ({"field": "diff.new_stakeholder_entered", "op": "exists", "value": True}, "exists takes no value"),
    ({"field": "economic_buyer", "op": "is_unknown", "value": "x"}, "is_unknown takes no value"),
    ({"field": "blockers", "op": "contains", "value": {"owner": "rep"}}, "unknown item pattern key"),
    ({"field": "blockers", "op": "contains", "value": {}}, "empty item pattern"),
    ({"field": "transition.target", "op": "eq", "value": "EXPANSION"}, "unknown transition attribute"),
    ({"field": "Motion", "op": "eq", "value": "expansion"}, "field names are snake case"),
    ({"field": "motion", "op": "eq", "value": ["expansion"]}, "eq takes a scalar"),
]


@pytest.mark.parametrize(("condition", "why"), BAD_CONDITIONS, ids=[b[1] for b in BAD_CONDITIONS])
def test_malformed_conditions_are_rejected(condition: dict, why: str) -> None:
    doc = scoped_k17()
    doc["situation_signature"] = [condition]
    assert errors("knowledge", doc), why


def test_bad_history_status_is_rejected() -> None:
    doc = scoped_k17()
    doc["status_history"][1]["to_status"] = "promoted"
    assert errors("knowledge", doc)


def test_lifecycle_rules_validate_and_equal_the_example() -> None:
    rules = load_json(LIFECYCLE)
    assert errors("knowledge_lifecycle", rules) == []
    assert rules == example("knowledge_lifecycle")


def test_lifecycle_rungs_ascend() -> None:
    rungs = load_json(LIFECYCLE)["rungs"]
    for lower, upper in zip(rungs, rungs[1:]):
        assert upper["min_decisions"] > lower["min_decisions"]
        assert upper["min_positive_reactions"] >= lower["min_positive_reactions"]
        assert upper["max_counterexample_share"] <= lower["max_counterexample_share"]


def test_lifecycle_rejects_out_of_order_rungs() -> None:
    rules = load_json(LIFECYCLE)
    rules["rungs"] = list(reversed(rules["rungs"]))
    assert errors("knowledge_lifecycle", rules)


def test_applicable_statuses_match_the_sql_partial_index() -> None:
    m = re.search(r"CREATE INDEX knowledge_applicable_idx ON knowledge \(status\) WHERE status IN \((.*?)\);", SQL)
    assert m
    assert set(re.findall(r"'([^']+)'", m.group(1))) == set(load_json(LIFECYCLE)["applicable_statuses"])


def _columns(table: str) -> set[str]:
    cols: set[str] = set()
    for body in re.findall(rf"CREATE TABLE {table} \((.*?)\n\);", SQL, re.S):
        cols |= {m.group(1) for m in re.finditer(r"^\s+([a-z_]+)\s+[a-z]", body, re.M)}
    cols |= set(re.findall(rf"ALTER TABLE {table}\b[^;]*?ADD COLUMN ([a-z_]+)", SQL, re.S))
    for block in re.findall(rf"ALTER TABLE {table}\n(.*?);", SQL, re.S):
        cols |= set(re.findall(r"ADD COLUMN\s+([a-z_]+)", block))
    return cols


# Contract property -> SQL home (migration 0007 comment; ADR-0013).
KNOWLEDGE_SQL = {
    "id": "id", "key": "key", "title": "title", "situation_signature": "situation_signature",
    "applicability_conditions": "applicability_conditions", "guidance": "guidance", "status": "status",
    "counts": "counts", "counterexamples": "counterexamples", "exceptions": "exceptions",
    "evidence_classes": "evidence_classes", "used_by_evaluators": "used_by_evaluators", "provenance": "provenance",
    "created_at": "created_at", "last_validated_at": "last_validated_at",
}
DERIVED = {"supporting_decision_episode_ids": "knowledge_evidence", "status_history": "knowledge_status_history"}


def test_every_knowledge_property_has_a_sql_home() -> None:
    props = set(load_json(SCHEMAS / "knowledge.v1.json")["properties"])
    assert props == set(KNOWLEDGE_SQL) | set(DERIVED)
    assert set(KNOWLEDGE_SQL.values()) <= _columns("knowledge")
    assert {"support_count", "guidance_do", "guidance_dont"} <= _columns("knowledge")


def test_status_history_items_match_the_history_table() -> None:
    item = load_json(SCHEMAS / "knowledge.v1.json")["properties"]["status_history"]["items"]
    assert set(item["properties"]) <= _columns("knowledge_status_history")
    kinds = set(item["properties"]["evidence_kind"]["enum"]) - {None}
    m = re.search(r"evidence_kind\s+text CHECK \(evidence_kind IN \((.*?)\)\)", SQL, re.S)
    assert m and set(re.findall(r"'([^']+)'", m.group(1))) == kinds
