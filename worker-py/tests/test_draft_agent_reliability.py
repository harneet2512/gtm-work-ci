"""Agent-loop reliability, reproduced from the 2026-10-02 live benchmark with a scripted model and a fake core.

Live failure (3/25 runs): the model repeated the identical `state` pull six times, never called `people`, then named
a person it never pulled; the run died with ungrounded_proposal.
"""
from __future__ import annotations

import pytest

import draft_doubles as dbl
import draft_situations as sit
from draft_helpers import acme_core, run
from ghost_worker.draft.agent import MIN_CORRECTION_S
from ghost_worker.draft.deadline import Deadline
from ghost_worker.draft.prompt import AGENT_VERSION, build_agent_system_prompt
from ghost_worker.errors import ToolBudgetExceededError, UngroundedProposalError
from ghost_worker.llm.provider import TurnResult

WAIT_BODY = sit.ACME_GUIDED_WAIT.request_copy
CORRECTION = "not returned by the people/state tools"


def wait_decision(*, name_marco: bool = True) -> TurnResult:
    avoid = ((sit.MARCO, "No outbound CTA before Tuesday"),) if name_marco else ()
    return dbl.decision_turn("wait", "Marco is reviewing internally; wait until Tuesday.", avoid=avoid,
                             used_guidance=True, knowledge=[sit.K21], wait_until=sit.TUESDAY,
                             evidence=sit.ACME_EVIDENCE)


def same_pull(times: int, tool: str = "state") -> list[TurnResult]:
    return [dbl.tools_turn(dbl.call(i, tool)) for i in range(1, times + 1)]


def tool_messages(provider: dbl.ScriptedProvider, turn: int = -1) -> list[str]:
    return [m["content"] for m in provider.turn_calls[turn]["messages"] if m["role"] == "tool"]


def corrections(provider: dbl.ScriptedProvider) -> list[str]:
    return [m["content"] for m in provider.turn_calls[-1]["messages"]
            if m["role"] == "user" and CORRECTION in m["content"]]


def pulled_tools(core: dbl.FakeCore) -> list[str]:
    return [r.url.path.rsplit("/", 1)[-1] for r in core.requests]


# --- the live failure, end to end -----------------------------------------------------------------

def test_live_repro_repeated_state_then_an_unpulled_person_now_completes() -> None:
    """Before: 6 core pulls of state, budget spent, ungrounded_proposal (7 model turns).

    After: 1 pull; the 2nd repeat closes pulling, so the model must decide (live, no tools means a forced JSON
    answer); it names Marco, gets the corrective turn with tools re-opened, pulls people and completes (6 turns)."""
    provider = dbl.ScriptedProvider([*same_pull(3), wait_decision(),
                                     dbl.tools_turn(dbl.call(7, "people")), wait_decision()])
    core = acme_core()
    response = run(provider, core, WAIT_BODY())
    assert pulled_tools(core) == ["state", "people"]
    assert response.tool_calls == 2  # the repeats spent no budget
    assert provider.turn_calls[3]["tools"] == [] and provider.turn_calls[4]["tools"]  # closed, then re-opened
    assert len(provider.turn_calls) == 6
    assert response.decision.action == "wait"
    assert [p.person_id for p in response.decision.who_not_to_involve] == [sit.MARCO]
    assert response.output.knowledge_refs_used == (sit.K21,)


# --- identical tool calls --------------------------------------------------------------------------

def test_a_repeated_identical_pull_never_reaches_core_or_spends_budget() -> None:
    provider = dbl.ScriptedProvider([*same_pull(3), dbl.decision_turn("no_action", "nothing new")])
    core = acme_core()
    response = run(provider, core, WAIT_BODY())
    assert pulled_tools(core) == ["state"] and response.tool_calls == 1
    first, *repeats = tool_messages(provider)
    assert '"access_id": 101' in first and '"items"' in first
    for note in repeats:
        assert "already pulled; see earlier result" in note and '"access_id": 101' in note
        assert '"items"' not in note  # a pointer to the earlier result, not the packet again
    assert "closed this run's pulls: decide now" in repeats[-1]
    assert provider.turn_calls[-1]["tools"] == []  # two repeats: the model is stuck, so it must decide


