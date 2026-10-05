"""Python-side conformance tests for contracts/schemas (mirrors core-go/internal/contracts)."""
from __future__ import annotations

import copy
import json
from collections.abc import Callable, Iterator
from pathlib import Path

import pytest
from jsonschema import Draft202012Validator, FormatChecker, ValidationError
from referencing import Registry, Resource

CONTRACTS = Path(__file__).resolve().parents[2] / "contracts"
SCHEMAS = CONTRACTS / "schemas"
EXAMPLES = CONTRACTS / "examples"


def load_json(path: Path) -> dict:
    return json.loads(path.read_text(encoding="utf-8"))


def _registry() -> Registry:
    resources = []
    for path in SCHEMAS.glob("*.json"):
        doc = load_json(path)
        Draft202012Validator.check_schema(doc)
        resources.append((doc["$id"], Resource.from_contents(doc)))
    return Registry().with_resources(resources)


REGISTRY = _registry()


def validator(name: str) -> Draft202012Validator:
    return Draft202012Validator(load_json(SCHEMAS / f"{name}.v1.json"), registry=REGISTRY,
                                format_checker=FormatChecker())


def example(name: str) -> dict:
    return load_json(EXAMPLES / f"{name}.example.json")


EXAMPLE_NAMES = sorted(p.name.removesuffix(".example.json") for p in EXAMPLES.glob("*.example.json"))


def test_every_schema_has_an_example() -> None:
    schema_names = {p.name.removesuffix(".v1.json") for p in SCHEMAS.glob("*.v1.json")} - {"common"}
    assert schema_names == set(EXAMPLE_NAMES)


@pytest.mark.parametrize("name", EXAMPLE_NAMES)
def test_example_validates(name: str) -> None:
    errors = list(validator(name).iter_errors(example(name)))
    assert not errors, [f"{list(e.absolute_path)}: {e.message}" for e in errors]


def _set(path: list, value: object) -> Callable[[dict], None]:
    def apply(doc: dict) -> None:
        target = doc
        for key in path[:-1]:
            target = target[key]
        target[path[-1]] = value
    return apply


def _drop(key: str) -> Callable[[dict], None]:
    return lambda doc: doc.pop(key)


def _all_errors(errors: list[ValidationError]) -> Iterator[ValidationError]:
    for err in errors:
        yield err
        yield from _all_errors(list(err.context))


