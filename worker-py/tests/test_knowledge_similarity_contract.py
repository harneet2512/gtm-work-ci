"""ADR-0013 amendment 2: closest-match reuse of learned knowledge. The similarity rules, the optional guidance entry
field and the worker model, in step with core-go/internal/knowledge/similarity.go."""
from __future__ import annotations

import copy

import pytest

from ghost_worker.draft.prompt import render_guidance
from ghost_worker.models.draft import DecisionGuidance
from test_contracts import CONTRACTS, SCHEMAS, example, load_json, validator

RULES = CONTRACTS / "knowledge" / "similarity.v1.json"
FEATURES = {"transition", "signals", "topic", "stage", "motion", "relationship_state", "transition_status", "champion_status", "health"}
SIMILARITY = {"status": "candidate", "score": 0.67, "matched": ["stage: negotiation"], "differs": ["motion: lesson expansion, case renewal"]}
K = "0c17c000-0000-4000-8000-0000000000a1"


def errors(name: str, doc: dict) -> list[str]:
    return [f"{list(e.absolute_path)}: {e.message}" for e in validator(name).iter_errors(doc)]


def test_the_similarity_rules_validate_and_match_their_example() -> None:
    rules = load_json(RULES)
    assert errors("knowledge_similarity", rules) == []
    assert rules == example("knowledge_similarity")
    assert {f["id"] for f in rules["features"]} == FEATURES
    assert 0 < rules["threshold"] <= 1 and 1 <= rules["min_comparable_features"] <= len(rules["features"])
    assert 0 < rules["min_comparable_weight"] <= sum(f["weight"] for f in rules["features"])


@pytest.mark.parametrize("mutate", [
    lambda d: d.update(threshold=0), lambda d: d.update(threshold=1.5), lambda d: d.update(min_comparable_features=0), lambda d: d.update(min_comparable_weight=0), lambda d: d.pop("min_comparable_weight"),
    lambda d: d.update(tune=True), lambda d: d["features"][0].update(id="mood"), lambda d: d["features"][0].update(weight=0),
    lambda d: d["features"][0].pop("rationale"), lambda d: d.update(applies_to_created_from=["authored"]),
])
def test_malformed_similarity_rules_are_rejected(mutate) -> None:
    doc = copy.deepcopy(load_json(RULES))
    mutate(doc)
    assert errors("knowledge_similarity", doc)


def guidance_with(similarity: dict | None) -> dict:
    doc = copy.deepcopy(example("decision_guidance"))
    doc["supporting_knowledge"][0]["knowledge_id"] = K
    if similarity is not None:
        doc["supporting_knowledge"][0]["similarity"] = similarity
    return doc


def test_a_guidance_entry_may_carry_the_lessons_status_and_similarity() -> None:
    assert errors("decision_guidance", guidance_with(SIMILARITY)) == []
    for bad in ({**SIMILARITY, "score": 1.2}, {**SIMILARITY, "status": "gospel"}, {**SIMILARITY, "extra": 1}, {"score": 0.5}):
        assert errors("decision_guidance", guidance_with(bad)), bad


def test_the_worker_reads_the_similarity_and_renders_unchanged_without_it() -> None:
    parsed = DecisionGuidance.model_validate(guidance_with(SIMILARITY))
    assert parsed.supporting_knowledge[0].similarity.score == 0.67
    assert '"similarity"' in render_guidance(parsed)
    plain = DecisionGuidance.model_validate(guidance_with(None))
    assert plain.supporting_knowledge[0].similarity is None
    assert "similarity" not in render_guidance(plain)  # cassette keys of every case without an offered lesson stay valid


def test_the_retrieval_record_def_exists_in_the_schema() -> None:
    schema = load_json(SCHEMAS / "knowledge_similarity.v1.json")
    decisions = schema["$defs"]["retrieval"]["properties"]["candidates"]["items"]["properties"]["decision"]["enum"]
    assert decisions == ["applicable", "below_threshold", "insufficient_features", "exception_blocked"]
