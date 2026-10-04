"""Cross-artifact parity: JSON Schema enums must match the SQL CHECK lists, and the
OpenAPI documents must be valid OpenAPI 3.1 with resolvable $refs."""
from __future__ import annotations

import re
from pathlib import Path

import pytest
import yaml
from openapi_spec_validator import validate
from openapi_spec_validator.readers import read_from_filename

from test_contracts import CONTRACTS, SCHEMAS, load_json

MIGRATIONS = CONTRACTS.parent / "core-go" / "internal" / "store" / "migrations"
SQL = "\n".join(p.read_text(encoding="utf-8") for p in sorted(MIGRATIONS.glob("*.sql")))


def sql_in_list(column: str) -> set[str]:
    """Values of the first `<column> ... CHECK (<column> IN (...))` list in the migrations."""
    m = re.search(rf"\b{column}\b[^;]*?CHECK \({column} IN \((.*?)\)\)", SQL, re.S)
    assert m, f"no CHECK list for {column}"
    return set(re.findall(r"'([^']+)'", m.group(1)))


def sql_array_list(constraint: str) -> set[str]:
    m = re.search(rf"CONSTRAINT {constraint} CHECK \(reason_codes <@ ARRAY\[(.*?)\]", SQL, re.S)
    assert m, f"no ARRAY list for {constraint}"
    return set(re.findall(r"'([^']+)'", m.group(1)))


COMMON = load_json(SCHEMAS / "common.v1.json")["$defs"]

PARITY = [
    ("source_system", set(COMMON["sourceSystem"]["enum"])),
    ("decision", set(load_json(SCHEMAS / "human_decision.v1.json")["properties"]["decision"]["enum"])),
    # WP17: the eval input's people directory mirrors the people table.
    ("kind", set(load_json(SCHEMAS / "deterministic_eval_input.v1.json")["$defs"]["person"]["properties"]["kind"]["enum"])),
]


def test_eval_input_prior_actions_use_the_draft_action_vocabulary() -> None:
    """Prior actions are draft actions that reached the world, plus CRM writes (ADR-0014)."""
    eval_input = load_json(SCHEMAS / "deterministic_eval_input.v1.json")
    prior = set(eval_input["$defs"]["priorAction"]["properties"]["action"]["enum"])
    actions = set(load_json(SCHEMAS / "agent_run_output.v1.json")["properties"]["proposed_action_type"]["enum"])
    assert prior - {"crm_update"} <= actions


def test_internal_only_column_matches_the_eval_input_rule() -> None:
    assert "CHECK (NOT internal_only OR kind = 'employee')" in SQL


def test_internal_only_column_type_and_default_match_the_eval_input() -> None:
    assert "ADD COLUMN internal_only boolean NOT NULL DEFAULT false" in SQL
    prop = load_json(SCHEMAS / "deterministic_eval_input.v1.json")["$defs"]["person"]["properties"]["internal_only"]
    assert prop["type"] == "boolean"


@pytest.mark.parametrize(("column", "json_values"), PARITY, ids=[p[0] for p in PARITY])
def test_json_enum_matches_sql_check(column: str, json_values: set[str]) -> None:
    assert sql_in_list(column) == json_values


def test_context_tool_matches_latest_sql_constraint() -> None:
    """0017 adds graph_neighborhood to the tool list; compare against the last definition."""
    json_values = set(load_json(SCHEMAS / "context_packet.v1.json")["properties"]["tool"]["enum"])
    assert "graph_neighborhood" in json_values
    assert latest_up_constraint("context_access_log_tool_check", "tool") == json_values


def test_reason_codes_match_sql() -> None:
    json_values = set(load_json(SCHEMAS / "trigger_evaluation.v1.json")["properties"]["reason_codes"]["items"]["enum"])
    assert sql_array_list("trigger_evaluations_known_reasons") == json_values


def test_agent_run_status_matches_sql() -> None:
    """The latest definition of agent_runs_status_check (0004, redefined by 0016 for 'recorded')."""
    json_values = set(load_json(SCHEMAS / "agent_run.v1.json")["properties"]["status"]["enum"])
    assert latest_up_constraint("agent_runs_status_check", "status") == json_values


@pytest.mark.parametrize("doc", ["core.yaml", "worker.yaml"])
def test_openapi_documents_are_valid(doc: str) -> None:
    path = CONTRACTS / "openapi" / doc
    spec, base_uri = read_from_filename(str(path))
    validate(spec, base_uri=base_uri)
    assert yaml.safe_load(Path(path).read_text(encoding="utf-8"))["openapi"].startswith("3.1")


def test_rel_type_matches_latest_sql_constraint() -> None:
    """The edge vocabulary is redefined by later migrations; compare against the last definition."""
    assert latest_up_constraint("relationships_rel_type_check", "rel_type") == set(COMMON["relType"]["enum"])


