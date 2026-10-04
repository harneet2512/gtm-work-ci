"""Doubles for /v1/strategies, /v1/judge and /v1/revise: a scripted model that answers by schema name, a fake core
with every pull tool, and request builders. No live calls: nothing here reaches a network."""
from __future__ import annotations

import copy
import threading
from collections.abc import Mapping
from typing import Any

import draft_doubles as dbl
import judge_doubles as jd
from ghost_worker.llm.provider import LLMResult, TurnResult

RUN_ID = "0f0a0000-0000-4000-8000-000000000601"
ACCOUNT = jd.ACCOUNT
EPISODE = "0e9e0000-0000-4000-8000-000000000a01"
TRIGGER = jd.ACT_1
REP = jd.REP
CHAMPION, SECURITY = jd.PERSON_A, jd.PERSON_B
OUTSIDER = "0d000000-0000-4000-8000-0000000000ff"  # a person no pull returns
K_APPLIED = "0c17c000-0000-4000-8000-000000000017"
K_EXCEPTION = "0c17c000-0000-4000-8000-000000000021"
QUOTE = "Please send the security questionnaire answers."

PACKETS: dict[str, list[dict]] = {
    "state": [
        {"kind": "state_header", "account_id": ACCOUNT, "account_name": "Account One", "opportunity_id": None,
         "version": 3, "coverage_gaps": ["economic_buyer"]},
        {"field_path": "stage", "value": "Technical evaluation", "known": True, "evidence_refs": []},
        {"field_path": "champion", "value": CHAMPION, "known": True, "evidence_refs": []},
        {"field_path": "champion_status", "value": "active", "known": True, "evidence_refs": []},
        {"field_path": "motion", "value": "expansion", "known": True, "evidence_refs": []},
        {"field_path": "next_milestone", "value": "Security team reads the questionnaire", "known": True,
         "evidence_refs": []},
    ],
    "people": [jd._member(CHAMPION, "Person A", "champion"), jd._member(SECURITY, "Person B", "security")],
    "activities": [
        {"activity_id": TRIGGER, "activity_type": "EmailReply", "occurred_at": "2026-09-29T15:00:00Z",
         "is_trigger": True, "excerpt": jd.REQUEST, "participants": [{"person_id": SECURITY, "role": "from"}]},
        {"activity_id": jd.ACT_2, "activity_type": "EmailReply", "occurred_at": "2026-09-28T09:00:00Z",
         "is_trigger": False, "summary": "Person B leads security.",
         "participants": [{"person_id": CHAMPION, "role": "from"}]},
    ],
    "recent_diffs": [{"version": 3, "material": True,
                      "changes": [{"field": "buying_group", "op": "added", "material": True}]}],
    "commitments": [{"text": "Send the security questionnaire answers", "owner_person_id": REP,
                     "due_at": "2026-09-30T17:00:00Z", "status": "open"}],
    "evidence": [],
}


def fake_core() -> dbl.FakeCore:
    return dbl.FakeCore(copy.deepcopy(PACKETS))


def ev(quote: str | None = QUOTE) -> dict[str, Any]:
    full = dbl.ev(TRIGGER, None, quote, SECURITY, "2026-09-29T15:00:00Z")
    return {k: v for k, v in full.items() if v is not None}


FIVE = {"what_changed": "Person B asked for the questionnaire answers.",
        "why_state_changed": "Person B wrote to us first-party in the latest email.",
        "what_remains_unknown": "When the security team can review the answers.",
        "prior_knowledge_applies": "No prior knowledge applies.",
        "why_next_action": "It moves the security review forward."}
CLASS_OF = {"send_email": "REPLY", "schedule_meeting": "MEETING", "share_document": "SHARE_DOCUMENT",
            "internal_note": "INTERNAL_TASK", "wait": "WAIT", "no_action": "NO_ACTION"}


def planned(strategy_type: str, action: str = "send_email", *, to=((SECURITY, "asked"),),
            cc=((CHAMPION, "champion"),), knowledge: list[str] | None = None,
            action_class: str | None = None) -> dict[str, Any]:
    return {"strategy_type": strategy_type, "title": strategy_type.replace("_", " ").title(),
            "description": f"{strategy_type}: one line of intent", "rationale": f"{strategy_type} is plausible now.",
            "five_questions": dict(FIVE), "action": action, "action_class": action_class or CLASS_OF[action],
            "to": dbl.people(*to), "cc": dbl.people(*cc), "state_refs": ["blockers"],
            "evidence_refs": [ev()], "knowledge_refs_used": knowledge or []}


def plan_turn(*strategies: dict[str, Any]) -> TurnResult:
    return TurnResult(content={"strategies": list(strategies)}, model=dbl.MODEL, usage={})


