"""Helpers for building synthetic AccountState snapshots, signals and claims (no rules, no detector)."""
from __future__ import annotations

import uuid
from datetime import datetime, timedelta

ACT = "0ac70000-0000-4000-8000-000000000101"           # a generic activity id used as evidence
CHAMPION = "0b0e0000-0000-4000-8000-000000000018"       # the account's current champion (person id)
OTHER = "0b0e0000-0000-4000-8000-000000000099"          # some other customer-side person
UNKNOWN_STATE = {"value": "unknown", "transition_id": None, "confirmed_at": None}


def ts(value: str) -> datetime:
    return datetime.fromisoformat(value.replace("Z", "+00:00"))


def at(base: str, days: float = 0, seconds: int = 0) -> str:
    return (ts(base) + timedelta(days=days, seconds=seconds)).strftime("%Y-%m-%dT%H:%M:%SZ")


def fld(value: object, as_of: str | None = "2026-09-10T00:00:00Z", standing: str = "first_party_ai") -> dict:
    """One AccountState field. Use fld("unknown") for an unknown field; a known field cites evidence at as_of."""
    known = value not in ("unknown", [])
    return {"value": value, "known": known, "standing": standing if known else None, "as_of": as_of if known else None,
            "evidence_refs": [{"activity_id": ACT, "occurred_at": as_of}] if known else []}


def state(relationship: dict | None = None, **fields: dict) -> dict:
    """An AccountState with sensible defaults (an active champion, expansion motion in the CRM, low risk).
    relationship is {"value": "REORG"|"EXPANSION"|..., "transition_id": uuid, "confirmed_at": iso} or UNKNOWN_STATE.
    Override any field by name, e.g. state(champion_status=fld("departed"))."""
    base = {
        "champion": fld(CHAMPION), "champion_status": fld("active"),
        "champion_since": fld("2026-09-25T00:00:00Z", "2026-09-25T00:00:00Z"),
        "economic_buyer": fld("unknown"), "decision_process": fld("unknown"), "current_commitments": fld([]),
        "relationship_risk": fld("low"), "motion": fld("expansion", standing="crm_explicit"),
    }
    base.update(fields)
    members = [{"person_id": CHAMPION, "display_name": "Champion", "roles": ["champion"], "status": "active",
                "evidence_refs": [{"activity_id": ACT}]}]
    rel = relationship or {"value": "unknown", "transition_id": None, "confirmed_at": None}
    return {"fields": base, "buying_group": members, "relationship_state": rel}


def sig(kind: str, created_at: str, is_open: bool = True, subject: str | None = None) -> dict:
    return {"id": str(uuid.uuid4()), "signal_type": kind, "created_at": created_at, "open": is_open, "subject_person_id": subject,
            "evidence_refs": [{"activity_id": ACT, "occurred_at": created_at}]}


def claim(path: str, occurred_at: str, subject: str | None = None, standing: str = "first_party_ai") -> dict:
    return {"id": str(uuid.uuid4()), "field_path": path, "occurred_at": occurred_at, "status": "active", "standing": standing,
            "source_activity_id": ACT, "subject_person_id": subject}
