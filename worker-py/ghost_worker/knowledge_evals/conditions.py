"""Deterministic comparison of knowledge conditions (ADR-0013 semantics).

A condition constrains one field. `narrows(a, b)` is true when every situation that satisfies `a` also
satisfies `b` (a is at least as narrow as b on b's field). Scope and exception quality are built on it."""
from __future__ import annotations

import json
from dataclasses import dataclass
from dataclasses import field as dc_field
from typing import Any

from .models import Condition


def norm(value: Any) -> str:
    """Strings compare case-insensitively with whitespace collapsed (claims.ItemKey); others canonically."""
    if isinstance(value, str):
        return " ".join(value.lower().split()).rstrip(".;,")
    if isinstance(value, dict):  # an item pattern: normalise each key's value
        return json.dumps({k: norm(v) for k, v in value.items()}, sort_keys=True)
    if isinstance(value, (list, tuple)):
        return json.dumps(sorted(norm(v) for v in value))
    if isinstance(value, (int, float)) and not isinstance(value, bool):
        return json.dumps(float(value))  # 1 and 1.0 are the same value, as in the Go matcher
    return json.dumps(value, sort_keys=True)


# Buying-group member statuses that count as holding a role (ADR-0013: not departed, inactive or disengaged).
ENGAGED = frozenset({"active", "new", "weakening", "unknown"})


def _is_role(field: str) -> bool:
    return field.startswith("buying_group.")


@dataclass(frozen=True)
class Constraint:
    field: str
    kind: str  # set | not_set | exists | not_exists | unknown | contains
    values: frozenset[str]
    raw: Any = dc_field(default=None, compare=False)


def constraint(c: Condition) -> Constraint:
    if c.op in ("eq", "in"):
        values = c.value if c.op == "in" else [c.value]
        return Constraint(c.field, "set", frozenset(norm(v) for v in values))
    if c.op == "neq":
        return Constraint(c.field, "not_set", frozenset({norm(c.value)}))
    if c.op == "contains":
        return Constraint(c.field, "contains", frozenset({norm(c.value)}), c.value)
    kind = {"exists": "exists", "not_exists": "not_exists", "is_unknown": "unknown"}[c.op]
    return Constraint(c.field, kind, frozenset())


def narrows(a: Condition, b: Condition) -> bool:
    """Every situation satisfying a also satisfies b (same field required)."""
    ca, cb = constraint(a), constraint(b)
    if ca.field != cb.field:
        return False
    if cb.kind == "set":
        return ca.kind == "set" and ca.values <= cb.values
    if cb.kind == "not_set":
        return (ca.kind == "set" and not (ca.values & cb.values)) or (ca.kind == "not_set" and cb.values <= ca.values)
    if cb.kind == "exists":
        return ca.kind in ("exists", "contains") or (ca.kind == "set" and _set_implies_exists(ca))
    if cb.kind == "not_exists":
        return ca.kind in ("not_exists", "unknown")
    if cb.kind == "contains":
        return ca.kind == "contains" and _pattern_narrows(ca.raw, cb.raw)
    return ca.kind == cb.kind and ca.values == cb.values


def _set_implies_exists(c: Constraint) -> bool:
    """A known value implies exists; on a role, only if every listed status is an engaged one."""
    return not _is_role(c.field) or c.values <= ENGAGED


def _pattern(value: Any) -> tuple[str, frozenset[str] | None]:
    if isinstance(value, str):
        return norm(value), None
    status = value.get("status")
    statuses = None if status is None else frozenset(norm(v) for v in ([status] if isinstance(status, str) else status))
    return norm(value.get("text", "")), statuses


def _pattern_narrows(a: Any, b: Any) -> bool:
    """contains a implies contains b: b's text is inside a's, and a's statuses are within b's."""
    (ta, sa), (tb, sb) = _pattern(a), _pattern(b)
    if tb not in ta:
        return False
    return sb is None or (sa is not None and sa <= sb)


def covered(reference: Condition, inferred: tuple[Condition, ...]) -> bool:
    """The inferred scope is at least as narrow as the reference condition."""
    return any(narrows(c, reference) for c in inferred)


def implied(inferred: Condition, reference: tuple[Condition, ...]) -> bool:
    """The reference scope already implies the inferred condition (so it adds no narrowing)."""
    return any(narrows(r, inferred) for r in reference)


def equivalent(a: tuple[Condition, ...], b: tuple[Condition, ...]) -> bool:
    return all(covered(x, a) for x in b) and all(implied(x, b) for x in a)


def disjoint(a: Condition, b: Condition) -> bool:
    """No situation satisfies both (only decided for value sets and exists/not_exists on one field)."""
    ca, cb = constraint(a), constraint(b)
    if ca.field != cb.field:
        return False
    if {ca.kind, cb.kind} == {"exists", "not_exists"}:
        return True
    if _is_role(ca.field):  # several members can hold one role with different statuses
        sets = [x for x in (ca, cb) if x.kind == "set"]
        return len(sets) == 1 and {ca.kind, cb.kind} == {"set", "not_exists"} and sets[0].values <= ENGAGED
    if ca.kind == "set" and cb.kind == "set":
        return not (ca.values & cb.values)
    if {ca.kind, cb.kind} == {"set", "not_exists"}:
        return True
    if ca.kind == "set" and cb.kind == "not_set":
        return ca.values <= cb.values
    if cb.kind == "set" and ca.kind == "not_set":
        return cb.values <= ca.values
    return False
