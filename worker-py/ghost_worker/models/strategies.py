"""POST /v1/strategies, /v1/judge and /v1/revise request/response models (contracts/openapi/worker.yaml).

StrategyCandidate mirrors contracts/schemas/strategy_candidate.v1.json. The worker returns candidates without
`draft_index` and `eval_bundle_ref` (core assigns both), so those two keys are left out of the JSON when unset.
"""
from __future__ import annotations

from typing import Annotated, Any, Literal

from pydantic import Field, StringConstraints, model_serializer, model_validator

from .draft import (
    DEFAULT_MAX_TOOL_CALLS,
    MAX_STATE_HEADER_CHARS,
    MAX_TOOL_CALLS_LIMIT,
    RUN_TOKEN_PATTERN,
    DecisionGuidance,
    EvalType,
    EvidenceRef,
    FinishedArtifact,
    ProposedAction,
    Recipient,
    RepProfile,
    StateTransition,
    TriggerContext,
    Uuid,
    _Frozen,
)
from .usage import UsageSummary

CANDIDATE_COUNT = 3
FIVE_QUESTIONS = ("what_changed", "why_state_changed", "what_remains_unknown", "prior_knowledge_applies",
                  "why_next_action")
# Decision class -> the tool actions that carry it out (contracts/schemas/strategy_candidate.v1.json allOf).
ACTION_CLASSES: dict[str, tuple[str, ...]] = {
    "REPLY": ("send_email",), "MEETING": ("schedule_meeting",), "SHARE_DOCUMENT": ("share_document",),
    "INTERNAL_TASK": ("internal_note",), "WAIT": ("wait",), "NO_ACTION": ("no_action",),
    "ASK_RESEARCH": ("internal_note",), "CRM_UPDATE": ("internal_note",), "HUMAN_REVIEW": ("internal_note",),
    "UNKNOWN": ("no_action",), "EXPANSION_MOTION": ("send_email", "schedule_meeting", "share_document")}
ActionClass = Literal["REPLY", "MEETING", "SHARE_DOCUMENT", "INTERNAL_TASK", "WAIT", "NO_ACTION", "ASK_RESEARCH",
                      "EXPANSION_MOTION", "CRM_UPDATE", "UNKNOWN", "HUMAN_REVIEW"]
Answer = Annotated[str, Field(min_length=1, max_length=600)]
StrategyType = Annotated[str, StringConstraints(pattern=r"^[a-z][a-z0-9_]*$", max_length=80)]
RunToken = Annotated[str, Field(min_length=32, max_length=512, pattern=RUN_TOKEN_PATTERN, repr=False)]


class FiveQuestions(_Frozen):
    """What changed, why we think the state changed, what remains unknown, what prior knowledge applies, why this
    is the next action: every visible recommendation answers all five."""

    what_changed: Answer
    why_state_changed: Answer
    what_remains_unknown: Answer
    prior_knowledge_applies: Answer
    why_next_action: Answer


class StrategyCandidate(_Frozen):
    candidate_id: Uuid
    strategy_type: StrategyType
    title: Annotated[str, Field(min_length=1, max_length=80)]
    description: Annotated[str, Field(min_length=1, max_length=300)]
    ranking: Annotated[int, Field(ge=1)]
    preferred_by_agent: bool
    rationale: Annotated[str, Field(min_length=1, max_length=1500)]
    state_refs: tuple[str, ...]
    evidence_refs: Annotated[tuple[EvidenceRef, ...], Field(min_length=1)]
    knowledge_refs: tuple[Uuid, ...]
    action_type: ProposedAction
    action_class: ActionClass
    five_questions: FiveQuestions
    to: tuple[Recipient, ...]
    cc: tuple[Recipient, ...]
    subject: Annotated[str, Field(max_length=300)] | None
    full_action_artifact: FinishedArtifact
    preview: Annotated[str, Field(min_length=1, max_length=600)]
    draft_index: Annotated[int, Field(ge=1)] | None = None
    eval_bundle_ref: Uuid | None = None

    @model_validator(mode="after")
    def _allof_rules(self) -> StrategyCandidate:
        if self.preferred_by_agent != (self.ranking == 1):
            raise ValueError("preferred_by_agent is true exactly for ranking 1")
        if any(r.role != "to" for r in self.to) or any(r.role != "cc" for r in self.cc):
            raise ValueError("to holds role 'to' recipients and cc holds role 'cc' recipients")
        if self.subject != self.full_action_artifact.subject:
            raise ValueError("subject must equal full_action_artifact.subject")
        if self.action_type not in ACTION_CLASSES[self.action_class]:
            raise ValueError(f"{self.action_class} is carried out as {' or '.join(ACTION_CLASSES[self.action_class])}")
        if self.action_type == "send_email" and (not self.to or self.full_action_artifact.channel != "email"):
            raise ValueError("send_email needs a recipient and the email channel")
        return self

    @model_serializer(mode="wrap")
    def _omit_unassigned(self, handler: Any) -> dict[str, Any]:
        data = handler(self)
        for key in ("draft_index", "eval_bundle_ref"):
            if data.get(key) is None:
                data.pop(key, None)
        for key in ("evidence_refs", "to", "cc"):  # optional keys of the nested objects are absent, not null
            data[key] = [{k: v for k, v in item.items() if v is not None} for item in data[key]]
        return data


