"""Prompt and output schema of the human-delta labeler (POST /v1/human-delta)."""
from __future__ import annotations

import json
from typing import Any

from ..models.humandelta import HumanDeltaRequest

OUTPUT_SCHEMA_NAME = "human_delta_labels_v1"

# The response shape the provider must return; semantic_labels and candidate_criterion are further
# validated against the pydantic models (contract enums) in service.py.
_LABELS = [
    "reduced_pressure", "increased_pressure", "kept_champion_involved",
    "removed_unnecessary_stakeholders", "added_missing_stakeholder", "delayed_cta",
    "removed_cta", "smaller_ask", "larger_ask", "changed_channel", "corrected_fact",
    "deferred_to_buyer_timing", "style_only"]

_CRITERION = {
    "type": "object",
    "additionalProperties": False,
    "required": ["statement", "suggested_eval_type"],
    "properties": {
        "statement": {"type": "string", "minLength": 1, "maxLength": 1000},
        "suggested_eval_type": {"type": "string"},
        "knowledge_candidate_id": {"type": ["string", "null"], "format": "uuid"}}}

OUTPUT_SCHEMA: dict[str, Any] = {
    "type": "object",
    "additionalProperties": False,
    "required": ["semantic_labels", "candidate_criterion"],
    "properties": {
        "semantic_labels": {"type": "array", "items": {"type": "string", "enum": _LABELS}},
        "candidate_criterion": {"anyOf": [{"type": "null"}, _CRITERION]}},
}

SYSTEM = """You label why a human edited an AI-proposed sales action before sending it.

You are given the selected candidate action, the final artifact the human is about to send, the literal
structured diff between them, and whether any existing evaluation predicted the change (`unexplained`).

Answer with:
- semantic_labels: the motivations the change most plausibly serves, drawn ONLY from this vocabulary:
  reduced_pressure, increased_pressure, kept_champion_involved, removed_unnecessary_stakeholders,
  added_missing_stakeholder, delayed_cta, removed_cta, smaller_ask, larger_ask, changed_channel,
  corrected_fact, deferred_to_buyer_timing, style_only.
  Prefer the empty list or a single label over a spray; 'style_only' means the substance is unchanged.
- candidate_criterion: REQUIRED exactly when the change is unexplained — a narrow, checkable statement a
  new evaluation could verify in the future ("the human softens a CTA when the buyer just asked for time"),
  with suggested_eval_type naming the eval axis it belongs to. For an explained change answer null: the
  listed evals already account for it.

Judge only what the diff shows. Do not invent account facts; everything you need is in the request.
"""


def build_user_prompt(request: HumanDeltaRequest) -> str:
    candidate = request.selected_candidate.model_dump(mode="json", exclude_none=True, exclude={"selected_eval_suite"})
    final = request.final_action.model_dump(mode="json", exclude_none=True)
    payload = {
        "selected_candidate": candidate,
        "final_action": final,
        "literal_changes": [c.model_dump(mode="json", exclude_none=True) for c in request.literal_changes],
        "unexplained": request.unexplained,
        "explaining_evals": [e.model_dump(mode="json") for e in request.explaining_evals],
    }
    return json.dumps(payload, indent=2)
