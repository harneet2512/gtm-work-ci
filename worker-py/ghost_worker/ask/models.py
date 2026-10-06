"""POST /v1/ask request and response (contracts/openapi/worker.yaml)."""
from __future__ import annotations

from typing import Annotated, Literal

from pydantic import BaseModel, ConfigDict, Field

from ..models.usage import UsageSummary

ActionKind = Literal["play_next", "demo_status"]
MAX_TOOL_CALLS_CAP = 6
DEADLINE_CAP_S = 60.0


class AskRequest(BaseModel):
    model_config = ConfigDict(frozen=True, extra="forbid")

    text: Annotated[str, Field(min_length=1, max_length=2000)]
    channel_kind: Literal["dm", "thread"]
    ask_token: Annotated[str, Field(min_length=16, max_length=512)]
    max_tool_calls: Annotated[int, Field(ge=1, le=MAX_TOOL_CALLS_CAP)] = MAX_TOOL_CALLS_CAP
    deadline_s: Annotated[float, Field(gt=0, le=DEADLINE_CAP_S)] = DEADLINE_CAP_S


class Citation(BaseModel):
    model_config = ConfigDict(frozen=True)

    call_id: str
    tool: str
    label: str
    url: str | None = None


class ProposedActionOut(BaseModel):
    model_config = ConfigDict(frozen=True)

    kind: ActionKind
    summary: Annotated[str, Field(min_length=1, max_length=400)]


class AskResponse(BaseModel):
    model_config = ConfigDict(frozen=True)

    answer_markdown: str
    citations: tuple[Citation, ...] = ()
    proposed_action: ProposedActionOut | None = None
    dont_know: bool
    timed_out: bool = False
    tool_calls: int
    model: str
    usage: UsageSummary | None = None