class GeneratorFeedback(_Frozen):
    """One explained human correction queued for this account's next strategies call (worker.yaml
    generator_feedback, HAR-119): the eval whose earlier failure the send-time edit repaired, plus the
    instruction the generator applies proactively. Corrections are data, never evidence to cite."""

    eval_result_id: Uuid
    eval_type: EvalType
    instruction: Annotated[str, Field(min_length=1, max_length=1000)]


class StrategiesRequest(_Frozen):
    run_id: Uuid
    account_id: Uuid
    decision_episode_id: Uuid
    workflow: Literal["post_interaction_followup"]
    trigger_context: TriggerContext
    account_change_id: Uuid | None = None
    state_header: Annotated[str, Field(max_length=MAX_STATE_HEADER_CHARS)]
    run_token: RunToken
    candidate_count: Literal[3]
    max_tool_calls: Annotated[int, Field(ge=1, le=MAX_TOOL_CALLS_LIMIT)] = DEFAULT_MAX_TOOL_CALLS
    rep_profile: RepProfile | None = None
    decision_guidance: DecisionGuidance | None = None
    state_transition: StateTransition | None = None
    generator_feedback: Annotated[tuple[GeneratorFeedback, ...], Field(max_length=20)] = ()


class StrategiesResponse(_Frozen):
    candidates: Annotated[tuple[StrategyCandidate, ...], Field(min_length=3, max_length=3)]
    model: str
    tool_calls: Annotated[int, Field(ge=0)]
    cited_access_ids: tuple[int, ...]
    usage: UsageSummary | None = None  # HAR-145: what the request cost (a metric, never an eval)


class EvalSuite(_Frozen):
    name: Annotated[str, StringConstraints(pattern=r"^[a-z][a-z0-9_]*$")]
    excluded_eval_types: tuple[str, ...]


class JudgeRequest(_Frozen):
    run_id: Uuid
    account_id: Uuid
    draft_index: Annotated[int, Field(ge=1)]
    candidate: StrategyCandidate
    run_token: RunToken
    trigger_context: TriggerContext | None = None
    eval_suite: EvalSuite | None = None
    offered_knowledge: Annotated[tuple[dict[str, Any], ...], Field(max_length=50)] = ()


class JudgeResponse(_Frozen):
    items: Annotated[tuple[dict[str, Any], ...], Field(min_length=1)]
    model: str
    usage: UsageSummary | None = None  # HAR-145: what the request cost (a metric, never an eval)


class RevisionFeedback(_Frozen):
    eval_result_id: Uuid
    instruction: Annotated[str, Field(min_length=1, max_length=1000)]


class ReviseRequest(_Frozen):
    run_id: Uuid
    account_id: Uuid
    draft_index: Annotated[int, Field(ge=1)]
    candidate: StrategyCandidate
    feedback: Annotated[tuple[RevisionFeedback, ...], Field(min_length=1, max_length=20)]
    run_token: RunToken


class ReviseResponse(_Frozen):
    candidate: StrategyCandidate
    model: str
    usage: UsageSummary | None = None  # HAR-145: what the request cost (a metric, never an eval)