HAR97_PARITY = [
    ("evaluator", set(load_json(SCHEMAS / "eval_result.v1.json")["$defs"]["evalType"]["enum"])),
    ("human_action", set(load_json(SCHEMAS / "decision_episode.v1.json")["properties"]["human_action"]["enum"])),
    ("reaction_type", set(load_json(SCHEMAS / "customer_reaction.v1.json")["properties"]["reaction_type"]["enum"])),
    ("polarity", set(load_json(SCHEMAS / "customer_reaction.v1.json")["properties"]["polarity"]["enum"])),
    ("to_status", set(load_json(SCHEMAS / "knowledge.v1.json")["properties"]["status"]["enum"])),
    ("evidence_class", set(load_json(SCHEMAS / "eval_result.v1.json")["properties"]["evidence_class"]["enum"])),
]


@pytest.mark.parametrize(("column", "json_values"), HAR97_PARITY[1:], ids=[p[0] for p in HAR97_PARITY[1:]])
def test_har97_json_enum_matches_sql_check(column: str, json_values: set[str]) -> None:
    assert sql_in_list(column) == json_values


def test_eval_type_domain_matches_schema() -> None:
    """The domain is created in 0007 and redefined by later migrations (0021: state_transition_support)."""
    found = re.findall(r"(?:CREATE DOMAIN eval_type AS text|ALTER DOMAIN eval_type ADD CONSTRAINT eval_type_check) "
                       r"CHECK \(VALUE IN \((.*?)\)\)", _sections("up"), re.S)
    assert found, "eval_type domain missing"
    assert set(re.findall(r"'([^']+)'", found[-1])) == HAR97_PARITY[0][1]
    assert "state_transition_support" in HAR97_PARITY[0][1]


def test_knowledge_and_evaluator_status_lists_match_sql() -> None:
    m = re.search(r"CREATE TABLE knowledge \(.*?status\s+text NOT NULL DEFAULT 'candidate' CHECK \(status IN \((.*?)\)\)", SQL, re.S)
    assert m
    assert set(re.findall(r"'([^']+)'", m.group(1))) == set(load_json(SCHEMAS / "knowledge.v1.json")["properties"]["status"]["enum"])
    m = re.search(r"CREATE TABLE evaluator_versions \(.*?status\s+text NOT NULL CHECK \(status IN \((.*?)\)\)", SQL, re.S)
    assert m
    assert set(re.findall(r"'([^']+)'", m.group(1))) == set(load_json(SCHEMAS / "evaluator_version.v1.json")["properties"]["status"]["enum"])


def test_signal_type_matches_latest_sql_constraint() -> None:
    expected = set(load_json(SCHEMAS / "signal.v1.json")["properties"]["signal_type"]["enum"])
    assert "field_contradicted" in expected
    assert latest_up_constraint("signals_signal_type_check", "signal_type") == expected


def _sections(marker: str) -> str:
    """Concatenated Up (or Down) sections of all migrations."""
    parts = [part.split("-- +goose Down") for part in SQL.split("-- +goose Up")[1:]]
    return "\n".join(p[0] if marker == "up" else (p[1] if len(p) > 1 else "") for p in parts)


def _constraint_lists(sql: str, name: str, column: str) -> list[set[str]]:
    found = re.findall(rf"ADD CONSTRAINT {name} CHECK \({column} IN \((.*?)\)\)", sql, re.S)
    return [set(re.findall(r"'([^']+)'", f)) for f in found]


def latest_up_constraint(name: str, column: str) -> set[str]:
    """Values of the last re-definition of a named CHECK constraint across Up migrations."""
    lists = _constraint_lists(_sections("up"), name, column)
    assert lists, f"{name} is never redefined"
    return lists[-1]


def test_signal_down_migration_restores_the_original_list() -> None:
    original = set(re.findall(r"'([^']+)'", re.search(
        r"signal_type\s+text NOT NULL CHECK \(signal_type IN \((.*?)\)\)", SQL, re.S).group(1)))
    downs = _constraint_lists(_sections("down"), "signals_signal_type_check", "signal_type")
    assert downs and downs[-1] == original


def test_standing_matches_latest_sql_constraints() -> None:
    """ADR-0009 redefines the standing CHECKs; claims and relationships must both match the schema."""
    expected = set(COMMON["standing"]["enum"])
    assert "first_party_record" in expected
    assert latest_up_constraint("claims_standing_check", "standing") == expected
    assert latest_up_constraint("relationships_standing_check", "standing") == expected


