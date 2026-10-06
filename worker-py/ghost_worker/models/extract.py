"""POST /v1/extract request/response and the error envelope (contracts/openapi/worker.yaml)."""
from __future__ import annotations

from typing import Annotated, Literal

from pydantic import BaseModel, ConfigDict, Field

from .activity import MAX_FIELD_CHARS, MAX_LIST_ITEMS, Activity
from .claim_candidate import ClaimCandidate

MAX_TEXT_CHARS = 60000


class KnownPerson(BaseModel):
    model_config = ConfigDict(frozen=True, extra="ignore")

    raw_identity: Annotated[str, Field(max_length=MAX_FIELD_CHARS)]
    display_name: Annotated[str, Field(max_length=MAX_FIELD_CHARS)]
    title: Annotated[str, Field(max_length=MAX_FIELD_CHARS)] | None = None
    internal: bool | None = None  # True: our (seller) org; False: the account (buyer side); None: unknown


class ExtractRequest(BaseModel):
    model_config = ConfigDict(frozen=True, extra="forbid")

    activity: Activity
    text: Annotated[str, Field(min_length=1, max_length=MAX_TEXT_CHARS)]
    known_people: Annotated[tuple[KnownPerson, ...], Field(max_length=MAX_LIST_ITEMS)] = ()
    extractor_version: str = "extract-v4"


class ExtractResponse(BaseModel):
    model_config = ConfigDict(frozen=True, extra="forbid")

    claims: tuple[ClaimCandidate, ...]
    model: str
    extractor_version: str
    dropped: Annotated[int, Field(ge=0)]
    rejected: Annotated[int, Field(ge=0)] = 0  # removed by deterministic guards (extract/guards.py)


class ErrorBody(BaseModel):
    model_config = ConfigDict(frozen=True)

    code: str
    message: str


class ErrorEnvelope(BaseModel):
    model_config = ConfigDict(frozen=True)

    error: ErrorBody

    @classmethod
    def of(cls, code: str, message: str) -> ErrorEnvelope:
        return cls(error=ErrorBody(code=code, message=message))


class HealthResponse(BaseModel):
    model_config = ConfigDict(frozen=True)

    status: str
    model: str
    llm_mode: Literal["live", "record", "replay", "cache"]
