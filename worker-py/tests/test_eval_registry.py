"""HAR-140 (WP36): contracts/evals/eval_registry.json is valid against its schema and mirrors HAR-97's canonical eval
strategy (E1-E22 and the operational M1-M5, section 12 fields)."""
from __future__ import annotations

import copy
from collections import Counter

import pytest

from test_contracts import CONTRACTS, example, load_json, validator

REGISTRY = load_json(CONTRACTS / "evals" / "eval_registry.json")
EVALS: list[dict] = REGISTRY["evals"]
BY_ID = {e["id"]: e for e in EVALS}
FAMILIES = {f["id"]: f for f in REGISTRY["families"]}

# HAR-97 canonical sections 4-8: the number of sub-evals listed under each family (E21 lists the regression set, the
# capability set and the required slices). Beyond the spec's own lists, 6 planned evals close gaps the matrix found
# (E5.9, E18.12, E20.9-E20.11, E22.10), each citing the HAR-97 section it extends. E11 has 9: its tenth, correction
# propagation latency (E11.9), is a latency metric and lives under M4. M1-M5 are metrics (`metrics`), one each.
SUB_EVALS = {"E1": 11, "E2": 6, "E3": 9, "E4": 9, "E5": 9, "E6": 8, "E7": 5, "E8": 13, "E9": 8, "E10": 8, "E11": 9,
             "E12": 10, "E13": 14, "E14": 9, "E15": 13, "E16": 8, "E17": 10, "E18": 12, "E19": 12, "E20": 11, "E21": 3,
             "E22": 10}
METRIC_FAMILIES = {"M1", "M2", "M3", "M4", "M5"}


def test_registry_and_example_validate() -> None:
    for doc in (REGISTRY, example("eval_registry")):
        errors = list(validator("eval_registry").iter_errors(doc))
        assert not errors, [f"{list(e.absolute_path)}: {e.message}" for e in errors]


@pytest.mark.parametrize(("why", "mutate"), [
    ("a planned eval has no grader yet", lambda e: e.update(status="planned", grader="model", mode=None, implemented_by=[])),
    ("an existing eval names its grader", lambda e: e.update(grader=None)),
    ("a blocking eval protects a named invariant", lambda e: e.update(mode="blocking", invariant=None)),
    ("validation evals never mutate knowledge", lambda e: e.update(**{"class": "validation", "knowledge_mutation_allowed": True})),
    ("operational evals live on surface 20", lambda e: e.update(**{"class": "operational", "knowledge_mutation_allowed": False})),
    ("unknown dimension", lambda e: e.update(dimensions=["VIBES"])),
    ("surface out of range", lambda e: e.update(surfaces=[21])),
    ("owner is a Linear issue", lambda e: e.update(owner="WP36")),
    ("id is E<n>[.<m>] or M<n>", lambda e: e.update(id="X1.1")),
    ("a specified contract carries all nine section 12 definitions", lambda e: e.update(contract_status="specified")),
    ("an implemented eval says where its definitions live", lambda e: e.pop("definitions_ref")),
    ("an implemented eval names its gold", lambda e: e["definitions_ref"].update(gold=[])),
])
def test_schema_rejects_inconsistent_evals(why: str, mutate) -> None:
    doc = copy.deepcopy(REGISTRY)
    target = next(e for e in doc["evals"] if e["status"] == "implemented" and e["mode"] == "blocking")
    mutate(target)
    assert list(validator("eval_registry").iter_errors(doc)), why


def test_ids_are_unique_and_follow_their_family() -> None:
    assert not [i for i, n in Counter(e["id"] for e in EVALS).items() if n > 1]
    assert set(FAMILIES) == set(SUB_EVALS) | METRIC_FAMILIES and len(FAMILIES) == 27
    for e in EVALS:
        assert e["family"] in FAMILIES and e["id"].split(".")[0] == e["family"], e["id"]
        assert e["class"] == FAMILIES[e["family"]]["class"] and e["owner"], e["id"]


def test_every_canonical_sub_eval_is_registered() -> None:
    assert dict(Counter(e["family"] for e in EVALS)) == SUB_EVALS
    assert len(EVALS) == 207
    assert Counter(e["status"] for e in EVALS) == {"planned": 124, "partial": 42, "implemented": 39, "hidden": 2}
    assert dict(Counter(m["family"] for m in REGISTRY["metrics"])) == {m: 1 for m in sorted(METRIC_FAMILIES)}


