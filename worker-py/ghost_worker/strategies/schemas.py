"""Model-facing schemas of the strategy generator and the revision planner (strict-mode friendly: closed objects,
every key required, optional values nullable). Neither is a public contract: the response contract is
StrategyCandidate (contracts/schemas/strategy_candidate.v1.json)."""
from __future__ import annotations

import re
from collections.abc import Mapping
from typing import Annotated, Any, get_args

from pydantic import BaseModel, ConfigDict, Field

from ..draft.schemas import EVIDENCE_REF_SCHEMA, parse_model_output
from ..models.draft import UUID_PATTERN, Channel, EvidenceRef, ProposedAction, Uuid
from ..models.strategies import ACTION_CLASSES, CANDIDATE_COUNT, FIVE_QUESTIONS, ActionClass, FiveQuestions, StrategyType

PLAN_SCHEMA_NAME = "strategy_plan_v1"
ARTIFACTS_SCHEMA_NAME = "strategy_artifacts_v1"
REVISION_SCHEMA_NAME = "strategy_revision_v1"

_NULLABLE_STRING: dict[str, Any] = {"type": ["string", "null"]}
_PERSON: dict[str, Any] = {"type": "object", "additionalProperties": False, "required": ["person_id", "why"],
                           "properties": {"person_id": {"type": "string", "pattern": UUID_PATTERN},
                                          "why": {"type": "string"}}}

PLAN_SCHEMA: dict[str, Any] = {
    "type": "object", "additionalProperties": False, "required": ["strategies"],
    "properties": {"strategies": {
        "type": "array", "minItems": CANDIDATE_COUNT, "maxItems": CANDIDATE_COUNT,
        "items": {
            "type": "object", "additionalProperties": False,
            "required": ["strategy_type", "title", "description", "rationale", "five_questions", "action",
                         "action_class", "to", "cc", "state_refs", "evidence_refs", "knowledge_refs_used"],
            "properties": {
                "strategy_type": {"type": "string", "pattern": "^[a-z][a-z0-9_]*$"},
                "title": {"type": "string"}, "description": {"type": "string"}, "rationale": {"type": "string"},
                "five_questions": {"type": "object", "additionalProperties": False, "required": list(FIVE_QUESTIONS),
                                   "properties": {k: {"type": "string"} for k in FIVE_QUESTIONS}},
                "action": {"type": "string", "enum": list(get_args(ProposedAction))},
                "action_class": {"type": "string", "enum": list(ACTION_CLASSES)},
                "to": {"type": "array", "items": _PERSON}, "cc": {"type": "array", "items": _PERSON},
                "state_refs": {"type": "array", "items": {"type": "string"}},
                "evidence_refs": {"type": "array", "items": EVIDENCE_REF_SCHEMA},
                "knowledge_refs_used": {"type": "array", "items": {"type": "string"}}}}}},
}

_ARTIFACT: dict[str, Any] = {
    "type": "object", "additionalProperties": False, "required": ["channel", "subject", "body", "attachments"],
    "properties": {"channel": {"type": "string", "enum": list(get_args(Channel))}, "subject": _NULLABLE_STRING,
                   "body": {"type": "string"}, "attachments": {"type": "array", "items": {"type": "string"}}}}

ARTIFACTS_SCHEMA: dict[str, Any] = {
    "type": "object", "additionalProperties": False, "required": ["artifacts"],
    "properties": {"artifacts": {"type": "array", "items": {
        "type": "object", "additionalProperties": False, "required": ["strategy_type", "artifact"],
        "properties": {"strategy_type": {"type": "string"}, "artifact": _ARTIFACT}}}},
}

REVISION_SCHEMA: dict[str, Any] = {
    "type": "object", "additionalProperties": False, "required": ["to", "cc", "artifact", "rationale"],
    "properties": {"to": {"type": "array", "items": _PERSON}, "cc": {"type": "array", "items": _PERSON},
                   "artifact": _ARTIFACT, "rationale": {"type": "string"}},
}


class _Out(BaseModel):
    model_config = ConfigDict(frozen=True, extra="ignore")


class PersonChoice(_Out):
    person_id: Uuid
    why: Annotated[str, Field(max_length=500)]


