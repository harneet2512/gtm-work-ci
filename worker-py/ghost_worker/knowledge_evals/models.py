"""Inputs and outputs of the HAR-97 L4 knowledge evals (frozen values; malformed input fails loudly)."""
from __future__ import annotations

from typing import Any, Literal

from pydantic import BaseModel, ConfigDict, Field, model_validator

Op = Literal["eq", "neq", "in", "contains", "exists", "not_exists", "is_unknown"]
_VALUELESS = {"exists", "not_exists", "is_unknown"}


class Condition(BaseModel):
    """One knowledge condition (contracts/schemas/knowledge.v1.json#/$defs/condition)."""

    model_config = ConfigDict(frozen=True, extra="forbid")

    field: str = Field(min_length=1, pattern=r"^(diff\.[a-z_]+|buying_group\.[a-z_]+|transition\.(from_state|to_state|status)|[a-z_]+)$")
    op: Op
    value: Any = None

    @model_validator(mode="after")
    def _value_shape(self) -> Condition:
        if self.op in _VALUELESS and self.value is not None:
            raise ValueError(f"{self.op} takes no value")
        if self.op in ("eq", "neq") and not isinstance(self.value, (str, int, float, bool)):
            raise ValueError(f"{self.op} needs a scalar value")
        if self.op == "in" and not (isinstance(self.value, (list, tuple)) and self.value):
            raise ValueError("in needs a non-empty list")
        if self.op == "contains" and not (isinstance(self.value, str) and self.value.strip() or isinstance(self.value, dict) and self.value):
            raise ValueError("contains needs a string or an item pattern")
        return self

    def render(self) -> str:
        return f"{self.field} {self.op}" if self.value is None else f"{self.field} {self.op} {self.value!r}"


class KnowledgeException(BaseModel):
    model_config = ConfigDict(frozen=True, extra="ignore")

    description: str
    conditions: tuple[Condition, ...] = Field(min_length=1)


class Guidance(BaseModel):
    model_config = ConfigDict(frozen=True, extra="ignore")

    summary: str = Field(min_length=1)
    do: tuple[str, ...] = ()
    dont: tuple[str, ...] = ()


class InferredKnowledge(BaseModel):
    """The part of a Knowledge object the learner inferred (contract fields; others are ignored)."""

    model_config = ConfigDict(frozen=True, extra="ignore")

    title: str = Field(min_length=1)
    situation_signature: tuple[Condition, ...] = Field(min_length=1)
    applicability_conditions: tuple[Condition, ...] = ()
    guidance: Guidance
    exceptions: tuple[KnowledgeException, ...] = ()

    @property
    def scope(self) -> tuple[Condition, ...]:
        return self.situation_signature + self.applicability_conditions


class ReferenceLesson(BaseModel):
    """What a strong GTM reviewer would learn from the correction (gold).

    reusable=False means the correction holds no reusable principle (a typo, a one-off fact) and the right
    learner output is no knowledge at all."""

    model_config = ConfigDict(frozen=True, extra="forbid")

    reusable: bool
    principle: str = ""
    scope: tuple[Condition, ...] = ()
    exceptions: tuple[KnowledgeException, ...] = ()

    @model_validator(mode="after")
    def _reusable_has_content(self) -> ReferenceLesson:
        if self.reusable and (not self.principle or not self.scope):
            raise ValueError("a reusable lesson needs a principle and a scope")
        if not self.reusable and (self.principle or self.scope or self.exceptions):
            raise ValueError("a non-reusable lesson carries no principle, scope or exceptions")
        return self


class Correction(BaseModel):
    """The episode the learner learned from (HAR-97 L4: state + proposal + evals + human edit + final artifact)."""

    model_config = ConfigDict(frozen=True, extra="forbid")

    account_state_summary: str = Field(min_length=1, max_length=4000)
    agent_proposal: str = Field(min_length=1, max_length=4000)
    eval_results: str = Field(default="", max_length=4000)
    human_edit: str = Field(min_length=1, max_length=4000)
    final_artifact: str = Field(default="", max_length=4000)
