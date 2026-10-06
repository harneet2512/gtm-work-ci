"""The Ask Cliff agent loop: the model calls core's read tools (at most 6 calls, 60 s), then answers with the ids of the
tool calls its answer rests on. The worker enforces the grounding rules itself, whatever the model says:
a citation of a call that returned nothing is dropped, and an answer left with no citation and no proposed action is
replaced by "I don't know"."""
from __future__ import annotations

import json
import logging
from dataclasses import dataclass, field
from typing import Any

from pydantic import BaseModel, ConfigDict, ValidationError

from ..draft.deadline import Deadline
from ..errors import DraftDeadlineError, InvalidModelOutputError
from ..llm.provider import LLMProvider, ToolCall
from .core_client import AskToolClient
from .models import AskRequest, AskResponse, Citation, ProposedActionOut
from .prompt import IDK_ANSWER, NO_ANSWER, PROPOSAL_ONLY_ANSWER, TIMEOUT_ANSWER, build_system_prompt, build_user_prompt
from .tools import ACTION_KINDS, ASK_SCHEMA, ASK_SCHEMA_NAME, PROPOSE_TOOL, TOOL_SPECS, check_arguments

log = logging.getLogger(__name__)

MAX_TOOL_CALLS = 6
BUDGET_EXHAUSTED = "tool budget exhausted; answer with what you have"
MAX_REPEATS = 2  # identical repeated calls tolerated before the tools are withdrawn
MAX_CITATIONS = 20

__all__ = ["IDK_ANSWER", "MAX_TOOL_CALLS", "run_ask"]


class _Final(BaseModel):
    model_config = ConfigDict(extra="ignore")

    answer_markdown: str
    cited_calls: list[str] = []
    dont_know: bool = False
    proposed_action: dict[str, Any] | None = None


@dataclass
class _State:
    budget: int
    used: int = 0
    repeats: int = 0
    results: dict[str, dict[str, Any]] = field(default_factory=dict)  # call_id -> tool result
    tools_by_id: dict[str, str] = field(default_factory=dict)
    memo: dict[str, tuple[str, dict[str, Any]]] = field(default_factory=dict)
    proposal: ProposedActionOut | None = None
    model: str = ""

    @property
    def can_call(self) -> bool:
        return self.used < self.budget and self.repeats < MAX_REPEATS


def _assistant_message(calls: tuple[ToolCall, ...]) -> dict[str, Any]:
    return {"role": "assistant", "content": None, "tool_calls": [
        {"id": c.id, "type": "function",
         "function": {"name": c.name, "arguments": json.dumps(dict(c.arguments), sort_keys=True)}} for c in calls]}


def _tool_message(provider_id: str, payload: dict[str, Any]) -> dict[str, Any]:
    return {"role": "tool", "tool_call_id": provider_id, "content": json.dumps(payload, sort_keys=True, ensure_ascii=False)}


def _propose(arguments: Any, state: _State) -> dict[str, Any]:
    kind, summary = arguments.get("kind"), arguments.get("summary")
    if kind not in ACTION_KINDS or not isinstance(summary, str) or not summary.strip():
        return {"error": f"kind must be one of {', '.join(ACTION_KINDS)} and a summary is required"}
    if state.proposal is None:
        state.proposal = ProposedActionOut(kind=kind, summary=summary.strip()[:400])
    return {"ok": True, "note": "Proposed. Nothing has run: a human must confirm it first. Tell the reader that."}


def _data_call(call: ToolCall, state: _State, tools: AskToolClient, deadline: Deadline) -> dict[str, Any]:
    try:
        args = check_arguments(call.name, dict(call.arguments))
    except ValueError as exc:
        return {"error": str(exc)}
    key = call.name + json.dumps(args, sort_keys=True)
    if key in state.memo:
        state.repeats += 1
        call_id, earlier = state.memo[key]
        return {"call_id": call_id, "note": "identical to an earlier call; same result", **earlier}
    if state.used >= state.budget:
        return {"error": BUDGET_EXHAUSTED}
    state.used += 1
    call_id = f"t{state.used}"
    result = tools.call(call.name, args, timeout_s=deadline.check(f"calling {call.name}"))
    state.results[call_id] = result
    state.tools_by_id[call_id] = call.name
    state.memo[key] = (call_id, result)
    return {"call_id": call_id, **result}


