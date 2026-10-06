"""POST /v1/judgment-inference (HAR-97 D5, HAR-129 section 11): the real JudgmentInference producer.

After the human's choice (and send) the model says why the human chose what they chose and classifies the
meaning of any edit. Everything objective is computed here, never taken from the model:
  - agreement is the choice itself, eval_differences come from the two eval bundles;
  - a click alone is at most a weak signal, an edit at most moderate; only an explicit instruction that quotes
    the human's own words is strong (E10.4);
  - a recipient edit is always classed stakeholder/recipient (E10.3);
  - an unknown inference offers no labels (E10.6);
  - evidence refs are only activities the candidates really cite; with none available the call is refused
    (rule R1: no evidence, no inference), never invented.
"""
from __future__ import annotations

import json
import logging
from typing import Any

from pydantic import BaseModel, ConfigDict, ValidationError

from ..errors import InvalidModelOutputError, InvalidRequestError
from ..llm.provider import LLMProvider
from .inference_models import (EDIT_CLASSES, LABELS, SIGNALS, InferenceRequest, InferenceResponse)

log = logging.getLogger(__name__)

PROMPT_VERSION = "judgment_inference:v1"
SCHEMA_NAME = "judgment_inference_v1"
RECIPIENT_KINDS = {"recipient_added", "recipient_removed", "recipient_role_changed"}
SIGNAL_RANK = {"weak": 0, "moderate": 1, "strong": 2}

OUTPUT_SCHEMA: dict[str, Any] = {
    "type": "object", "additionalProperties": False,
    "required": ["statement", "semantic_labels", "edit_class", "signal_strength", "explicit_instructions",
                 "unknown", "confidence", "candidate_differences", "evidence_activity_ids", "knowledge_refs",
                 "no_applicable_knowledge"],
    "properties": {
        "statement": {"type": "string", "minLength": 1, "maxLength": 1500},
        "semantic_labels": {"type": "array", "items": {"type": "string", "enum": list(LABELS)}},
        "edit_class": {"type": "array", "items": {"type": "string", "enum": list(EDIT_CLASSES)}},
        "signal_strength": {"type": "string", "enum": list(SIGNALS)},
        "explicit_instructions": {"type": "array", "items": {
            "type": "object", "additionalProperties": False, "required": ["instruction", "quote"],
            "properties": {"instruction": {"type": "string"}, "quote": {"type": "string"}}}},
        "unknown": {"type": "boolean"},
        "confidence": {"type": "number", "minimum": 0, "maximum": 1},
        "candidate_differences": {"type": "array", "items": {"type": "string"}},
        "evidence_activity_ids": {"type": "array", "items": {"type": "string"}},
        "knowledge_refs": {"type": "array", "items": {"type": "string"}},
        "no_applicable_knowledge": {"type": "boolean"}}}

SYSTEM = """You interpret a human's judgment in a B2B sales workflow run by gtm_ai.

You get the three candidate actions gtm_ai proposed (strategy type, action class, rationale, evidence), which one
gtm_ai preferred, which one the human chose, the eval verdicts of both, any edits the human made, and any note.
Say why the human most plausibly chose what they chose, and what an edit means.

Rules:
- statement: one or two sentences, what the human valued differently from gtm_ai (or that they agreed).
- semantic_labels: only from the vocabulary; fewer is better; empty when none fits.
- edit_class: what kind of change an edit made (factual, state, strategy, stakeholder, timing, cta, style,
  recipient, risk, tone, wording, new_info). A copy edit is wording or tone, never strategy; new_info is a fact the
  human added that the candidates did not have. Empty when there are no edits.
- signal_strength: weak for a bare choice, moderate for an edit, strong ONLY when the human stated an explicit
  instruction in their note; explicit_instructions must quote the note verbatim and never be invented.
- confidence: 0 to 1, how sure you are of your own reading.
- unknown: true when you cannot tell why; then give no labels and say so in the statement.
- evidence_activity_ids: only activity ids that appear in the candidates' evidence.
Do not invent account facts."""


class _Output(BaseModel):
    model_config = ConfigDict(extra="ignore")

    statement: str
    semantic_labels: list[str] = []
    edit_class: list[str] = []
    signal_strength: str = "weak"
    explicit_instructions: list[dict[str, str]] = []
    unknown: bool = False
    confidence: float = 0.5
    candidate_differences: list[str] = []
    evidence_activity_ids: list[str] = []
    knowledge_refs: list[str] = []
    no_applicable_knowledge: bool = True


def _candidates(req: InferenceRequest) -> dict[str, dict[str, Any]]:
    return {str(c.get("candidate_id")): c for c in req.strategy_set.get("candidates", [])}


def _view(c: dict[str, Any]) -> dict[str, Any]:
    keys = ("candidate_id", "strategy_type", "action_class", "action_type", "rationale", "ranking")
    out = {k: c.get(k) for k in keys}
    out["evidence_activity_ids"] = [r.get("activity_id") for r in c.get("evidence_refs", [])]
    return out


