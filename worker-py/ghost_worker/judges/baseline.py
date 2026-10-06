"""Reference judge that calls no model, for the benchmark: what agreement does a trivial grader get?

AlwaysPassProvider answers every judge with verdict pass, the rubric's pass label and no diagnostics. A real
judge has to beat it on fail recall and false-pass rate; on raw agreement it can look deceptively good because
many gold judgments are passes. It names one state field it did not read ("stage", present in every case), because
rule R1 (no evidence, no pass) would otherwise turn its pass into unknown: R1 is a floor on honesty, not a measure
of quality, and a nominal citation clears it, which is why the baseline is still the trivial grader to beat.
"""
from __future__ import annotations

from collections.abc import Mapping
from typing import Any

from ..errors import InvalidModelOutputError
from ..llm.provider import LLMResult, TurnResult
from .rubric import Rubric

ALWAYS_PASS_MODEL = "baseline/always-pass"


class AlwaysPassProvider:
    def __init__(self, rubrics: Mapping[str, Rubric]) -> None:
        self._by_schema = {r.schema_name: r for r in rubrics.values()}

    def complete_json(self, *, system: str, user: str, schema: dict[str, Any], schema_name: str) -> LLMResult:
        rubric = self._by_schema.get(schema_name)
        if rubric is None:
            raise InvalidModelOutputError(f"baseline has no rubric for {schema_name}")
        criteria = {cid: {"finding": "unknown", "direction": None, "magnitude": None, "note": ""}
                    for cid in rubric.criterion_ids()}
        content = {"verdict": "pass", "label": rubric.pass_label, "diagnostics": [], "blocks": False,
                   "reason": "Baseline: every candidate passes.", "criteria": criteria, "state_refs": ["stage"],
                   "evidence_refs": [], "knowledge_refs": [], "suggested_correction": None, "confidence": 0.5}
        return LLMResult(content=content, model=ALWAYS_PASS_MODEL)

    def complete_turn(self, *, system: str, messages: list[dict[str, Any]], tools: list[dict[str, Any]],
                      schema: dict[str, Any], schema_name: str) -> TurnResult:
        raise InvalidModelOutputError("the baseline judge does not take tool turns")
