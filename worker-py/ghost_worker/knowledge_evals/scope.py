"""HAR-97 L4 'Knowledge scope / exception quality' — the deterministic part.

Scope: did the learner infer the conditions under which the knowledge applies (state and transition)?
Exceptions: did it preserve the known exceptions, and can each of them ever fire?"""
from __future__ import annotations

from typing import Literal

from pydantic import BaseModel, ConfigDict

from .conditions import covered, disjoint, equivalent, implied
from .models import Condition, KnowledgeException

ScopeLabel = Literal["SCOPE_MATCH", "OVER_GENERALISED", "OVER_NARROW", "WRONG_SCOPE"]
ExceptionLabel = Literal["EXCEPTIONS_CAPTURED", "EXCEPTIONS_PARTIAL", "EXCEPTIONS_MISSED", "NO_EXCEPTIONS_EXPECTED"]

TRANSITION_FIELDS = ("relationship_state", "transition.from_state", "transition.to_state", "transition.status")


class ScopeResult(BaseModel):
    model_config = ConfigDict(frozen=True)

    label: ScopeLabel
    missing: tuple[str, ...]  # reference conditions the inferred scope does not enforce (over-generalisation)
    extra: tuple[str, ...]  # inferred conditions the reference does not need (over-narrowing)
    condition_recall: float
    condition_precision: float
    transition_scope_missing: bool  # the reference is scoped to a transition/state and the learner dropped it


class ExceptionResult(BaseModel):
    model_config = ConfigDict(frozen=True)

    label: ExceptionLabel
    recall: float | None  # None when the reference has no exceptions
    captured: tuple[str, ...]
    missed: tuple[str, ...]
    extra: tuple[str, ...]
    unreachable: tuple[str, ...]  # inferred exceptions that contradict the scope and can never fire


def scope_quality(inferred: tuple[Condition, ...], reference: tuple[Condition, ...]) -> ScopeResult:
    missing = tuple(r.render() for r in reference if not covered(r, inferred))
    extra = tuple(c.render() for c in inferred if not implied(c, reference))
    if missing and extra:
        label: ScopeLabel = "WRONG_SCOPE"
    elif missing:
        label = "OVER_GENERALISED"
    elif extra:
        label = "OVER_NARROW"
    else:
        label = "SCOPE_MATCH"
    dropped_transition = any(r.field in TRANSITION_FIELDS and not covered(r, inferred) for r in reference)
    return ScopeResult(
        label=label, missing=missing, extra=extra,
        condition_recall=_ratio(len(reference) - len(missing), len(reference)),
        condition_precision=_ratio(len(inferred) - len(extra), len(inferred)),
        transition_scope_missing=dropped_transition)


def exception_quality(inferred: tuple[KnowledgeException, ...], reference: tuple[KnowledgeException, ...],
                      scope: tuple[Condition, ...]) -> ExceptionResult:
    unreachable = tuple(x.description for x in inferred if _unreachable(x, scope))
    reachable = tuple(x for x in inferred if x.description not in unreachable)
    captured, missed = [], []
    for ref in reference:
        hit = any(equivalent(x.conditions, ref.conditions) for x in reachable)
        (captured if hit else missed).append(ref.description)
    extra = tuple(x.description for x in reachable
                  if not any(equivalent(x.conditions, r.conditions) for r in reference))
    if not reference:
        label: ExceptionLabel = "NO_EXCEPTIONS_EXPECTED"
    elif not missed:
        label = "EXCEPTIONS_CAPTURED"
    elif captured:
        label = "EXCEPTIONS_PARTIAL"
    else:
        label = "EXCEPTIONS_MISSED"
    return ExceptionResult(label=label, recall=_ratio(len(captured), len(reference)) if reference else None,
                           captured=tuple(captured), missed=tuple(missed), extra=extra, unreachable=unreachable)


def _unreachable(x: KnowledgeException, scope: tuple[Condition, ...]) -> bool:
    """An exception is checked only when the scope holds; if one of its conditions contradicts the scope it never fires."""
    return any(disjoint(c, s) for c in x.conditions for s in scope)


def _ratio(num: int, den: int) -> float:
    return 1.0 if den == 0 else num / den
