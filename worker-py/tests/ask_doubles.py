"""Test doubles for Ask Cliff: a scripted model and a fake core serving POST /internal/ask/tools/{tool}."""
from __future__ import annotations

import copy
import json
from typing import Any

import httpx

from ghost_worker.llm.provider import ToolCall, TurnResult

MODEL = "qwen/qwen3.8-flash"
CORE_URL = "http://127.0.0.1:8080"
ASK_TOKEN = "at1.9999999999." + "n" * 16 + "." + "a" * 64
EPISODE_URL = "http://web.test/episodes/e1?mode=trace&span=state%3Ax"


def call(n: int, name: str, **arguments: Any) -> ToolCall:
    return ToolCall(id=f"prov_{n}", name=name, arguments=arguments)


def tools_turn(*calls: ToolCall) -> TurnResult:
    return TurnResult(tool_calls=calls, model=MODEL, usage={"total_tokens": 50})


def final_turn(answer: str, cited: list[str] | None = None, *, dont_know: bool = False,
               action: dict[str, str] | None = None) -> TurnResult:
    content = {"answer_markdown": answer, "cited_calls": cited or [], "dont_know": dont_know,
               "proposed_action": action}
    return TurnResult(content=content, model=MODEL, usage={"total_tokens": 60})


class ScriptedAskProvider:
    """Plays scripted turns in order; records what each turn was shown. `always` repeats one turn forever."""

    def __init__(self, turns: list[TurnResult], *, always: TurnResult | None = None, final_when_no_tools: TurnResult | None = None) -> None:
        self._turns = list(turns)
        self._always = always
        self._final = final_when_no_tools
        self.calls: list[dict[str, Any]] = []

    def complete_turn(self, *, system: str, messages: list[dict], tools: list[dict], schema: dict,
                      schema_name: str) -> TurnResult:
        self.calls.append({"system": system, "messages": copy.deepcopy(messages), "tools": tools,
                           "schema_name": schema_name})
        if not tools and self._final is not None:
            return self._final
        if self._turns:
            return self._turns.pop(0)
        if self._always is not None:
            return self._always
        raise AssertionError("the scripted provider ran out of turns")

    def complete_json(self, **_: Any) -> Any:  # pragma: no cover - the ask loop never uses it
        raise AssertionError("ask uses complete_turn only")


def tool_result(tool: str, data: Any, *, empty: bool = False, links: list[dict] | None = None,
                ok: bool = True) -> dict[str, Any]:
    return {"tool": tool, "ok": ok, "empty": empty, "truncated": False, "data": data,
            "links": links if links is not None else [{"label": "Episode trace", "url": EPISODE_URL}]}


class FakeAskCore:
    """Serves the tool endpoint; `results` maps tool name to the body to return (or a callable)."""

    def __init__(self, results: dict[str, Any] | None = None, status: int = 200) -> None:
        self.results = results or {}
        self.status = status
        self.requests: list[httpx.Request] = []
        self.transport = httpx.MockTransport(self._handle)

    def _handle(self, request: httpx.Request) -> httpx.Response:
        self.requests.append(request)
        tool = request.url.path.rsplit("/", 1)[-1]
        if self.status != 200:
            return httpx.Response(self.status, json={"error": {"code": "x", "message": "x"}})
        body = self.results.get(tool, tool_result(tool, {"name": "MedTech"}))
        return httpx.Response(200, json=body)

    @property
    def tool_names(self) -> list[str]:
        return [r.url.path.rsplit("/", 1)[-1] for r in self.requests]

    def bodies(self) -> list[dict[str, Any]]:
        return [json.loads(r.content) for r in self.requests]