# (schema, mutation, instance path the rejection must point at, why)
BAD_CASES = [
    ("knowledge_attribution", _drop("used"), [], "E7 keeps used apart from retrieved and applicable"),
    ("knowledge_attribution", _set(["influence", "evals", 1, "verdict"], "maybe"), ["influence", "evals", 1, "verdict"],
     "an E7 verdict is pass, warn, fail or unknown"),
    ("knowledge_attribution", _set(["influence", "counterfactual", "status"], "guessed"), ["influence", "counterfactual", "status"],
     "the counterfactual has a closed set of statuses"),
    ("knowledge_attribution", _set(["influence", "counterfactual", "status"], "not_run"), ["influence", "candidates", 0, "change"],
     "without a compared counterfactual there is no changed column"),
    ("knowledge_attribution", _set(["influence", "candidates", 0, "change"], None), ["influence", "candidates", 0, "change"],
     "a compared counterfactual gives every candidate its changed column"),
    ("strategy_set", _drop("no_acceptable_candidate"), [], "a strategy set says whether any candidate is acceptable"),
    ("strategy_set", _set(["no_acceptable_candidate"], "yes"), ["no_acceptable_candidate"], "no_acceptable_candidate is a boolean"),
    ("eval_bundle", _set(["candidate_policy"], {"transition_status": "CANDIDATE", "status": "restricted", "reasons": ["vibes"],
                                                "requires_human_review": True}), ["candidate_policy", "reasons", 0],
     "a policy reason comes from the closed set"),
    ("claim", _drop("evidence_quote"), [], "AI claim needs verbatim evidence (§18)"),
    ("claim", _set(["standing"], "vibes"), ["standing"], "unknown standing"),
    ("claim", _set(["standing"], "first_party_record"), ["extractor"], "record claim must come from a rule (ADR-0009)"),
    ("account_state", _set(["fields", "economic_buyer", "value"], "Jane Doe"),
     ["fields", "economic_buyer", "value"], "unknown field must say 'unknown' (§18)"),
    ("account_state", _set(["fields", "stage", "evidence_refs"], []),
     ["fields", "stage", "evidence_refs"], "known field must point to evidence (§7)"),
    ("account_state", _set(["fields", "stage", "value"], "unknown"),
     ["fields", "stage", "value"], "known field cannot be 'unknown'"),
    # OpportunityState (ADR-0016)
    ("opportunity_state", _drop("opportunity_id"), [], "an opportunity state is keyed by a deal"),
    ("opportunity_state", _set(["opportunity_id"], None), ["opportunity_id"], "the deal id cannot be null"),
    ("opportunity_state", _set(["fields", "stage", "evidence_refs"], []), ["fields", "stage", "evidence_refs"],
     "known field must point to evidence (§7)"),
    ("opportunity_state", _set(["fields", "amount", "value"], "unknown"), ["fields", "amount", "value"],
     "known amount cannot be 'unknown'"),
    ("opportunity_state", _set(["opportunities"], []), [], "no account-level list on a deal"),
    ("account_state", lambda d: d["opportunities"][0].pop("is_primary"), ["opportunities", 0],
     "a summary says whether it is the primary deal"),
    # role provenance on buying-group members (ADR-0016)
    ("account_state", _set(["buying_group", 0, "role_source"], "guessed"), ["buying_group", 0, "role_source"],
     "role_source is recorded or inferred"),
    ("account_state", lambda d: d["buying_group"][0].update(role_source=None, role_basis=None),
     ["buying_group", 0, "role_source"], "a member with a role cannot have a null role_source"),
    ("account_state", _set(["buying_group", 0, "roles"], ["unknown"]), ["buying_group", 0, "role_source"],
     "a member whose only role is unknown has no role_source"),
    ("account_state", _set(["buying_group", 0, "role_basis"], "x" * 201), ["buying_group", 0, "role_basis"],
     "role_basis is short"),
    ("opportunity_state", _set(["buying_group", 0, "role_source"], "guessed"), ["buying_group", 0, "role_source"],
     "a deal's members are held to the same role provenance rule"),
    ("trigger_evaluation", _set(["eligible"], False), ["agent_run_id"], "ineligible evaluation cannot carry a run (§10)"),
    ("trigger_evaluation", _set(["reason_codes"], ["no_material_change"]), ["reason_codes", 0],
     "eligible evaluation needs eligible reasons"),
    ("trigger_evaluation", _set(["reason_codes"], []), ["reason_codes"], "evaluation needs a reason"),
    ("human_decision", _set(["edited_artifact"], None), ["edited_artifact"], "edit needs edited artifact"),
    ("activity", _set(["activity_type"], "Telepathy"), ["activity_type"], "non-canonical activity type (§2)"),
    ("activity", _set(["idempotency_key"], "abc"), ["idempotency_key"], "idempotency key must be sha256 hex"),
    ("source_event", _drop("source_event_key"), [], "event key required"),
    # WP32 (HAR-131): origin / provenance markers
    ("source_event", _set(["origin"], "synthetic"), [], "a synthetic event names its layer version"),
    ("source_event", _set(["origin"], "dataset"), [], "a dataset replay names its dataset"),
    ("source_event", _set(["origin"], "imagined"), ["origin"], "origin vocabulary"),
    ("source_event", _set(["provenance"], "synthetic:v1"), [], "provenance needs an origin"),
    ("source_event", lambda d: d.update(origin="live", provenance="crmarena-pro:b2b"), [],
     "a live event has no dataset provenance"),
    ("source_event", lambda d: d.update(origin="dataset", provenance="synthetic:v1"), ["origin"],
     "a synthetic provenance cannot be relabelled as a dataset"),
    ("source_event", lambda d: d.update(origin="synthetic", provenance="crmarena-pro:b2b"), ["provenance"],
     "a synthetic event carries synthetic:v<n>"),
    ("source_event", lambda d: d.update(origin="synthetic", provenance="synthetic:latest"), ["provenance"],
     "synthetic layers are versioned"),
    ("agent_run", _set(["trigger_activity_ids"], []), ["trigger_activity_ids"], "run traceable to activities (§11)"),
    ("agent_run", _set(["status"], "executed"), ["status"], "dry run cannot execute (§18)"),
    ("agent_run", _set(["steps", 2, "external_effect_id"], "gmail:123"), ["steps", 2, "external_effect_id"],
     "dry run cannot record an external effect (§18)"),
    ("claim_candidate", _set(["evidence_quote"], ""), ["evidence_quote"], "candidate quote cannot be empty"),
    ("agent_run_output", _set(["proposed_action_type"], "spam_everyone"), ["proposed_action_type"], "unknown action"),
    ("agent_run_output", _set(["recipients"], []), ["recipients"], "email needs a recipient"),
    ("agent_run_output", _set(["evidence_refs"], []), ["evidence_refs"], "action must cite evidence (§11)"),
    ("account_state", _set(["fields", "stage", "winning_claim_id"], None), ["fields", "stage", "winning_claim_id"],
     "claim-won field needs its winning claim"),
    ("account_state", _set(["fields", "last_customer_interaction", "evidence_refs"], []),
     ["fields", "last_customer_interaction", "evidence_refs"], "derived field still needs evidence (§7)"),
    ("account_state", lambda d: d["fields"]["stage"].update(derived=True, winning_claim_id=None),
     ["fields", "stage", "winning_claim_id"],
     "only computed fields may be derived (claim-won fields keep their winner)"),
    ("signal", _set(["signal_type"], "field_contradicted"), ["details"], "a contradiction signal must name both claims (ADR-0008)"),
    ("signal", _set(["signal_type"], "new_stakeholder_entered"), ["expires_at"],
     "an EVENT signal carries the end of its window (ADR-0015)"),
    ("signal", _set(["expires_at"], "2026-10-13T15:42:00Z"), ["expires_at"],
     "a STANDING signal has no window; the state decides when it closes (ADR-0015)"),
    # HAR-97 contracts (WP15)
    ("eval_result", _set(["verdict"], "warn"), ["verdict"], "only failures can block (HAR-97 §7)"),
    ("eval_result", _set(["eval_version"], "latest"), ["eval_version"], "evaluators are versioned (HAR-97 §21)"),
    ("eval_result", _set(["eval_type"], "vibes"), ["eval_type"], "unknown evaluator"),
    ("agent_run_draft", _set(["revision_feedback"], []), ["revision_feedback"], "revision draft must cite eval feedback"),
    ("agent_run_draft", _set(["draft_index"], 1), ["draft_index"], "draft 1 comes from the account agent"),
    ("human_delta", _set(["candidate_criterion"], None), ["candidate_criterion"],
     "unexplained human correction must propose a candidate criterion (HAR-97 §9)"),
    ("decision_episode", _set(["human_action"], "THUMBS_UP"), ["human_action"], "human action vocabulary (HAR-97 §8)"),
    ("knowledge", _set(["situation_signature"], []), ["situation_signature"], "knowledge must be scoped (HAR-97 §11)"),
    ("knowledge", _set(["status"], "gospel"), ["status"], "knowledge lifecycle states"),
    ("decision_guidance", _set(["recommended_action"], "spam_everyone"), ["recommended_action"], "guidance uses action vocabulary"),
    ("evaluator_version", _set(["status"], "live"), ["status"], "evaluator lifecycle states"),
    ("customer_reaction", _set(["polarity"], "ecstatic"), ["polarity"], "reaction polarity"),
    ("eval_catalog", _set(["eval_types", "champion_continuity", "evidence_class"], "vibes"),
     ["eval_types", "champion_continuity", "evidence_class"], "one known evidence class per eval type (HAR-97 §15)"),
    ("eval_catalog", lambda d: d["eval_types"].update(vibes=d["eval_types"]["champion_continuity"]), ["eval_types"],
     "catalog keys are eval types"),
    ("eval_catalog", _set(["eval_types", "champion_continuity", "evidence_class"], "product_rule"),
     ["eval_types", "champion_continuity", "evidence_class"],
     "semantic evals carry a GTM evidence class (HAR-114)"),
    ("eval_catalog", lambda d: d["eval_types"]["knowledge_applicability"].pop("blocking_rule"),
     ["eval_types", "knowledge_applicability"], "a never-blocking eval states its rule"),
    ("eval_catalog", lambda d: d["eval_types"]["champion_continuity"].pop("blocking_rule"),
     ["eval_types", "champion_continuity"], "a semantic eval that can block states when (HAR-116 review)"),
    ("eval_catalog", _set(["eval_types", "champion_continuity", "har97_sections"], ["section 5.5"]),
     ["eval_types", "champion_continuity", "har97_sections", 0], "HAR-97 section format"),
    ("eval_catalog", _set(["eval_types", "champion_continuity", "labels"], ["CHAMPION_INCLUDED", "CHAMPION_INCLUDED"]),
     ["eval_types", "champion_continuity", "labels"], "labels are unique"),
    ("eval_catalog", _set(["eval_types", "champion_continuity", "labels"], ["champion_included"]),
     ["eval_types", "champion_continuity", "labels", 0], "labels are upper snake case"),
    ("eval_catalog", lambda d: d["out_of_scope"]["trigger_eval"].pop("reason"), ["out_of_scope", "trigger_eval"],
     "a scoped-out eval says why"),
    ("eval_catalog", lambda d: d["out_of_scope"].update({"Trigger Eval": d["out_of_scope"]["trigger_eval"]}), ["out_of_scope"],
     "out_of_scope keys are snake case"),
    ("eval_catalog", _set(["out_of_scope", "trigger_eval", "contract"], "trigger_evaluation.json"),
     ["out_of_scope", "trigger_eval", "contract"], "scoped-out contract names a v1 schema"),
    ("eval_catalog", lambda d: d["case_types"].update({"Too-Soon": "x"}), ["case_types"], "case types are snake case"),
    ("eval_catalog", _set(["case_types", "champion_bypass"], "x" * 301), ["case_types", "champion_bypass"],
     "case type description length"),
    ("eval_catalog", _set(["description"], "x" * 2001), ["description"], "catalog description length"),
    ("eval_catalog", _set(["eval_types", "champion_continuity", "blocking_rule"], "Never blocks: really."),
     ["eval_types", "champion_continuity", "blocking_rule"], "a blocking eval cannot say it never blocks"),
    ("decision_guidance", _set(["supporting_knowledge", 0, "exceptions_checked", 0, "triggered"], True),
     ["supporting_knowledge", 0, "applies"], "knowledge cannot apply while an exception is triggered (ADR-0011)"),
    ("account_state", _set(["fields", "champion_status", "value"], "wavering"), ["fields", "champion_status", "value"],
     "champion_status vocabulary"),
    ("account_state", _set(["fields", "relationship_risk", "value"], "evaluating a competitor"),
     ["fields", "relationship_risk", "value"], "relationship_risk is a level"),
    # WP17 deterministic eval input (HAR-115, ADR-0014)
    ("deterministic_eval_input", _set(["people", 1, "internal_only"], True), ["people", 1, "kind"],
     "only employees can be internal-only"),
    ("deterministic_eval_input", lambda d: d["people"][0].update(account_id=d["account_id"]), ["people", 0, "account_id"],
     "an employee belongs to no customer account"),
    ("deterministic_eval_input", _set(["execute_mode"], "yolo"), ["execute_mode"],
     "execute mode vocabulary (WP17)"),
    ("deterministic_eval_input", _set(["policy", "autonomy_level"], "yolo"), ["policy", "autonomy_level"],
     "autonomy ladder vocabulary"),
    ("deterministic_eval_input", _set(["policy", "allowed_tools"], ["teleport"]), ["policy", "allowed_tools", 0],
     "tool vocabulary"),
    ("deterministic_eval_input", _set(["assets", 0, "share_condition"], None), ["assets", 0, "share_condition"],
     "a confidential asset states its sharing condition"),
    ("deterministic_eval_input", _set(["prior_actions", 0, "action"], "schedule_meeting"),
     ["prior_actions", 0, "meeting_start"], "a prior meeting records its start"),
    ("deterministic_eval_input", _set(["crm", "stages"], []), ["crm", "stages"], "CRM stages are listed"),
]


