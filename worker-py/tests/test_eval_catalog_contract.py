"""HAR-114 / HAR-97: contracts/evals/eval_catalog.json is complete and agrees with the other eval contracts.

Every evalType has exactly one entry (kind, one evidence class, labels, blocking rule, verified HAR-97 sections); the
HAR-97 evals that produce no EvalResult on a draft are scoped out explicitly; the examples agree with the catalog."""
from __future__ import annotations

from collections import Counter

from test_contracts import CONTRACTS, EXAMPLES, SCHEMAS, load_json

CATALOG = load_json(CONTRACTS / "evals" / "eval_catalog.json")
CATALOG_SCHEMA = load_json(SCHEMAS / "eval_catalog.v1.json")
EVAL_TYPES: dict[str, dict] = CATALOG["eval_types"]
EVAL_RESULT = load_json(SCHEMAS / "eval_result.v1.json")
GTM_CLASSES = set(CATALOG_SCHEMA["$defs"]["gtmEvidenceClass"]["enum"])


def test_catalog_covers_every_eval_type_and_evidence_class() -> None:
    assert list(EVAL_TYPES) == EVAL_RESULT["$defs"]["evalType"]["enum"]
    assert set(CATALOG["evidence_classes"]) == set(EVAL_RESULT["properties"]["evidence_class"]["enum"])
    assert GTM_CLASSES < set(CATALOG["evidence_classes"])
    used = Counter(e["evidence_class"] for e in EVAL_TYPES.values())
    assert set(used) == set(CATALOG["evidence_classes"]), "every evidence class is used by some eval"
    assert used["deal_data"] < used["methodology"], "HAR-97 §15(GTM): not every eval is presented as empirically proven"


def test_semantic_evals_use_gtm_evidence_classes_and_rules_are_product_rules() -> None:
    for name, entry in EVAL_TYPES.items():
        assert (entry["evidence_class"] in GTM_CLASSES) == (entry["kind"] == "semantic"), name


def test_every_semantic_eval_states_its_blocking_rule() -> None:
    """HAR-116 review: blocking comes from the catalog, never from judge code or rubric files."""
    for name, entry in EVAL_TYPES.items():
        if entry["kind"] != "semantic":
            continue
        rule = entry.get("blocking_rule")
        assert rule, f"{name}: semantic evals need a blocking_rule"
        assert entry["can_block"] != rule.startswith("Never blocks"), name


def test_evidence_classes_follow_the_review_decisions() -> None:
    """deal_data = observed customer/deal behaviour only (§15(GTM)); learned-knowledge evals are not 'proven'."""
    cls = {name: entry["evidence_class"] for name, entry in EVAL_TYPES.items()}
    assert cls["next_step_quality"] == cls["grounding"] == "deal_data"
    assert cls["champion_strength"] == cls["champion_continuity"]
    assert cls["knowledge_applicability"] == cls["exception_awareness"] == "methodology"


def test_sections_follow_har97() -> None:
    sections = {name: entry["har97_sections"] for name, entry in EVAL_TYPES.items()}
    assert all(sections[n] == ["§4"] for n, e in EVAL_TYPES.items()
               if e["kind"] == "deterministic" and n != "state_transition_support")
    assert sections["state_transition_support"][0] == "L1", "HAR-97 pitch §9 L1: is the transition supported by evidence?"
    expected = {"knowledge_applicability": "§5.14", "exception_awareness": "§5.15", "trajectory": "§6",
                "champion_continuity": "§5.5", "buyer_readiness": "§15(GTM).1", "stakeholder_selection": "§5.3",
                "human_delta": "§9", "evidence_sufficiency": "§5.16"}
    assert {n: sections[n][0] for n in expected} == expected
    assert "§5.4" in sections["stakeholder_coverage"], "breadth (§5.4) is separate from stakeholder selection (§5.3)"
    assert "L3" in sections["trajectory"] and "L4" in sections["knowledge_applicability"]
    gtm = [n for n, s in sections.items() if any(x.startswith("§15(GTM)") for x in s)]
    assert gtm and all("L2" in sections[n] for n in gtm), "GTM-grounded evals are the L2 decision layer"


def test_breadth_labels_belong_to_coverage_not_selection() -> None:
    breadth = {"UNDER_THREADED", "RIGHT_SIZED", "OVER_THREADED"}
    assert breadth <= set(EVAL_TYPES["stakeholder_coverage"]["labels"])
    assert not breadth & set(EVAL_TYPES["stakeholder_selection"]["labels"])


def test_knowledge_label_precedence_is_signature_first() -> None:
    rule = EVAL_TYPES["knowledge_applicability"]["label_rule"]
    assert rule.startswith("Signature first") and rule.index("DOES_NOT_APPLY") < rule.index("EXCEPTION_TRIGGERED")


def test_har97_evals_outside_eval_result_are_scoped_out() -> None:
    scoped = CATALOG["out_of_scope"]
    assert {"trigger_eval", "decision_ranking", "l1_context_state", "l4_knowledge_learning", "l5_meta_evals"} <= set(scoped)
    assert not set(scoped) & set(EVAL_TYPES)
    for name, entry in scoped.items():  # a named contract must exist; labels only where a contract defines them
        if "contract" in entry:
            assert (SCHEMAS / entry["contract"]).exists(), name
    trigger = load_json(SCHEMAS / scoped["trigger_eval"]["contract"])
    assert {"eligible", "reason_codes"} <= set(trigger["properties"]), "the trigger outcome lives in TriggerEvaluation"
    assert scoped["trigger_eval"]["labels"] == [], "HAR-97 trigger outcomes have no contract vocabulary to copy"


def test_evaluator_version_example_agrees_with_catalog() -> None:
    example = load_json(EXAMPLES / "evaluator_version.example.json")
    entry = EVAL_TYPES[example["evaluator"]]
    assert set(example["labels"]) <= set(entry["labels"]) and example["kind"] == entry["kind"]
    assert {x["expected_label"] for x in example["examples"]} <= set(entry["labels"])


def test_eval_result_example_agrees_with_catalog() -> None:
    example = load_json(EXAMPLES / "eval_result.example.json")
    entry = EVAL_TYPES[example["eval_type"]]
    assert (example["kind"], example["evidence_class"]) == (entry["kind"], entry["evidence_class"])
    assert example["label"] in entry["labels"] and (entry["can_block"] or not example["blocking"])


def test_catalog_example_is_an_excerpt_of_the_catalog() -> None:
    example = load_json(EXAMPLES / "eval_catalog.example.json")
    for part in ("eval_types", "out_of_scope", "case_types"):
        assert all(CATALOG[part][k] == v for k, v in example[part].items()), part


# ---------- HAR-126 / ADR-0012: state transition support (routing: test_transition_routing_contract.py) ----------
def test_state_transition_support_is_a_deterministic_l1_eval() -> None:
    entry = EVAL_TYPES["state_transition_support"]
    assert (entry["kind"], entry["evidence_class"], entry["can_block"]) == ("deterministic", "product_rule", True)
    assert entry["labels"] == ["SUPPORTED", "UNSUPPORTED", "NO_TRANSITION"]
    assert "ADR-0012" in entry["description"]


def test_transition_detection_is_scoped_out_to_the_detector() -> None:
    entry = CATALOG["out_of_scope"]["transition_detection"]
    assert entry["contract"] == "state_transition.v1.json" and "HAR-126" in entry["measured_by"]
