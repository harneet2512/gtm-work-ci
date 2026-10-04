"""Activity (contracts/schemas/activity.v1.json). The worker only reads it, but validates strictly."""
from __future__ import annotations

from datetime import datetime
from typing import Annotated

from pydantic import BaseModel, ConfigDict, Field, StringConstraints

from .enums import ActivityType, ParticipantRole, SourceSystem, Visibility

UUID_PATTERN = r"^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$"
Uuid = Annotated[str, StringConstraints(pattern=UUID_PATTERN)]
MAX_FIELD_CHARS = 512
MAX_LIST_ITEMS = 100
NonEmpty = Annotated[str, StringConstraints(min_length=1, max_length=MAX_FIELD_CHARS)]
ShortStr = Annotated[str, StringConstraints(max_length=MAX_FIELD_CHARS)]


class _Frozen(BaseModel):
    model_config = ConfigDict(frozen=True, extra="forbid")


class Participant(_Frozen):
    raw_identity: NonEmpty
    display_name: ShortStr | None = None
    role: ParticipantRole
    person_id: Uuid | None = None


class Permissions(_Frozen):
    visibility: Visibility
    allowed_person_ids: Annotated[tuple[Uuid, ...], Field(max_length=MAX_LIST_ITEMS)] | None = None


class Provenance(_Frozen):
    source_system: SourceSystem
    source_object_id: NonEmpty
    connector: ShortStr | None = None
    connector_version: ShortStr | None = None


class Activity(_Frozen):
    id: Uuid
    idempotency_key: Annotated[str, StringConstraints(pattern=r"^[0-9a-f]{64}$")]
    activity_type: ActivityType
    source_system: SourceSystem
    source_object_id: NonEmpty
    source_event_id: Uuid
    occurred_at: datetime
    ingested_at: datetime
    participants: Annotated[tuple[Participant, ...], Field(max_length=MAX_LIST_ITEMS)]
    account_id: Uuid | None = None
    opportunity_id: Uuid | None = None
    account_hint: ShortStr | None = None
    opportunity_hint: ShortStr | None = None
    payload_ref: ShortStr
    summary: Annotated[str, Field(max_length=1000)] | None = None
    permissions: Permissions
    provenance: Provenance
    caused_by_activity_id: Uuid | None = None
    correlation_id: Uuid | None = None
