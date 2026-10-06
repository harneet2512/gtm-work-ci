"""One semantic judge call: rubric + catalog entry + context -> a contract EvalResult (or a recorded error)."""
from __future__ import annotations

import logging
import uuid
from collections.abc import Callable, Mapping
from datetime import datetime, timezone
from typing import Any

from pydantic import BaseModel, ConfigDict, Field

from ..errors import LLMError
from ..llm.provider import LLMProvider
from .catalog import EvalCatalog
from .context import Frozen, JudgeContext, thaw
from .output import interpret, output_schema, parse_answer
from .prompt import build_system_prompt, build_user_prompt
from .rubric import Rubric

log = logging.getLogger(__name__)

RESULT_NAMESPACE = uuid.UUID("5e1f0d2a-6b9c-4c38-9a8e-2f7d1c0b9e18")
Clock = Callable[[], datetime]


def utc_now() -> datetime:
    return datetime.now(timezone.utc)


class JudgeRun(BaseModel):
    """Which draft of which AgentRun is judged, and which repeated trial (HAR-97 §20) this is."""

    model_config = ConfigDict(frozen=True)

    agent_run_id: str
    draft_index: int = Field(default=1, ge=1)
    trial: int = Field(default=1, ge=1)
    candidate_id: str | None = None  # the StrategyCandidate judged; without one the run itself is the judged object


class JudgeOutcome(BaseModel):
    """A judge's EvalResult (contract-valid) plus its per-criterion findings, or the error that prevented it."""

    model_config = ConfigDict(frozen=True)

    eval_type: str
    trial: int
    result: Frozen = None
    criteria: Frozen = None
    error: str | None = None

    @property
    def ok(self) -> bool:
        return self.result is not None

    def eval_result(self) -> dict[str, Any] | None:
        """The EvalResult as plain JSON (validates against contracts/schemas/eval_result.v1.json)."""
        return thaw(self.result)


def result_id(run: JudgeRun, eval_type: str) -> str:
    return str(uuid.uuid5(RESULT_NAMESPACE, f"{run.agent_run_id}:{run.draft_index}:{eval_type}:{run.trial}"))


def judged_object(run: JudgeRun) -> dict[str, str]:
    """What this result is about (eval_result.v1.json judged_object)."""
    if run.candidate_id:
        return {"type": "StrategyCandidate", "id": run.candidate_id}
    return {"type": "AgentRun", "id": run.agent_run_id}


def span_id(run: JudgeRun) -> str:
    """The trace span the result attaches to: a judge looks at a candidate, so the candidates span of the run."""
    return f"candidates:{run.agent_run_id}"


def _eval_result(fields: Mapping[str, Any], rubric: Rubric, catalog: EvalCatalog, run: JudgeRun, model: str,
                 created_at: datetime) -> dict[str, Any]:
    entry = catalog.entry(rubric.eval_type)
    body = {k: v for k, v in fields.items() if k != "criteria"}
    return {"id": result_id(run, rubric.eval_type), "agent_run_id": run.agent_run_id, "draft_index": run.draft_index,
            "eval_type": rubric.eval_type, "eval_version": rubric.eval_version, "kind": entry.kind, **body,
            "evidence_class": entry.evidence_class, "model": model,
            "created_at": created_at.isoformat().replace("+00:00", "Z"),
            "judged_object": judged_object(run), "span_id": span_id(run)}


def judge(provider: LLMProvider, rubric: Rubric, catalog: EvalCatalog, context: JudgeContext, run: JudgeRun,
          *, clock: Clock = utc_now) -> JudgeOutcome:
    """Run one judge. Provider and output failures (LLMError) become an outcome with `error`; nothing else is
    swallowed."""
    entry = catalog.entry(rubric.eval_type)
    try:
        answer = provider.complete_json(system=build_system_prompt(rubric, entry, catalog),
                                        user=build_user_prompt(rubric, context, run.agent_run_id),
                                        schema=output_schema(rubric, entry, catalog), schema_name=rubric.schema_name)
        fields = interpret(parse_answer(answer.content), rubric, entry, catalog, context)
    except LLMError as exc:
        log.warning("judge failed", extra={"eval_type": rubric.eval_type, "error": type(exc).__name__})
        return JudgeOutcome(eval_type=rubric.eval_type, trial=run.trial, error=f"{type(exc).__name__}: {exc}")
    result = _eval_result(fields, rubric, catalog, run, answer.model, clock())
    return JudgeOutcome(eval_type=rubric.eval_type, trial=run.trial, result=result, criteria=fields["criteria"])
