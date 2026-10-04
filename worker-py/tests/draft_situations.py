"""Hand-written account-agent situations: request, fake-core packets, and the scripted model turns.

The scripted turns are NOT a model: they are the responses an agent is *expected* to give, written by hand from
the seed world (Acme / Beta / Northstar) and the contracts/examples story (HAR-97 §16).
scripts/seed_draft_cassettes.py runs each situation once through a RecordingProvider wrapped around
ScriptedProvider to write cassettes/draft/<key>.json; tests then replay those cassettes with FakeProvider and a
fake core.
"""
from __future__ import annotations

import copy
from dataclasses import dataclass
from typing import Any

from draft_doubles import (
    RUN_TOKEN,
    call,
    decision_turn,
    ev,
    people,
    skill_output,
    tools_turn,
    uid,
)
from ghost_worker.llm.provider import TurnResult
from test_contracts import example

RUN_ID = "0f0a0000-0000-4000-8000-000000000601"
K17 = "0c17c000-0000-4000-8000-000000000017"
K21 = "0c17c000-0000-4000-8000-000000000021"
DANA = uid("0b0e0000", 1)

ACME = uid("0a0c0000", 1)
ACME_CRM_ACT = uid("0ac70000", 100)
ACME_EMAIL_ACT = uid("0ac70000", 101)
ACME_CLAIM = uid("0c1a0000", 201)
MARCO = uid("0b0e0000", 18)
PRIYA = uid("0b0e0000", 17)
MARCO_QUOTE = ("Before we can sign off on the EU rollout we'll need your SOC2 Type II report "
               "and the pen-test summary.")
PRIYA_QUOTE = "Priya will stay the point of contact on the commercial side."

BETA = uid("0a0c0000", 2)
BETA_ACT = uid("0ac70000", 201)
BETA_STATE_ACT = uid("0ac70000", 200)
BETA_OWNER = uid("0b0e0000", 31)

NORTHSTAR = uid("0a0c0000", 3)
NS_ACT = uid("0ac70000", 301)
NS_CLAIM = uid("0c1a0000", 301)
ELENA = uid("0b0e0000", 41)
SAM = uid("0b0e0000", 42)
ELENA_QUOTE = "I'm handing the evaluation to Sam; please work with him from here."

TUESDAY = "2026-10-06T09:00:00Z"


def request_body(account_id: str, trigger_activity: str, *, signals: list[str], reasons: list[str],
                 header: str, guidance: dict | None = None) -> dict[str, Any]:
    body: dict[str, Any] = {
        "run_id": RUN_ID, "account_id": account_id, "workflow": "post_interaction_followup",
        "trigger_context": {"trigger_activity_ids": [trigger_activity], "signal_types": signals,
                            "reason_codes": reasons, "rep_person_id": DANA},
        "state_header": header, "run_token": RUN_TOKEN,
    }
    if guidance is not None:
        body["decision_guidance"] = guidance
    return body


# --- fake core packets -------------------------------------------------------------------------

ACME_PACKETS: dict[str, list[dict]] = {
    "state": [
        {"field_path": "stage", "value": "Technical evaluation", "known": True,
         "evidence_refs": [{"activity_id": ACME_CRM_ACT}]},
        {"field_path": "motion", "value": "expansion", "known": True,
         "evidence_refs": [{"activity_id": ACME_CRM_ACT}]},
        {"field_path": "champion", "value": PRIYA, "known": True,
         "evidence_refs": [{"activity_id": ACME_CRM_ACT}]},
        {"field_path": "champion_status", "value": "active", "known": True,
         "evidence_refs": [{"activity_id": ACME_EMAIL_ACT, "claim_id": ACME_CLAIM, "quote": PRIYA_QUOTE}]},
        {"field_path": "blockers", "value": "SOC2 Type II report and pen-test summary outstanding", "known": True,
         "evidence_refs": [{"activity_id": ACME_EMAIL_ACT, "claim_id": ACME_CLAIM, "quote": MARCO_QUOTE}]},
    ],
    "people": [
        {"person_id": MARCO, "display_name": "Marco Silva", "title": "Head of Security", "side": "external",
         "buying_group_role": "security", "engagement": "new"},
        {"person_id": PRIYA, "display_name": "Priya Raman", "title": "VP Engineering", "side": "external",
         "buying_group_role": "champion", "engagement": "active"},
        {"person_id": DANA, "display_name": "Dana Whitfield", "title": "Account Executive", "side": "internal"},
    ],
    "evidence": [
        {"claim_id": ACME_CLAIM, "field_path": "blockers", "quote": MARCO_QUOTE, "activity_id": ACME_EMAIL_ACT,
         "speaker_person_id": MARCO},
    ],
    "activities": [
        {"activity_id": ACME_EMAIL_ACT, "activity_type": "EmailReceived", "occurred_at": "2026-09-29T15:42:00Z",
         "summary": "Marco (security) asks for the SOC2 Type II report and pen-test summary; says Priya stays "
                    "the commercial point of contact."},
    ],
}

