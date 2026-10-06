"""Pydantic models must agree with the frozen JSON Schemas."""
from __future__ import annotations

from typing import get_args

import pytest
from pydantic import ValidationError

from ghost_worker.models import (
    Activity,
    ClaimCandidate,
    ErrorEnvelope,
    ExtractRequest,
    ExtractResponse,
    FieldPath,
    Role,
)
from test_contracts import SCHEMAS, example, load_json, validator


def _assert_valid(schema: str, doc: dict) -> None:
    errors = list(validator(schema).iter_errors(doc))
    assert not errors, [f"{list(e.absolute_path)}: {e.message}" for e in errors]


def test_field_path_literal_matches_claim_schema_enum() -> None:
    defs = load_json(SCHEMAS / "claim.v1.json")["$defs"]
    pending = defs["fieldPathPendingExtraction"]["enum"]
    assert set(pending) <= set(defs["fieldPath"]["enum"])
    assert list(get_args(FieldPath)) == [p for p in defs["fieldPath"]["enum"] if p not in pending],         "the worker emits every contract field path except those pending extract-v5 (ADR-0012)"


def test_role_literal_matches_candidate_schema_enum() -> None:
    enum = load_json(SCHEMAS / "claim_candidate.v1.json")["properties"]["role"]["enum"]
    assert [*get_args(Role), None] == enum


def test_claim_candidate_example_round_trips_and_validates() -> None:
    candidate = ClaimCandidate.model_validate(example("claim_candidate"))
    _assert_valid("claim_candidate", candidate.model_dump(mode="json"))


def test_claim_candidate_with_due_at_serialises_as_rfc3339() -> None:
    candidate = ClaimCandidate(field_path="commitment", value="send SOC2", confidence=0.8,
                               evidence_quote="we will send it", due_at="2026-10-08T10:00:00Z")
    dumped = candidate.model_dump(mode="json")
    assert dumped["due_at"] == "2026-10-08T10:00:00Z"
    _assert_valid("claim_candidate", dumped)


def test_activity_example_round_trips_and_validates() -> None:
    activity = Activity.model_validate(example("activity"))
    _assert_valid("activity", activity.model_dump(mode="json", exclude_none=True))


def test_extract_request_and_response_shapes(email_request: dict) -> None:
    request = ExtractRequest.model_validate(email_request)
    assert request.extractor_version == "extract-v4"
    response = ExtractResponse(claims=(ClaimCandidate.model_validate(example("claim_candidate")),),
                               model="m", extractor_version="extract-v4", dropped=0)
    dumped = response.model_dump(mode="json")
    for claim in dumped["claims"]:
        _assert_valid("claim_candidate", claim)
    assert set(dumped) == {"claims", "model", "extractor_version", "dropped", "rejected"}
    assert dumped["rejected"] == 0


def test_models_are_frozen() -> None:
    candidate = ClaimCandidate.model_validate(example("claim_candidate"))
    with pytest.raises(ValidationError):
        candidate.confidence = 0.1  # type: ignore[misc]


@pytest.mark.parametrize("patch", [
    {"field_path": "mood"},
    {"confidence": 1.5},
    {"confidence": -0.1},
    {"evidence_quote": ""},
    {"role": "wizard"},
    {"surprise": 1},
])
def test_claim_candidate_rejects_bad_values(patch: dict) -> None:
    with pytest.raises(ValidationError):
        ClaimCandidate.model_validate({**example("claim_candidate"), **patch})


@pytest.mark.parametrize("mutate", [
    lambda r: r.update(text=""),
    lambda r: r.update(text="x" * 60001),
    lambda r: r.pop("text"),
    lambda r: r.pop("activity"),
    lambda r: r.update(core_url="http://evil.example"),
    lambda r: r["activity"].update(activity_type="Telepathy"),
    lambda r: r.update(known_people=[{"raw_identity": "a"}]),
])
def test_extract_request_rejects_invalid(email_request: dict, mutate) -> None:
    mutate(email_request)
    with pytest.raises(ValidationError):
        ExtractRequest.model_validate(email_request)


def test_extract_request_accepts_max_length_text(email_request: dict) -> None:
    email_request["text"] = "x" * 60000
    assert len(ExtractRequest.model_validate(email_request).text) == 60000


def test_error_envelope_shape() -> None:
    assert ErrorEnvelope.of("invalid_request", "bad").model_dump() == {
        "error": {"code": "invalid_request", "message": "bad"}}
