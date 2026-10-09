"""POST /v1/decision-judge: the model judges of Bucket 2 (HAR-97 D1, D2, D3, D8) and the ranking rationale
producer (D3a). One model call per request.

Each judge returns per-dimension verdicts (one call covers every dimension, which keeps cost low) and obeys:
  - R1: a pass, warn or fail that cites no allowed evidence id is unknown; ids outside `evidence_ids` are dropped;
  - a missing dimension is unknown, never pass; `abstain` is read as unknown;
  - R2: decision judges never see an email body, subject or preview; only candidate_quality and final_artifact do;
  - R3: the set-level judge sees all three candidates in an order shuffled per trial (position bias control).
"""
from __future__ import annotations

import copy
import json
import logging
import hashlib
import random
import re
from typing import Any, Literal

from pydantic import BaseModel, ConfigDict, Field

from ..errors import InvalidModelOutputError
from ..llm.provider import LLMProvider

_UUID = re.compile(r"[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}")
log = logging.getLogger(__name__)

PROMPT_VERSION = "decision_judge:v1"
Kind = Literal["intent_fit", "set_quality", "candidate_quality", "ranking", "final_artifact", "rank_rationale",
              "b1_inference_boundary", "b3_prior_context", "b5_precedent_relevance", "b8_synthesis"]
# Bucket 1 (B1, B3, B5, B8) judges read the source text of the episode; Bucket 2 decision judges never do (R2).
BUCKET1_KINDS = ("b1_inference_boundary", "b3_prior_context", "b5_precedent_relevance", "b8_synthesis")
VERDICTS = ("pass", "warn", "fail", "unknown", "not_relevant")
BODY_KEYS = ("full_action_artifact", "subject", "preview", "body", "artifact")

DIMENSIONS: dict[str, tuple[str, ...]] = {
    "intent_fit": ("intent_follows_state", "timing", "relationship_supports", "uncertainty_handled",
                   "knowledge_used_correctly", "quiet_move_considered"),
    "set_quality": ("materially_different", "coverage", "no_dominated"),
    "candidate_quality": ("fit", "grounding", "intent_coherence", "recipients", "timing", "cta",
                          "factual_integrity", "knowledge_use", "unsupported_assumptions", "risk"),
    "b1_inference_boundary": ("inference_boundary",),
    "b3_prior_context": ("supporting_and_conflicting_links",),
    "b5_precedent_relevance": ("relevance_and_misses",),
    "b8_synthesis": ("confidence_and_omissions",),
    "ranking": ("supported_by_evidence_state", "supported_by_knowledge", "uncertainty_reflected",
                "no_blocked_preferred", "rationale_matches_basis"),
    "final_artifact": ("intent_preserved", "claims_grounded", "cta_timing_correct"),
}

TASKS: dict[str, str] = {
    "intent_fit": "Was the class of reaction right for the current state: ACT, WAIT, ASK, ESCALATE or NO_ACTION? "
                  "Set right_reaction to the class you judge right and quiet_move_considered to whether a quiet "
                  "move (wait, ask, no action) was considered in the set.",
    "set_quality": "Judge the three candidates as a set: are they materially different decisions, not paraphrases "
                   "(materially_different); do they cover the defensible moves (coverage); is any candidate "
                   "obviously dominated or invalid yet silently included (no_dominated, fail when one is)?",
    "candidate_quality": "Judge this one candidate on every dimension. unsupported_assumptions fails when a premise "
                         "relies on a fact the state marks unknown. factual_integrity covers numbers, dates, "
                         "commitments. risk covers relationship and policy constraints.",
    "ranking": "Judge the ranking: is it supported by the state and evidence, by applicable knowledge, does it "
               "reflect uncertainty, is no blocked or restricted candidate preferred, and does the stated "
               "rationale match the real basis of the order?",
    "final_artifact": "Judge the exact artifact about to be sent: does it preserve the final intent, are its claims "
                      "grounded in the cited evidence, are the CTA and timing correct?",
    "b1_inference_boundary": "Does the extraction keep an explicit fact apart from an inference? A claim labelled a fact "
                             "must be stated in its activity; a conclusion drawn from the activity must be labelled an "
                             "inference. Fail when an inference is presented as a source fact.",
    "b3_prior_context": "Does each new claim link to the prior claims that support it and the ones that conflict with it? "
                        "Fail when a supporting or conflicting prior claim is ignored or a conflict is silently resolved.",
    "b5_precedent_relevance": "Are the retrieved precedents situationally relevant to the present state (same transition, "
                              "stage, stakeholders), not merely textually similar, and was an important earlier case missed?",
    "b8_synthesis": "Are the stated beliefs about the account supported by the evidence, is confidence appropriate, and "
                    "is important counter-evidence omitted from the summary?",
}

