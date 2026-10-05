"""ADR-0013: DecisionGuidance knowledge entries may carry the current evidence behind their matched conditions."""
from __future__ import annotations

import copy

from ghost_worker.draft.prompt import render_guidance
from ghost_worker.models.draft import DecisionGuidance
from test_contracts import example, validator

REF = {"activity_id": "0ac70000-0000-4000-8000-000000000101", "claim_id": "0c1a0000-0000-4000-8000-000000000001"}


def with_evidence(refs: list) -> dict:
    doc = copy.deepcopy(example("decision_guidance"))
    doc["supporting_knowledge"][0]["current_evidence_refs"] = refs
    return doc


def test_current_evidence_refs_validate() -> None:
    assert not list(validator("decision_guidance").iter_errors(with_evidence([REF])))


def test_malformed_current_evidence_is_rejected() -> None:
    for bad in ([{"claim_id": REF["claim_id"]}], [{"activity_id": "not-a-uuid"}], [{**REF, "extra": 1}], "x"):
        assert list(validator("decision_guidance").iter_errors(with_evidence(bad))), bad


def test_worker_model_reads_current_evidence_and_renders_unchanged_without_it() -> None:
    parsed = DecisionGuidance.model_validate(with_evidence([REF]))
    assert parsed.supporting_knowledge[0].current_evidence_refs[0].activity_id == REF["activity_id"]
    plain = DecisionGuidance.model_validate(example("decision_guidance"))
    assert plain.supporting_knowledge[0].current_evidence_refs is None
    assert "current_evidence_refs" not in render_guidance(plain)  # draft cassette keys stay valid
