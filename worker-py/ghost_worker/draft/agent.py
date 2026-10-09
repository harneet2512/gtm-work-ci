"""The account agent's pull loop: tools map 1:1 to core /internal/ctx/{tool}; every pull is logged and capped.

Reliability rules (2026-10-02 live benchmark): a repeated identical call is answered from the earlier pull (see
loop_state), and a decision naming people no people/state pull returned gets ONE corrective turn, while at least
MIN_CORRECTION_S of the deadline is left, before the service's grounding guard rejects it. Nothing is ever
force-fed: the model still pulls.
"""
from __future__ import annotations

import json
import logging
from collections.abc import Callable, Mapping
from dataclasses import dataclass
from typing import Any, Generic, TypeVar

from ..errors import CoreContextError, ToolBudgetExceededError
from ..llm.provider import LLMProvider, ToolCall
from ..models.draft import DraftRequest
from .core_client import ContextPacket, CoreContextClient, dump_json, parse_tool_name, tool_params
from .deadline import Deadline
from .grounding import PullLog
from .loop_state import LoopState
from .prompt import build_agent_system_prompt, build_agent_user_prompt
from .schemas import DECISION_SCHEMA, DECISION_SCHEMA_NAME, TOOL_SPECS, AccountDecision, parse_model_output

log = logging.getLogger(__name__)

BUDGET_EXHAUSTED = "tool budget exhausted; decide with what you have"
MIN_CORRECTION_S = 5.0  # a corrective turn started with less time left would end as a timeout, hiding the cause


T = TypeVar("T")


@dataclass(frozen=True)
class LoopResult(Generic[T]):
    answer: T
    tool_calls: int
    model: str
    pulls: PullLog


@dataclass(frozen=True)
class AgentResult:
    decision: AccountDecision
    tool_calls: int
    model: str
    pulls: PullLog


@dataclass(frozen=True)
class ToolOutcome:
    """What the model is told about one tool call, and the packet core returned (None if it never got there)."""

    payload: dict[str, Any]
    packet: ContextPacket | None


def _assistant_message(calls: tuple[ToolCall, ...]) -> dict[str, Any]:
    return {"role": "assistant", "content": None, "tool_calls": [
        {"id": c.id, "type": "function",
         "function": {"name": c.name, "arguments": json.dumps(dict(c.arguments), sort_keys=True)}}
        for c in calls]}


def _tool_message(call_id: str, payload: dict[str, Any]) -> dict[str, Any]:
    return {"role": "tool", "tool_call_id": call_id, "content": dump_json(payload)}


def _execute(call: ToolCall, core: CoreContextClient, deadline: Deadline) -> ToolOutcome:
    """One tool call. Bad requests from the model become an error result; core failures propagate (-> 502)."""
    try:
        tool = parse_tool_name(call.name)
        params = tool_params(call.arguments)
    except ValueError as exc:
        return ToolOutcome({"error": str(exc)}, None)
    try:
        packet = core.pull(tool, params, timeout_s=deadline.check(f"pulling {tool}"))
    except CoreContextError as exc:
        if not exc.rejected:
            raise
        # Core answered 400 (an evidence pull without field_path, say): the model's arguments were refused. Tell the model, as for
        # any other bad argument, instead of failing the whole run with "core returned HTTP 400".
        return ToolOutcome({"error": f"core rejected the {tool} request (HTTP 400): check the arguments; the evidence tool needs field_path"}, None)
    return ToolOutcome(packet.as_payload(), packet)


def _answer(call: ToolCall, state: LoopState, core: CoreContextClient,
            deadline: Deadline) -> tuple[dict[str, Any], LoopState]:
    """One call: a repeat is answered from the earlier pull; otherwise it spends budget (if any is left)."""
    earlier = state.earlier_access_id(call)
    if earlier is not None:
        return state.repeat_note(earlier), state.after_repeat()
    if not state.can_pull:
        return {"error": BUDGET_EXHAUSTED}, state
    outcome = _execute(call, core, deadline)
    return outcome.payload, state.after_pull(call, outcome.packet)


def _answer_batch(calls: tuple[ToolCall, ...], state: LoopState, core: CoreContextClient,
                  deadline: Deadline) -> tuple[tuple[dict[str, Any], ...], LoopState]:
    messages: list[dict[str, Any]] = []
    for call in calls:
        payload, state = _answer(call, state, core, deadline)
        messages.append(_tool_message(call.id, payload))
    return tuple(messages), state


