"""HAR-97 L4 knowledge evals over one learner output, compared with a reference lesson.

- knowledge_extraction_fidelity: did the correction reveal a reusable principle (and did the learner notice),
  and did it infer the right principle (semantic judge) without over-generalising it?
- knowledge_scope_exception_quality: did it scope the knowledge to the right state/transition and capture the
  exceptions (deterministic)?"""
from __future__ import annotations

from typing import Literal

from pydantic import BaseModel, ConfigDict

from .models import Correction, InferredKnowledge, ReferenceLesson
from .principle import PrincipleJudge, PrincipleJudgment
from .scope import ExceptionResult, ScopeResult, exception_quality, scope_quality

ReusableLabel = Literal["PRINCIPLE_DETECTED", "PRINCIPLE_MISSED", "SPURIOUS_PRINCIPLE", "CORRECT_ABSTENTION"]
Verdict = Literal["pass", "fail"]


class ExtractionFidelity(BaseModel):
    model_config = ConfigDict(frozen=True)

    eval_type: Literal["knowledge_extraction_fidelity"] = "knowledge_extraction_fidelity"
    verdict: Verdict
    reusable: ReusableLabel
    principle: PrincipleJudgment | None  # None when nothing was (or should have been) learned
    over_generalised: bool  # the inferred rule drops conditions the lesson needs ("Always CC the champion")


class ScopeExceptionQuality(BaseModel):
    model_config = ConfigDict(frozen=True)

    eval_type: Literal["knowledge_scope_exception_quality"] = "knowledge_scope_exception_quality"
    verdict: Verdict
    scope: ScopeResult | None
    exceptions: ExceptionResult | None


class KnowledgeEvalReport(BaseModel):
    model_config = ConfigDict(frozen=True)

    extraction_fidelity: ExtractionFidelity
    scope_exception_quality: ScopeExceptionQuality


def reusable_label(reference: ReferenceLesson, inferred: InferredKnowledge | None) -> ReusableLabel:
    if reference.reusable:
        return "PRINCIPLE_DETECTED" if inferred is not None else "PRINCIPLE_MISSED"
    return "SPURIOUS_PRINCIPLE" if inferred is not None else "CORRECT_ABSTENTION"


def evaluate(correction: Correction, reference: ReferenceLesson, inferred: InferredKnowledge | None,
             judge: PrincipleJudge) -> KnowledgeEvalReport:
    """Run both L4 evals. The judge is called only when there is a principle to compare."""
    reusable = reusable_label(reference, inferred)
    if reusable != "PRINCIPLE_DETECTED" or inferred is None:
        ok: Verdict = "pass" if reusable == "CORRECT_ABSTENTION" else "fail"
        return KnowledgeEvalReport(
            extraction_fidelity=ExtractionFidelity(verdict=ok, reusable=reusable, principle=None, over_generalised=False),
            scope_exception_quality=ScopeExceptionQuality(verdict=ok, scope=None, exceptions=None))
    scope = scope_quality(inferred.scope, reference.scope)
    exceptions = exception_quality(inferred.exceptions, reference.exceptions, inferred.scope)
    principle = judge.judge(correction, reference, inferred)
    over = scope.label in ("OVER_GENERALISED", "WRONG_SCOPE")
    fidelity_ok = principle.label == "SAME_PRINCIPLE" and not over
    scope_ok = scope.label == "SCOPE_MATCH" and exceptions.label in ("EXCEPTIONS_CAPTURED", "NO_EXCEPTIONS_EXPECTED") \
        and not exceptions.unreachable
    return KnowledgeEvalReport(
        extraction_fidelity=ExtractionFidelity(verdict="pass" if fidelity_ok else "fail", reusable=reusable,
                                               principle=principle, over_generalised=over),
        scope_exception_quality=ScopeExceptionQuality(verdict="pass" if scope_ok else "fail", scope=scope,
                                                      exceptions=exceptions))
