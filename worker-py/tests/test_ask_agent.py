"""Ask Cliff as an agent: the deadline bounds an in-flight model turn, the plan-act-observe loop chains tools up to its cap,
a proposed step pauses the work, and the conversation reaches the model (scripted model and fake core, no live call)."""
from __future__ import annotations

import json
import threading
import time
from typing import Any

import pytest

import ask_doubles as d
from ghost_worker.ask.core_client import AskToolClient
from ghost_worker.ask.loop import MAX_TOOL_CALLS, run_ask
from ghost_worker.ask.models import AskRequest, HistoryTurn
from ghost_worker.ask.prompt import TIMEOUT_ANSWER
from ghost_worker.draft.deadline import Deadline
from ghost_worker.errors import ProviderError
from ghost_worker.llm.call_bound import bounded_by, remaining_s
from ghost_worker.llm.limited import ConcurrencyLimitedProvider
from ghost_worker.llm.litellm_provider import LiteLLMProvider


def run(provider: Any, core: d.FakeAskCore, request: AskRequest, deadline: Deadline | None = None):
    with AskToolClient(d.CORE_URL, d.ASK_TOKEN, timeout_s=5, max_bytes=16384, transport=core.transport) as tools:
        return run_ask(request, provider, tools, deadline=deadline or Deadline(120))


def ask_request(text: str = "q", **extra: Any) -> AskRequest:
    return AskRequest(text=text, channel_kind="thread", ask_token=d.ASK_TOKEN, **extra)


class SlowProvider:
    """A model turn that takes `seconds`, and notes the bound the loop published to the provider stack."""

    def __init__(self, seconds: float) -> None:
        self.seconds = seconds
        self.bound_seen: float | None = None
        self.finished = threading.Event()

    def complete_turn(self, **_: Any):
        self.bound_seen = remaining_s()
        time.sleep(self.seconds)
        self.finished.set()
        return d.final_turn("late [t1]", ["t1"])

    def complete_json(self, **_: Any):  # pragma: no cover
        raise AssertionError("ask uses complete_turn only")


def test_the_deadline_cuts_a_model_turn_that_is_still_running() -> None:
    provider = SlowProvider(3.0)
    started = time.monotonic()
    out = run(provider, d.FakeAskCore(), ask_request(), Deadline(0.3))
    elapsed = time.monotonic() - started
    assert elapsed < 1.5, f"the turn was waited for ({elapsed:.1f}s)"
    assert out.timed_out and out.dont_know and out.answer_markdown == TIMEOUT_ANSWER
    assert not provider.finished.is_set()  # the slow call was abandoned, not waited for


def test_the_remaining_time_is_handed_to_the_provider_call() -> None:
    provider = SlowProvider(0.0)
    run(provider, d.FakeAskCore(), ask_request(), Deadline(30))
    assert provider.bound_seen is not None and 0 < provider.bound_seen <= 30
    assert remaining_s() is None  # nothing leaks out of the turn


def test_a_litellm_call_is_bounded_by_the_request_deadline_not_only_its_own() -> None:
    provider = LiteLLMProvider(model="m", fallback_model=None, api_key="k", timeout_s=90, deadline_s=90)
    left = lambda model, deadline: deadline - time.monotonic()  # noqa: E731 - the budget the attempt is given
    assert 80 < provider._guarded(left) <= 90
    with bounded_by(Deadline(5)):
        assert 0 < provider._guarded(left) <= 5


def test_a_provider_slot_is_not_waited_for_past_the_request_deadline() -> None:
    limited = ConcurrencyLimitedProvider(SlowProvider(0.0), max_concurrent=1, acquire_timeout_s=30)
    held = threading.Event()
    release = threading.Event()

    def hold() -> None:
        with limited._slot():
            held.set()
            release.wait(5)

    holder = threading.Thread(target=hold, daemon=True)
    holder.start()
    assert held.wait(2)
    started = time.monotonic()
    with bounded_by(Deadline(0.2)), pytest.raises(ProviderError):
        limited.complete_turn(system="s", messages=[], tools=[], schema={}, schema_name="n")
    release.set()
    assert time.monotonic() - started < 2