def test_the_repeat_note_points_at_the_tools_not_yet_used() -> None:
    provider = dbl.ScriptedProvider([dbl.tools_turn(dbl.call(1, "state"), dbl.call(2, "people")),
                                     dbl.tools_turn(dbl.call(3, "state")),
                                     dbl.decision_turn("no_action", "nothing new")])
    run(provider, acme_core(), WAIT_BODY())
    note = tool_messages(provider)[-1]
    assert "tools not yet used: recent_diffs, evidence, activities, commitments" in note
    assert "or decide now" in note  # it must not push the model to pull everything


def test_a_repeat_inside_one_batch_is_answered_once_from_core() -> None:
    provider = dbl.ScriptedProvider([dbl.tools_turn(dbl.call(1, "people"), dbl.call(2, "people")),
                                     dbl.decision_turn("no_action", "nothing new")])
    core = acme_core()
    response = run(provider, core, WAIT_BODY())
    assert pulled_tools(core) == ["people"] and response.tool_calls == 1
    assert "already pulled" in tool_messages(provider)[1]


@pytest.mark.parametrize("second", [{"field_path": "stage"}, {"limit": 3}])
def test_the_same_tool_with_different_arguments_is_a_new_pull(second: dict) -> None:
    provider = dbl.ScriptedProvider([dbl.tools_turn(dbl.call(1, "state")),
                                     dbl.tools_turn(dbl.call(2, "state", **second)),
                                     dbl.decision_turn("no_action", "nothing new")])
    core = acme_core()
    assert run(provider, core, WAIT_BODY()).tool_calls == 2 and pulled_tools(core) == ["state", "state"]


def test_a_stuck_model_is_stopped_after_two_repeats_without_spending_budget() -> None:
    """Every repeat costs a model turn (the deadline was the largest live failure class), so it stays bounded."""
    provider = dbl.ScriptedProvider(same_pull(20))
    core = acme_core()
    with pytest.raises(ToolBudgetExceededError, match="1/6 calls, 2 repeats"):
        run(provider, core, WAIT_BODY())
    assert pulled_tools(core) == ["state"]
    assert len(provider.turn_calls) == 4  # 1 pull + 2 repeats + the tool-less turn (live: a forced decision)


def test_after_pulls_close_a_new_call_in_the_same_batch_is_not_pulled() -> None:
    provider = dbl.ScriptedProvider([dbl.tools_turn(dbl.call(1, "state")),
                                     dbl.tools_turn(dbl.call(2, "state"), dbl.call(3, "state"), dbl.call(4, "people")),
                                     dbl.decision_turn("no_action", "nothing new")])
    core = acme_core()
    assert run(provider, core, WAIT_BODY()).tool_calls == 1 and pulled_tools(core) == ["state"]
    assert "budget exhausted" in tool_messages(provider)[-1]


def test_an_invalid_call_is_not_cached_as_a_pull() -> None:
    provider = dbl.ScriptedProvider([dbl.tools_turn(dbl.call(1, "shell")), dbl.tools_turn(dbl.call(2, "shell")),
                                     dbl.decision_turn("no_action", "nothing new")])
    core = acme_core()
    assert run(provider, core, WAIT_BODY()).tool_calls == 2 and core.requests == []
    assert all('"error"' in m for m in tool_messages(provider))


# --- one corrective turn for people the model never pulled ----------------------------------------

def test_one_corrective_turn_lets_the_model_drop_an_unpulled_person() -> None:
    provider = dbl.ScriptedProvider([*same_pull(1), wait_decision(), wait_decision(name_marco=False)])
    response = run(provider, acme_core(), WAIT_BODY())
    assert response.decision.who_not_to_involve == () and response.tool_calls == 1
    (note,) = corrections(provider)
    assert note.startswith(f"person {sit.MARCO} was {CORRECTION}; pull people or drop them")
    assert provider.turn_calls[-1]["tools"]  # budget left: it may pull people
    assistant = provider.turn_calls[-1]["messages"][-2]
    assert assistant["role"] == "assistant" and sit.MARCO in assistant["content"]  # its own decision, echoed


def test_the_correction_names_every_unpulled_person() -> None:
    """Live acme_unguided run: the model named the rep (Dana), whom only the people tool returns."""
    both = dbl.decision_turn("no_action", "Quiet.", involve=((sit.DANA, "our rep"),), avoid=((sit.MARCO, "busy"),))
    provider = dbl.ScriptedProvider([*same_pull(1), both, dbl.decision_turn("no_action", "Quiet.")])
    run(provider, acme_core(), WAIT_BODY())
    (note,) = corrections(provider)
    assert note.startswith(f"persons {', '.join(sorted((sit.DANA, sit.MARCO)))} were not returned")


