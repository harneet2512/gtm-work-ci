"""The pull loop: tool cap, argument validation, token scope, core failures and the overall deadline."""
from __future__ import annotations

import logging

import pytest

import draft_doubles as dbl
import draft_situations as sit
from draft_helpers import acme_core, replay, run
from ghost_worker.draft.deadline import Deadline
from ghost_worker.errors import CoreContextError, DraftDeadlineError, ToolBudgetExceededError
from ghost_worker.llm.provider import TurnResult


def endless(n: int = 20) -> list[TurnResult]:
    """A model that never stops pulling, each call distinct (identical repeats are answered without a pull)."""
    return [dbl.tools_turn(dbl.call(i, "people", limit=i)) for i in range(1, n)]


def test_tool_call_cap_stops_the_loop_and_a_persistent_model_fails() -> None:
    body = sit.ACME_UNGUIDED.request_copy() | {"max_tool_calls": 2}
    provider = dbl.ScriptedProvider(endless())
    core = acme_core()
    with pytest.raises(ToolBudgetExceededError):
        run(provider, core, body)
    assert len(core.requests) == 2  # never a third pull
    assert provider.turn_calls[-1]["tools"] == []  # the final turn offered no tools: it had to decide


def test_default_cap_is_six_pulls() -> None:
    provider = dbl.ScriptedProvider(endless())
    core = acme_core()
    with pytest.raises(ToolBudgetExceededError):
        run(provider, core, sit.ACME_UNGUIDED.request_copy())
    assert len(core.requests) == 6


def test_calls_beyond_the_cap_inside_one_turn_get_an_error_not_a_pull() -> None:
    body = sit.ACME_UNGUIDED.request_copy() | {"max_tool_calls": 2}
    provider = dbl.ScriptedProvider(
        [dbl.tools_turn(dbl.call(1, "state"), dbl.call(2, "people"), dbl.call(3, "activities")),
         dbl.decision_turn("no_action", "enough")])
    core = acme_core()
    response = run(provider, core, body)
    assert response.tool_calls == 2 and len(core.requests) == 2
    tool_messages = [m for m in provider.turn_calls[-1]["messages"] if m["role"] == "tool"]
    assert len(tool_messages) == 3 and "budget" in tool_messages[2]["content"]


@pytest.mark.parametrize(("name", "arguments"), [
    ("shell", {}), ("people", {"account_id": sit.BETA}), ("people", {"limit": 500}),
    ("evidence", {"field_path": "x"})])
def test_invalid_tool_requests_are_answered_with_an_error_and_never_reach_core(name: str, arguments: dict) -> None:
    provider = dbl.ScriptedProvider([dbl.tools_turn(dbl.call(1, name, **arguments)),
                                     dbl.decision_turn("no_action", "nothing to do")])
    core = acme_core()
    response = run(provider, core, sit.ACME_UNGUIDED.request_copy())
    assert core.requests == [] and response.tool_calls == 1
    (tool_message,) = [m for m in provider.turn_calls[1]["messages"] if m["role"] == "tool"]
    assert '"error"' in tool_message["content"]


def test_tool_results_reach_the_model_with_their_access_id() -> None:
    provider = dbl.ScriptedProvider([dbl.tools_turn(dbl.call(1, "people")), dbl.decision_turn("no_action", "ok")])
    run(provider, acme_core(), sit.ACME_UNGUIDED.request_copy())
    messages = provider.turn_calls[1]["messages"]
    assert messages[1]["role"] == "assistant" and messages[1]["tool_calls"][0]["function"]["name"] == "people"
    assert messages[2]["tool_call_id"] == "call_1" and '"access_id": 101' in messages[2]["content"]


def test_pulls_are_scoped_to_the_run_token_and_core_url() -> None:
    _, core = replay(sit.ACME_UNGUIDED)
    assert {r.headers["authorization"] for r in core.requests} == {f"Bearer {dbl.RUN_TOKEN}"}
    assert all(r.url.path.startswith("/internal/ctx/") for r in core.requests)
    assert all("account" not in str(r.url.query) for r in core.requests)


def test_run_token_never_reaches_the_model_or_the_logs(caplog: pytest.LogCaptureFixture) -> None:
    caplog.set_level(logging.DEBUG)
    provider = dbl.ScriptedProvider([dbl.tools_turn(dbl.call(1, "people")), dbl.decision_turn("no_action", "ok")])
    run(provider, acme_core(), sit.ACME_UNGUIDED.request_copy())
    assert dbl.RUN_TOKEN not in repr(provider.turn_calls) and dbl.RUN_TOKEN not in caplog.text


# --- failures ------------------------------------------------------------------------------------

def test_core_403_is_a_core_error() -> None:
    core = dbl.FakeCore(sit.ACME_PACKETS, token="another-token-" + "z" * 30)
    provider = dbl.ScriptedProvider([dbl.tools_turn(dbl.call(1, "state"))])
    with pytest.raises(CoreContextError, match="403"):
        run(provider, core, sit.ACME_UNGUIDED.request_copy())


def test_core_timeout_is_a_core_error() -> None:
    provider = dbl.ScriptedProvider([dbl.tools_turn(dbl.call(1, "state"))])
    with pytest.raises(CoreContextError):
        run(provider, dbl.FakeCore(sit.ACME_PACKETS, timeout=True), sit.ACME_UNGUIDED.request_copy())


def test_overall_deadline_is_enforced_between_steps() -> None:
    now = [0.0]

    class Slow(dbl.ScriptedProvider):
        def complete_turn(self, **kw: object) -> TurnResult:
            now[0] += 11  # the model call alone burns the whole budget
            return super().complete_turn(**kw)

    provider = Slow([dbl.tools_turn(dbl.call(1, "state"))])
    core = acme_core()
    with pytest.raises(DraftDeadlineError):
        run(provider, core, sit.ACME_UNGUIDED.request_copy(), deadline=Deadline(10, clock=lambda: now[0]))
    assert core.requests == []


def test_deadline_also_bounds_the_skill_step() -> None:
    now = [0.0]

    class SlowDecision(dbl.ScriptedProvider):
        def complete_turn(self, **kw: object) -> TurnResult:
            result = super().complete_turn(**kw)
            now[0] += 11 if result.content is not None else 0
            return result

    decision = dbl.decision_turn("send_email", "x", evidence=sit.ACME_EVIDENCE)
    provider = SlowDecision([decision], sit.ACME_UNGUIDED.skill)
    with pytest.raises(DraftDeadlineError):
        run(provider, acme_core(), sit.ACME_UNGUIDED.request_copy(), deadline=Deadline(10, clock=lambda: now[0]))
    assert provider.json_calls == []


def test_deadline_object_reports_remaining_time() -> None:
    now = [100.0]
    deadline = Deadline(5, clock=lambda: now[0])
    assert deadline.check("x") == 5
    now[0] = 103
    assert deadline.remaining() == 2
    now[0] = 106
    with pytest.raises(DraftDeadlineError, match="x"):
        deadline.check("x")