BETA_PACKETS: dict[str, list[dict]] = {
    "state": [
        {"field_path": "stage", "value": "Proposal", "known": True,
         "evidence_refs": [{"activity_id": BETA_STATE_ACT}]},
        {"field_path": "owner", "value": BETA_OWNER, "known": True,
         "evidence_refs": [{"activity_id": BETA_STATE_ACT}]},
    ],
    "recent_diffs": [
        {"version": 4, "material": False, "changes": [],
         "note": "Calendar invite moved by 30 minutes; no state field changed.",
         "activity_id": BETA_ACT},
    ],
}

NORTHSTAR_PACKETS: dict[str, list[dict]] = {
    "state": [
        {"field_path": "motion", "value": "expansion", "known": True, "evidence_refs": []},
        {"field_path": "champion", "value": ELENA, "known": True, "evidence_refs": []},
        {"field_path": "champion_status", "value": "active", "known": True, "evidence_refs": []},
        {"field_path": "delegation", "value": f"{ELENA} -> {SAM}", "known": True,
         "evidence_refs": [{"activity_id": NS_ACT, "claim_id": NS_CLAIM, "quote": ELENA_QUOTE}]},
    ],
    "people": [
        {"person_id": ELENA, "display_name": "Elena Petrova", "title": "Director of IT", "side": "external",
         "buying_group_role": "champion", "engagement": "delegated_away"},
        {"person_id": SAM, "display_name": "Sam Okafor", "title": "Security Engineer", "side": "external",
         "buying_group_role": "technical_evaluator", "engagement": "active"},
    ],
    "evidence": [
        {"claim_id": NS_CLAIM, "field_path": "delegation", "quote": ELENA_QUOTE, "activity_id": NS_ACT,
         "speaker_person_id": ELENA},
    ],
}


def northstar_guidance() -> dict[str, Any]:
    """K17 matched on motion/champion, but its 'champion explicitly delegated' exception is triggered."""
    guidance = copy.deepcopy(example("decision_guidance"))
    guidance.update({
        "id": uid("06d1de00", 3001), "account_id": NORTHSTAR, "recommended_action": "send_email",
        "why_now": "Sam entered the thread, but Elena explicitly delegated the evaluation to him.",
        "who_to_involve": people((SAM, "Delegated owner of the evaluation")),
        "who_not_to_involve": [], "not_recommended": [], "created_at": "2026-09-29T16:00:12Z",
    })
    guidance["supporting_knowledge"] = [{
        "knowledge_id": K17, "applies": False,
        "matched_conditions": ["motion eq expansion", "champion_status eq active"],
        "unmatched_conditions": ["no explicit delegation"],
        "exceptions_checked": [{
            "exception": "Champion explicitly delegated ownership", "triggered": True,
            "evidence_refs": [{"activity_id": NS_ACT, "quote": ELENA_QUOTE, "speaker_person_id": ELENA,
                               "occurred_at": "2026-09-29T15:50:00Z"}]}],
    }]
    return guidance


def wait_guidance() -> dict[str, Any]:
    """HAR-97 §16: 'WAIT until Tuesday; do not generate an outbound CTA today'."""
    guidance = copy.deepcopy(example("decision_guidance"))
    guidance.update({
        "id": uid("06d1de00", 3002), "recommended_action": "wait", "wait_until": TUESDAY,
        "why_now": "Marco is reviewing the package internally; a new ask today would crowd him.",
        "not_recommended": [{"action": "Outbound CTA today", "why": "The next step belongs to Marco's side."}],
        "who_to_involve": [], "who_not_to_involve": people((MARCO, "No outbound CTA before Tuesday")),
        "recommended_channel": "none", "ask_strength": "none",
    })
    guidance["supporting_knowledge"] = [{
        "knowledge_id": K21, "applies": True, "matched_conditions": ["security review in progress"],
        "unmatched_conditions": [], "exceptions_checked": [
            {"exception": "Customer asked for a call", "triggered": False, "evidence_refs": []}]}]
    return guidance


# --- situations --------------------------------------------------------------------------------

@dataclass(frozen=True)
class Situation:
    name: str
    request: dict[str, Any]
    packets: dict[str, list[dict]]
    turns: tuple[TurnResult, ...]
    skill: dict[str, Any] | None = None

    def request_copy(self) -> dict[str, Any]:
        return copy.deepcopy(self.request)


ACME_HEADER = ("Acme Corp (expansion, Technical evaluation, health at_risk). Champion Priya Raman (active). "
               "Changed: Marco Silva (security) entered the buying group and asked for SOC2 + pen-test.")
ACME_PULLS = (tools_turn(call(1, "state"), call(2, "people")),
              tools_turn(call(3, "evidence", field_path="blockers"), call(4, "activities", limit=5)))
ACME_EVIDENCE = [ev(ACME_EMAIL_ACT, ACME_CLAIM, MARCO_QUOTE, MARCO, "2026-09-29T15:42:00Z")]
MARCO_WHY = "Requested the security documents"
PRIYA_WHY = "Active champion; keep commercial continuity"


def _acme(guidance: dict | None) -> dict[str, Any]:
    return request_body(ACME, ACME_EMAIL_ACT, signals=["new_stakeholder_entered"],
                        reasons=["eligible_stakeholder_change"], header=ACME_HEADER, guidance=guidance)