class PlannedStrategy(_Out):
    strategy_type: StrategyType
    title: Annotated[str, Field(min_length=1, max_length=80)]
    description: Annotated[str, Field(min_length=1, max_length=300)]
    rationale: Annotated[str, Field(min_length=1, max_length=1500)]
    five_questions: FiveQuestions
    action: ProposedAction
    action_class: ActionClass
    to: tuple[PersonChoice, ...]
    cc: tuple[PersonChoice, ...]
    state_refs: tuple[str, ...]
    evidence_refs: tuple[EvidenceRef, ...]
    knowledge_refs_used: tuple[Uuid, ...] = ()


TITLE_MAX = 80
_CLAUSE_END = re.compile(r"[.;:!?\u2014]|\s-\s|,\s")


def derive_title(strategy_type: str, description: str) -> str:
    """A deterministic title for a candidate whose model left it out: the strategy type in words, plus the first clause of the description,
    cut at a word to the title limit. Nothing is invented: both parts are the model's own words."""
    head = " ".join(strategy_type.replace("_", " ").split()).capitalize() if strategy_type else ""
    clause = _CLAUSE_END.split(description.strip(), maxsplit=1)[0].strip() if description else ""
    title = f"{head}: {clause}" if head and clause else (head or clause)
    if len(title) > TITLE_MAX:
        title = title[:TITLE_MAX - 3].rsplit(" ", 1)[0].rstrip(" :,;-") + "..."
    return title


def normalize_plan(content: Any) -> Any:
    """Repairs the purely cosmetic slips of the planner's answer before validation (qwen3.8-flash sometimes omits a title): a missing,
    blank or over-long `title` is derived from the strategy type and description. Substance (evidence, recipients, rationale, the
    five questions, the action) is never filled in: a plan without it still fails validation. The input is not modified."""
    # The provider hands over frozen mappings and tuples, not dict and list.
    if not isinstance(content, Mapping) or not isinstance(content.get("strategies"), (list, tuple)):
        return content
    items = []
    for item in content["strategies"]:
        if not isinstance(item, Mapping):
            items.append(item)
            continue
        title, fixed = item.get("title"), dict(item)
        stype, desc = item.get("strategy_type"), item.get("description")
        if isinstance(stype, str) and (not isinstance(title, str) or not title.strip() or len(title) > TITLE_MAX):
            source = title if isinstance(title, str) and len(title) > TITLE_MAX else ""
            fixed["title"] = derive_title(stype, desc if isinstance(desc, str) else "") if not source else _cut(source)
        items.append(fixed)
    return {**content, "strategies": items}


def _cut(text: str) -> str:
    text = " ".join(text.split())
    return text if len(text) <= TITLE_MAX else text[:TITLE_MAX - 3].rsplit(" ", 1)[0].rstrip(" :,;-") + "..."


class StrategyPlan(_Out):
    strategies: Annotated[tuple[PlannedStrategy, ...], Field(min_length=CANDIDATE_COUNT, max_length=CANDIDATE_COUNT)]

    @property
    def named_people(self) -> tuple[str, ...]:
        return tuple(p.person_id for s in self.strategies for p in (*s.to, *s.cc))


class ArtifactBody(_Out):
    channel: Channel
    subject: Annotated[str, Field(max_length=300)] | None = None
    body: Annotated[str, Field(max_length=20000)]
    attachments: tuple[str, ...] = ()


class StrategyArtifact(_Out):
    strategy_type: str
    artifact: ArtifactBody


class StrategyArtifacts(_Out):
    artifacts: tuple[StrategyArtifact, ...]


class Revision(_Out):
    to: tuple[PersonChoice, ...]
    cc: tuple[PersonChoice, ...]
    artifact: ArtifactBody
    rationale: Annotated[str, Field(min_length=1, max_length=1500)]


__all__ = ["derive_title", "normalize_plan", "ARTIFACTS_SCHEMA", "ARTIFACTS_SCHEMA_NAME", "PLAN_SCHEMA", "PLAN_SCHEMA_NAME", "REVISION_SCHEMA",
           "REVISION_SCHEMA_NAME", "ArtifactBody", "PlannedStrategy", "Revision", "StrategyArtifacts",
           "StrategyPlan", "parse_model_output"]