def _prompt(req: InferenceRequest, cands: dict[str, dict[str, Any]]) -> str:
    h = req.human_strategy_decision
    payload = {"candidates": [_view(c) for c in cands.values()],
               "gtm_ai_preferred": h.get("original_agent_preference"),
               "human_chose": h.get("selected_candidate_id"),
               "eval_verdicts": {b.get("strategy_candidate_id"): {i.get("eval_type"): i.get("verdict")
                                                                  for i in b.get("items", [])}
                                 for b in req.eval_bundles},
               "edits": h.get("edits", []), "human_note": req.human_note,
               "state_context": req.state_context}
    return json.dumps(payload, indent=2, default=str)


def eval_differences(req: InferenceRequest) -> list[dict[str, Any]]:
    """Per eval type, where gtm_ai's preferred and the human's chosen candidate got different verdicts."""
    h = req.human_strategy_decision
    by_cand = {b.get("strategy_candidate_id"): {i["eval_type"]: i.get("verdict") for i in b.get("items", [])}
               for b in req.eval_bundles}
    pref, chosen = by_cand.get(h.get("original_agent_preference"), {}), by_cand.get(h.get("selected_candidate_id"), {})
    return [{"eval_type": t, "agent_preference_verdict": pref[t], "human_choice_verdict": chosen[t]}
            for t in pref if t in chosen and pref[t] != chosen[t]]


def _signal(model_value: str, has_edits: bool, quoted: bool) -> str:
    ceiling = "strong" if quoted else ("moderate" if has_edits else "weak")
    return model_value if SIGNAL_RANK[model_value] <= SIGNAL_RANK[ceiling] else ceiling


def _instructions(raw: list[dict[str, str]], note: str | None) -> list[dict[str, str]]:
    """Only instructions whose quote really appears in the human's note survive (never invented)."""
    text = (note or "").lower()
    return [{"instruction": i["instruction"][:500], "quote": i["quote"][:1000]} for i in raw
            if i.get("instruction") and i.get("quote") and i["quote"].lower() in text]


def _evidence(out: _Output, cands: dict[str, dict[str, Any]], chosen: str) -> tuple[list[dict[str, str]], bool]:
    """The evidence refs and whether the model really cited them. Only ids the model cited AND a candidate carries
    count as cited. With none, the chosen candidate's evidence is kept as uncited context (the contract needs a
    ref) and the caller marks the inference unknown, so it can never read as a grounded interpretation."""
    allowed = [r["activity_id"] for c in cands.values() for r in c.get("evidence_refs", []) if r.get("activity_id")]
    picked = [a for a in dict.fromkeys(out.evidence_activity_ids) if a in allowed]
    if picked:
        return [{"activity_id": a} for a in picked], True
    context = [r["activity_id"] for r in cands[chosen].get("evidence_refs", []) if r.get("activity_id")]
    if not context:
        context = list(dict.fromkeys(allowed))[:3]
    if not context:
        raise InvalidRequestError("no candidate cites evidence, so no judgment inference can be grounded")
    return [{"activity_id": a} for a in context], False


def _classes(model_classes: list[str], edits: list[dict[str, Any]]) -> list[str]:
    classes = [c for c in dict.fromkeys(model_classes) if c in EDIT_CLASSES] if edits else []
    if any(e.get("kind") in RECIPIENT_KINDS for e in edits) and not {"stakeholder", "recipient"} & set(classes):
        classes = [*classes, "recipient"]
    return classes


def infer_judgment(req: InferenceRequest, llm: LLMProvider) -> InferenceResponse:
    cands = _candidates(req)
    h = req.human_strategy_decision
    chosen = str(h.get("selected_candidate_id"))
    if chosen not in cands:
        raise InvalidRequestError("the chosen candidate is not in the strategy set")
    result = llm.complete_json(system=SYSTEM, user=_prompt(req, cands), schema=OUTPUT_SCHEMA,
                               schema_name=SCHEMA_NAME)
    try:
        out = _Output.model_validate(result.content)
    except ValidationError as exc:
        raise InvalidModelOutputError(f"judgment inference out of contract: {exc.error_count()} errors") from exc
    if any(label not in LABELS for label in out.semantic_labels) or out.signal_strength not in SIGNALS:
        raise InvalidModelOutputError("judgment inference used a label outside the vocabulary")
    edits = list(h.get("edits") or [])
    instructions = _instructions(out.explicit_instructions, req.human_note)
    refs, cited = _evidence(out, cands, chosen)
    unknown = out.unknown or not cited
    delta: dict[str, Any] = {
        "statement": out.statement if cited or out.unknown else out.statement + " (not grounded: the inference cited no candidate evidence)",
        "semantic_labels": [] if unknown else list(dict.fromkeys(out.semantic_labels)),
        "confidence": round(min(1.0, max(0.0, out.confidence)), 3) if not unknown else min(0.3, max(0.0, out.confidence)),
        "edit_class": _classes(out.edit_class, edits),
        "signal_strength": "weak" if unknown else _signal(out.signal_strength, bool(edits), bool(instructions)),
        "explicit_instructions": instructions, "unknown": unknown}
    evidence = {"candidate_differences": out.candidate_differences[:10] or ["the two candidates differ"],
                "evidence_refs": refs, "eval_differences": eval_differences(req),
                "knowledge_refs": out.knowledge_refs, "no_applicable_knowledge": out.no_applicable_knowledge}
    log.info("judgment inferred", extra={"episode_id": req.decision_episode_id, "unknown": unknown,
                                         "model": result.model})
    return InferenceResponse(inferred_semantic_delta=delta, evidence=evidence, model=result.model)
