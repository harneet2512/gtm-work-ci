"""Test doubles and builders for the account agent: a scripted model, a fake core, strict-schema builders."""
from __future__ import annotations

import copy
import json
from typing import Any
from urllib.parse import urlparse

import httpx

from ghost_worker.llm.provider import LLMResult, ToolCall, TurnResult

MODEL = "deepseek/deepseek-v4-flash"
CORE_URL = "http://127.0.0.1:8080"
RUN_TOKEN = "run-token-" + "k" * 32
SKILL_SCHEMA_NAME = "post_interaction_followup_v1"

Person = tuple[str, str]  # (person_id, why)


def uid(prefix: str, n: int) -> str:
    return f"{prefix}-0000-4000-8000-{n:012d}"


def ev(activity_id: str, claim_id: str | None = None, quote: str | None = None, speaker: str | None = None,
       at: str | None = None) -> dict[str, Any]:
    """Strict-schema evidence reference (every key present, optional ones null)."""
    return {"activity_id": activity_id, "claim_id": claim_id, "quote": quote, "speaker_person_id": speaker,
            "occurred_at": at}


def people(*pairs: Person) -> list[dict[str, str]]:
    return [{"person_id": p, "why": w} for p, w in pairs]


def call(n: int, name: str, **arguments: Any) -> ToolCall:
    return ToolCall(id=f"call_{n}", name=name, arguments=arguments)


def tools_turn(*calls: ToolCall) -> TurnResult:
    return TurnResult(tool_calls=calls, model=MODEL, usage={"total_tokens": 100})


def decision_turn(action: str, why_now: str, *, involve: tuple[Person, ...] = (), avoid: tuple[Person, ...] = (),
                  used_guidance: bool = False, knowledge: list[str] | None = None, wait_until: str | None = None,
                  evidence: list[dict] | None = None) -> TurnResult:
    content = {"action": action, "why_now": why_now, "who_to_involve": people(*involve),
               "who_not_to_involve": people(*avoid), "used_guidance": used_guidance,
               "knowledge_refs_used": knowledge or [], "wait_until": wait_until, "evidence_refs": evidence or []}
    return TurnResult(content=content, model=MODEL, usage={"total_tokens": 120})


def skill_output(*, recipients: list[tuple[str, str, str]], channel: str = "email", subject: str,
                 body: str, next_step: str, reason: str, evidence: list[dict]) -> dict[str, Any]:
    return {
        "recipients": [{"person_id": p, "role": r, "why": w} for p, r, w in recipients],
        "finished_artifact": {"channel": channel, "subject": subject, "body": body, "attachments": []},
        "crm_next_step_intent": {"next_step": next_step, "due_at": None, "stage_change": None},
        "reason": reason, "evidence_refs": evidence,
    }


class ScriptedProvider:
    """Plays scripted turns and skill output; authors cassettes and spies on what each step was shown."""

    def __init__(self, turns: list[TurnResult], skill: dict[str, Any] | None = None) -> None:
        self._turns = list(turns)
        self._skill = skill
        self.turn_calls: list[dict[str, Any]] = []
        self.json_calls: list[dict[str, Any]] = []

    def complete_turn(self, *, system: str, messages: list[dict], tools: list[dict], schema: dict,
                      schema_name: str) -> TurnResult:
        self.turn_calls.append({"system": system, "messages": copy.deepcopy(messages), "tools": tools,
                                "schema_name": schema_name})
        return self._turns.pop(0)

    def complete_json(self, *, system: str, user: str, schema: dict, schema_name: str) -> LLMResult:
        self.json_calls.append({"system": system, "user": user, "schema_name": schema_name})
        assert self._skill is not None, "situation has no skill output"
        return LLMResult(content=copy.deepcopy(self._skill), model=MODEL, usage={"total_tokens": 200})


class FakeCore:
    """Stands in for core GET /internal/ctx/{tool}: checks the run token, serves bounded packets, logs requests."""

    def __init__(self, packets: dict[str, list[dict]], *, token: str = RUN_TOKEN, status: int = 200,
                 timeout: bool = False, base_url: str = CORE_URL) -> None:
        self.packets = packets
        self.token = token
        self.status = status
        self.timeout = timeout
        self.host = urlparse(base_url).netloc
        self.requests: list[httpx.Request] = []

    @property
    def transport(self) -> httpx.MockTransport:
        return httpx.MockTransport(self._handle)

    def _handle(self, request: httpx.Request) -> httpx.Response:
        self.requests.append(request)
        assert request.url.netloc.decode() == self.host, f"worker called {request.url}"
        if self.timeout:
            raise httpx.ReadTimeout("core too slow", request=request)
        if self.status != 200 or request.headers.get("authorization") != f"Bearer {self.token}":
            return httpx.Response(self.status if self.status != 200 else 403,
                                  json={"error": {"code": "forbidden", "message": "no"}})
        tool = request.url.path.rsplit("/", 1)[-1]
        if tool not in self.packets:
            return httpx.Response(404, json={"error": {"code": "not_found", "message": "no"}})
        items = self.packets[tool]
        size = len(json.dumps(items, separators=(",", ":")).encode())
        return httpx.Response(200, json={"access_id": 100 + len(self.requests), "tool": tool, "items": items,
                                         "truncated": False, "bytes": size,
                                         "limits": {"max_items": 10, "max_bytes": 8192}})