def _correction(decision: Mapping[str, Any], unknown: tuple[str, ...], tools_left: bool) -> tuple[dict[str, Any], ...]:
    """The model's own decision, then why it cannot stand: the people it named that no pull returned."""
    who = f"person {unknown[0]} was" if len(unknown) == 1 else f"persons {', '.join(unknown)} were"
    fix = "pull people or drop them" if tools_left else "drop them (no tool calls are left)"
    return ({"role": "assistant", "content": dump_json(decision)},
            {"role": "user", "content": f"{who} not returned by the people/state tools; {fix}, then reply with "
                                        "the complete decision JSON again."})


def _needs_correction(unknown: tuple[str, ...], state: LoopState, deadline: Deadline) -> bool:
    """One corrective turn per run, and only while the deadline leaves time for it."""
    return bool(unknown) and not state.corrected and deadline.remaining() >= MIN_CORRECTION_S


def run_loop(*, system: str, user: str, schema: dict[str, Any], schema_name: str, max_tool_calls: int,
             provider: LLMProvider, core: CoreContextClient, deadline: Deadline, run_id: str,
             parse: Callable[[dict[str, Any]], T], people_of: Callable[[T], tuple[str, ...]],
             review: Callable[[T], str | None] | None = None) -> LoopResult[T]:
    """The pull loop shared by the account agent and the strategy generator: the model pulls context with the
    tools, then answers with one `schema`-shaped object that `parse` validates. People the answer names (via
    `people_of`) that no pull returned earn ONE corrective turn (see the module docstring); so does an answer
    that `review` objects to (it returns the message to send back, or None). The one corrective turn is shared:
    whichever problem comes first uses it, and the answer after it is returned as it stands."""
    messages: list[dict[str, Any]] = [{"role": "user", "content": user}]
    state = LoopState(budget=max_tool_calls)
    while True:
        deadline.check("the agent turn")
        tools = TOOL_SPECS if state.can_pull else []  # budget spent or stuck repeating: it must decide now
        turn = provider.complete_turn(system=system, messages=messages, tools=tools,
                                      schema=schema, schema_name=schema_name)
        if turn.content is not None:
            answer = parse(dict(turn.content))
            unknown = state.pulls.unknown_people(people_of(answer))
            if _needs_correction(unknown, state, deadline):
                log.warning("agent named unpulled people; one corrective turn",
                            extra={"run_id": run_id, "unknown_people": len(unknown)})
                messages.extend(_correction(dict(turn.content), unknown, state.budget_left))
                state = state.after_correction()
                continue
            objection = review(answer) if review and not unknown else None
            if objection and not state.corrected and deadline.remaining() >= MIN_CORRECTION_S:
                log.warning("answer reviewed and sent back; one corrective turn", extra={"run_id": run_id})
                messages.extend(({"role": "assistant", "content": dump_json(dict(turn.content))},
                                 {"role": "user", "content": objection}))
                state = state.after_correction()
                continue
            return LoopResult(answer=answer, tool_calls=state.used, model=turn.model, pulls=state.pulls)
        if not tools:
            raise ToolBudgetExceededError(f"agent kept requesting tools after its pulls closed "
                                          f"({state.used}/{max_tool_calls} calls, {state.repeats} repeats)")
        messages.append(_assistant_message(turn.tool_calls))
        answers, state = _answer_batch(turn.tool_calls, state, core, deadline)
        messages.extend(answers)
        log.info("agent pulled context", extra={"run_id": run_id, "tool_calls": state.used,
                                                "repeats": state.repeats})


def run_agent(request: DraftRequest, provider: LLMProvider, core: CoreContextClient,
              deadline: Deadline) -> AgentResult:
    result = run_loop(
        system=build_agent_system_prompt(request.max_tool_calls), user=build_agent_user_prompt(request),
        schema=DECISION_SCHEMA, schema_name=DECISION_SCHEMA_NAME, max_tool_calls=request.max_tool_calls,
        provider=provider, core=core, deadline=deadline, run_id=request.run_id,
        parse=lambda content: parse_model_output(AccountDecision, content, "agent decision"),
        people_of=lambda decision: decision.named_people)
    return AgentResult(decision=result.answer, tool_calls=result.tool_calls, model=result.model, pulls=result.pulls)
