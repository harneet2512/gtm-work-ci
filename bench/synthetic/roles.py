"""Buying-group roles from contact titles (spec §a). Pure functions; ties break by a seeded stream."""
from __future__ import annotations

import re
from collections.abc import Mapping, Sequence
from dataclasses import dataclass

from .seeding import Stream

SENIORITY_CUES = (  # first match wins, most senior first
    (5, ("chief", "ceo", "cfo", "coo", "cto", "cio", "ciso", "president", "founder", "owner")),
    (4, ("vp", "vice president", "svp", "evp", "head of", "general manager")),
    (3, ("director",)),
    (2, ("manager", "lead", "principal", "supervisor")),
)
FUNCTION_CUES = (
    ("finance", ("finance", "financial", "cfo", "controller", "procurement", "purchasing")),
    ("technical", ("engineer", "engineering", "architect", "it", "cto", "cio", "developer", "data", "security",
                   "ciso", "systems")),
    ("operations", ("operations", "coo", "supply", "logistics", "facilities")),
)
ROLES = ("champion", "economic_buyer", "decision_maker", "technical_evaluator", "end_user")


@dataclass(frozen=True)
class Person:
    id: str
    seniority: int
    function: str


def _has(title: str, cue: str) -> bool:
    return re.search(rf"\b{re.escape(cue)}\b", title) is not None


def parse_title(title: str | None) -> tuple[int, str]:
    """(seniority 1-5, function) from a title; a missing title is an individual contributor in business."""
    t = (title or "").lower()
    seniority = next((s for s, cues in SENIORITY_CUES if any(_has(t, c) for c in cues)), 1)
    function = next((f for f, cues in FUNCTION_CUES if any(_has(t, c) for c in cues)), "business")
    return seniority, function


def _top(people: Sequence[Person], stream: Stream) -> Person | None:
    """Highest seniority; ties broken by a seeded shuffle (stable for a given stream)."""
    if not people:
        return None
    best = max(p.seniority for p in people)
    return stream.shuffled([p for p in people if p.seniority == best])[0]


def account_roles(people: Sequence[Person], technical_line: bool, stream: Stream) -> Mapping[str, str]:
    """Account-level roles: economic buyer, optional decision maker, technical evaluators; rest end users."""
    eb = (_top([p for p in people if p.function == "finance"], stream)
          or _top([p for p in people if p.seniority == 5], stream) or _top(people, stream))
    wanted = "technical" if technical_line else "operations"
    dm = _top([p for p in people if p.seniority == 4 and p.function == wanted and p is not eb], stream)
    roles = {p.id: "end_user" for p in people}
    if eb is not None:
        roles[eb.id] = "economic_buyer"
    if dm is not None:
        roles[dm.id] = "decision_maker"
    techs = [p for p in people if p.function == "technical" and 1 <= p.seniority <= 3 and roles[p.id] == "end_user"]
    for p in stream.shuffled(techs)[:2]:
        roles[p.id] = "technical_evaluator"
    return roles


def deal_champion(people: Sequence[Person], roles: Mapping[str, str], primary_contact: str | None,
                  outbound_counts: Mapping[str, int]) -> str | None:
    """The primary contact if seniority 2-4 and not the economic buyer, else the most-emailed such contact."""
    eligible = {p.id: p for p in people if 2 <= p.seniority <= 4 and roles.get(p.id) != "economic_buyer"}
    if primary_contact in eligible:
        return primary_contact
    if not eligible:
        return None
    return max(sorted(eligible), key=lambda pid: outbound_counts.get(pid, 0))
