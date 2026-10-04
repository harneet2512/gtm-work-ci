"""POST /v1/human-delta request/response models (contracts/openapi/worker.yaml + human_delta.v1.json).

HAR-139 / HAR-97 §9: core computes the literal diff of the human's final artifact against the selected
candidate and decides `unexplained`; the worker supplies the semantic part — `semantic_labels`, and a
`candidate_criterion` when no existing eval predicted the change. The call is self-contained: no run_token,
no /internal/ctx pulls.
"""
from __future__ import annotations

from typing import Annotated, Any, Literal

from pydantic import Field, model_validator

from .draft import FinishedArtifact, Recipient, Uuid, _Frozen
from .strategies import StrategyCandidate

# contracts/schemas/human_delta.v1.json literal_changes[].kind
LiteralChangeKind = Literal[
    "recipient_added", "recipient_removed", "recipient_role_changed", "subject_changed", "cta_changed",
    "timing_changed", "paragraph_added", "paragraph_removed", "paragraph_edited", "channel_changed",
    "action_type_changed", "attachment_added", "attachment_removed", "crm_next_step_changed"]

# contracts/schemas/human_delta.v1.json semantic_labels enum
SemanticLabel = Literal[
    "reduced_pressure", "increased_pressure", "kept_champion_involved", "removed_unnecessary_stakeholders",
    "added_missing_stakeholder", "delayed_cta", "removed_cta", "smaller_ask", "larger_ask",
    "changed_channel", "corrected_fact", "deferred_to_buyer_timing", "style_only"]

# contracts/schemas/eval_result.v1.json $defs/evalType (candidate_criterion.suggested_eval_type and
# explaining_evals[].eval_type share it).
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


class ToRecipient(Recipient):
    """final_action.to[]: role is pinned to 'to' by the contract."""

    role: Literal["to"]


class CcRecipient(Recipient):
    """final_action.cc[]: role is pinned to 'cc' by the contract."""

    role: Literal["cc"]


class LiteralChange(_Frozen):
    """One entry of literal_changes: `before`/`after` are free-form (the contract types them {})."""

    kind: LiteralChangeKind
    before: Any = None
    after: Any = None


class ExplainingEval(_Frozen):
    """An eval result that predicted the human's change (eval_result_id is the persisted eval_runs row)."""

    eval_result_id: Uuid
    eval_type: EvalType
    reason: Annotated[str, Field(max_length=2000)]


class DeltaFinalAction(_Frozen):
    """What the human is about to send."""

    to: Annotated[tuple[ToRecipient, ...], Field(min_length=1)]
    cc: tuple[CcRecipient, ...] = ()
    artifact: FinishedArtifact


class CandidateCriterion(_Frozen):
    """The eval axis a learning loop scopes and backtests for an unexplained delta."""

    statement: Annotated[str, Field(min_length=1, max_length=1000)]
    suggested_eval_type: EvalType
    knowledge_candidate_id: Uuid | None = None


class HumanDeltaRequest(_Frozen):
    run_id: Uuid
    decision_episode_id: Uuid
    account_id: Uuid
    selected_candidate: StrategyCandidate
    final_action: DeltaFinalAction
    literal_changes: tuple[LiteralChange, ...]
    unexplained: bool
    explaining_evals: Annotated[tuple[ExplainingEval, ...], Field(max_length=20)]

    @model_validator(mode="after")
    def _unexplained_has_no_explainers(self) -> HumanDeltaRequest:
        # Contract: explaining_evals is empty exactly when unexplained is true.
        if self.unexplained and self.explaining_evals:
            raise ValueError("an unexplained delta carries no explaining_evals")
        if not self.unexplained and not self.explaining_evals:
            raise ValueError("an explained delta names at least one explaining_eval")
        return self


class HumanDeltaResponse(_Frozen):
    semantic_labels: tuple[SemanticLabel, ...]
    candidate_criterion: CandidateCriterion | None
    model: str
