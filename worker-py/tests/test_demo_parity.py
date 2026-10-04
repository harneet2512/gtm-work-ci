"""JSON <-> SQL parity for the HAR-129 demo objects (migration 0019, ADR-0017): enums, text limits, the strategy
type pattern and the property-to-column mapping must agree, so a contract change cannot drift from the tables."""
from __future__ import annotations

import re

import pytest
import yaml

from test_contract_parity import MIGRATIONS
from test_contracts import CONTRACTS, SCHEMAS, load_json

SQL_0019 = next(MIGRATIONS.glob("0019_*.sql")).read_text(encoding="utf-8")
ALL_SQL = "\n".join(p.read_text(encoding="utf-8") for p in sorted(MIGRATIONS.glob("*.sql")))


def schema(name: str) -> dict:
    return load_json(SCHEMAS / f"{name}.v1.json")


def up_section(sql: str) -> str:
    return sql.split("-- +goose Up")[1].split("-- +goose Down")[0]


UP = up_section(SQL_0019)
SQL_0023 = up_section(next(MIGRATIONS.glob("0023_*.sql")).read_text(encoding="utf-8"))


def table_block(table: str) -> str:
    m = re.search(rf"CREATE TABLE {table} \((.*?)\n\);", UP, re.S)
    assert m, f"no CREATE TABLE {table} in 0019"
    return m.group(1)


def in_list(text: str, column: str) -> set[str]:
    m = re.search(rf"CHECK \({column} IN \((.*?)\)\)", text, re.S)
    assert m, f"no CHECK ({column} IN (...)) found"
    return set(re.findall(r"'([^']+)'", m.group(1)))


EPISODE_ADDS = re.search(r"ALTER TABLE decision_episodes(.*?)CREATE INDEX", UP, re.S).group(1)

ENUMS = [
    ("episode status", in_list(EPISODE_ADDS, "status"), schema("decision_episode")["properties"]["status"]["enum"]),
    ("episode learning_scope", in_list(EPISODE_ADDS, "learning_scope"), schema("decision_episode")["properties"]["learning_scope"]["enum"]),
    ("send_decision", in_list(table_block("human_strategy_decisions"), "send_decision"),
     schema("human_strategy_decision")["properties"]["send_decision"]["enum"]),
    ("hsd surface", in_list(table_block("human_strategy_decisions"), "surface"), schema("human_decision")["properties"]["surface"]["enum"]),
    ("inference agreement", in_list(table_block("judgment_inferences"), "agreement"),
     schema("judgment_inference")["properties"]["agreement"]["enum"]),
    ("inference human_verdict", in_list(table_block("judgment_inferences"), "human_verdict"),
     schema("judgment_inference")["properties"]["human_verdict"]["enum"]),
    ("inference verdict_surface", in_list(table_block("judgment_inferences"), "verdict_surface"),
     schema("human_decision")["properties"]["surface"]["enum"]),
    ("candidate action_type", in_list(table_block("strategy_candidates"), "action_type"),
     schema("agent_run_output")["properties"]["proposed_action_type"]["enum"]),
    ("candidate action_class", in_list(SQL_0023, "action_class"), schema("strategy_candidate")["properties"]["action_class"]["enum"]),
    ("episode transition_status",
     set(re.findall(r"'([A-Z]+)'", re.search(r"transition_status IN \((.*?)\)", SQL_0023, re.S).group(1))),
     schema("common")["$defs"]["transitionStatus"]["enum"]),
    ("drafts source", in_list(UP.split("agent_run_drafts_source_check", 1)[1], "source"),
     schema("agent_run_draft")["properties"]["source"]["enum"]),
]


@pytest.mark.parametrize(("name", "sql_values", "json_values"), ENUMS, ids=[e[0] for e in ENUMS])
def test_enum_matches_sql_check(name: str, sql_values: set[str], json_values: list[str]) -> None:
    assert sql_values == set(json_values)


