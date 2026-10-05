"""Neutral test inputs and doubles for the semantic judges (no invented companies: 'Account One', 'Person A').

`neutral_case()` is a minimal eval-case-shaped dict; `context()` builds a JudgeContext from it with overrides;
`answer()` builds a complete judge answer for a rubric; ScriptedJudgeProvider replays answers by schema name and
records every call (thread-safe)."""
from __future__ import annotations

import copy
import threading
from collections.abc import Mapping
from typing import Any

from ghost_worker.judges import JudgeContext, load_catalog, load_rubrics
from ghost_worker.judges.rubric import Rubric
from ghost_worker.llm.provider import LLMResult

CATALOG = load_catalog()
RUBRICS = load_rubrics(CATALOG)
MODEL = "deepseek/deepseek-v4-flash"
RUN_ID = "0d000000-0000-4000-8000-0000000000aa"
ACCOUNT = "0d000000-0000-4000-8000-000000000001"
PERSON_A = "0d000000-0000-4000-8000-000000000011"  # champion
PERSON_B = "0d000000-0000-4000-8000-000000000012"  # security reviewer
REP = "0d000000-0000-4000-8000-000000000021"
ACT_1 = "0d000000-0000-4000-8000-000000000101"
ACT_2 = "0d000000-0000-4000-8000-000000000102"
K_1 = "0d000000-0000-4000-8000-000000000301"
REQUEST = "Please send the security questionnaire answers. I cannot commit to a review date until my team has read them."


def _member(person_id: str, name: str, role: str) -> dict[str, Any]:
    return {"person_id": person_id, "display_name": name, "title": None, "roles": [role], "status": "active",
            "delegated_to_person_id": None, "last_engaged_at": None, "evidence_refs": []}


def neutral_case() -> dict[str, Any]:
    state = {"account_id": ACCOUNT, "account_name": "Account One", "opportunity_id": None,
             "fields": {"stage": "Technical evaluation", "health": "on_track", "motion": "expansion",
                        "champion": PERSON_A, "champion_status": "active", "economic_buyer": "unknown",
                        "relationship_risk": "low",
                        "blockers": [{"text": "Security review of the questionnaire", "status": "open"}],
                        "decision_process": "Security sign-off, then commercial approval",
                        "next_milestone": "Security team reads the questionnaire", "next_meeting": None,
                        "current_commitments": []},
             "buying_group": [_member(PERSON_A, "Person A", "champion"), _member(PERSON_B, "Person B", "security")],
             "coverage_gaps": ["economic_buyer"]}
    trigger = {"activity_id": ACT_1, "activity_type": "EmailReply", "occurred_at": "2026-09-29T15:00:00Z",
               "actor_person_id": PERSON_B, "event_file": "neutral.json", "synthetic": True, "text": REQUEST}
    support = {"activity_id": ACT_2, "activity_type": "EmailReply", "occurred_at": "2026-09-28T09:00:00Z",
               "actor_person_id": PERSON_A, "event_file": None, "synthetic": True,
               "text": "Person B leads security and will review the answers."}
    commitments = [{"text": "Send the security questionnaire answers", "owner_person_id": REP,
                    "due_at": "2026-09-30T17:00:00Z", "status": "open"}]
    action = {"proposed_action_type": "send_email",
              "recipients": [{"person_id": PERSON_B, "role": "to", "why": "asked for the answers"},
                             {"person_id": PERSON_A, "role": "cc", "why": "champion"}],
              "finished_artifact": {"channel": "email", "subject": "Security questionnaire",
                                    "body": "Hi, the answers are attached. Can we sign the order form on Friday?",
                                    "attachments": ["answers.pdf"]},
              "crm_next_step_intent": {"next_step": "Security review", "due_at": None, "stage_change": None},
              "reason": "Person B asked for the answers.",
              "evidence_refs": [{"activity_id": ACT_1, "quote": "Please send the security questionnaire answers."}],
              "knowledge_refs_used": [], "wait_until": None}
    context = {"now": "2026-09-29T15:30:00Z", "state": state,
               "recent_changes": {"summary": "Person B asked for the questionnaire answers.",
                                  "material_diff_fields": ["buying_group"], "signals": ["customer_replied"]},
               "trigger": trigger, "supporting_activities": [support], "commitments": commitments,
               "timeline_facts": {"last_inbound_at": "2026-09-29T15:00:00Z", "last_outbound_at": "2026-09-26T10:00:00Z",
                                  "stated_timing": "no review date until the answers are read"},
               "offered_knowledge": []}
    return {"id": "neutral_case", "title": "GOLD-TITLE-MUST-NOT-LEAK", "notes": "GOLD-NOTES-MUST-NOT-LEAK",
            "context": context, "candidate_action": action,
            "expected": [{"eval_type": "buyer_readiness", "rationale": "GOLD-RATIONALE-MUST-NOT-LEAK"}]}


def context(*, action: Mapping[str, Any] | None = None, fields: Mapping[str, Any] | None = None,
            **context_overrides: Any) -> JudgeContext:
    case = neutral_case()
    if action:
        case["candidate_action"] = {**case["candidate_action"], **action}
    if fields:
        case["context"]["state"]["fields"].update(fields)
    case["context"].update(copy.deepcopy(context_overrides))
    return JudgeContext.from_eval_case(case)


def answer(rubric: Rubric, *, verdict: str = "pass", label: str | None = "PASS_LABEL", **overrides: Any) -> dict[str, Any]:
    """A complete, valid answer for `rubric` (label defaults to the rubric's pass label)."""
    criteria = {c.id: {"finding": "met", "direction": "matches" if c.output == "direction_magnitude" else None,
                       "magnitude": "none" if c.output == "direction_magnitude" else None, "note": "ok"}
                for c in rubric.criteria}
    body = {"verdict": verdict, "label": rubric.pass_label if label == "PASS_LABEL" else label, "diagnostics": [],
            "blocks": False, "reason": "Person B asked for the answers and cannot commit to a date yet.",
            "criteria": criteria, "state_refs": ["next_milestone"],
            "evidence_refs": [{"activity_id": ACT_1, "quote": "I cannot commit to a review date"}],
            "knowledge_refs": [], "suggested_correction": None, "confidence": 0.8}
    return {**body, **overrides}


class ScriptedJudgeProvider:
    """Returns the scripted content (or raises the scripted exception) per schema name; records calls."""

    def __init__(self, answers: Mapping[str, Any], model: str = MODEL) -> None:
        self.answers = dict(answers)
        self.model = model
        self.calls: list[dict[str, Any]] = []
        self._lock = threading.Lock()

    def complete_json(self, *, system: str, user: str, schema: dict[str, Any], schema_name: str) -> LLMResult:
        with self._lock:
            self.calls.append({"system": system, "user": user, "schema": schema, "schema_name": schema_name,
                               "thread": threading.current_thread().name})
        scripted = self.answers[schema_name]
        if isinstance(scripted, Exception):
            raise scripted
        return LLMResult(content=scripted, model=self.model)


def scripted(*pairs: tuple[str, dict[str, Any]]) -> ScriptedJudgeProvider:
    return ScriptedJudgeProvider({RUBRICS[name].schema_name: content for name, content in pairs})
