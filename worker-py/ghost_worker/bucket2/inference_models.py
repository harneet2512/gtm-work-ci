"""Request and response models of POST /v1/judgment-inference (contracts/openapi/worker.yaml)."""
from __future__ import annotations

from typing import Any

from pydantic import BaseModel, ConfigDict, Field

LABELS = ("reduced_pressure", "increased_pressure", "kept_champion_involved", "removed_unnecessary_stakeholders",
          "added_missing_stakeholder", "delayed_cta", "removed_cta", "smaller_ask", "larger_ask", "changed_channel",
          "corrected_fact", "deferred_to_buyer_timing", "style_only")
EDIT_CLASSES = ("factual", "state", "strategy", "stakeholder", "timing", "cta", "style", "recipient", "risk", "tone", "wording",
                "new_info", "unknown")
SIGNALS = ("weak", "moderate", "strong")


class InferenceRequest(BaseModel):
    """strategy_set, eval_bundles and human_strategy_decision are the stored contract documents, passed through.
    human_note and state_context are optional context (the note when the human already gave one)."""

    model_config = ConfigDict(frozen=True, extra="forbid")

    decision_episode_id: str
    strategy_set: dict[str, Any]
    eval_bundles: list[dict[str, Any]] = Field(min_length=3, max_length=3)
    human_strategy_decision: dict[str, Any]
    run_token: str = Field(min_length=32)
    human_note: str | None = Field(default=None, max_length=2000)
    state_context: dict[str, Any] | None = None


class InferenceResponse(BaseModel):
    model_config = ConfigDict(frozen=True)

    inferred_semantic_delta: dict[str, Any]
    evidence: dict[str, Any]
    model: str
