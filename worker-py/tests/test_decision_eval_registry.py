"""WP-A (contracts-decision-evals): the registry organises every eval into the three canonical buckets and their gates
(B1-B9, D1-D10, S1-S5; metrics are S6) with the question each asks and what it improves, and maps every decision and
feedback-loop eval (E8-E17) to the object it judges, its trace span and its demo moment; metrics are not evals; the areas
file, the trace, the EvalResult and the new contracts agree with it."""
from __future__ import annotations

import copy
import re

import pytest
from decision_eval_mapping import (ALSO_GATES, ALSO_SPANS, BUCKETS, DEMO_MOMENTS, EXPECTED, FAMILY_GATE, FLOW, GATES, HIDDEN,
                                   NOT_BUILT_GATES, REBUILD, gate_of)
from test_contracts import CONTRACTS, example, load_json, validator

REGISTRY = load_json(CONTRACTS / "evals" / "eval_registry.json")
AREAS = load_json(CONTRACTS / "evals" / "eval_areas.json")
EVALS: list[dict] = REGISTRY["evals"]
BY_ID = {e["id"]: e for e in EVALS}
FAMILIES = {f["id"]: f for f in REGISTRY["families"]}
GATE_TABLE = {g["id"]: g for g in REGISTRY["gates"]}
LOOP_FAMILIES = [f"E{n}" for n in range(8, 18)]
AREA_OF_FAMILY = {fam: a["id"] for a in AREAS["areas"] for fam in a["families"]}
SPAN_KINDS = set(load_json(CONTRACTS / "schemas" / "episode_trace.v1.json")["$defs"]["spanKind"]["enum"])


def test_registry_validates_and_metrics_are_not_evals() -> None:
    errors = list(validator("eval_registry").iter_errors(REGISTRY))
    assert not errors, [f"{list(e.absolute_path)}: {e.message}" for e in errors]
    assert not [e["id"] for e in EVALS if e["id"].startswith("M")], "M1-M5 are metrics, never evals"
    assert [m["id"] for m in REGISTRY["metrics"]] == ["M1", "M2", "M3", "M4", "M5"]
    assert all(m["class"] == "operational" and not m["knowledge_mutation_allowed"] for m in REGISTRY["metrics"])
    assert not [e for e in EVALS if e["class"] == "operational"]


def test_the_three_buckets_and_their_gates_are_canonical() -> None:
    assert [b["id"] for b in REGISTRY["buckets"]] == list(BUCKETS)
    assert {g["id"]: g["bucket"] for g in REGISTRY["gates"]} == GATES
    assert [g["id"] for g in REGISTRY["gates"]] == list(GATES), "gates are listed in bucket and flow order"
    for g in REGISTRY["gates"]:
        assert g["question"].endswith("?") and g["improves"].endswith("."), g["id"]
        assert "Ghost" not in g["question"] + g["improves"] + g["name"], g["id"]
    flow = [g["id"] for g in REGISTRY["gates"] if g["bucket"] == "decision_action"]
    assert flow == list(FLOW) and [GATE_TABLE[i]["order"] for i in FLOW] == list(range(1, 11))


def test_d6_and_d10_are_gates_with_no_eval_yet() -> None:
    used = {e["gate"] for e in EVALS}
    assert {"D6", "D10"} <= set(GATE_TABLE) and not used & set(NOT_BUILT_GATES)
    assert set(GATE_TABLE) - used - {"S6"} == set(NOT_BUILT_GATES), "every other gate holds at least one eval"


def test_every_eval_carries_its_bucket_gate_question_and_improvement() -> None:
    for e in EVALS:
        assert e["gate"] == gate_of(e["id"]) and e["bucket"] == GATES[e["gate"]], e["id"]
        assert e["legacy_id"] == e["id"], e["id"]
        assert e["question"] == GATE_TABLE[e["gate"]]["question"] and e["improves"] == GATE_TABLE[e["gate"]]["improves"], e["id"]
        assert [g in GATES for g in e.get("also_gates", [])] == [True] * len(e.get("also_gates", [])), e["id"]
        assert e.get("also_gates", []) == ALSO_GATES.get(e["id"], []), e["id"]
        assert e["gate"] not in e.get("also_gates", []), e["id"]


