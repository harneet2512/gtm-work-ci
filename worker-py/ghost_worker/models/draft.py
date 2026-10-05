"""POST /v1/draft request/response plus the contract mirrors they embed (contracts/openapi/worker.yaml).

AgentRunOutput, EvidenceRef and DecisionGuidance mirror contracts/schemas; the `allOf` rules of
agent_run_output (email needs a recipient + email channel, any real action needs evidence) are enforced here.
"""
from __future__ import annotations

from datetime import datetime
from typing import Annotated, Any, Literal, get_args

from pydantic import AfterValidator, BaseModel, ConfigDict, Field, StringConstraints, model_validator

from .usage import UsageSummary

UUID_PATTERN = r"^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$"
RUN_TOKEN_PATTERN = r"^[A-Za-z0-9._~+/=-]+$"  # bearer-token charset: nothing that could split a header
MAX_STATE_HEADER_CHARS = 2000
DEFAULT_MAX_TOOL_CALLS = 6
MAX_TOOL_CALLS_LIMIT = 12

ProposedAction = Literal["send_email", "schedule_meeting", "share_document", "internal_note", "wait", "no_action"]
# One source of truth for "does this decision produce an outbound/internal action?": quiet decisions need no
# skill, no recipients and no evidence; every other action does (AgentRunOutput allOf, the service and the skill).
QUIET_ACTIONS: frozenset[str] = frozenset({"wait", "no_action"})
SKILL_ACTIONS: frozenset[str] = frozenset(get_args(ProposedAction)) - QUIET_ACTIONS
Channel = Literal["email", "slack", "crm_note", "none"]
RecipientRole = Literal["to", "cc", "bcc"]
SignalType = Literal[
    "new_stakeholder_entered", "champion_weakened", "champion_reactivated", "champion_delegated",
    "security_blocker_appeared", "blocker_resolved", "pricing_interest", "expansion_interest", "stakeholder_gap",
    "next_meeting_missing", "commitment_overdue", "customer_replied", "customer_went_silent", "meeting_accepted",
    "support_risk_spike", "product_usage_increased", "stage_regressed", "stage_advanced"]
ReasonCode = Literal[
    "eligible_customer_replied", "eligible_meeting_completed", "eligible_stakeholder_change",
    "eligible_blocker_change", "no_material_change", "no_relevant_signal", "open_run_exists",
    "rep_already_replied", "cooldown", "champion_unknown", "permission_denied", "account_unresolved"]
# contracts/schemas/eval_result.v1.json $defs/evalType (every model that names an eval axis shares it:
# humandelta's explaining_evals[] and candidate_criterion, strategies' generator_feedback[]).
EvalType = Literal[
    "recipient_correctness", "date_commitment_consistency", "pricing_integrity", "crm_writeback",
    "duplicate_action", "provenance_coverage", "permission_policy", "state_transition_support",
    "buyer_readiness", "cta_calibration", "next_step_quality", "stakeholder_selection",
    "stakeholder_coverage", "economic_buyer_coverage", "champion_strength", "champion_continuity",
    "decision_process", "business_case", "momentum", "action_stage_fit", "expansion_readiness",
    "customer_risk_sensitivity", "relationship_pressure", "timing_cadence", "next_action_quality",
    "grounding", "commitment_consistency", "state_change_relevance", "channel_appropriateness",
    "rep_style", "knowledge_applicability", "exception_awareness", "evidence_sufficiency", "trajectory",
    "human_delta"]


def _rfc3339(value: str) -> str:
    try:
        parsed = datetime.fromisoformat(value)
    except ValueError:
        raise ValueError("must be an RFC 3339 timestamp") from None
    if parsed.tzinfo is None:
        raise ValueError("timestamp needs a timezone")
    return value


Uuid = Annotated[str, StringConstraints(pattern=UUID_PATTERN)]
Timestamp = Annotated[str, AfterValidator(_rfc3339)]
WhyNow = Annotated[str, Field(min_length=1, max_length=1000)]


class _Frozen(BaseModel):
    model_config = ConfigDict(frozen=True, extra="forbid")


class EvidenceRef(_Frozen):
    activity_id: Uuid
    claim_id: Uuid | None = None
    quote: Annotated[str, Field(max_length=2000)] | None = None
    speaker_person_id: Uuid | None = None
    occurred_at: Timestamp | None = None


class Recipient(_Frozen):
    person_id: Uuid
    role: RecipientRole
    why: Annotated[str, Field(max_length=500)] | None = None


class FinishedArtifact(_Frozen):
    channel: Channel
    subject: Annotated[str, Field(max_length=300)] | None = None
    body: Annotated[str, Field(max_length=20000)]
    attachments: tuple[str, ...] = ()


class CrmNextStepIntent(_Frozen):
    next_step: Annotated[str, Field(max_length=500)]
    due_at: Timestamp | None = None
    stage_change: str | None = None


class AgentRunOutput(_Frozen):
    proposed_action_type: ProposedAction
    recipients: tuple[Recipient, ...]
    finished_artifact: FinishedArtifact
    crm_next_step_intent: CrmNextStepIntent
    reason: Annotated[str, Field(max_length=2000)]
    evidence_refs: tuple[EvidenceRef, ...]
    knowledge_refs_used: tuple[Uuid, ...] = ()
    wait_until: Timestamp | None = None

    @model_validator(mode="after")
    def _allof_rules(self) -> AgentRunOutput:
        if self.proposed_action_type == "send_email":
            if not self.recipients:
                raise ValueError("send_email needs at least one recipient")
            if self.finished_artifact.channel != "email":
                raise ValueError("send_email needs the email channel")
        if self.proposed_action_type in SKILL_ACTIONS and not self.evidence_refs:
            raise ValueError("an action needs at least one evidence_ref")
        return self


