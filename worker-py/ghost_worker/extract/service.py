"""Extraction use case: prompts -> provider -> verified candidates. No HTTP, no globals."""
from __future__ import annotations

import logging
import time

from ..errors import UnsupportedExtractorVersionError
from ..llm.provider import LLMProvider
from ..models import ExtractRequest, ExtractResponse
from .guards import reject_buyer_side_owners
from .identities import build_identity_resolver
from .postprocess import postprocess
from .prompt import EXTRACTOR_VERSION, build_system_prompt, build_user_prompt
from .schema import OUTPUT_SCHEMA, OUTPUT_SCHEMA_NAME

log = logging.getLogger(__name__)


def extract_claims(request: ExtractRequest, provider: LLMProvider) -> ExtractResponse:
    if request.extractor_version != EXTRACTOR_VERSION:
        raise UnsupportedExtractorVersionError(f"unsupported extractor_version {request.extractor_version!r}")
    started = time.monotonic()
    result = provider.complete_json(
        system=build_system_prompt(), user=build_user_prompt(request),
        schema=OUTPUT_SCHEMA, schema_name=OUTPUT_SCHEMA_NAME)
    elapsed_ms = round((time.monotonic() - started) * 1000)
    processed = postprocess(result.content, request.text, build_identity_resolver(request))
    claims, rejected = reject_buyer_side_owners(processed.claims, request.known_people)
    log.info("extract complete", extra={
        "activity_id": request.activity.id, "model": result.model, "claims": len(claims),
        "dropped": processed.dropped, "discarded": processed.discarded, "truncated": processed.truncated,
        "rejected": rejected, "usage": result.usage, "elapsed_ms": elapsed_ms})
    return ExtractResponse(claims=claims, model=result.model, extractor_version=EXTRACTOR_VERSION,
                           dropped=processed.dropped, rejected=rejected)
