"""Immutable bookkeeping of the agent's pull loop: budget spent, what was pulled, and answers to repeated calls.

A tool call identical to one core already answered in this run (same tool, same arguments) never reaches core
again and never spends pull budget: the model is pointed at the earlier result. A model that repeats itself is
stuck, and every repeat still costs a model turn, so after MAX_FREE_REPEATS repeats pulling closes and the model
must decide with what it has (the one corrective turn for unpulled people re-opens it if budget is left).
"""
from __future__ import annotations

from dataclasses import dataclass, replace
from typing import Any

from ..llm.provider import ToolCall
from .core_client import TOOL_NAMES, ContextPacket, dump_json
from .grounding import PullLog

MAX_FREE_REPEATS = 2


def call_key(call: ToolCall) -> str:
    """Identity of a tool call for repeat detection: the tool name and its canonical arguments."""
    return f"{call.name} {dump_json(dict(call.arguments))}"


@dataclass(frozen=True)
class LoopState:
    """The loop's state after each answered call; every `after_*` returns a new state."""

    budget: int
    pulls: PullLog = PullLog()
    used: int = 0  # pull budget spent (the response's `tool_calls`); repeats never count
    repeats: int = 0  # repeated calls answered since pulling last opened
    answered: tuple[tuple[str, int], ...] = ()  # (call_key, access_id) of every pull core answered
    corrected: bool = False  # the one corrective turn for unpulled people was spent

    @property
    def budget_left(self) -> bool:
        return self.used < self.budget

    @property
    def can_pull(self) -> bool:
        """Tools are offered only while pull budget is left and the model is not stuck repeating itself."""
        return self.budget_left and self.repeats < MAX_FREE_REPEATS

    def earlier_access_id(self, call: ToolCall) -> int | None:
        key = call_key(call)
        return next((access_id for k, access_id in self.answered if k == key), None)

    def after_pull(self, call: ToolCall, packet: ContextPacket | None) -> LoopState:
        """A call that spent budget; `packet` is None when it was rejected before reaching core."""
        if packet is None:
            return replace(self, used=self.used + 1)
        return replace(self, used=self.used + 1, pulls=self.pulls.record(packet),
                       answered=(*self.answered, (call_key(call), packet.access_id)))

    def after_repeat(self) -> LoopState:
        return replace(self, repeats=self.repeats + 1)

    def after_correction(self) -> LoopState:
        """The corrective turn re-opens pulling (with a fresh repeat allowance) if pull budget is left."""
        return replace(self, corrected=True, repeats=0)

    def repeat_note(self, access_id: int) -> dict[str, Any]:
        """The tool result for a repeated call: a pointer to the earlier result, never the packet again."""
        pulled = {ref.tool for ref in self.pulls.refs}
        unused = ", ".join(t for t in TOOL_NAMES if t not in pulled) or "none"
        note = f"already pulled; see earlier result (access_id {access_id}); "
        if self.repeats + 1 >= MAX_FREE_REPEATS:
            note += "repeated calls closed this run's pulls: decide now with what you have."
        else:
            note += f"tools not yet used: {unused}. Pull one of those only if you need it, or decide now."
        return {"access_id": access_id, "note": note}
