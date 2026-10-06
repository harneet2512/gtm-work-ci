"""POST /v1/decision-judge (HAR-97 D1, D2, D3, D8) and the ranking rationale producer. One model call per
request, per-dimension verdicts, rule R1 enforced by the service. Providers are scripted (no live model)."""
from __future__ import annotations

import json
from typing import Any

from draft_doubles import ScriptedProvider
from fastapi.testclient import TestClient
from ghost_worker.app import create_app

A, B, C = ("0e7a1000-0000-4000-8000-00000000ca0%d" % i for i in (1, 2, 3))
EV1, EV2 = "0e7a1000-0000-4000-8000-00000000ac01", "state:relationship_state"


def cand(cid: str, stype: str) -> dict[str, Any]:
    return {"candidate_id": cid, "strategy_type": stype, "action_class": stype.upper(), "rationale": "r " + stype,
            "full_action_artifact": {"body": "SECRET BODY " + stype}, "subject": "SUBJ", "preview": "PREV"}


def call(skill: dict[str, Any], kind: str, **over: Any):
    from test_api import settings
    prov = ScriptedProvider(turns=[], skill=skill)
    client = TestClient(create_app(settings=settings(), provider=prov))
    body = {"kind": kind, "subject_id": "set-1", "payload": {"candidates": [cand(A, "act"), cand(B, "wait"), cand(C, "ask")]},
            "evidence_ids": [EV1, EV2]}
    body.update(over)
    return client.post("/v1/decision-judge", json=body), prov


def dims(*items: tuple[str, str, list[str]]) -> dict[str, Any]:
    return {"dimensions": [{"dimension": d, "verdict": v, "why": f"why {d}", "evidence_refs": e} for d, v, e in items],
            "summary": "ok", "right_reaction": None, "quiet_move_considered": None}


INTENT = ["intent_follows_state", "timing", "relationship_supports", "uncertainty_handled",
          "knowledge_used_correctly", "quiet_move_considered"]


def test_intent_fit_returns_per_dimension_verdicts_with_evidence() -> None:
    skill = dims(*[(d, "pass", [EV1]) for d in INTENT])
    skill["right_reaction"], skill["quiet_move_considered"] = "WAIT", True
    resp, prov = call(skill, "intent_fit")
    assert resp.status_code == 200, resp.text
    out = resp.json()
    assert [d["dimension"] for d in out["dimensions"]] == INTENT
    assert all(d["verdict"] == "pass" and d["evidence_refs"] == [EV1] for d in out["dimensions"])
    assert out["right_reaction"] == "WAIT" and out["quiet_move_considered"] is True
    assert out["prompt_version"] and out["model"]
    assert len(prov.json_calls) == 1


def test_pass_without_evidence_is_coerced_to_unknown() -> None:
    skill = dims(*[(d, "pass", []) for d in INTENT])
    out = call(skill, "intent_fit")[0].json()
    assert {d["verdict"] for d in out["dimensions"]} == {"unknown"}


def test_evidence_outside_the_allowed_ids_is_dropped_then_unknown() -> None:
    skill = dims(*[(d, "pass", ["made-up"]) for d in INTENT])
    out = call(skill, "intent_fit")[0].json()
    assert {d["verdict"] for d in out["dimensions"]} == {"unknown"}
    assert all(d["evidence_refs"] == [] for d in out["dimensions"])


def test_abstain_spelling_is_read_as_unknown() -> None:
    skill = dims(*[(d, "abstain", []) for d in INTENT])
    out = call(skill, "intent_fit")[0].json()
    assert {d["verdict"] for d in out["dimensions"]} == {"unknown"}


def test_missing_dimension_is_unknown_not_pass() -> None:
    skill = dims(("timing", "pass", [EV1]))
    out = call(skill, "intent_fit")[0].json()
    by = {d["dimension"]: d["verdict"] for d in out["dimensions"]}
    assert by["timing"] == "pass" and by["intent_follows_state"] == "unknown"


def test_decision_judges_never_see_the_email_bodies() -> None:
    skill = dims(*[(d, "pass", [EV1]) for d in INTENT])
    _, prov = call(skill, "intent_fit")
    seen = prov.json_calls[0]["user"]
    assert "SECRET BODY" not in seen and "SUBJ" not in seen