def test_the_family_default_holds_unless_an_exception_is_named() -> None:
    for fam, gate in FAMILY_GATE.items():
        evals = [e for e in EVALS if e["family"] == fam]
        assert all(e["gate"] == gate for e in evals) or any(e["gate"] != gate for e in evals), fam
    assert {e["gate"] for e in EVALS if e["family"] == "E18"} == {"S1"}
    assert {e["gate"] for e in EVALS if e["family"] == "E19"} == {"S2"}
    assert {e["gate"] for e in EVALS if e["family"] in ("E15", "E17")} == {"B9"}


def test_metrics_are_the_s6_gate_and_stay_out_of_the_eval_counts() -> None:
    for m in REGISTRY["metrics"]:
        assert (m["gate"], m["bucket"], m["legacy_id"]) == ("S6", "system_health", m["id"])
    assert not [e for e in EVALS if e["gate"] == "S6"]


def test_no_new_e_ids_are_invented() -> None:
    assert not [e["id"] for e in EVALS if int(re.match(r"E(\d+)", e["id"]).group(1)) > 22]
    assert {f["id"] for f in REGISTRY["families"]} <= {f"E{i}" for i in range(1, 23)} | {f"M{i}" for i in range(1, 6)}


def test_the_owner_deviations_from_the_canonical_text_are_recorded_not_hidden() -> None:
    deviations = {d["id"]: d for d in REGISTRY["known_deviations"]}
    assert set(deviations) == {"gold_model_reference", "single_trial"}
    assert "openai/gpt-5.6-sol-pro" in deviations["gold_model_reference"]["decision"] and "not human" in deviations["gold_model_reference"]["decision"]
    assert deviations["gold_model_reference"]["applies_to"] == ["S2"] and deviations["single_trial"]["applies_to"] == ["S3"]
    assert REGISTRY["gold_source"] == {"kind": "model_reference", "model": "openai/gpt-5.6-sol-pro"}


def test_gold_comes_from_a_stronger_model_not_humans() -> None:
    assert "not human" in load_json(CONTRACTS / "schemas" / "eval_registry.v1.json")["properties"]["gold_source"]["description"]
    broken = copy.deepcopy(REGISTRY)
    broken["gold_source"] = {"kind": "human_label", "model": "x"}
    assert list(validator("eval_registry").iter_errors(broken))
    assert "gold_source" not in " ".join(k for e in EVALS for k in e), "no per-eval human-label fields are invented"


def test_correction_propagation_latency_is_a_metric_not_an_eval() -> None:
    assert "E11.9" not in BY_ID
    m4 = next(m for m in REGISTRY["metrics"] if m["id"] == "M4")
    assert "E11.9" in m4["reclassified_from"] and m4["family"] == "M4"


def test_out_of_scope_evals_are_hidden() -> None:
    for eval_id in HIDDEN:
        assert BY_ID[eval_id]["status"] == "hidden", eval_id
    assert not [e["id"] for e in EVALS if e["status"] == "hidden" and e["id"] not in HIDDEN]


def test_the_e16_invariant_is_the_learning_continuation_proof() -> None:
    for text in (FAMILIES["E16"]["invariant"], *(e["invariant"] for e in EVALS if e["family"] == "E16" and e["invariant"])):
        assert "B over A" not in text and "C equals A" not in text and "arm" not in text.lower(), text
        assert "later" in text and "retriev" in text, text


def test_wrong_implementation_pointers_say_none_rebuild() -> None:
    for eval_id in REBUILD:
        e = BY_ID[eval_id]
        assert e["implemented_by"] == ["none (rebuild)"] and e["status"] == "planned", eval_id
        assert e["grader"] is None and e["mode"] is None and "definitions_ref" not in e, eval_id


def test_every_loop_eval_carries_its_audited_mapping() -> None:
    loop = [e for e in EVALS if e["family"] in LOOP_FAMILIES and e["status"] != "hidden"]
    assert {e["id"] for e in loop} == set(EXPECTED)
    for e in loop:
        gate, span, (obj_type, id_field), moments = EXPECTED[e["id"]]
        assert e["job"] == "decision_loop" and e["area"] == "decision_learning", e["id"]
        assert e["gate"] == gate, e["id"]
        assert e["span_kind"] == span and e["span_kind"] in SPAN_KINDS, e["id"]
        assert e["judged_object"] == {"type": obj_type, "id_field": id_field}, e["id"]
        assert e["demo_moment"] == list(moments) and set(moments) <= set(DEMO_MOMENTS), e["id"]
        assert e.get("also_span_kinds", []) == ALSO_SPANS.get(e["id"], []), e["id"]


