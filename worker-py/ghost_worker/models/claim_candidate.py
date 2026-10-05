"""ClaimCandidate (contracts/schemas/claim_candidate.v1.json)."""
from __future__ import annotations

from datetime import datetime
from typing import Annotated, Any

from pydantic import BaseModel, ConfigDict, Field

from .enums import FieldPath, Role

MAX_EVIDENCE_QUOTE_CHARS = 2000  # claim_candidate.v1.json evidence_quote maxLength


class ClaimCandidate(BaseModel):
    model_config = ConfigDict(frozen=True, extra="forbid")

    field_path: FieldPath
    value: Any
    confidence: Annotated[float, Field(ge=0, le=1)]
    evidence_quote: Annotated[str, Field(min_length=1, max_length=MAX_EVIDENCE_QUOTE_CHARS)]
    speaker_identity: str | None = None
    subject_identity: str | None = None
    role: Role | None = None
    due_at: datetime | None = None