def test_semantic_labels_match_the_human_delta_vocabulary() -> None:
    m = re.search(r"semantic_labels <@ ARRAY\[(.*?)\]\)", table_block("judgment_inferences"), re.S)
    assert m
    labels = set(re.findall(r"'([^']+)'", m.group(1)))
    assert labels == set(schema("human_delta")["properties"]["semantic_labels"]["items"]["enum"])


def test_episode_status_values_are_all_documented_in_the_schema_description() -> None:
    description = schema("decision_episode")["properties"]["status"]["description"]
    for value in schema("decision_episode")["properties"]["status"]["enum"]:
        assert value in description


def test_strategy_type_pattern_is_the_same_in_json_and_sql() -> None:
    pattern = schema("strategy_candidate")["properties"]["strategy_type"]["pattern"]
    assert f"strategy_type ~ '{pattern}'" in table_block("strategy_candidates")


# (schema, property, table, column): the JSON maxLength equals the SQL length bound.
LENGTHS = [
    ("business_intelligence_update", "summary", "business_intelligence_updates", "summary"),
    ("business_intelligence_update", "why_it_matters", "business_intelligence_updates", "why_it_matters"),
    ("demo_manifest", "why_selected", "demo_manifests", "why_selected"),
    ("strategy_candidate", "title", "strategy_candidates", "title"),
    ("strategy_candidate", "description", "strategy_candidates", "description"),
    ("strategy_candidate", "rationale", "strategy_candidates", "rationale"),
    ("strategy_candidate", "preview", "strategy_candidates", "preview"),
    ("human_strategy_decision", "actor_label", "human_strategy_decisions", "actor_label"),
]


@pytest.mark.parametrize(("name", "prop", "table", "column"), LENGTHS, ids=[f"{t}.{c}" for _, _, t, c in LENGTHS])
def test_text_limits_match(name: str, prop: str, table: str, column: str) -> None:
    max_length = schema(name)["properties"][prop]["maxLength"]
    min_length = schema(name)["properties"][prop].get("minLength", 1)
    assert f"length({column}) BETWEEN {min_length} AND {max_length}" in table_block(table)


# schema -> (table, {json property: column or None when derived}); properties not listed map to the same column.
MAPPING = {
    "demo_manifest": ("demo_manifests", {}),
    "account_change": ("account_changes", {}),
    "business_intelligence_update": ("business_intelligence_updates", {}),
    "strategy_set": ("strategy_sets", {"candidates": None}),
    "strategy_candidate": ("strategy_candidates", {
        "candidate_id": "id", "to": "to_recipients", "cc": "cc_recipients", "eval_bundle_ref": "eval_bundle_id"}),
    "eval_bundle": ("eval_bundles", {"strategy_candidate_id": None}),
    "human_strategy_decision": ("human_strategy_decisions", {}),
    "judgment_inference": ("judgment_inferences", {"inferred_semantic_delta": "inferred_statement"}),
}
# Episode properties that are derived from other tables, not stored on decision_episodes.
EPISODE_DERIVED = {"trigger_activity_ids", "eval_result_ids", "human_delta_id", "customer_reaction_ids", "business_outcome_ids"}


def added_later(table: str) -> set[str]:
    """Columns that any migration adds with ALTER TABLE <table> ... ADD COLUMN (e.g. 0024 business_intelligence_updates.transition)."""
    out: set[str] = set()
    for m in re.finditer(rf"ALTER TABLE {table}\b(.*?);", ALL_SQL, re.S):
        out |= set(re.findall(r"ADD COLUMN ([a-z_0-9]+)", m.group(1)))
    return out

def added_in_0023(table: str) -> str:
    m = re.search(rf"ALTER TABLE {table}\n(.*?);", SQL_0023, re.S)
    return m.group(1) if m else ""


def columns(table: str) -> set[str]:
    if table == "decision_episodes":
        block = re.search(r"CREATE TABLE decision_episodes \((.*?)\n\);", ALL_SQL, re.S).group(1) + EPISODE_ADDS
    else:
        block = table_block(table)
    block += "\n" + added_in_0023(table)
    cols = set(re.findall(r"^\s{4}([a-z_0-9]+)\s+(?:uuid|text|integer|boolean|jsonb|timestamptz|numeric)", block, re.M))
    return cols | set(re.findall(r"ADD COLUMN ([a-z_0-9]+)", block)) | added_later(table)


