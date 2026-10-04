"""A scripted stand-in for the model, for REHEARSING the whole stack at zero live calls (python -m bench.uplift rehearse).

It is not a result and nothing it says is ever reported: it lets the real worker, the real core pull API, the real
orchestrator and the real validators run end to end on the real worlds, so integration bugs are found before the scarce
daily free-model request cap is spent. It answers the planner with tool calls and then a plan that follows the guidance
when the guidance says something applies (holds the price) and otherwise re-prices, and the drafter with templated text.
"""
from __future__ import annotations

import json
import re
from typing import Any

from ghost_worker.app import create_app as _create_app
from ghost_worker.llm.provider import LLMResult, ToolCall, TurnResult

APPLIES = re.compile(r'"applies":\s*true.*?"knowledge_id":\s*"([0-9a-f-]{36})"', re.S)
UUID = re.compile(r"[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}")


def _items(messages: list[dict[str, Any]]) -> dict[str, list[dict[str, Any]]]:
    """Tool results by tool name."""
    names: dict[str, str] = {}
    out: dict[str, list[dict[str, Any]]] = {}
    for m in messages:
        for call in m.get("tool_calls") or []:
            names[call["id"]] = call["function"]["name"]
        if m.get("role") == "tool":
            payload = json.loads(m["content"])
            out.setdefault(names.get(m["tool_call_id"], payload.get("tool", "?")), []).extend(payload.get("items", []))
    return out


def _guided(messages: list[dict[str, Any]]) -> list[str]:
    text = messages[0]["content"]
    return APPLIES.findall(text.split("Decision Guidance", 1)[1]) if "Decision Guidance" in text and '"applies": true' in text else []


def _uuid(value: Any) -> str | None:
    return value if isinstance(value, str) and UUID.fullmatch(value) else None


def _walk(node: Any):
    stack = [node]
    while stack:
        n = stack.pop()
        if isinstance(n, dict):
            for k, v in n.items():
                yield k, v
                stack.append(v)
        elif isinstance(n, list):
            stack.extend(n)


class ScriptedProvider:
    """Implements the worker's LLMProvider protocol with canned answers; deterministic."""

    def complete_turn(self, *, system: str, messages: list[dict[str, Any]], tools: list[dict[str, Any]],
                      schema: dict[str, Any], schema_name: str) -> TurnResult:
        results = _items(messages)
        if tools and not results:
            return TurnResult(model="scripted/model", tool_calls=(
                ToolCall(id="c1", name="people", arguments={}), ToolCall(id="c2", name="activities", arguments={}),
                ToolCall(id="c3", name="state", arguments={})))
        people = [u for i in results.get("people", []) if (u := _uuid(i.get("person_id")))]
        owner = next((u for i in results.get("state", []) for k, v in _walk(i) if k == "value" and (u := _uuid(v))), None)
        activity = next((u for i in results.get("activities", []) if (u := _uuid(i.get("activity_id")))), None)
        buyer = people[0]
        rep = owner or buyer
        colleague = rep
        guided = _guided(messages)
        evidence = [{"activity_id": activity, "quote": "scripted"}]
        five = {k: "scripted answer" for k in ("what_changed", "why_state_changed", "what_remains_unknown",
                                               "prior_knowledge_applies", "why_next_action")}
        top = ("reinforce_value_case", "Reinforce the value case", "Hold the quoted price and walk through the return") if guided \
            else ("offer_price_concession", "Offer a price concession", "Reduce the quoted amount to keep the deal moving")
        strategies = [
            {"strategy_type": top[0], "title": top[1], "description": top[2], "rationale": "scripted", "five_questions": five,
             "action": "send_email", "action_class": "REPLY", "to": [{"person_id": buyer, "why": "the buyer"}], "cc": [],
             "state_refs": ["stage"], "evidence_refs": evidence, "knowledge_refs_used": guided},
            {"strategy_type": "schedule_walkthrough_call", "title": "Schedule a walkthrough", "description": "Offer a call with a colleague",
             "rationale": "scripted", "five_questions": five, "action": "schedule_meeting", "action_class": "MEETING",
             "to": [{"person_id": buyer, "why": "the buyer"}], "cc": [{"person_id": colleague, "why": "a colleague"}],
             "state_refs": ["stage"], "evidence_refs": evidence, "knowledge_refs_used": []},
            {"strategy_type": "ask_internal_owner", "title": "Ask the account owner", "description": "Check the budget cycle",
             "rationale": "scripted", "five_questions": five, "action": "internal_note", "action_class": "ASK_RESEARCH",
             "to": [{"person_id": rep, "why": "the owner"}], "cc": [], "state_refs": ["stage"], "evidence_refs": evidence,
             "knowledge_refs_used": []}]
        return TurnResult(model="scripted/model", content={"strategies": strategies})

    def complete_json(self, *, system: str, user: str, schema: dict[str, Any], schema_name: str) -> LLMResult:
        plan = json.loads(user.split("Strategies (best first):\n", 1)[1].split("\n\n", 1)[0])
        arts = []
        for s in plan:
            channel = "slack" if s["action"] == "internal_note" else "email"
            arts.append({"strategy_type": s["strategy_type"], "artifact": {
                "channel": channel, "subject": None if channel != "email" else f"Re: {s['title']}",
                "body": f"Hi, {s['description']}. ({s['strategy_type'].replace('_', ' ')})", "attachments": []}})
        return LLMResult(model="scripted/model", content={"artifacts": arts})


def create_app():  # uvicorn --factory target
    return _create_app(provider=ScriptedProvider())
