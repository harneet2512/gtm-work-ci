"""Builders for transition-detector scenarios (ADR-0012): synthetic AccountStates, signals and claims shaped like the
contracts. Shared by the reference-evaluator tests and the Go parity cases (transition_parity.py)."""
from __future__ import annotations

import copy
import uuid
from datetime import timedelta

from transition_reference import Context, Outcome, evaluate, ts
from transition_rules_lib import RULES

ANCHOR, NOW = "2026-09-01T00:00:00Z", "2026-10-01T00:00:00Z"
ACT = "0ac70000-0000-4000-8000-000000000101"
CHAMPION, OTHER = "0b0e0000-0000-4000-8000-000000000018", "0b0e0000-0000-4000-8000-000000000099"
OPEN_REORG = {"status": "CANDIDATE", "to_state_candidate": "REORG", "last_updated_at": "2026-09-25T00:00:00Z"}
UNKNOWN_STATE = {"value": "unknown", "transition_id": None, "confirmed_at": None}
OPEN_EXPANSION = {"status": "CANDIDATE", "to_state_candidate": "EXPANSION", "last_updated_at": "2026-09-20T00:00:00Z"}


def days_before(n: int, ref: str = NOW) -> str:
    return (ts(ref) - timedelta(days=n)).isoformat().replace("+00:00", "Z")


def fld(value: object, as_of: str | None = "2026-09-10T00:00:00Z", standing: str = "first_party_ai") -> dict:
    known = value not in ("unknown", [])
    return {"value": value, "known": known, "standing": standing if known else None, "as_of": as_of if known else None,
            "evidence_refs": [{"activity_id": ACT, "occurred_at": as_of}] if known else []}


def state(relationship: dict | None = None, **fields: dict) -> dict:
    base = {
        "champion": fld(CHAMPION), "champion_status": fld("active"),
        "champion_since": fld("2026-09-25T00:00:00Z", "2026-09-25T00:00:00Z"),
        "economic_buyer": fld("unknown"), "decision_process": fld("unknown"), "current_commitments": fld([]),
        "relationship_risk": fld("low"), "motion": fld("expansion", standing="crm_explicit"),
    }
    base.update(fields)
    members = [{"person_id": CHAMPION, "display_name": "Champion", "roles": ["champion"], "status": "active",
                "evidence_refs": [{"activity_id": ACT}]}]
    rel = relationship or {"value": "REORG", "transition_id": str(uuid.uuid4()), "confirmed_at": ANCHOR}
    return {"fields": base, "buying_group": members, "relationship_state": rel}


def sig(kind: str, created_at: str, is_open: bool = True, subject: str | None = None) -> dict:
    return {"id": str(uuid.uuid4()), "signal_type": kind, "created_at": created_at, "occurred_at": created_at, "open": is_open, "subject_person_id": subject,
            "evidence_refs": [{"activity_id": ACT, "occurred_at": created_at}]}


def claim(path: str, occurred_at: str = "2026-09-12T00:00:00Z", subject: str | None = None, standing: str = "first_party_ai") -> dict:
    return {"id": str(uuid.uuid4()), "field_path": path, "occurred_at": occurred_at, "status": "active", "standing": standing,
            "source_activity_id": ACT, "subject_person_id": subject}


NEED, STAKEHOLDER, VALUE = claim("expansion_need"), claim("buying_group.member", subject=OTHER), claim("business_value")
CANDIDATE_CLAIMS = (NEED, STAKEHOLDER)
STABLE_OWNER = {"champion_since": fld("2026-09-05T00:00:00Z", "2026-09-05T00:00:00Z")}


def run(st: dict, signals: tuple = (), claims: tuple = CANDIDATE_CLAIMS, now: str = NOW,
        open_transition: dict | None = None, rules: dict = RULES, computed_at: str | None = None) -> Outcome:
    return evaluate(rules, Context(st, tuple(signals), tuple(claims), now, open_transition, computed_at))


def keys(facts: tuple) -> set[str]:
    return {f.key for f in facts}


def confirmed_case() -> tuple[dict, tuple, tuple]:
    return state(**STABLE_OWNER), (sig("stage_advanced", "2026-09-20T00:00:00Z"),), (NEED, STAKEHOLDER, VALUE)


def rival_rules(to_state: str = "RECOVERY") -> dict:
    """The v1 rule set plus a copy of REORG -> EXPANSION that targets another state, to exercise ambiguity."""
    rules = copy.deepcopy(RULES)
    rival = copy.deepcopy(rules["transitions"][0])
    rival.update(id=f"reorg_to_{to_state.lower()}", to_state=to_state)
    rules["transitions"].append(rival)
    return rules


def rival_gate_moved() -> dict:
    """rival_rules with the EXPANSION gate closed, so only the rival's gate holds."""
    rules = rival_rules()
    rules["transitions"][0]["candidate"]["requires_any"] = ["value_signal_present"]
    return rules


def deal(opportunity_id: str, is_open: bool = True, **fields: dict) -> dict:
    """One OpportunityState as the rules read it: the deal's own fields and buying group."""
    s = state(**fields)
    return {"opportunity_id": opportunity_id, "is_open": is_open, "fields": s["fields"], "buying_group": s["buying_group"]}


DEAL_A, DEAL_B = "0d0a0000-0000-4000-8000-00000000000a", "0d0b0000-0000-4000-8000-00000000000b"