@pytest.mark.parametrize("name", sorted(MAPPING), ids=sorted(MAPPING))
def test_every_property_has_a_column(name: str) -> None:
    table, renames = MAPPING[name]
    cols = columns(table)
    for prop in schema(name)["properties"]:
        column = renames.get(prop, prop)
        if column is None:
            continue
        assert column in cols, f"{name}.{prop} has no column {table}.{column}"
    judged = {renames.get(p, p) for p in schema(name)["properties"]}
    extra = cols - judged - {"created_at", "updated_at", "strategy_set_id", "agent_run_id", "semantic_labels", "held_out_event_id"}
    assert not extra, f"{table} has columns the contract does not know: {sorted(extra)}"


def test_held_out_payload_pin_matches_the_contract() -> None:
    """The manifest pins the payload of event N: the schema requires payload_sha256 on held_out_event and SQL checks the same pattern."""
    pattern = schema("held_out_event")["properties"]["payload_sha256"]["pattern"]
    assert "payload_sha256" in schema("demo_manifest")["properties"]["held_out_event"]["required"]
    m = re.search(r"demo_manifests_held_out_payload_pinned\s+CHECK \(coalesce\(held_out_event ->> 'payload_sha256' ~ '([^']+)'", ALL_SQL)
    assert m, "migration 0024 must constrain demo_manifests.held_out_event.payload_sha256"
    assert m.group(1) == pattern


def test_decision_episode_properties_map_to_columns_or_are_derived() -> None:
    cols = columns("decision_episodes")
    for prop in schema("decision_episode")["properties"]:
        assert prop in cols or prop in EPISODE_DERIVED, f"decision_episode.{prop} is neither a column nor derived"
    for prop in EPISODE_DERIVED:
        assert prop not in cols


def test_required_non_derived_properties_are_not_null_columns() -> None:
    """A property the contract requires must not be nullable in SQL (unless a default fills it)."""
    for name, (table, renames) in MAPPING.items():
        block = table_block(table)
        for prop in schema(name)["required"]:
            column = renames.get(prop, prop)
            spec = schema(name)["properties"][prop]
            types = spec.get("type") if isinstance(spec.get("type"), list) else [spec.get("type")]
            # nullable as a type list ("string", "null") or as a oneOf with a null branch (BI `transition`: required, nullable)
            nullable = "null" in types or any(o.get("type") == "null" for o in spec.get("oneOf", []))
            if column is None or column == "created_at" or nullable:
                continue
            m = re.search(rf"^\s{{4}}{column}\s+[^\n]*", block, re.M) or re.search(
                rf"ADD COLUMN {column}\s+[^\n]*", added_in_0023(table))
            assert m, (name, prop)
            line = m.group(0)
            assert "NOT NULL" in line or "PRIMARY KEY" in line, f"{table}.{column} must be NOT NULL: {line.strip()}"


# worker.yaml operations that exist as contract only; the worker work packages remove them as they land.
WORKER_PENDING = {
    "/v1/judgment-inference": "HAR-129 judgment inference (HAR-119)",
}


def test_every_worker_operation_is_implemented_or_pending() -> None:
    """Drift guard for worker.yaml (the core.yaml twin is TestEveryOperationIsTestedOrPending)."""
    spec = yaml.safe_load((CONTRACTS / "openapi" / "worker.yaml").read_text(encoding="utf-8"))
    app_source = (CONTRACTS.parent / "worker-py" / "ghost_worker" / "app.py").read_text(encoding="utf-8")
    implemented = set(re.findall(r'@app\.(?:get|post)\("(/[^"]*)"', app_source))
    for path in spec["paths"]:
        assert path in implemented or path in WORKER_PENDING, f"{path} is neither implemented nor listed as pending"
    for path in WORKER_PENDING:
        assert path in spec["paths"], f"{path} is pending but not in worker.yaml"
        assert path not in implemented, f"{path} is implemented: remove it from WORKER_PENDING"