def test_candidate_quality_does_see_the_artifact_and_covers_all_dimensions() -> None:
    names = ["fit", "grounding", "intent_coherence", "recipients", "timing", "cta", "factual_integrity",
             "knowledge_use", "unsupported_assumptions", "risk"]
    skill = dims(*[(d, "warn" if d == "cta" else "pass", [EV1]) for d in names])
    resp, prov = call(skill, "candidate_quality", subject_id=A,
                      payload={"candidate": cand(A, "act")})
    out = resp.json()
    assert [d["dimension"] for d in out["dimensions"]] == names
    assert "SECRET BODY act" in prov.json_calls[0]["user"]
    assert len(prov.json_calls) == 1


def test_set_quality_order_is_shuffled_but_deterministic_per_trial() -> None:
    skill = dims(*[(d, "pass", [EV1]) for d in ("materially_different", "coverage", "no_dominated")])
    orders = []
    for trial in (0, 0, 1, 2, 3, 4):
        _, prov = call(skill, "set_quality", trial=trial)
        user = json.loads(prov.json_calls[0]["user"])
        orders.append(tuple(c["candidate_id"] for c in user["candidates"]))
    assert orders[0] == orders[1]
    assert len(set(orders)) > 1


def test_ranking_judge_dimensions() -> None:
    names = ["supported_by_evidence_state", "supported_by_knowledge", "uncertainty_reflected",
             "no_blocked_preferred", "rationale_matches_basis"]
    resp, _ = call(dims(*[(d, "pass", [EV1]) for d in names]), "ranking")
    assert [d["dimension"] for d in resp.json()["dimensions"]] == names


def test_final_artifact_dimensions() -> None:
    names = ["intent_preserved", "claims_grounded", "cta_timing_correct"]
    resp, _ = call(dims(*[(d, "fail" if d == "claims_grounded" else "pass", [EV2]) for d in names]),
                   "final_artifact", payload={"artifact": {"body": "x"}})
    by = {d["dimension"]: d["verdict"] for d in resp.json()["dimensions"]}
    assert by["claims_grounded"] == "fail"


def test_unknown_kind_is_422() -> None:
    resp, _ = call({}, "nonsense")
    assert resp.status_code == 422


def test_invalid_model_verdict_is_502() -> None:
    skill = dims(("timing", "excellent", [EV1]))
    resp, _ = call(skill, "intent_fit")
    assert resp.status_code == 502


def test_rank_rationale_produces_one_reason_per_adjacent_pair() -> None:
    skill = {"pairwise_reasons": [
        {"ranked_higher_id": A, "ranked_lower_id": B, "reason": "A fits the state", "evidence_refs": [EV1],
         "knowledge_refs": []},
        {"ranked_higher_id": B, "ranked_lower_id": C, "reason": "B waits", "evidence_refs": ["junk"],
         "knowledge_refs": []}]}
    resp, _ = call(skill, "rank_rationale", payload={"order": [A, B, C], "candidates": [cand(A, "act"), cand(B, "wait"), cand(C, "ask")]})
    assert resp.status_code == 200, resp.text
    pairs = resp.json()["pairwise_reasons"]
    assert [(p["ranked_higher_id"], p["ranked_lower_id"]) for p in pairs] == [(A, B), (B, C)]
    assert pairs[1]["evidence_refs"] == []  # unknown refs are dropped, the reason stays (a weaker reason, not absent)


def test_rank_rationale_missing_pair_is_filled_without_evidence() -> None:
    skill = {"pairwise_reasons": [{"ranked_higher_id": A, "ranked_lower_id": B, "reason": "x", "evidence_refs": [EV1],
                                   "knowledge_refs": []}]}
    resp, _ = call(skill, "rank_rationale", payload={"order": [A, B, C], "candidates": []})
    pairs = resp.json()["pairwise_reasons"]
    assert len(pairs) == 2 and pairs[1]["evidence_refs"] == [] and "no reason" in pairs[1]["reason"]
