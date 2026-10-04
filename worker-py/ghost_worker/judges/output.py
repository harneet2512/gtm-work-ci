"""The judge's answer: a strict JSON schema per rubric, its Pydantic twin, and the checks that turn it into the
fields of an EvalResult (labels from the catalog, blocking per the catalog, refs only from the judge's context)."""
from __future__ import annotations

from collections.abc import Mapping
from typing import Annotated, Any, Literal

from pydantic import BaseModel, ConfigDict, Field, ValidationError

from ..errors import InvalidModelOutputError
from .catalog import CatalogEntry, EvalCatalog
from .context import JudgeContext
from .rubric import Rubric

Verdict = Literal["pass", "warn", "fail", "abstain"]
Finding = Literal["met", "not_met", "partial", "not_applicable", "unknown"]
Direction = Literal["below", "matches", "above"]
Magnitude = Literal["none", "slight", "large"]
_NULLABLE = {"type": ["string", "null"]}


def _criterion_schema() -> dict[str, Any]:
    return {"type": "object", "additionalProperties": False,
            "required": ["finding", "direction", "magnitude", "note"],
            "properties": {"finding": {"type": "string", "enum": list(Finding.__args__)},
                           "direction": {"type": ["string", "null"], "enum": [*Direction.__args__, None]},
                           "magnitude": {"type": ["string", "null"], "enum": [*Magnitude.__args__, None]},
                           "note": {"type": "string"}}}


def output_schema(rubric: Rubric, entry: CatalogEntry, catalog: EvalCatalog) -> dict[str, Any]:
    """Strict-mode friendly: closed objects, every key required, optional values nullable."""
    label = ({"type": ["string", "null"], "enum": [*entry.labels, None]} if entry.labels else {"type": "null"})
    ids = list(rubric.criterion_ids())
    return {
        "type": "object", "additionalProperties": False,
        "required": ["verdict", "label", "diagnostics", "blocks", "reason", "criteria", "state_refs",
                     "evidence_refs", "knowledge_refs", "suggested_correction", "confidence"],
        "properties": {
            "verdict": {"type": "string", "enum": list(Verdict.__args__)},
            "label": label,
            "diagnostics": {"type": "array", "items": {"type": "string", "enum": list(catalog.diagnostics)}},
            "blocks": {"type": "boolean"},
            "reason": {"type": "string"},
            "criteria": {"type": "object", "additionalProperties": False, "required": ids,
                         "properties": {cid: _criterion_schema() for cid in ids}},
            "state_refs": {"type": "array", "items": {"type": "string"}},
            "evidence_refs": {"type": "array", "items": {
                "type": "object", "additionalProperties": False, "required": ["activity_id", "quote"],
                "properties": {"activity_id": {"type": "string"}, "quote": _NULLABLE}}},
            "knowledge_refs": {"type": "array", "items": {"type": "string"}},
            "suggested_correction": _NULLABLE,
            "confidence": {"type": "number", "minimum": 0, "maximum": 1},
        },
    }


class _Answer(BaseModel):
    model_config = ConfigDict(frozen=True, extra="ignore")


class CriterionFinding(_Answer):
    finding: Finding
    direction: Direction | None = None
    magnitude: Magnitude | None = None
    note: Annotated[str, Field(max_length=1000)] = ""


class CitedEvidence(_Answer):
    activity_id: str
    quote: str | None = None


class JudgeAnswer(_Answer):
    verdict: Verdict
    label: str | None = None
    diagnostics: tuple[str, ...] = ()
    blocks: bool = False
    reason: Annotated[str, Field(min_length=1, max_length=2000)]
    criteria: Mapping[str, CriterionFinding]
    state_refs: tuple[str, ...] = ()
    evidence_refs: tuple[CitedEvidence, ...] = ()
    knowledge_refs: tuple[str, ...] = ()
    suggested_correction: Annotated[str, Field(max_length=2000)] | None = None
    confidence: Annotated[float, Field(ge=0, le=1)]


def parse_answer(content: Mapping[str, Any]) -> JudgeAnswer:
    try:
        return JudgeAnswer.model_validate(dict(content))
    except ValidationError as exc:
        fields = ", ".join(sorted({".".join(str(p) for p in e["loc"]) or "<root>" for e in exc.errors()}))
        raise InvalidModelOutputError(f"judge answer is malformed (fields: {fields})") from None