def test_source_event_origin_matches_sql() -> None:
    """WP32 (HAR-131): the origin vocabulary and the provenance patterns are the same in JSON and SQL."""
    assert sql_in_list("origin") == set(COMMON["recordOrigin"]["enum"])
    m = re.search(r"provenance\s+text CHECK \(provenance ~ '(.*?)'\)", SQL)
    assert m and m.group(1) == COMMON["recordProvenance"]["pattern"]
    synthetic = load_json(SCHEMAS / "source_event.v1.json")["$defs"]["syntheticProvenance"]["pattern"]
    m = re.search(r"origin IS DISTINCT FROM 'synthetic' OR provenance ~ '(.*?)'", SQL)
    assert m and m.group(1) == synthetic


def test_worker_extractor_version_default_matches_the_contract() -> None:
    """worker.yaml's extractor_version default is the one prompt version the worker serves."""
    from ghost_worker.extract.prompt import EXTRACTOR_VERSION
    from ghost_worker.models import ExtractRequest

    spec = yaml.safe_load((CONTRACTS / "openapi" / "worker.yaml").read_text(encoding="utf-8"))
    body = spec["paths"]["/v1/extract"]["post"]["requestBody"]["content"]["application/json"]["schema"]
    default = body["properties"]["extractor_version"]["default"]
    assert default == EXTRACTOR_VERSION == ExtractRequest.model_fields["extractor_version"].default


def _worker_error_codes_in_contract() -> set[str]:
    spec = yaml.safe_load((CONTRACTS / "openapi" / "worker.yaml").read_text(encoding="utf-8"))
    error = spec["components"]["responses"]["Error"]["content"]["application/json"]["schema"]["properties"]["error"]
    return set(re.findall(r"\b[a-z]+(?:_[a-z]+)+\b", error["properties"]["code"]["description"]))


def _subclasses(cls: type) -> list[type]:
    found: list[type] = []
    for sub in cls.__subclasses__():
        found.extend([sub, *_subclasses(sub)])
    return found


def test_every_worker_error_code_is_listed_in_the_worker_contract() -> None:
    from ghost_worker.errors import WorkerError

    codes = {WorkerError.code, *(sub.code for sub in _subclasses(WorkerError))}
    listed = _worker_error_codes_in_contract()
    assert codes <= listed, f"error codes missing from worker.yaml: {sorted(codes - listed)}"


def test_inconsistent_draft_is_a_contract_error_code() -> None:
    """A skill that disobeys the decision is a consistency failure, not a provider failure (live benchmark)."""
    assert "inconsistent_draft" in _worker_error_codes_in_contract()


# ---------- HAR-126 / ADR-0012: state transitions ----------
TRANSITION_TABLE = re.search(r"CREATE TABLE state_transitions \((.*?)\n\);", SQL, re.S)


def _transition_check(column: str) -> set[str]:
    assert TRANSITION_TABLE, "state_transitions table missing"
    m = re.search(rf"\b{column}\b[^,]*?CHECK \({column} IN \((.*?)\)\)", TRANSITION_TABLE.group(1), re.S)
    assert m, f"no CHECK list for state_transitions.{column}"
    return set(re.findall(r"'([^']+)'", m.group(1)))


def test_transition_status_matches_sql() -> None:
    assert _transition_check("status") == set(COMMON["transitionStatus"]["enum"])


def test_relationship_states_match_sql() -> None:
    states = set(COMMON["relationshipState"]["enum"])
    assert _transition_check("to_state_candidate") == states
    assert _transition_check("from_state") == states | {"unknown"}


def test_transition_history_status_matches_sql() -> None:
    m = re.search(r"CREATE TABLE state_transition_history \(.*?status\s+text NOT NULL CHECK \(status IN \((.*?)\)\)", SQL, re.S)
    assert m, "state_transition_history missing"
    assert set(re.findall(r"'([^']+)'", m.group(1))) == set(COMMON["transitionStatus"]["enum"])


def test_rule_set_version_format_matches_schema() -> None:
    pattern = COMMON["ruleSetVersion"]["pattern"]
    assert f"rule_set_version ~ '{pattern}'" in TRANSITION_TABLE.group(1)


def test_close_reason_matches_sql() -> None:
    reasons = load_json(SCHEMAS / "state_transition.v1.json")["properties"]["close_reason"]["enum"]
    assert _transition_check("close_reason") == {r for r in reasons if r is not None}


def test_rule_set_motion_vocabulary_matches_sql() -> None:
    """state.motion literals in the rule set are validated against this list; it must equal opportunities.motion."""
    rules = load_json(CONTRACTS / "transitions" / "rules.v1.json")
    assert sql_in_list("motion") == set(rules["vocabularies"]["motion"])


def test_transitions_are_read_only_in_every_api() -> None:
    """ADR-0012: the agent (and any API client) reads transitions; only the detector writes them."""
    for doc in ("core.yaml", "worker.yaml"):
        spec = yaml.safe_load((CONTRACTS / "openapi" / doc).read_text(encoding="utf-8"))
        for path, item in spec["paths"].items():
            if "transition" in path:
                assert set(item) - {"parameters", "summary", "description"} == {"get"}, (doc, path)
    core = yaml.safe_load((CONTRACTS / "openapi" / "core.yaml").read_text(encoding="utf-8"))
    assert "/accounts/{account_id}/transitions" in core["paths"]


