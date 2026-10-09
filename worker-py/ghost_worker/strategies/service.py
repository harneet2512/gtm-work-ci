"""/v1/strategies: three materially distinct, complete candidates from the bounded context plus DecisionGuidance.

Two model steps, like /v1/draft (agent then skill): the planner pulls context and decides WHAT the three strategies
are and for WHOM (no style hints reach it); the drafter writes the three finished artifacts (the rep profile reaches
only this step). Distinctness is enforced in code, not asked for in a prompt.
"""
from __future__ import annotations

import logging
import re
import uuid
from collections.abc import Sequence

from ..draft.agent import run_loop
from ..draft.core_client import CoreContextClient
from ..draft.deadline import Deadline
from ..draft.grounding import PullLog
from ..errors import InvalidStrategiesError, UngroundedProposalError
from ..llm.provider import LLMProvider
from ..models.draft import DecisionGuidance, FinishedArtifact, Recipient
from ..models.strategies import StrategiesRequest, StrategiesResponse, StrategyCandidate
from .distinct import Shape, type_collisions, violations
from .prompt import (build_drafter_system_prompt, build_drafter_user_prompt, build_planner_system_prompt,
                     build_planner_user_prompt, plan_correction)
from .schemas import (normalize_plan, ARTIFACTS_SCHEMA, ARTIFACTS_SCHEMA_NAME, PLAN_SCHEMA, PLAN_SCHEMA_NAME, ArtifactBody,
                      PlannedStrategy, StrategyArtifacts, StrategyPlan, parse_model_output)

log = logging.getLogger(__name__)

CANDIDATE_NAMESPACE = uuid.UUID("2b6f0a54-1c0e-4e0e-9a55-3c1d7d41a0b7")
PREVIEW_CHARS = 600
QUIET = frozenset({"wait", "no_action"})
_GREETING = re.compile(r"^\s*(hi|hello|dear|hey)\b[^\n]*\n+", re.I)


def applied_knowledge(guidance: DecisionGuidance | None, claimed: Sequence[str]) -> tuple[str, ...]:
    """Knowledge a candidate may record as used: its guidance entry applies and no exception fired (I4)."""
    if guidance is None:
        return ()
    ok = {k.knowledge_id for k in guidance.supporting_knowledge
          if k.applies and not any(e.triggered for e in k.exceptions_checked)}
    return tuple(dict.fromkeys(k for k in claimed if k in ok))


def preview_of(body: str, fallback: str) -> str:
    """A meaningful excerpt of the body (no new text): greeting dropped, whitespace collapsed, cut at a word."""
    text = " ".join(_GREETING.sub("", body.strip(), count=1).split())
    if not text:
        return fallback[:PREVIEW_CHARS]
    if len(text) <= PREVIEW_CHARS:
        return text
    return text[:PREVIEW_CHARS - 3].rsplit(" ", 1)[0] + "..."


def _plan_objection(plan: StrategyPlan) -> str | None:
    reasons = type_collisions([s.strategy_type for s in plan.strategies])
    return plan_correction(reasons) if reasons else None


def _plan(request: StrategiesRequest, provider: LLMProvider, core: CoreContextClient, deadline: Deadline):
    """Planner loop. A plan whose strategy types collide is sent back once (the loop's one corrective turn,
    which keeps the pulls); a second collision is an invalid strategy set."""
    result = run_loop(
        system=build_planner_system_prompt(request.max_tool_calls), user=build_planner_user_prompt(request),
        schema=PLAN_SCHEMA, schema_name=PLAN_SCHEMA_NAME, max_tool_calls=request.max_tool_calls,
        provider=provider, core=core, deadline=deadline, run_id=request.run_id,
        parse=lambda c: parse_model_output(StrategyPlan, normalize_plan(c), "strategy plan"),
        people_of=lambda p: p.named_people, review=_plan_objection)
    if _plan_objection(result.answer):
        raise InvalidStrategiesError("strategy types are not unique after the corrective turn")
    return result