def _label(answer: JudgeAnswer, entry: CatalogEntry) -> str | None:
    if not entry.labels:
        if answer.label is not None:
            raise InvalidModelOutputError(f"{entry.eval_type} has no labels but the judge gave {answer.label}")
        return None
    if answer.label is None and answer.verdict != "abstain":
        raise InvalidModelOutputError(f"{entry.eval_type} needs one of its catalog labels")
    if answer.label is not None and answer.label not in entry.labels:
        raise InvalidModelOutputError(f"label {answer.label} is not a catalog label of {entry.eval_type}")
    return answer.label


def _diagnostics(answer: JudgeAnswer, rubric: Rubric, catalog: EvalCatalog) -> tuple[str, ...]:
    """Only the EvalResult contract's diagnostics. The HAR-97 L2 spellings (missing_decision_maker, ...) are not
    accepted: the output schema's enum is the contract enum, so they could never reach this check."""
    unknown = sorted(set(answer.diagnostics) - set(catalog.diagnostics))
    if unknown:
        raise InvalidModelOutputError(f"unknown diagnostics {unknown}")
    return tuple(dict.fromkeys(answer.diagnostics))


def _criteria(answer: JudgeAnswer, rubric: Rubric) -> dict[str, dict[str, Any]]:
    expected = set(rubric.criterion_ids())
    if set(answer.criteria) != expected:
        raise InvalidModelOutputError(f"criteria must be exactly {sorted(expected)}")
    for c in rubric.criteria:
        found = answer.criteria[c.id]
        if c.output == "direction_magnitude" and found.finding not in {"not_applicable", "unknown"} and (
                found.direction is None or found.magnitude is None):
            raise InvalidModelOutputError(f"criterion {c.id} needs a direction and a magnitude")
    return {cid: answer.criteria[cid].model_dump() for cid in rubric.criterion_ids()}


def _squash(text: str) -> str:
    return " ".join(text.split())


def _evidence(answer: JudgeAnswer, context: JudgeContext) -> list[dict[str, Any]]:
    """Cited activities must be in the judge's context (the trigger or a supporting activity); a quote is kept only
    if it is verbatim there. An id that only the candidate cites (its text was never shown to the judge) is
    unverified: dropped, never copied into evidence_refs or activity_refs. Any other unknown id is an error."""
    activities, refs, seen = context.activities(), [], set()
    candidate_cited = context.candidate_cited()
    for cited in answer.evidence_refs:
        activity = activities.get(cited.activity_id)
        if activity is None and cited.activity_id in candidate_cited:
            continue
        if activity is None:
            raise InvalidModelOutputError("judge cited an activity that is not in its context")
        if cited.activity_id in seen:
            continue
        seen.add(cited.activity_id)
        ref: dict[str, Any] = {"activity_id": cited.activity_id}
        if cited.quote and _squash(cited.quote) in _squash(str(activity.get("text", ""))):
            ref["quote"] = cited.quote
        if activity.get("actor_person_id"):
            ref["speaker_person_id"] = activity["actor_person_id"]
        if activity.get("occurred_at"):
            ref["occurred_at"] = activity["occurred_at"]
        refs.append(ref)
    return refs


def _knowledge(answer: JudgeAnswer, context: JudgeContext) -> list[str]:
    offered = context.knowledge_ids()
    if set(answer.knowledge_refs) - offered:
        raise InvalidModelOutputError("judge cited knowledge that was not offered")
    return list(dict.fromkeys(answer.knowledge_refs))


def interpret(answer: JudgeAnswer, rubric: Rubric, entry: CatalogEntry, catalog: EvalCatalog,
              context: JudgeContext) -> dict[str, Any]:
    """EvalResult fields decided by the answer; blocking = catalog can_block AND fail AND the rule is met."""
    evidence = _evidence(answer, context)
    known_refs = context.state_ref_names()
    return {
        "verdict": answer.verdict,
        "label": _label(answer, entry),
        "diagnostics": list(_diagnostics(answer, rubric, catalog)),
        "blocking": entry.can_block and answer.verdict == "fail" and answer.blocks,
        "reason": answer.reason,
        "state_refs": [r for r in dict.fromkeys(answer.state_refs) if r in known_refs],
        "activity_refs": [r["activity_id"] for r in evidence],
        "evidence_refs": evidence,
        "knowledge_refs": _knowledge(answer, context),
        "suggested_correction": answer.suggested_correction,
        "confidence": answer.confidence,
        "criteria": _criteria(answer, rubric),
    }
