"""Pure post-processing of raw model output into verified ClaimCandidates (the trust boundary)."""
from __future__ import annotations

import json
import math
from dataclasses import dataclass
from datetime import datetime
from typing import Any, get_args

from pydantic import ValidationError

from ..errors import InvalidModelOutputError
from ..models import ClaimCandidate, FieldPath, Role
from .identities import IdentityResolver
from .quotes import QuoteMatcher

MAX_CLAIMS = 25
_FIELD_PATHS = frozenset(get_args(FieldPath))
_ROLES = frozenset(get_args(Role))


@dataclass(frozen=True)
class PostprocessResult:
    claims: tuple[ClaimCandidate, ...]
    dropped: int      # quote missing / not verbatim: reported to the caller as `dropped`
    discarded: int    # structurally invalid candidates (bad enum, confidence range, role, value, ...)
    truncated: int    # valid candidates cut by the MAX_CLAIMS cap


class _Discard(Exception):
    """Internal: candidate rejected for a non-quote reason."""


class _QuoteDrop(Exception):
    """Internal: candidate rejected because its quote is not verifiable."""


def _confidence(raw: object) -> float:
    if isinstance(raw, bool) or not isinstance(raw, (int, float)) or math.isnan(raw) or not 0 <= raw <= 1:
        raise _Discard
    return float(raw)


def _role(raw: object) -> str | None:
    if raw is None:
        return None
    if not isinstance(raw, str) or raw not in _ROLES:
        raise _Discard
    return raw


def _parse_due_at(raw: object) -> datetime | None:
    if not isinstance(raw, str) or not raw.strip():
        return None
    try:
        parsed = datetime.fromisoformat(raw.strip())
    except ValueError:
        return None
    return parsed if parsed.tzinfo is not None else None


def _value(raw: object) -> Any:
    if raw is None or (isinstance(raw, str) and not raw.strip()):
        raise _Discard
    return raw


def _build(item: object, quotes: QuoteMatcher, identities: IdentityResolver) -> ClaimCandidate:
    if not isinstance(item, dict):
        raise _Discard
    field_path = item.get("field_path")
    if not isinstance(field_path, str) or field_path not in _FIELD_PATHS:
        raise _Discard
    confidence = _confidence(item.get("confidence"))
    value = _value(item.get("value"))
    role = _role(item.get("role"))
    quote = quotes.resolve(item.get("evidence_quote"))
    if quote is None:
        raise _QuoteDrop
    try:
        return ClaimCandidate(
            field_path=field_path, value=value, confidence=confidence, evidence_quote=quote,
            speaker_identity=identities.resolve(item.get("speaker_identity")),
            subject_identity=identities.resolve(item.get("subject_identity")),
            role=role, due_at=_parse_due_at(item.get("due_at")),
        )
    except ValidationError:
        raise _Discard from None


def _dedupe(claims: list[ClaimCandidate]) -> list[ClaimCandidate]:
    seen: set[str] = set()
    unique: list[ClaimCandidate] = []
    for claim in claims:
        key = json.dumps(claim.model_dump(mode="json"), sort_keys=True, default=str)
        if key not in seen:
            seen.add(key)
            unique.append(claim)
    return unique


def _cap(claims: list[ClaimCandidate]) -> list[ClaimCandidate]:
    if len(claims) <= MAX_CLAIMS:
        return claims
    ranked = sorted(range(len(claims)), key=lambda i: (-claims[i].confidence, i))[:MAX_CLAIMS]
    return [claims[i] for i in sorted(ranked)]


def postprocess(raw: object, text: str, identities: IdentityResolver) -> PostprocessResult:
    if not isinstance(raw, dict) or not isinstance(raw.get("claims"), list):
        raise InvalidModelOutputError("model output lacks a 'claims' array")
    quotes = QuoteMatcher(text)
    kept: list[ClaimCandidate] = []
    dropped = discarded = 0
    for item in raw["claims"]:
        try:
            kept.append(_build(item, quotes, identities))
        except _QuoteDrop:
            dropped += 1
        except _Discard:
            discarded += 1
    unique = _dedupe(kept)
    capped = _cap(unique)
    return PostprocessResult(claims=tuple(capped), dropped=dropped, discarded=discarded,
                             truncated=len(unique) - len(capped))