SYSTEM = """You are an evaluator for gtm_ai, a B2B sales decision assistant. Answer with one verdict per requested
dimension: pass, warn, fail, unknown or not_relevant. Cite evidence_refs only from the evidence ids you were
given; a verdict that cites none is treated as unknown, so say unknown when the evidence cannot settle it. Keep each
why to one or two sentences. Do not invent facts."""


class JudgeRequest(BaseModel):
    model_config = ConfigDict(frozen=True, extra="forbid")

    kind: Kind
    subject_id: str = Field(min_length=1, max_length=200)
    payload: dict[str, Any]
    evidence_ids: list[str] = []
    trial: int = Field(default=0, ge=0)


class DimensionVerdict(BaseModel):
    model_config = ConfigDict(frozen=True)

    dimension: str
    verdict: Literal["pass", "warn", "fail", "unknown", "not_relevant"]
    why: str
    evidence_refs: list[str]


class JudgeResponse(BaseModel):
    model_config = ConfigDict(frozen=True)

    kind: str
    subject_id: str
    dimensions: list[DimensionVerdict] = []
    summary: str = ""
    right_reaction: str | None = None
    quiet_move_considered: bool | None = None
    pairwise_reasons: list[dict[str, Any]] = []
    model: str
    prompt_version: str = PROMPT_VERSION


_DIM_SCHEMA = {"type": "object", "additionalProperties": False, "required": ["dimension", "verdict", "why", "evidence_refs"],
               "properties": {"dimension": {"type": "string"}, "verdict": {"type": "string", "enum": [*VERDICTS, "abstain"]},
                              "why": {"type": "string"}, "evidence_refs": {"type": "array", "items": {"type": "string"}}}}
JUDGE_SCHEMA: dict[str, Any] = {
    "type": "object", "additionalProperties": False, "required": ["dimensions", "summary"],
    "properties": {"dimensions": {"type": "array", "items": _DIM_SCHEMA}, "summary": {"type": "string"},
                   "right_reaction": {"type": ["string", "null"]}, "quiet_move_considered": {"type": ["boolean", "null"]}}}
RATIONALE_SCHEMA: dict[str, Any] = {
    "type": "object", "additionalProperties": False, "required": ["pairwise_reasons"],
    "properties": {"pairwise_reasons": {"type": "array", "items": {
        "type": "object", "additionalProperties": False,
        "required": ["ranked_higher_id", "ranked_lower_id", "reason", "evidence_refs", "knowledge_refs"],
        "properties": {"ranked_higher_id": {"type": "string"}, "ranked_lower_id": {"type": "string"},
                       "reason": {"type": "string"}, "evidence_refs": {"type": "array", "items": {"type": "string"}},
                       "knowledge_refs": {"type": "array", "items": {"type": "string"}}}}}}}


def _strip_bodies(value: Any) -> Any:
    if isinstance(value, dict):
        return {k: _strip_bodies(v) for k, v in value.items() if k not in BODY_KEYS}
    if isinstance(value, list):
        return [_strip_bodies(v) for v in value]
    return value


def _prepare(req: JudgeRequest) -> dict[str, Any]:
    payload = copy.deepcopy(req.payload)
    if req.kind not in ("candidate_quality", "final_artifact", *BUCKET1_KINDS):
        payload = _strip_bodies(payload)
    if req.kind in ("set_quality", "intent_fit", "ranking") and isinstance(payload.get("candidates"), list):
        payload["candidates"] = _shuffled(payload["candidates"], req.trial)
    return payload


def _content_key(value: Any) -> str:
    """The candidate's content with every UUID masked: stable across runs, which mint fresh ids."""
    return _UUID.sub("<id>", json.dumps(value, sort_keys=True, default=str, ensure_ascii=False))


def _shuffled(candidates: list[Any], trial: int) -> list[Any]:
    """Position-bias shuffle that is a pure function of the candidates' content and the trial, never of run ids:
    the same set prompts byte-identically (up to id placeholders) on every run, so its cassette is found."""
    ordered = sorted(candidates, key=_content_key)
    seed = hashlib.sha256("|".join([*map(_content_key, ordered), str(trial)]).encode()).hexdigest()
    random.Random(seed).shuffle(ordered)
    return ordered


def _first_appearance(ids: list[str], payload: dict[str, Any]) -> list[str]:
    """Evidence ids in the order they first occur in the payload (the rest keep their order after them), so the
    id placeholders of the cache key follow the stable candidate order, not the order of random uuids."""
    text = json.dumps(payload, default=str, ensure_ascii=False)

    def position(evidence_id: str) -> int:
        found = text.find(evidence_id.rsplit(":", 1)[-1])  # "candidate:<uuid>" is quoted as the bare uuid
        return len(text) if found < 0 else found

    return sorted(ids, key=position)  # sorted() is stable


