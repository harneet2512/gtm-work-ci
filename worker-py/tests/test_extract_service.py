from __future__ import annotations

from typing import Any

import pytest

from ghost_worker.errors import InvalidModelOutputError, ProviderError, UnsupportedExtractorVersionError
from ghost_worker.extract.schema import OUTPUT_SCHEMA, OUTPUT_SCHEMA_NAME
from ghost_worker.extract.service import extract_claims
from ghost_worker.llm.provider import LLMResult
from ghost_worker.models import ExtractRequest


class Stub:
    def __init__(self, content: Any = None, error: Exception | None = None) -> None:
        self.content, self.error, self.calls = content, error, []

    def complete_json(self, **kwargs: Any) -> LLMResult:
        self.calls.append(kwargs)
        if self.error:
            raise self.error
        return LLMResult(content=self.content, model="stub-model", usage={})


def item(quote: str, **kw: Any) -> dict:
    base = {"field_path": "blockers", "value": "x", "confidence": 0.8, "evidence_quote": quote,
            "speaker_identity": None, "subject_identity": None, "role": None, "due_at": None}
    return {**base, **kw}


def test_extract_returns_verified_claims_and_metadata(email_request: dict) -> None:
    request = ExtractRequest.model_validate(email_request)
    stub = Stub({"claims": [item("I lead security at Acme"), item("never said this")]})
    response = extract_claims(request, stub)
    assert [c.evidence_quote for c in response.claims] == ["I lead security at Acme"]
    assert (response.model, response.extractor_version, response.dropped) == ("stub-model", "extract-v4", 1)


def test_owner_claims_naming_buyer_side_people_are_rejected_and_counted(email_request: dict) -> None:
    email_request["known_people"][1]["internal"] = False  # Priya Shah, buyer side
    email_request["known_people"][2]["internal"] = True   # Dana Kim, seller side
    stub = Stub({"claims": [item("I lead security at Acme", field_path="owner", value="Priya Shah"),
                            item("I lead security at Acme", field_path="owner", value="Dana Kim"),
                            item("I lead security at Acme", field_path="champion", value="Priya Shah")]})
    response = extract_claims(ExtractRequest.model_validate(email_request), stub)
    assert [(c.field_path, c.value) for c in response.claims] == [("owner", "Dana Kim"), ("champion", "Priya Shah")]
    assert (response.rejected, response.dropped) == (1, 0)


def test_extract_calls_provider_with_schema_and_prompts(email_request: dict) -> None:
    stub = Stub({"claims": []})
    extract_claims(ExtractRequest.model_validate(email_request), stub)
    call = stub.calls[0]
    assert call["schema"] is OUTPUT_SCHEMA and call["schema_name"] == OUTPUT_SCHEMA_NAME
    assert email_request["text"] in call["user"]


def test_unsupported_extractor_version_is_rejected(email_request: dict) -> None:
    email_request["extractor_version"] = "extract-v9"
    stub = Stub({"claims": []})
    with pytest.raises(UnsupportedExtractorVersionError):
        extract_claims(ExtractRequest.model_validate(email_request), stub)
    assert stub.calls == []


def test_provider_errors_propagate(email_request: dict) -> None:
    with pytest.raises(ProviderError):
        extract_claims(ExtractRequest.model_validate(email_request), Stub(error=ProviderError("down")))


def test_malformed_model_output_raises(email_request: dict) -> None:
    with pytest.raises(InvalidModelOutputError):
        extract_claims(ExtractRequest.model_validate(email_request), Stub({"nope": 1}))


def test_extract_log_carries_usage_and_latency_but_no_text(email_request: dict, caplog: pytest.LogCaptureFixture) -> None:
    request = ExtractRequest.model_validate(email_request)
    with caplog.at_level("INFO", logger="ghost_worker.extract.service"):
        extract_claims(request, Stub({"claims": [item("I lead security at Acme")]}))
    record = next(r for r in caplog.records if r.getMessage() == "extract complete")
    assert isinstance(record.elapsed_ms, int) and record.elapsed_ms >= 0
    assert record.usage == {}
    assert "I lead security at Acme" not in str(record.__dict__)
