"""Draft use case: account agent decides, skill drafts, the grounding guard checks. No HTTP, no globals."""
from __future__ import annotations

import logging

from ..errors import InvalidRequestError
from ..llm.provider import LLMProvider
from ..models.draft import SKILL_ACTIONS, Decision, DecisionGuidance, DraftRequest, DraftResponse
from .agent import run_agent
from .core_client import CoreContextClient
from .deadline import Deadline
from .drafts import RunContext, build_action_draft, build_quiet_draft
from .schemas import AccountDecision

log = logging.getLogger(__name__)


def _applied_knowledge(guidance: DecisionGuidance | None, claimed: tuple[str, ...]) -> tuple[str, ...]:
    """Knowledge that can be recorded as used: the guidance marks it applying AND none of its exceptions fired."""
    if guidance is None:
        return ()
    applied = {k.knowledge_id for k in guidance.supporting_knowledge
               if k.applies and not any(e.triggered for e in k.exceptions_checked)}
    return tuple(dict.fromkeys(k for k in claimed if k in applied))


def _reported_decision(decision: AccountDecision, knowledge: tuple[str, ...]) -> Decision:
    return Decision(action=decision.action, why_now=decision.why_now, used_guidance=bool(knowledge),
                    wait_until=decision.wait_until if decision.action == "wait" else None,
                    who_to_involve=decision.who_to_involve, who_not_to_involve=decision.who_not_to_involve)


def draft_action(request: DraftRequest, provider: LLMProvider, core: CoreContextClient, *,
                 deadline: Deadline) -> DraftResponse:
    guidance = request.decision_guidance
    if guidance is not None and guidance.account_id != request.account_id:
        raise InvalidRequestError("decision_guidance.account_id does not match account_id")

    agent = run_agent(request, provider, core, deadline)
    decision = agent.decision
    agent.pulls.require_known_people(decision.named_people, "decision")  # after the agent's one corrective turn

    knowledge = _applied_knowledge(guidance, decision.knowledge_refs_used) if decision.used_guidance else ()
    ctx = RunContext(request=request, provider=provider, deadline=deadline, pulls=agent.pulls, decision=decision,
                     reported_decision=_reported_decision(decision, knowledge), knowledge_refs=knowledge,
                     agent_model=agent.model)
    draft = build_action_draft(ctx) if decision.action in SKILL_ACTIONS else build_quiet_draft(ctx)

    log.info("draft complete", extra={
        "run_id": request.run_id, "account_id": request.account_id, "action": draft.output.proposed_action_type,
        "tool_calls": agent.tool_calls, "access_ids": list(agent.pulls.access_ids),
        "dropped_evidence": draft.dropped_evidence, "used_guidance": ctx.reported_decision.used_guidance,
        "model": draft.model})
    return DraftResponse(output=draft.output, decision=ctx.reported_decision, model=draft.model,
                         tool_calls=agent.tool_calls, cited_access_ids=draft.cited_access_ids)