def _user_prompt(req: JudgeRequest, payload: dict[str, Any], names: tuple[str, ...]) -> str:
    return json.dumps({"task": TASKS[req.kind], "dimensions": list(names), "evidence_ids": _first_appearance(req.evidence_ids, payload),
                       **payload}, indent=2, default=str)


def _clean(dim: dict[str, Any] | None, name: str, allowed: set[str]) -> DimensionVerdict:
    if dim is None:
        return DimensionVerdict(dimension=name, verdict="unknown", why="the judge gave no verdict for this dimension",
                                evidence_refs=[])
    verdict = "unknown" if dim["verdict"] == "abstain" else dim["verdict"]
    refs = [r for r in dict.fromkeys(dim.get("evidence_refs") or []) if r in allowed]
    if verdict in ("pass", "warn", "fail") and not refs:
        verdict = "unknown"
    return DimensionVerdict(dimension=name, verdict=verdict, why=(dim.get("why") or "no reason given")[:1000],
                            evidence_refs=refs)


def judge(req: JudgeRequest, llm: LLMProvider) -> JudgeResponse:
    if req.kind == "rank_rationale":
        return rank_rationale(req, llm)
    names = DIMENSIONS[req.kind]
    result = llm.complete_json(system=SYSTEM, user=_user_prompt(req, _prepare(req), names), schema=JUDGE_SCHEMA,
                               schema_name=f"decision_judge_{req.kind}_v1")
    content = result.content
    raw = content.get("dimensions")
    if not isinstance(raw, list) or any(not isinstance(d, dict) or d.get("verdict") not in (*VERDICTS, "abstain") for d in raw):
        raise InvalidModelOutputError("decision judge out of contract: dimensions or verdicts are invalid")
    by_name = {d.get("dimension"): d for d in raw}
    allowed = set(req.evidence_ids)
    dims = [_clean(by_name.get(n), n, allowed) for n in names]
    reaction = content.get("right_reaction") if req.kind == "intent_fit" else None
    quiet = content.get("quiet_move_considered") if req.kind == "intent_fit" else None
    log.info("decision judged", extra={"kind": req.kind, "subject_id": req.subject_id, "model": result.model})
    return JudgeResponse(kind=req.kind, subject_id=req.subject_id, dimensions=dims,
                         summary=str(content.get("summary", ""))[:1000], right_reaction=reaction,
                         quiet_move_considered=quiet if isinstance(quiet, bool) else None, model=result.model)


def rank_rationale(req: JudgeRequest, llm: LLMProvider) -> JudgeResponse:
    """D3a: the reason for every adjacent pair of the order. A pair the model skipped gets an explicit no-reason
    entry with no evidence (a weaker reason, not an absent one); refs outside the allowed ids are dropped."""
    order = [str(x) for x in req.payload.get("order") or []]
    user = json.dumps({"task": "For each adjacent pair of the order, say why the higher candidate is ranked above "
                               "the lower, citing only the evidence ids and knowledge ids given.",
                       "evidence_ids": req.evidence_ids, "knowledge_ids": req.payload.get("knowledge_ids") or [],
                       "order": order, **{k: _strip_bodies(v) for k, v in req.payload.items()
                                          if k not in ("order", "knowledge_ids")}}, indent=2, default=str)
    result = llm.complete_json(system=SYSTEM, user=user, schema=RATIONALE_SCHEMA, schema_name="rank_rationale_v1")
    try:
        raw = result.content["pairwise_reasons"]
        if not isinstance(raw, list):
            raise TypeError("pairwise_reasons")
    except (KeyError, TypeError) as exc:
        raise InvalidModelOutputError("rank rationale out of contract") from exc
    evidence = set(req.evidence_ids)
    knowledge = set(req.payload.get("knowledge_ids") or [])
    by_pair = {(p.get("ranked_higher_id"), p.get("ranked_lower_id")): p for p in raw if isinstance(p, dict)}
    pairs: list[dict[str, Any]] = []
    for hi, lo in zip(order, order[1:]):
        p = by_pair.get((hi, lo))
        if p is None or not str(p.get("reason") or "").strip():
            pairs.append({"ranked_higher_id": hi, "ranked_lower_id": lo, "evidence_refs": [], "knowledge_refs": [],
                          "reason": "no reason was given for this pair"})
            continue
        pairs.append({"ranked_higher_id": hi, "ranked_lower_id": lo, "reason": str(p["reason"])[:1000],
                      "evidence_refs": [r for r in dict.fromkeys(p.get("evidence_refs") or []) if r in evidence],
                      "knowledge_refs": [r for r in dict.fromkeys(p.get("knowledge_refs") or []) if r in knowledge]})
    return JudgeResponse(kind=req.kind, subject_id=req.subject_id, pairwise_reasons=pairs, model=result.model)