def test_a_multi_part_ask_chains_tools_and_each_call_sees_the_earlier_results() -> None:
    core = d.FakeAskCore({
        "list_accounts": d.tool_result("list_accounts", [{"name": "MedTech Advances"}, {"name": "EcoLite Innovations"}]),
        "account_state": d.tool_result("account_state", {"stage": "Negotiation"}),
        "gate_results": d.tool_result("gate_results", [{"gate": "D4", "verdict": "warn"}])})
    provider = d.ScriptedAskProvider([
        d.tools_turn(d.call(1, "list_accounts")),
        d.tools_turn(d.call(2, "account_state", account="EcoLite Innovations")),  # the name came from the first result
        d.tools_turn(d.call(3, "gate_results", account="EcoLite Innovations", gate="D4")),
        d.final_turn("EcoLite is in Negotiation and D4 warned [t2][t3].", ["t2", "t3"])])
    out = run(provider, core, ask_request("Compare EcoLite's stage and its D4 result"))
    assert core.tool_names == ["list_accounts", "account_state", "gate_results"]
    assert out.tool_calls == 3 and [c.call_id for c in out.citations] == ["t2", "t3"]
    third_turn_messages = provider.calls[2]["messages"]
    assert any("EcoLite Innovations" in m.get("content", "") for m in third_turn_messages if m["role"] == "tool")
    assert provider.calls[3]["tools"] and len(provider.calls) == 4


def test_twelve_sequential_calls_are_allowed_and_the_thirteenth_is_not() -> None:
    assert MAX_TOOL_CALLS == 12
    turns = [d.tools_turn(d.call(i, "timeline", account="MedTech", limit=i)) for i in range(1, 14)]
    provider = d.ScriptedAskProvider(turns, final_when_no_tools=d.final_turn("From what I read [t1].", ["t1"]))
    core = d.FakeAskCore()
    out = run(provider, core, ask_request())
    assert len(core.requests) == 12 and out.tool_calls == 12
    assert provider.calls[-1]["tools"] == []  # the budget is spent: the last turn must answer
    assert not out.dont_know


def test_a_proposed_step_pauses_the_work_the_model_must_answer_without_more_tools() -> None:
    provider = d.ScriptedAskProvider([
        d.tools_turn(d.call(1, "propose_action", kind="play_next", summary="Release the next event")),
        d.tools_turn(d.call(2, "gate_results", account="MedTech")),  # a model that ignores the pause
    ], final_when_no_tools=d.final_turn("I will play the next event; once it ran I will check the evals."))
    core = d.FakeAskCore()
    out = run(provider, core, ask_request("Play the next event and tell me which evals warned"))
    assert core.requests == [] and out.proposed_action is not None and out.proposed_action.kind == "play_next"
    assert provider.calls[1]["tools"] == []
    assert not out.dont_know and out.citations == ()


def test_the_conversation_is_shown_to_the_model_as_what_was_said_not_as_evidence() -> None:
    history = (HistoryTurn(role="summary", text="Earlier in this conversation: asked about MedTech"),
               HistoryTurn(role="user", text="What stage is MedTech in?"), HistoryTurn(role="cliff", text="Negotiation."))
    provider = d.ScriptedAskProvider([d.final_turn("I don't know.", dont_know=True)])
    run(provider, d.FakeAskCore(), ask_request("why?", history=history))
    first = provider.calls[0]["messages"][0]["content"]
    for want in ("Summary of earlier turns: Earlier in this conversation", "Person: What stage is MedTech in?", "Cliff: Negotiation.",
                 "not evidence", "Question (asked in a thread in the team channel):\nwhy?"):
        assert want in first
    assert first.index("Person: What stage") < first.index("Question (asked")


def test_a_question_without_history_is_shown_as_before() -> None:
    provider = d.ScriptedAskProvider([d.final_turn("I don't know.", dont_know=True)])
    run(provider, d.FakeAskCore(), ask_request("hello"))
    assert provider.calls[0]["messages"][0]["content"] == "Question (asked in a thread in the team channel):\nhello"


def test_the_system_prompt_asks_for_a_plan_and_a_pause_at_a_state_change() -> None:
    provider = d.ScriptedAskProvider([d.final_turn("I don't know.", dont_know=True)])
    run(provider, d.FakeAskCore(), ask_request())
    system = provider.calls[0]["system"]
    for phrase in ("plan", "chain tools", "propose_action", "resumed", "at most 12 tools"):
        assert phrase in system
    assert "ghost" not in system.lower() and json.dumps(system)
