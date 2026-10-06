"""/v1/human-delta: semantic labels for a HumanDelta, computed inside core's send transaction.

The call is self-contained (no run_token, no core pulls — contracts/openapi/worker.yaml). The labels come
from the model; contract correctness is enforced here: labels must be in the vocabulary (pydantic Literal
on parse), an unexplained delta must carry a usable candidate_criterion, and an explained delta's criterion
is dropped — the listed evals already account for it.
"""
from __future__ import annotations

import logging

from pydantic import BaseModel, ConfigDict, ValidationError

from ..errors import InvalidModelOutputError
from ..llm.provider import LLMProvider
from ..models.humandelta import CandidateCriterion, HumanDeltaRequest, HumanDeltaResponse, SemanticLabel
from .prompt import OUTPUT_SCHEMA, OUTPUT_SCHEMA_NAME, SYSTEM, build_user_prompt

log = logging.getLogger(__name__)


class _LabelOutput(BaseModel):
    """What the provider returns; validated again by the frozen response model at the API boundary."""

    model_config = ConfigDict(frozen=True)

    semantic_labels: tuple[SemanticLabel, ...] = ()
    candidate_criterion: CandidateCriterion | None = None


def _dedupe(labels: tuple[SemanticLabel, ...]) -> tuple[SemanticLabel, ...]:
    """semantic_labels is uniqueItems in the contract: keep first occurrence order."""
    return tuple(dict.fromkeys(labels))


def label_human_delta(request: HumanDeltaRequest, llm: LLMProvider) -> HumanDeltaResponse:
    result = llm.complete_json(system=SYSTEM, user=build_user_prompt(request),
                               schema=OUTPUT_SCHEMA, schema_name=OUTPUT_SCHEMA_NAME)
    try:
        out = _LabelOutput.model_validate(result.content)
    except ValidationError as exc:
        raise InvalidModelOutputError(f"delta labels out of contract: {exc.error_count()} errors") from exc
    criterion = out.candidate_criterion if request.unexplained else None
    if request.unexplained and criterion is None:
        raise InvalidModelOutputError("an unexplained delta needs a candidate_criterion")
    labels = _dedupe(out.semantic_labels)
    log.info("human delta labelled", extra={"episode_id": request.decision_episode_id,
                                          "unexplained": request.unexplained, "labels": len(labels),
                                          "model": result.model})
    return HumanDeltaResponse(semantic_labels=labels, candidate_criterion=criterion, model=result.model)
