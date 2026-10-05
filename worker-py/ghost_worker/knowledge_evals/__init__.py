"""HAR-97 L4 knowledge evals (WP20, HAR-118): extraction fidelity and scope / exception quality.

Structural comparisons are code; only the principle comparison is semantic and goes through the LLM provider
(replayed from cassettes in tests, never live)."""
from .fidelity import ExtractionFidelity, KnowledgeEvalReport, ScopeExceptionQuality, evaluate, reusable_label
from .models import Condition, Correction, Guidance, InferredKnowledge, KnowledgeException, ReferenceLesson
from .principle import PRINCIPLE_JUDGE_VERSION, PrincipleJudge, PrincipleJudgment
from .scope import ExceptionResult, ScopeResult, exception_quality, scope_quality

__all__ = [
    "PRINCIPLE_JUDGE_VERSION", "Condition", "Correction", "ExceptionResult", "ExtractionFidelity", "Guidance",
    "InferredKnowledge", "KnowledgeEvalReport", "KnowledgeException", "PrincipleJudge", "PrincipleJudgment",
    "ReferenceLesson", "ScopeExceptionQuality", "ScopeResult", "evaluate", "exception_quality", "reusable_label",
    "scope_quality",
]
