"""Materially distinct candidates (engineering contract I10): not restyled copies of one action.

Two candidates are NOT distinct when they share a strategy_type, or when they take the same kind of action toward
the same people with largely the same words. The Go orchestrator re-checks the same rule with the same constants
(core-go/internal/orchestrator/distinct.go); fixtures/orchestrator/distinctness_cases.json pins both.
"""
from __future__ import annotations

import re
from collections.abc import Sequence
from dataclasses import dataclass

# Share of content words two bodies must have in common (Jaccard) to count as the same message.
SIMILARITY_LIMIT = 0.6
_WORD = re.compile(r"[a-z0-9']+")
_STOP = frozenset("""a an and are as at be but by can for from have i if in is it of on or our so that the their
them then there these they this to us we will with you your""".split())


@dataclass(frozen=True)
class Shape:
    """What distinctness compares for one candidate."""

    strategy_type: str
    action_type: str
    people: frozenset[str]  # to + cc person ids
    body: str


def content_words(text: str) -> frozenset[str]:
    return frozenset(w for w in _WORD.findall(text.lower()) if w not in _STOP and len(w) > 1)


def similarity(a: str, b: str) -> float:
    wa, wb = content_words(a), content_words(b)
    if not wa and not wb:
        return 1.0
    return len(wa & wb) / len(wa | wb)


def violation(a: Shape, b: Shape) -> str | None:
    """Why `a` and `b` are not materially distinct, or None."""
    if a.strategy_type == b.strategy_type:
        return f"share the strategy_type {a.strategy_type}"
    if a.action_type == b.action_type and a.people == b.people and similarity(a.body, b.body) >= SIMILARITY_LIMIT:
        return f"{a.strategy_type} and {b.strategy_type} are the same {a.action_type} to the same people, reworded"
    return None


def violations(shapes: Sequence[Shape]) -> tuple[str, ...]:
    found = []
    for i, a in enumerate(shapes):
        for b in shapes[i + 1:]:
            reason = violation(a, b)
            if reason:
                found.append(reason)
    return tuple(found)


def type_collisions(strategy_types: Sequence[str]) -> tuple[str, ...]:
    """The planning-time check, before any message exists: every strategy_type must be unique."""
    return tuple(f"share the strategy_type {t}" for t in dict.fromkeys(strategy_types) if strategy_types.count(t) > 1)