def _drafts(request: StrategiesRequest, provider: LLMProvider, deadline: Deadline, plan: StrategyPlan,
            pulls: PullLog) -> tuple[dict[str, ArtifactBody], str]:
    """Artifacts of the non-quiet strategies by strategy_type, and the model that wrote them."""
    writing = [s for s in plan.strategies if s.action not in QUIET]
    if not writing:
        return {}, ""
    summary = [{"strategy_type": s.strategy_type, "title": s.title, "description": s.description,
                "action": s.action, "to": [p.model_dump() for p in s.to], "cc": [p.model_dump() for p in s.cc],
                "rationale": s.rationale} for s in writing]
    model, correction = "", None
    for _ in range(2):
        deadline.check("the drafting step")
        result = provider.complete_json(system=build_drafter_system_prompt(), schema=ARTIFACTS_SCHEMA,
                                        user=build_drafter_user_prompt(request, summary, pulls.packets, correction),
                                        schema_name=ARTIFACTS_SCHEMA_NAME)
        model = result.model
        drafted = parse_model_output(StrategyArtifacts, result.content, "strategy artifacts")
        by_type = {a.strategy_type: a.artifact for a in drafted.artifacts}
        if set(by_type) != {s.strategy_type for s in writing}:
            raise InvalidStrategiesError("the drafting step did not return one artifact per written strategy")
        shapes = [Shape(s.strategy_type, s.action, frozenset(p.person_id for p in (*s.to, *s.cc)),
                        by_type[s.strategy_type].body) for s in writing]
        found = violations(shapes)
        if not found:
            return by_type, model
        correction = plan_correction(found)
    raise InvalidStrategiesError("candidates are not materially distinct: " + "; ".join(found))


def _candidate(request: StrategiesRequest, pulls: PullLog, planned: PlannedStrategy, rank: int,
               artifact: ArtifactBody | None) -> StrategyCandidate:
    grounded = pulls.ground_evidence(planned.evidence_refs)
    if not grounded.kept:
        raise UngroundedProposalError(f"strategy {planned.strategy_type} left with no evidence this run pulled")
    pulls.require_known_people((p.person_id for p in (*planned.to, *planned.cc)), "recipient")
    art = (FinishedArtifact(channel=artifact.channel, subject=artifact.subject, body=artifact.body,
                            attachments=artifact.attachments) if artifact else FinishedArtifact(channel="none", body=""))
    cid = str(uuid.uuid5(CANDIDATE_NAMESPACE, f"{request.run_id}:{rank}:{planned.strategy_type}"))
    try:
        return StrategyCandidate(
            candidate_id=cid, strategy_type=planned.strategy_type, title=planned.title,
            description=planned.description, ranking=rank, preferred_by_agent=rank == 1, rationale=planned.rationale,
            state_refs=planned.state_refs, evidence_refs=grounded.kept,
            knowledge_refs=applied_knowledge(request.decision_guidance, planned.knowledge_refs_used),
            action_type=planned.action, action_class=planned.action_class, five_questions=planned.five_questions,
            to=tuple(Recipient(person_id=p.person_id, role="to", why=p.why) for p in planned.to),
            cc=tuple(Recipient(person_id=p.person_id, role="cc", why=p.why) for p in planned.cc),
            subject=art.subject, full_action_artifact=art, preview=preview_of(art.body, planned.description))
    except ValueError as exc:
        raise InvalidStrategiesError(f"strategy {planned.strategy_type} violates the candidate contract: "
                                     f"{str(exc).splitlines()[-1][:160]}") from None


def generate_strategies(request: StrategiesRequest, provider: LLMProvider, core: CoreContextClient, *,
                        deadline: Deadline) -> StrategiesResponse:
    if request.decision_guidance is not None and request.decision_guidance.account_id != request.account_id:
        raise InvalidStrategiesError("decision_guidance.account_id does not match account_id")
    planned = _plan(request, provider, core, deadline)
    plan, pulls = planned.answer, planned.pulls
    artifacts, drafter_model = _drafts(request, provider, deadline, plan, pulls)
    candidates = tuple(_candidate(request, pulls, s, i, artifacts.get(s.strategy_type))
                       for i, s in enumerate(plan.strategies, start=1))
    cited = tuple(sorted({a for c in candidates for a in pulls.ground_evidence(c.evidence_refs).access_ids}))
    log.info("strategies complete", extra={"run_id": request.run_id, "tool_calls": planned.tool_calls,
                                           "types": [c.strategy_type for c in candidates]})
    return StrategiesResponse(candidates=candidates, model=drafter_model or planned.model,
                              tool_calls=planned.tool_calls, cited_access_ids=cited)
