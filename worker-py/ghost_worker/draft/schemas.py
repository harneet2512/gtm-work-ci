"""Model-facing schemas: tool specs, the agent's decision and the skill output.

JSON Schemas are strict-mode friendly (closed objects, every key required, optional values nullable); the
Pydantic twins validate what comes back. Neither is a public contract: the response contract is AgentRunOutput.
"""
from __future__ import annotations

from typing import Annotated, Any, TypeVar, get_args

from pydantic import BaseModel, ConfigDict, Field, ValidationError

from ..errors import InvalidModelOutputError
from ..models import FieldPath
from ..models.draft import (
    UUID_PATTERN,
    Channel,
    PersonRef,
    CrmNextStepIntent,
    EvidenceRef,
    FinishedArtifact,
    ProposedAction,
    Recipient,
    Timestamp,
    Uuid,
    WhyNow,
)
from .core_client import MAX_LIMIT, MIN_LIMIT, TOOL_NAMES

DECISION_SCHEMA_NAME = "account_agent_decision_v1"
SKILL_NAME = "post_interaction_followup"
SKILL_SCHEMA_NAME = "post_interaction_followup_v1"

_NULLABLE_STRING: dict[str, Any] = {"type": ["string", "null"]}
_ACTIONS = list(get_args(ProposedAction))
_PEOPLE_SCHEMA: dict[str, Any] = {"type": "array", "items": {
    "type": "object", "additionalProperties": False, "required": ["person_id", "why"],
    "properties": {"person_id": {"type": "string", "pattern": UUID_PATTERN}, "why": {"type": "string"}}}}

EVIDENCE_REF_SCHEMA: dict[str, Any] = {
    "type": "object", "additionalProperties": False,
    "required": ["activity_id", "claim_id", "quote", "speaker_person_id", "occurred_at"],
    "properties": {"activity_id": {"type": "string", "pattern": UUID_PATTERN},
                   "claim_id": _NULLABLE_STRING, "quote": _NULLABLE_STRING,
                   "speaker_person_id": _NULLABLE_STRING, "occurred_at": _NULLABLE_STRING},
}

DECISION_SCHEMA: dict[str, Any] = {
    "type": "object", "additionalProperties": False,
    "required": ["action", "why_now", "who_to_involve", "who_not_to_involve", "used_guidance",
                 "knowledge_refs_used", "wait_until", "evidence_refs"],
    "properties": {
        "action": {"type": "string", "enum": _ACTIONS},
        "why_now": {"type": "string"},
        "who_to_involve": _PEOPLE_SCHEMA,
        "who_not_to_involve": _PEOPLE_SCHEMA,
        "used_guidance": {"type": "boolean"},
        "knowledge_refs_used": {"type": "array", "items": {"type": "string"}},
        "wait_until": _NULLABLE_STRING,
        "evidence_refs": {"type": "array", "items": EVIDENCE_REF_SCHEMA},
    },
}

SKILL_SCHEMA: dict[str, Any] = {
    "type": "object", "additionalProperties": False,
    "required": ["recipients", "finished_artifact", "crm_next_step_intent", "reason", "evidence_refs"],
    "properties": {
        "recipients": {"type": "array", "items": {
            "type": "object", "additionalProperties": False, "required": ["person_id", "role", "why"],
            "properties": {"person_id": {"type": "string", "pattern": UUID_PATTERN},
                           "role": {"type": "string", "enum": ["to", "cc", "bcc"]}, "why": {"type": "string"}}}},
        "finished_artifact": {
            "type": "object", "additionalProperties": False, "required": ["channel", "subject", "body", "attachments"],
            "properties": {"channel": {"type": "string", "enum": list(get_args(Channel))},
                           "subject": _NULLABLE_STRING, "body": {"type": "string"},
                           "attachments": {"type": "array", "items": {"type": "string"}}}},
        "crm_next_step_intent": {
            "type": "object", "additionalProperties": False, "required": ["next_step", "due_at", "stage_change"],
            "properties": {"next_step": {"type": "string"}, "due_at": _NULLABLE_STRING,
                           "stage_change": _NULLABLE_STRING}},
        "reason": {"type": "string"},
        "evidence_refs": {"type": "array", "items": EVIDENCE_REF_SCHEMA},
    },
}


class _ModelOutput(BaseModel):
    model_config = ConfigDict(frozen=True, extra="ignore")


class AccountDecision(_ModelOutput):
    action: ProposedAction
    why_now: WhyNow
    who_to_involve: tuple[PersonRef, ...]
    who_not_to_involve: tuple[PersonRef, ...]
    used_guidance: bool
    knowledge_refs_used: tuple[Uuid, ...] = ()
    wait_until: Timestamp | None = None
    evidence_refs: tuple[EvidenceRef, ...] = ()

    @property
    def named_people(self) -> tuple[str, ...]:
        """Every person_id the decision names, in who_to_involve and who_not_to_involve."""
        return tuple(p.person_id for p in (*self.who_to_involve, *self.who_not_to_involve))


class SkillDraft(_ModelOutput):
    recipients: tuple[Recipient, ...]
    finished_artifact: FinishedArtifact
    crm_next_step_intent: CrmNextStepIntent
    reason: Annotated[str, Field(max_length=2000)]
    evidence_refs: tuple[EvidenceRef, ...]


M = TypeVar("M", bound=_ModelOutput)


def parse_model_output(model: type[M], content: dict[str, Any], what: str) -> M:
    try:
        return model.model_validate(content)
    except ValidationError as exc:
        fields = ", ".join(sorted({".".join(str(p) for p in e["loc"]) or "<root>" for e in exc.errors()}))
        raise InvalidModelOutputError(f"{what} is malformed (fields: {fields})") from None


_TOOL_DESCRIPTIONS = {
    "state": "Current account state: fields with their winning claim, confidence and evidence.",
    "recent_diffs": "Most recent state diffs (material and non-material changes) for this account.",
    "evidence": "Verbatim evidence quotes behind claims; filter with field_path.",
    "activities": "Recent activities (emails, meetings, calls) with summaries and ids.",
    "people": "People in the account's buying group and our own team, with ids, titles and roles.",
    "commitments": "Open commitments by either side, with owners and due dates.",
}

TOOL_SPECS: list[dict[str, Any]] = [
    {"type": "function", "function": {
        "name": name, "description": _TOOL_DESCRIPTIONS[name],
        "parameters": {"type": "object", "additionalProperties": False, "properties": {
            "field_path": {"type": "string", "enum": list(get_args(FieldPath)),
                           "description": "Optional: restrict to one account-state field."},
            "limit": {"type": "integer", "minimum": MIN_LIMIT, "maximum": MAX_LIMIT,
                      "description": "Optional: maximum items to return."}}}}}
    for name in TOOL_NAMES
]
