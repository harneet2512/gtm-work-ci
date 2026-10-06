"""Provider abstraction: structured-JSON completion and one tool-calling turn, nothing else."""
from __future__ import annotations

from typing import Any, Protocol

from pydantic import BaseModel, ConfigDict, Field, model_validator

from ..models.frozen import FrozenMap


class LLMResult(BaseModel):
    model_config = ConfigDict(frozen=True)

    content: dict[str, Any]  # extract postprocessing consumes a plain dict
    model: str
    usage: FrozenMap = Field(default_factory=dict)


class ToolCall(BaseModel):
    model_config = ConfigDict(frozen=True)

    id: str
    name: str
    arguments: FrozenMap


class TurnResult(BaseModel):
    """One model turn: either tool calls to execute, or the final structured `content` (never both)."""

    model_config = ConfigDict(frozen=True)

    content: FrozenMap | None = None
    tool_calls: tuple[ToolCall, ...] = ()
    model: str
    usage: FrozenMap = Field(default_factory=dict)

    @model_validator(mode="after")
    def _exactly_one_outcome(self) -> TurnResult:
        if (self.content is None) == (not self.tool_calls):
            raise ValueError("a turn has either content or tool_calls, exactly one")
        return self


class LLMProvider(Protocol):
    def complete_json(self, *, system: str, user: str, schema: dict[str, Any], schema_name: str) -> LLMResult:
        """Return a JSON object conforming (best effort) to `schema`. Raises LLMError subclasses."""
        ...

    def complete_turn(self, *, system: str, messages: list[dict[str, Any]], tools: list[dict[str, Any]],
                      schema: dict[str, Any], schema_name: str) -> TurnResult:
        """One step of a tool-calling conversation (OpenAI-style `messages` and function `tools`).

        With `tools` the model may call them or answer; with no tools it must answer with a JSON object
        conforming to `schema`. Raises LLMError subclasses."""
        ...