ACME_UNGUIDED = Situation(
    name="acme_unguided",
    request=_acme(None),
    packets=ACME_PACKETS,
    turns=(*ACME_PULLS, decision_turn(
        "schedule_meeting", "Marco asked for the security package; offer him a call to walk through it.",
        involve=((MARCO, MARCO_WHY),), evidence=ACME_EVIDENCE)),
    skill=skill_output(
        recipients=[(MARCO, "to", MARCO_WHY)], subject="Security review call",
        body=("Hi Marco,\n\nThanks for flagging the SOC2 and pen-test requirements. Could we grab 30 minutes "
              "this week to walk through them together?\n\nBest,\nDana"),
        next_step="Book security review call with Marco", evidence=ACME_EVIDENCE,
        reason="Marco is the active voice on the security review; a call is the fastest way to unblock."),
)

ACME_GUIDED = Situation(
    name="acme_guided_k17",
    request=_acme(example("decision_guidance")),
    packets=ACME_PACKETS,
    turns=(*ACME_PULLS, decision_turn(
        "send_email",
        "K17 applies: motion is expansion, Priya is the active champion, and Marco is a new stakeholder; "
        "no delegation exception is triggered. Send the package to Marco with Priya on cc.",
        involve=((MARCO, MARCO_WHY), (PRIYA, PRIYA_WHY)),
        used_guidance=True, knowledge=[K17], evidence=ACME_EVIDENCE)),
    skill=skill_output(
        recipients=[(MARCO, "to", MARCO_WHY), (PRIYA, "cc", PRIYA_WHY)],
        subject="Re: Expansion to EU teams",
        body=("Hi Marco,\n\nThanks for jumping in. Attached are our SOC2 Type II report and the latest pen-test "
              "summary. Happy to set up time with our security team once you've reviewed - whatever timing "
              "works for you and Priya.\n\nBest,\nDana"),
        next_step="Security review of SOC2 package by Acme security (Marco)", evidence=ACME_EVIDENCE,
        reason="Marco asked for SOC2 + pen-test before any review date; Priya stays commercial point of contact."),
)

ACME_GUIDED_WAIT = Situation(
    name="acme_guided_wait_until_tuesday",
    request=_acme(wait_guidance()),
    packets=ACME_PACKETS,
    turns=(tools_turn(call(1, "state"), call(2, "people")),
           decision_turn(
               "wait", "Guidance says Marco is reviewing internally: wait until Tuesday and send no outbound CTA "
                       "today.",
               avoid=((MARCO, "No outbound CTA before Tuesday"),), used_guidance=True, knowledge=[K21],
               wait_until=TUESDAY, evidence=ACME_EVIDENCE)),
)

BETA_NON_MATERIAL = Situation(
    name="beta_non_material",
    request=request_body(BETA, BETA_ACT, signals=[], reasons=["eligible_meeting_completed"],
                         header="Beta Inc (new business, Proposal). Changed: calendar invite time moved."),
    packets=BETA_PACKETS,
    turns=(tools_turn(call(1, "state"), call(2, "recent_diffs")),
           decision_turn("no_action", "The only change is a 30 minute calendar move; state and commitments are "
                                      "unchanged, so there is nothing to follow up on.")),
)

NORTHSTAR_DELEGATION = Situation(
    name="northstar_delegation_exception",
    request=request_body(
        NORTHSTAR, NS_ACT, signals=["champion_delegated"], reasons=["eligible_stakeholder_change"],
        header="Northstar (expansion). Elena Petrova (champion) delegated the evaluation to Sam Okafor.",
        guidance=northstar_guidance()),
    packets=NORTHSTAR_PACKETS,
    turns=(tools_turn(call(1, "state"), call(2, "people")),
           tools_turn(call(3, "evidence", field_path="delegation")),
           decision_turn(
               "send_email",
               "K17 matched but its exception is triggered: Elena explicitly delegated ownership to Sam, so I do "
               "not pull her back in; write to Sam.",
               involve=((SAM, "Delegated owner of the evaluation"),), used_guidance=False,
               evidence=[ev(NS_ACT, NS_CLAIM, ELENA_QUOTE, ELENA, "2026-09-29T15:50:00Z")])),
    skill=skill_output(
        recipients=[(SAM, "to", "Delegated owner of the evaluation")],
        subject="Next steps on the Northstar evaluation",
        body="Hi Sam,\n\nElena mentioned you are leading the evaluation now. Here is what we suggest next...\n\n"
             "Best,\nDana",
        next_step="Confirm evaluation plan with Sam",
        evidence=[ev(NS_ACT, NS_CLAIM, ELENA_QUOTE, ELENA, "2026-09-29T15:50:00Z")],
        reason="Elena delegated the evaluation to Sam in writing; the K17 champion-continuity exception applies."),
)

SITUATIONS: dict[str, Situation] = {s.name: s for s in (
    ACME_UNGUIDED, ACME_GUIDED, ACME_GUIDED_WAIT, BETA_NON_MATERIAL, NORTHSTAR_DELEGATION)}