def test_there_is_only_one_corrective_turn() -> None:
    provider = dbl.ScriptedProvider([*same_pull(1), wait_decision(), wait_decision()])
    with pytest.raises(UngroundedProposalError, match="decision"):
        run(provider, acme_core(), WAIT_BODY())
    assert len(provider.turn_calls) == 3 and len(corrections(provider)) == 1


def test_with_no_budget_left_the_correction_only_offers_dropping() -> None:
    provider = dbl.ScriptedProvider([*same_pull(1), wait_decision(), wait_decision(name_marco=False)])
    response = run(provider, acme_core(), WAIT_BODY() | {"max_tool_calls": 1})
    assert response.decision.who_not_to_involve == ()
    assert provider.turn_calls[-1]["tools"] == []
    (note,) = corrections(provider)
    assert "drop them" in note and "pull people" not in note


def test_a_correction_without_budget_that_asks_for_tools_fails_on_the_budget() -> None:
    provider = dbl.ScriptedProvider([*same_pull(1), wait_decision(), dbl.tools_turn(dbl.call(9, "people"))])
    core = acme_core()
    with pytest.raises(ToolBudgetExceededError):
        run(provider, core, WAIT_BODY() | {"max_tool_calls": 1})
    assert pulled_tools(core) == ["state"]


def test_in_one_batch_a_repeat_gets_the_note_and_a_new_call_over_budget_gets_an_error() -> None:
    provider = dbl.ScriptedProvider([dbl.tools_turn(dbl.call(1, "state"), dbl.call(2, "people"), dbl.call(3, "state")),
                                     dbl.decision_turn("no_action", "nothing new")])
    core = acme_core()
    assert run(provider, core, WAIT_BODY() | {"max_tool_calls": 1}).tool_calls == 1
    pulled, over_budget, repeat = tool_messages(provider)
    assert pulled_tools(core) == ["state"] and '"items"' in pulled
    assert "budget exhausted" in over_budget and "already pulled" in repeat


@pytest.mark.parametrize("decision_takes_s", [11.0, 10.0 - MIN_CORRECTION_S + 0.5])
def test_no_corrective_turn_without_enough_deadline_left(decision_takes_s: float) -> None:
    """Spent, or left but under MIN_CORRECTION_S: the run fails ungrounded (its real cause), not on the clock."""
    now = [0.0]

    class SlowDecision(dbl.ScriptedProvider):
        def complete_turn(self, **kw: object) -> TurnResult:
            result = super().complete_turn(**kw)
            now[0] += decision_takes_s if result.content is not None else 0
            return result

    provider = SlowDecision([*same_pull(1), wait_decision(), wait_decision(name_marco=False)])
    with pytest.raises(UngroundedProposalError):
        run(provider, acme_core(), WAIT_BODY(), deadline=Deadline(10, clock=lambda: now[0]))
    assert len(provider.turn_calls) == 2


def test_the_system_prompt_makes_the_budget_a_ceiling_not_a_target() -> None:
    """Live: every completed run used all 6 calls, one of each tool. The prompt now says to stop when enough."""
    prompt = build_agent_system_prompt(6)
    assert "at most 6 tool calls in total" in prompt  # the contract default is unchanged
    assert "a ceiling, not a target" in prompt
    assert "decide as soon as you have enough" in prompt
    assert "people (before you name anyone)" in prompt
    assert "Never repeat a call you already made" in prompt
    assert AGENT_VERSION == "account-agent-v3"  # wording changed: cassette keys changed


def test_the_loop_lets_the_model_stop_after_two_pulls() -> None:
    provider = dbl.ScriptedProvider([dbl.tools_turn(dbl.call(1, "state"), dbl.call(2, "people")), wait_decision()])
    response = run(provider, acme_core(), WAIT_BODY())
    assert response.tool_calls == 2 and len(provider.turn_calls) == 2
    assert provider.turn_calls[-1]["tools"]  # tools were still on offer: stopping was the model's choice


def test_a_grounded_decision_gets_no_corrective_turn() -> None:
    provider = dbl.ScriptedProvider([dbl.tools_turn(dbl.call(1, "state"), dbl.call(2, "people")), wait_decision()])
    run(provider, acme_core(), WAIT_BODY())
    assert len(provider.turn_calls) == 2 and corrections(provider) == []