def test_retrieval_families_are_intelligence_by_job_and_decision_learning_by_area() -> None:
    for fam in ("E5", "E6", "E7"):
        assert FAMILIES[fam]["job"] == "intelligence", fam
        for e in (x for x in EVALS if x["family"] == fam):
            assert (e["job"], e["area"]) == ("intelligence", "decision_learning"), e["id"]


def test_entry_area_and_job_follow_the_areas_file_and_the_family() -> None:
    for e in EVALS:
        assert e["area"] == AREA_OF_FAMILY.get(e["family"], "system"), e["id"]
        assert e["job"] == FAMILIES[e["family"]]["job"], e["id"]
        assert "stage" not in e, "the five-stage tabs are replaced by the bucket's own D1-D10 flow"


@pytest.mark.parametrize("field", ["job", "area", "bucket", "gate", "question", "improves", "legacy_id", "judged_object", "span_kind", "demo_moment"])
def test_a_loop_eval_without_its_mapping_is_rejected(field: str) -> None:
    doc = copy.deepcopy(REGISTRY)
    next(e for e in doc["evals"] if e["id"] == "E9.1").pop(field)
    assert list(validator("eval_registry").iter_errors(doc)), field


@pytest.mark.parametrize("field", ["bucket", "gate", "question", "improves", "legacy_id"])
def test_any_eval_without_its_gate_fields_is_rejected(field: str) -> None:
    doc = copy.deepcopy(REGISTRY)
    next(e for e in doc["evals"] if e["id"] == "E1.1").pop(field)
    assert list(validator("eval_registry").iter_errors(doc)), field


@pytest.mark.parametrize(("field", "value"), [("gate", "D11"), ("gate", "E8"), ("bucket", "growth"), ("span_kind", "vibes"),
                                              ("area", "growth"), ("demo_moment", ["M9"]), ("judged_object", {"type": "x"})])
def test_schema_rejects_an_invalid_mapping_value(field: str, value: object) -> None:
    doc = copy.deepcopy(REGISTRY)
    next(e for e in doc["evals"] if e["id"] == "E9.1")[field] = value
    assert list(validator("eval_registry").iter_errors(doc))


def test_areas_file_puts_the_feedback_families_in_decision_learning() -> None:
    assert AREA_OF_FAMILY["E10"] == AREA_OF_FAMILY["E11"] == AREA_OF_FAMILY["E12"] == "decision_learning"
    assert next(a for a in AREAS["areas"] if a["id"] == "cliff_experience")["families"] == []
    assert "stages" not in AREAS
    assert not list(validator("eval_areas").iter_errors(AREAS))


def test_family_spans_attach_the_right_trace_spans() -> None:
    spans = AREAS["family_spans"]
    assert (spans["E13"], spans["E14"]) == ("tool_call", "execution")
    assert spans["E16"] == "knowledge_used"
    assert spans["E12"] == "candidates" and set(spans.values()) <= SPAN_KINDS


def test_grounding_and_timing_are_split_so_no_judge_serves_two_evals() -> None:
    types = AREAS["eval_types"]
    assert types["decision_grounding"] == "E8" and types["artifact_grounding"] == "E12"
    assert types["decision_timing"] == "E8" and types["artifact_timing"] == "E12"
    catalog = load_json(CONTRACTS / "evals" / "eval_catalog.json")["eval_types"]
    assert {"decision_grounding", "artifact_grounding", "decision_timing", "artifact_timing"} <= set(catalog)
    serving: dict[str, set[str]] = {}
    for e in EVALS:
        for t in e.get("definitions_ref", {}).get("catalog_types", []):
            if t in ("grounding", "timing_cadence"):
                serving.setdefault(t, set()).add(e["id"])
    assert all(len(ids) == 1 for ids in serving.values()), serving