def test_operational_diagnostics_are_a_separate_class_that_never_mutates_knowledge() -> None:
    ops = REGISTRY["metrics"]
    assert [e["id"] for e in ops] == ["M1", "M2", "M3", "M4", "M5"]
    assert all(e["surfaces"] == [20] and not e["knowledge_mutation_allowed"] for e in ops)
    assert not [e["id"] for e in EVALS if e["class"] == "operational"]
    assert not [e["id"] for e in EVALS if e["class"] != "operational" and e["surfaces"] == [20]]


def test_knowledge_mutation_is_allowed_only_for_behavior_changing_and_learning_evals() -> None:
    for e in EVALS:
        assert e["knowledge_mutation_allowed"] == (e["class"] == "behavioral"), e["id"]


def test_stochastic_evals_require_repeated_trials_and_blocking_evals_name_an_invariant() -> None:
    for e in EVALS:
        assert e["repeated_trials_required"] == ("STOCHASTIC_RELIABILITY" in e["dimensions"]), e["id"]
        if e["mode"] == "blocking":
            assert e["invariant"], e["id"]


def test_every_model_grader_has_the_human_agreement_eval_to_calibrate_against() -> None:
    assert any(e["grader"] in ("model", "hybrid") for e in EVALS)
    assert BY_ID["E19.1"]["status"] in ("implemented", "partial") and "single-author" in BY_ID["E19.1"]["implemented_by"][0]


def test_implemented_evals_point_at_repo_artifacts_that_exist() -> None:
    root = CONTRACTS.parent
    for e in EVALS:
        if e["status"] in ("planned", "hidden"):
            continue
        assert e["implemented_by"], e["id"]
        for ref in e["implemented_by"]:
            for path in (p for p in ref.split() if "/" in p and "*" not in p and not p.startswith("(")):
                path = path.strip(";,()")
                if path.startswith(("core-go/", "worker-py/", "bench/", "fixtures/", "contracts/")):
                    base = path.split(" ")[0]
                    assert (root / base).exists() or (root / base.rsplit("/", 1)[0]).exists(), (e["id"], path)


def test_the_unspecified_contract_fields_are_pending_not_invented() -> None:
    assert {e["contract_status"] for e in EVALS} == {"pending"}
    assert not [e for e in EVALS if "pass_definition" in e or "failure_modes" in e]


SPECIFIED_FIELDS = ("purpose", "inputs", "required_trace_fields", "pass_definition", "warn_definition", "fail_definition",
                    "unknown_definition", "failure_modes", "gold_dataset", "affected_behavior")


def test_the_section_12_definition_fields_are_optional_slots_required_once_specified() -> None:
    doc = copy.deepcopy(REGISTRY)
    target = next(e for e in doc["evals"] if e["status"] == "planned")
    target.update(contract_status="specified", purpose="p", inputs=["Activity"], required_trace_fields=["evidence"],
                  pass_definition="x", warn_definition="x", fail_definition="x", unknown_definition="x",
                  failure_modes=["m"], gold_dataset="fixtures/evals", affected_behavior=["blocks_or_revises_action"])
    assert not list(validator("eval_registry").iter_errors(doc))
    for field in SPECIFIED_FIELDS:
        broken = copy.deepcopy(doc)
        next(e for e in broken["evals"] if e["id"] == target["id"]).pop(field)
        assert list(validator("eval_registry").iter_errors(broken)), field


def test_every_implemented_eval_points_at_its_catalog_types_and_gold() -> None:
    catalog = load_json(CONTRACTS / "evals" / "eval_catalog.json")["eval_types"]
    implemented = [e for e in EVALS if e["status"] == "implemented"]
    assert len(implemented) == 39
    for e in implemented:
        ref = e["definitions_ref"]
        assert set(ref["catalog_types"]) <= set(catalog), e["id"]
        assert ref["gold"] and all((CONTRACTS.parent / g).exists() for g in ref["gold"]), e["id"]
    assert BY_ID["E12.4"]["definitions_ref"]["catalog_types"] == ["recipient_correctness"]
    assert BY_ID["E12.2"]["definitions_ref"]["catalog_types"] == ["grounding"]
    assert "definitions_ref" not in BY_ID["E8.2"], "the strategy's assumptions are no longer judged by the artifact rubric"
    assert not [e["id"] for e in EVALS if e["status"] != "implemented" and "definitions_ref" in e]


def test_gap_evals_added_beyond_the_spec_lists_are_planned_and_cite_their_section() -> None:
    for i in ("E5.9", "E18.12", "E20.9", "E20.10", "E20.11", "E22.10"):
        assert BY_ID[i]["status"] == "planned" and BY_ID[i]["section"] and BY_ID[i]["owner"], i