def artifact(body: str, subject: str = "Re: security questionnaire", channel: str = "email") -> dict[str, Any]:
    return {"channel": channel, "subject": subject, "body": body, "attachments": []}


DISTINCT_BODIES = {
    "send_package_and_wait": "Hi Person B,\n\nAttached are the questionnaire answers. Take the time your team needs "
                             "and tell us when a review date works.\n\nBest,\nDana",
    "add_security_lead": "Hi Person B,\n\nI would like to introduce our security architect, who can walk your "
                         "engineers through the encryption design and residency options.\n\nBest,\nDana",
    "clarify_scope_first": "Hi Person B,\n\nBefore sending anything more, which regions and data classes should the "
                           "answers cover? Your reply lets us scope the response precisely.\n\nBest,\nDana"}


def default_plan() -> TurnResult:
    return plan_turn(planned("send_package_and_wait"), planned("add_security_lead", to=((SECURITY, "lead"),),
                                                              cc=((CHAMPION, "keep informed"),)),
                     planned("clarify_scope_first", cc=()))


def artifacts_json(bodies: Mapping[str, str] = DISTINCT_BODIES) -> dict[str, Any]:
    return {"artifacts": [{"strategy_type": t, "artifact": artifact(b)} for t, b in bodies.items()]}


class ScriptedStrategyProvider:
    """Plays agent turns in order and answers complete_json by schema name (a list is consumed in order);
    unscripted judge schemas get a generic passing answer. Records every call."""

    def __init__(self, turns: list[TurnResult], json_answers: Mapping[str, Any] | None = None) -> None:
        self.turns = list(turns)
        self.answers = {k: (list(v) if isinstance(v, list) else v) for k, v in (json_answers or {}).items()}
        self.turn_calls: list[dict[str, Any]] = []
        self.json_calls: list[dict[str, Any]] = []
        self._lock = threading.Lock()

    def complete_turn(self, *, system: str, messages: list[dict], tools: list[dict], schema: dict,
                      schema_name: str) -> TurnResult:
        with self._lock:
            self.turn_calls.append({"system": system, "messages": copy.deepcopy(messages), "schema_name": schema_name})
            return self.turns.pop(0)

    def complete_json(self, *, system: str, user: str, schema: dict, schema_name: str) -> LLMResult:
        with self._lock:
            self.json_calls.append({"system": system, "user": user, "schema_name": schema_name})
            scripted = self.answers.get(schema_name)
            if isinstance(scripted, list):
                scripted = scripted.pop(0)
            if scripted is None:
                scripted = next((jd.answer(r) for r in jd.RUBRICS.values() if r.schema_name == schema_name), None)
            assert scripted is not None, f"no scripted answer for {schema_name}"
        if isinstance(scripted, Exception):
            raise scripted
        return LLMResult(content=copy.deepcopy(scripted), model=dbl.MODEL, usage={})


def strategies_request(guidance: dict | None = None) -> dict[str, Any]:
    body: dict[str, Any] = {
        "run_id": RUN_ID, "account_id": ACCOUNT, "decision_episode_id": EPISODE,
        "workflow": "post_interaction_followup",
        "trigger_context": {"trigger_activity_ids": [TRIGGER], "signal_types": ["customer_replied"],
                            "reason_codes": ["eligible_customer_replied"], "rep_person_id": REP},
        "state_header": "Account One (expansion, Technical evaluation). Person B asked for the questionnaire.",
        "run_token": dbl.RUN_TOKEN, "candidate_count": 3}
    if guidance is not None:
        body["decision_guidance"] = guidance
    return body


def candidate_json(ranking: int = 1, strategy_type: str = "send_package_and_wait", **over: Any) -> dict[str, Any]:
    body = DISTINCT_BODIES.get(strategy_type, "Hi Person B,\n\nHere are the answers.\n\nBest,\nDana")
    cand = {"candidate_id": f"0ca00000-0000-4000-8000-0000000000a{ranking}", "strategy_type": strategy_type,
            "title": "Send package", "description": "Send the answers and let the buyer set timing.",
            "ranking": ranking, "preferred_by_agent": ranking == 1, "rationale": "Person B asked for the answers.",
            "state_refs": ["next_milestone"], "evidence_refs": [ev()], "knowledge_refs": [],
            "action_type": "send_email", "action_class": "REPLY", "five_questions": dict(FIVE),
            "to": [{"person_id": SECURITY, "role": "to", "why": "asked"}],
            "cc": [{"person_id": CHAMPION, "role": "cc", "why": "champion"}], "subject": "Re: security questionnaire",
            "full_action_artifact": artifact(body), "preview": "Attached are the questionnaire answers."}
    cand.update(over)
    return cand