class PersonRef(_Frozen):
    person_id: Uuid
    why: Annotated[str, Field(max_length=500)]


class NotRecommended(_Frozen):
    action: Annotated[str, Field(max_length=300)]
    why: Annotated[str, Field(max_length=500)]


class ExceptionChecked(_Frozen):
    exception: str
    triggered: bool
    evidence_refs: tuple[EvidenceRef, ...] = ()


class SupportingKnowledge(_Frozen):
    knowledge_id: Uuid
    applies: bool
    matched_conditions: tuple[str, ...]
    unmatched_conditions: tuple[str, ...] = ()
    exceptions_checked: tuple[ExceptionChecked, ...]
    # Optional (ADR-0013); None, not (), so guidance without it renders exactly as before (cassette keys).
    current_evidence_refs: tuple[EvidenceRef, ...] | None = None


class DecisionGuidance(_Frozen):
    id: Uuid
    account_id: Uuid
    agent_run_id: Uuid | None = None
    state_version: Annotated[int, Field(ge=0)]
    recommended_action: ProposedAction
    not_recommended: tuple[NotRecommended, ...] = ()
    why_now: WhyNow
    who_to_involve: tuple[PersonRef, ...]
    who_not_to_involve: tuple[PersonRef, ...]
    recommended_channel: Literal["email", "meeting", "slack_internal", "crm_only", "none"] | None = None
    ask_strength: Literal["none", "light", "moderate", "strong"] | None = None
    wait_until: Timestamp | None = None
    supporting_knowledge: tuple[SupportingKnowledge, ...]
    created_at: Timestamp


class TriggerContext(_Frozen):
    trigger_activity_ids: Annotated[tuple[Uuid, ...], Field(min_length=1, max_length=50)]
    signal_types: Annotated[tuple[SignalType, ...], Field(max_length=50)]
    reason_codes: Annotated[tuple[ReasonCode, ...], Field(max_length=50)]
    rep_person_id: Uuid | None = None


class RepProfile(_Frozen):
    """How this rep writes (HAR-97 §14 input, §24). Style hints for the drafting skill only; learning is WP24's."""

    brevity: Annotated[str, Field(max_length=200)] | None = None
    formality: Annotated[str, Field(max_length=200)] | None = None
    directness: Annotated[str, Field(max_length=200)] | None = None
    warmth: Annotated[str, Field(max_length=200)] | None = None
    pressure: Annotated[str, Field(max_length=200)] | None = None
    examples: Annotated[tuple[Annotated[str, Field(max_length=2000)], ...], Field(max_length=5)] = ()


TransitionStatus = Literal["CONFIRMED", "CANDIDATE", "UNRESOLVED", "REJECTED"]
OPEN_TRANSITION_STATUSES: frozenset[str] = frozenset({"CANDIDATE", "UNRESOLVED"})


class StateTransition(_Frozen):
    """contracts/schemas/state_transition.v1.json: read by the agent, never rewritten (ADR-0012). Facts keep their
    contract shape (key, description, required, satisfied, evidence_refs, ...)."""

    id: Uuid
    account_id: Uuid
    opportunity_id: Uuid | None = None
    from_state: str
    to_state_candidate: str | None
    status: TransitionStatus
    trigger_activity_ids: tuple[Uuid, ...]
    supporting_facts: tuple[dict[str, Any], ...]
    missing_facts: tuple[dict[str, Any], ...]
    contradicting_facts: tuple[dict[str, Any], ...]
    confidence: Annotated[float, Field(ge=0, le=1)]
    state_version: Annotated[int, Field(ge=0)]
    rule_set_version: str
    first_observed_at: Timestamp
    confirmed_at: Timestamp | None = None
    rejected_at: Timestamp | None = None
    closed_at: Timestamp | None = None
    close_reason: Literal["stale"] | None = None
    last_updated_at: Timestamp


class DraftRequest(_Frozen):
    run_id: Uuid
    account_id: Uuid
    workflow: Literal["post_interaction_followup"]
    trigger_context: TriggerContext
    state_header: Annotated[str, Field(max_length=MAX_STATE_HEADER_CHARS)]
    run_token: Annotated[str, Field(min_length=32, max_length=512, pattern=RUN_TOKEN_PATTERN, repr=False)]
    max_tool_calls: Annotated[int, Field(ge=1, le=MAX_TOOL_CALLS_LIMIT)] = DEFAULT_MAX_TOOL_CALLS
    decision_guidance: DecisionGuidance | None = None
    rep_profile: RepProfile | None = None
    state_transition: StateTransition | None = None


class Decision(_Frozen):
    """contracts/schemas/agent_run_draft.v1.json#/$defs/decision"""

    action: ProposedAction
    why_now: WhyNow
    used_guidance: bool
    wait_until: Timestamp | None = None
    who_to_involve: tuple[PersonRef, ...]
    who_not_to_involve: tuple[PersonRef, ...]


class DraftResponse(_Frozen):
    output: AgentRunOutput
    decision: Decision
    draft_index: Literal[1] = 1
    model: str
    tool_calls: Annotated[int, Field(ge=0)]
    cited_access_ids: tuple[int, ...]
    usage: UsageSummary | None = None  # HAR-145: what the request cost (a metric, never an eval)