def _answer_calls(calls: tuple[ToolCall, ...], state: _State, tools: AskToolClient,
                  deadline: Deadline) -> list[dict[str, Any]]:
    out: list[dict[str, Any]] = []
    for call in calls:
        if call.name == PROPOSE_TOOL:
            payload = _propose(call.arguments, state)
        else:
            payload = _data_call(call, state, tools, deadline)
        out.append(_tool_message(call.id, payload))
    return out


def _citations(cited: list[str], state: _State) -> tuple[Citation, ...]:
    out: list[Citation] = []
    seen: set[str] = set()
    for call_id in cited:
        result = state.results.get(call_id)
        if call_id in seen or result is None or not result.get("ok") or result.get("empty"):
            continue
        seen.add(call_id)
        tool = state.tools_by_id[call_id]
        links = [lk for lk in result.get("links", []) if isinstance(lk, dict) and lk.get("url")]
        if not links:
            out.append(Citation(call_id=call_id, tool=tool, label=tool.replace("_", " ")))
        for link in links:
            out.append(Citation(call_id=call_id, tool=tool, label=str(link.get("label") or tool)[:200], url=str(link["url"])))
    return tuple(out[:MAX_CITATIONS])


def _proposal(final: _Final, state: _State) -> ProposedActionOut | None:
    raw = final.proposed_action
    if raw and raw.get("kind") in ACTION_KINDS and isinstance(raw.get("summary"), str) and raw["summary"].strip():
        return ProposedActionOut(kind=raw["kind"], summary=raw["summary"].strip()[:400])
    return state.proposal


def _finish(content: dict[str, Any], state: _State) -> AskResponse:
    try:
        final = _Final.model_validate(content)
    except ValidationError:
        raise InvalidModelOutputError("the ask answer did not match its schema") from None
    citations = _citations(final.cited_calls, state)
    proposal = _proposal(final, state)
    if proposal is None and (final.dont_know or not citations):
        return _plain(IDK_ANSWER, state, dont_know=True)
    text = final.answer_markdown.strip() or IDK_ANSWER
    if not citations:  # a proposal is not a licence for an ungrounded answer: say only that a confirmation is needed
        text = PROPOSAL_ONLY_ANSWER
    return AskResponse(answer_markdown=text, citations=citations, proposed_action=proposal, dont_know=False,
                       tool_calls=state.used, model=state.model)


def _plain(text: str, state: _State, *, dont_know: bool = True, timed_out: bool = False) -> AskResponse:
    return AskResponse(answer_markdown=text, citations=(), proposed_action=None, dont_know=dont_know,
                       timed_out=timed_out, tool_calls=state.used, model=state.model or "none")


def run_ask(request: AskRequest, provider: LLMProvider, tools: AskToolClient, *, deadline: Deadline) -> AskResponse:
    system = build_system_prompt(request.max_tool_calls)
    messages: list[dict[str, Any]] = [{"role": "user", "content": build_user_prompt(request)}]
    state = _State(budget=request.max_tool_calls)
    try:
        while True:
            deadline.check("the next model turn")
            offered = TOOL_SPECS if state.can_call else []  # budget spent or stuck repeating: it must answer now
            turn = provider.complete_turn(system=system, messages=messages, tools=offered,
                                          schema=ASK_SCHEMA, schema_name=ASK_SCHEMA_NAME)
            state.model = turn.model
            if turn.content is not None:
                return _finish(dict(turn.content), state)
            if not offered:
                log.warning("ask model kept requesting tools after they were withdrawn",
                            extra={"tool_calls": state.used, "repeats": state.repeats})
                return _plain(NO_ANSWER, state)
            messages.append(_assistant_message(turn.tool_calls))
            messages.extend(_answer_calls(turn.tool_calls, state, tools, deadline))
    except DraftDeadlineError:
        log.warning("ask deadline reached", extra={"tool_calls": state.used})
        return _plain(TIMEOUT_ANSWER, state, timed_out=True)