def test_claim_field_paths_match_latest_sql_constraint() -> None:
    """0021 adds org_change, expansion_need and business_value (ADR-0012); the Down restores the 0003 list."""
    expected = set(load_json(SCHEMAS / "claim.v1.json")["$defs"]["fieldPath"]["enum"])
    assert {"org_change", "expansion_need", "business_value"} <= expected
    assert latest_up_constraint("claims_field_path_check", "field_path") == expected | {"amount"}
    before = sql_in_list("field_path") | {"amount"}  # 0003's list plus ADR-0016's amount (0017): what the Down of 0018 restores
    downs = _constraint_lists(_sections("down"), "claims_field_path_check", "field_path")
    assert downs and downs[-1] == before and before - {"amount"} | {"org_change", "expansion_need", "business_value"} == expected

def test_activity_type_matches_latest_sql_constraint() -> None:
    """0013 (HAR-130) redefines the activity vocabulary; compare against the last definition."""
    expected = set(COMMON["activityType"]["enum"])
    assert {"CRMTaskLogged", "QuoteCreated", "ContractSigned", "OrderPlaced", "ChatTranscriptReady"} <= expected
    assert latest_up_constraint("activities_activity_type_check", "activity_type") == expected


def _split_migration(path: Path) -> tuple[str, str]:
    """(Up, Down) text of one migration file."""
    up, _, down = path.read_text(encoding="utf-8").partition("-- +goose Down")
    return up, down


def test_activity_type_down_restores_the_previous_migrations_list() -> None:
    """Every migration that redefines the constraint must, on Down, restore what the migration before it
    left behind: the last earlier Up redefinition, or the original inline CHECK of 0002. A later migration
    changing the list must not make an earlier Down look wrong, nor the reverse."""
    name, column = "activities_activity_type_check", "activity_type"
    files = sorted(MIGRATIONS.glob("*.sql"))
    before: set[str] = sql_in_list(column)  # the original inline CHECK (migration 0002)
    checked = 0
    for path in files:
        up, down = _split_migration(path)
        (up_lists, down_lists) = (_constraint_lists(up, name, column), _constraint_lists(down, name, column))
        if down_lists:
            assert down_lists[-1] == before, f"{path.name}: Down does not restore the previous migration's list"
            checked += 1
        if up_lists:
            before = up_lists[-1]
    assert checked >= 1, "no migration redefines the activity type constraint on Down"
    # The state after the last Up is the contract's list.
    assert before == set(COMMON["activityType"]["enum"])


def test_worker_activity_type_literal_matches_schema() -> None:
    from typing import get_args

    from ghost_worker.models import ActivityType

    assert list(get_args(ActivityType)) == COMMON["activityType"]["enum"]


def test_claim_field_path_matches_latest_sql_constraint() -> None:
    """ADR-0016 added 'amount' (CRM rule only, never extracted); the CHECK list is redefined by a later migration."""
    extractable = set(load_json(SCHEMAS / "claim.v1.json")["$defs"]["fieldPath"]["enum"])
    assert latest_up_constraint("claims_field_path_check", "field_path") == extractable | {"amount"}


def test_opportunity_state_fields_are_account_state_fields_plus_amount() -> None:
    """ADR-0016: a deal carries every account-state field, and amount."""
    acct = set(load_json(SCHEMAS / "account_state.v1.json")["properties"]["fields"]["properties"])
    opp = set(load_json(SCHEMAS / "opportunity_state.v1.json")["properties"]["fields"]["properties"])
    # the three evidence lists of ADR-0012 are not folded into a state yet (extract-v5); the detector reads their claims
    assert opp == (acct - {"org_changes", "expansion_needs", "business_value"}) | {"amount"}


def _member_schema(schema: str) -> dict:
    doc = load_json(SCHEMAS / f"{schema}.v1.json")
    return doc["properties"]["buying_group"]["items"]


def test_role_source_is_one_vocabulary_in_both_states() -> None:
    """ADR-0016: members of AccountState and of OpportunityState are one definition; role_source is recorded,
    inferred or null (state is jsonb, so there is no SQL column to match)."""
    account = _member_schema("account_state")
    assert _member_schema("opportunity_state") == {"$ref": "account_state.v1.json#/properties/buying_group/items"}
    assert set(account["properties"]["role_source"]["enum"]) == {"recorded", "inferred", None}
    assert {"role_source", "role_basis"} <= set(account["properties"])
    assert account["properties"]["role_basis"]["maxLength"] <= 200