@pytest.mark.parametrize(("name", "mutate", "path", "why"), BAD_CASES, ids=[c[3] for c in BAD_CASES])
def test_known_bad_instance_rejected_for_the_right_reason(
        name: str, mutate: Callable[[dict], None], path: list, why: str) -> None:
    doc = copy.deepcopy(example(name))
    mutate(doc)
    errors = list(validator(name).iter_errors(doc))
    assert errors, f"expected rejection: {why}"
    paths = [list(e.absolute_path) for e in _all_errors(errors)]
    assert path in paths, f"{why}: rejected at {paths}, expected {path}"


@pytest.mark.parametrize("path", [CONTRACTS / "evals" / "eval_catalog.json"], ids=lambda p: p.name)
def test_eval_catalog_instance_validates(path: Path) -> None:
    errors = list(validator("eval_catalog").iter_errors(load_json(path)))
    assert not errors, [f"{list(e.absolute_path)}: {e.message}" for e in errors]


def test_valid_conflict_is_accepted() -> None:
    """ADR-0008: a field may carry the newer lower-standing claims that contradict its winner."""
    doc = copy.deepcopy(example("account_state"))
    field = doc["fields"]["next_milestone"]
    field["conflicts"] = [{"claim_id": field["winning_claim_id"], "standing": "first_party_ai",
                           "reason": "Newer customer email says no review until the documents are read."}]
    assert not list(validator("account_state").iter_errors(doc))


def test_signal_kinds_partition_the_signal_vocabulary() -> None:
    """ADR-0011: every signal type is STANDING (open while its condition holds) or EVENT (open for a fixed window)."""
    schema = load_json(SCHEMAS / "signal.v1.json")
    defs, types = schema["$defs"], set(schema["properties"]["signal_type"]["enum"])
    event, standing = set(defs["eventSignalType"]["enum"]), set(defs["standingSignalType"]["enum"])
    assert event | standing == types and not event & standing
    assert set(defs["standingOpenWhile"]["properties"]) == standing
    assert defs["eventWindowDays"]["const"] == 14


@pytest.mark.parametrize(("origin", "provenance"), [
    ("synthetic", "synthetic:v1"), ("dataset", "crmarena-pro:b2b"), ("live", None), (None, None)])
def test_source_event_origin_markers_accepted(origin: str | None, provenance: str | None) -> None:
    """WP32 (HAR-131): synthetic and dataset events declare where they come from; live ones may."""
    doc = copy.deepcopy(example("source_event"))
    doc.update({k: v for k, v in (("origin", origin), ("provenance", provenance)) if v is not None})
    assert not list(validator("source_event").iter_errors(doc))
