"""§7 routing: run only the semantic evals a candidate action needs.

"Do not create 15 model calls on every trivial action. Route the relevant eval subset from: workflow type +
account state + trigger + proposed action." Each rubric's `routing` block (data) says for which proposed actions
and under which predicates its judge runs; a skipped eval records why.
"""
from __future__ import annotations

from collections.abc import Iterable, Mapping
from typing import get_args
from types import MappingProxyType

from pydantic import BaseModel, ConfigDict

from ..errors import InvalidRequestError
from ..models.draft import ProposedAction
from .catalog import EvalCatalog
from .context import JudgeContext
from .predicates import PREDICATES
from .rubric import Rubric


class RouteDecision(BaseModel):
    model_config = ConfigDict(frozen=True, arbitrary_types_allowed=True)

    selected: tuple[str, ...]
    skipped: Mapping[str, str]


def _skip_reason(rubric: Rubric, context: JudgeContext) -> str | None:
    routing = rubric.routing
    if context.action_type not in routing.actions:
        return f"not run for proposed action {context.action_type}"
    failed = [name for name in routing.when_all if not PREDICATES[name](context)]
    if failed:
        return f"requires {', '.join(failed)}"
    if routing.when_any and not any(PREDICATES[name](context) for name in routing.when_any):
        return f"requires one of {', '.join(routing.when_any)}"
    return None


def route(context: JudgeContext, rubrics: Mapping[str, Rubric], catalog: EvalCatalog) -> RouteDecision:
    """The semantic evals to run for this candidate, in catalog order. Raises InvalidRequestError for a workflow
    the AgentRun contract does not know, or a proposed action type outside the AgentRunOutput vocabulary (which
    would otherwise silently skip every judge)."""
    if context.workflow not in catalog.workflows:
        raise InvalidRequestError(f"unknown workflow {context.workflow!r}")
    if context.action_type not in get_args(ProposedAction):
        raise InvalidRequestError(f"unknown proposed action {context.action_type!r}")
    reasons = {name: _skip_reason(rubric, context) for name, rubric in rubrics.items()}
    selected = catalog.order({name for name, reason in reasons.items() if reason is None})
    skipped = {name: reason for name, reason in reasons.items() if reason is not None}
    return RouteDecision(selected=selected, skipped=MappingProxyType(skipped))


def revision_subset(results: Iterable[Mapping[str, object]], routed: Iterable[str], catalog: EvalCatalog) -> tuple[str, ...]:
    """§7 after a revision (Draft 2): re-evaluate the affected dimensions (any eval that warned or failed on the
    previous draft) plus every routed dimension that can block."""
    routed_set = set(routed)
    affected = {str(r["eval_type"]) for r in results if r.get("verdict") in {"warn", "fail"}}
    blocking = {name for name in routed_set if catalog.entry(name).can_block}
    return catalog.order((affected & routed_set) | blocking)