def test_eval_result_requires_the_judged_object_the_span_and_allows_unknown() -> None:
    schema = load_json(CONTRACTS / "schemas" / "eval_result.v1.json")
    assert {"judged_object", "span_id"} <= set(schema["required"])
    assert "unknown" in schema["properties"]["verdict"]["enum"]
    assert "R1" in schema["description"]
    doc = example("eval_result")
    assert doc["judged_object"]["id"] and doc["span_id"]
    for field in ("judged_object", "span_id"):
        broken = copy.deepcopy(doc)
        broken.pop(field)
        assert list(validator("eval_result").iter_errors(broken)), field


def test_a_semantic_pass_must_cite_something() -> None:
    doc = copy.deepcopy(example("eval_result"))
    doc.update(kind="semantic", verdict="pass", blocking=False, model="m", state_refs=[], activity_refs=[], evidence_refs=[], knowledge_refs=[])
    assert list(validator("eval_result").iter_errors(doc)), "R1: no evidence, no pass"
    doc["verdict"] = "unknown"
    assert not list(validator("eval_result").iter_errors(doc))


def test_trace_gains_the_tool_call_and_execution_span_kinds() -> None:
    assert {"tool_call", "execution"} <= SPAN_KINDS


def test_knowledge_mutation_uses_the_canonical_operations() -> None:
    ops = load_json(CONTRACTS / "schemas" / "knowledge_mutation.v1.json")["properties"]["operation"]["enum"]
    assert ops == ["CREATE", "STRENGTHEN", "WEAKEN", "REFINE", "NARROW", "EXPAND", "ADD_EXCEPTION", "DISPUTE",
                   "MARK_STALE", "NO_CHANGE"]


def test_judgment_inference_carries_class_strength_instructions_and_unknown() -> None:
    schema = load_json(CONTRACTS / "schemas" / "judgment_inference.v1.json")
    delta = schema["properties"]["inferred_semantic_delta"]["properties"]
    assert {"edit_class", "signal_strength", "explicit_instructions", "unknown"} <= set(delta)
    doc = copy.deepcopy(example("judgment_inference"))
    doc["inferred_semantic_delta"].update(unknown=True, edit_class=[], signal_strength="weak", explicit_instructions=[])
    assert not list(validator("judgment_inference").iter_errors(doc))
    doc["inferred_semantic_delta"]["signal_strength"] = "huge"
    assert list(validator("judgment_inference").iter_errors(doc))


@pytest.mark.parametrize("name", ["decision_ranking", "knowledge_use"])
def test_new_contracts_validate_their_examples_and_reject_extras(name: str) -> None:
    doc = example(name)
    assert not list(validator(name).iter_errors(doc))
    assert list(validator(name).iter_errors({**doc, "score": 1}))
    assert list(validator(name).iter_errors({k: v for k, v in doc.items() if k != "id"}))


def test_a_ranking_names_a_reason_for_every_adjacent_pair() -> None:
    doc = copy.deepcopy(example("decision_ranking"))
    assert len(doc["pairwise_reasons"]) == len(doc["order"]) - 1
    doc["preferred_candidate_id"] = "not-a-uuid"
    assert list(validator("decision_ranking").iter_errors(doc))


def test_knowledge_use_keeps_retrieval_citation_and_conformance_apart() -> None:
    doc = copy.deepcopy(example("knowledge_use"))
    assert doc["conformance"] in ("followed", "overridden_with_reason", "contradicted", "ignored", "unknown")
    doc["conformance"] = "influenced"
    assert list(validator("knowledge_use").iter_errors(doc)), "used is never called influenced"


def _schema_properties(type_name: str) -> set[str]:
    if type_name == "TraceSpan":
        return set(load_json(CONTRACTS / "schemas" / "episode_trace.v1.json")["$defs"]["traceSpan"]["properties"])
    snake = "".join(f"_{c.lower()}" if c.isupper() else c for c in type_name).lstrip("_")
    return set(load_json(CONTRACTS / "schemas" / f"{snake}.v1.json")["properties"])


def test_every_judged_object_names_a_real_contract_and_a_field_it_has() -> None:
    pairs = {(e["judged_object"]["type"], e["judged_object"]["id_field"]) for e in EVALS if "judged_object" in e}
    assert pairs
    for type_name, id_field in sorted(pairs):
        assert id_field in _schema_properties(type_name), (type_name, id_field)
