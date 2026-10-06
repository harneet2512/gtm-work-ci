"""Turning an account decision into the Draft's AgentRunOutput: the drafting skill, or a quiet (wait/no_action) draft."""
from __future__ import annotations

from dataclasses import dataclass

from pydantic import ValidationError

from ..errors import InconsistentDraftError, InvalidModelOutputError, UngroundedProposalError
from ..llm.provider import LLMProvider
from ..models.draft import (
    AgentRunOutput,
    CrmNextStepIntent,
    Decision,
    DraftRequest,
    FinishedArtifact,
    Recipient,
)
from .deadline import Deadline
from .grounding import GroundedEvidence, PullLog
from .prompt import build_skill_system_prompt, build_skill_user_prompt
from .schemas import SKILL_SCHEMA, SKILL_SCHEMA_NAME, AccountDecision, SkillDraft, parse_model_output


@dataclass(frozen=True)
class RunContext:
    """Everything the draft builders need about one run (the decision, what was pulled, the reported decision)."""

    request: DraftRequest
    provider: LLMProvider
    deadline: Deadline
    pulls: PullLog
    decision: AccountDecision
    reported_decision: Decision
    knowledge_refs: tuple[str, ...]
    agent_model: str


@dataclass(frozen=True)
class DraftResult:
    output: AgentRunOutput
    model: str
    dropped_evidence: int
    cited_access_ids: tuple[int, ...]


def _require_evidence(grounded: GroundedEvidence, action: str) -> None:
    if not grounded.kept:
        raise UngroundedProposalError(
            f"{action} left with no evidence that this run pulled ({grounded.dropped} reference(s) dropped)")


def _honour_decision(decision: AccountDecision, recipients: tuple[Recipient, ...]) -> None:
    """The skill must address exactly the people the decision involves and never those it excludes."""
    addressed = {r.person_id for r in recipients}
    excluded = {p.person_id for p in decision.who_not_to_involve}
    required = {p.person_id for p in decision.who_to_involve}
    if addressed & excluded:
        raise InconsistentDraftError("skill addressed someone the decision says not to involve")
    if decision.action != "internal_note" and not required <= addressed:
        raise InconsistentDraftError("skill left out someone the decision says to involve")


def build_action_draft(ctx: RunContext) -> DraftResult:
    """Invoke post_interaction_followup, ground its output, assemble the AgentRunOutput."""
    ctx.deadline.check("the drafting skill")
    result = ctx.provider.complete_json(
        system=build_skill_system_prompt(),
        user=build_skill_user_prompt(ctx.request, ctx.reported_decision, ctx.pulls.packets),
        schema=SKILL_SCHEMA, schema_name=SKILL_SCHEMA_NAME)
    draft = parse_model_output(SkillDraft, result.content, "skill output")
    grounded = ctx.pulls.ground_evidence(draft.evidence_refs)
    _require_evidence(grounded, ctx.decision.action)
    ctx.pulls.require_known_people((r.person_id for r in draft.recipients), "recipient")
    _honour_decision(ctx.decision, draft.recipients)
    try:
        output = AgentRunOutput(
            proposed_action_type=ctx.decision.action, recipients=draft.recipients,
            finished_artifact=draft.finished_artifact, crm_next_step_intent=draft.crm_next_step_intent,
            reason=draft.reason, evidence_refs=grounded.kept, knowledge_refs_used=ctx.knowledge_refs)
    except ValidationError as exc:
        raise InvalidModelOutputError(
            f"skill output violates the AgentRunOutput contract: {exc.errors()[0]['msg']}") from None
    return DraftResult(output, result.model, grounded.dropped, grounded.access_ids)


def build_quiet_draft(ctx: RunContext) -> DraftResult:
    """wait / no_action: no skill, no recipients; the guidance knowledge that shaped the decision is kept."""
    decision = ctx.decision
    grounded = ctx.pulls.ground_evidence(decision.evidence_refs)
    waiting = decision.action == "wait"
    if waiting:
        next_step = f"Wait until {decision.wait_until}" if decision.wait_until else "Wait for the account"
    else:
        next_step = "No action"
    output = AgentRunOutput(
        proposed_action_type=decision.action, recipients=(),
        finished_artifact=FinishedArtifact(channel="none", body=""),
        crm_next_step_intent=CrmNextStepIntent(next_step=next_step),
        reason=decision.why_now, evidence_refs=grounded.kept, knowledge_refs_used=ctx.knowledge_refs,
        wait_until=decision.wait_until if waiting else None)
    return DraftResult(output, ctx.agent_model, grounded.dropped, grounded.access_ids)
