"""POST /v1/judgment-inference (HAR-97 D5): the real JudgmentInference producer. The provider is scripted (no
live model); contract rules are enforced by the service, not trusted from the model."""
from __future__ import annotations

import copy
from typing import Any

from draft_doubles import ScriptedProvider
from fastapi.testclient import TestClient
from ghost_worker.app import create_app

EPISODE = "0e7a1000-0000-4000-8000-0000000000e1"
CAND_A = "0e7a1000-0000-4000-8000-00000000ca01"
CAND_B = "0e7a1000-0000-4000-8000-00000000ca02"
CAND_C = "0e7a1000-0000-4000-8000-00000000ca03"
ACT = "0e7a1000-0000-4000-8000-00000000ac01"
ACT2 = "0e7a1000-0000-4000-8000-00000000ac02"
TOKEN = "t" * 40


def candidate(cid: str, rank: int, stype: str, aclass: str, evidence: list[str]) -> dict[str, Any]:
    return {"candidate_id": cid, "strategy_type": stype, "title": stype, "description": stype, "ranking": rank,
            "preferred_by_agent": rank == 1, "rationale": f"because {stype}", "action_class": aclass,
            "action_type": "email", "evidence_refs": [{"activity_id": a} for a in evidence], "knowledge_refs": [],
            "to": [], "cc": []}


def bundle(cid: str, verdicts: dict[str, str]) -> dict[str, Any]:
    return {"id": cid, "strategy_candidate_id": cid,
            "items": [{"eval_type": t, "verdict": v, "relevance_reason": "r", "result": None}
                      for t, v in verdicts.items()]}


def body(chosen: str = CAND_B, edits: list[dict[str, Any]] | None = None, **over: Any) -> dict[str, Any]:
    out = {
        "decision_episode_id": EPISODE,
        "strategy_set": {"id": "s", "candidates": [
            candidate(CAND_A, 1, "expand_now", "EXPAND", [ACT]),
            candidate(CAND_B, 2, "loop_in_champion", "CHAMPION", [ACT, ACT2]),
            candidate(CAND_C, 3, "wait", "WAIT", [])]},
        "eval_bundles": [bundle(CAND_A, {"cta_calibration": "warn", "timing_cadence": "pass"}),
                         bundle(CAND_B, {"cta_calibration": "pass", "timing_cadence": "pass"}),
                         bundle(CAND_C, {"cta_calibration": "pass", "timing_cadence": "warn"})],
        "human_strategy_decision": {"id": "h", "selected_candidate_id": chosen, "original_agent_preference": CAND_A,
                                    "edits": edits or [], "send_decision": "send"},
        "run_token": TOKEN}
    out.update(over)
    return out


def answer(**over: Any) -> dict[str, Any]:
    out = {"statement": "The human kept the champion in the loop before expanding.",
           "semantic_labels": ["kept_champion_involved"], "edit_class": ["strategy"], "signal_strength": "moderate",
           "explicit_instructions": [], "unknown": False,
           "candidate_differences": ["chosen loops in the champion; preferred pushes expansion"],
           "evidence_activity_ids": [ACT], "knowledge_refs": [], "no_applicable_knowledge": True}
    out.update(over)
    return out


def call(skill: dict[str, Any], req: dict[str, Any] | None = None):
    from test_api import settings
    prov = ScriptedProvider(turns=[], skill=skill)
    client = TestClient(create_app(settings=settings(), provider=prov))
    return client.post("/v1/judgment-inference", json=req or body()), prov


def test_overrode_inference_is_real_and_in_contract() -> None:
    resp, prov = call(answer())
    assert resp.status_code == 200, resp.text
    out = resp.json()
    d = out["inferred_semantic_delta"]
    assert d["statement"].startswith("The human kept")
    assert d["semantic_labels"] == ["kept_champion_involved"]
    assert out["evidence"]["evidence_refs"] == [{"activity_id": ACT}]
    assert out["model"]
    # eval_differences are computed by the worker from the two bundles, never taken from the model.
    diffs = {e["eval_type"]: e for e in out["evidence"]["eval_differences"]}
    assert diffs["cta_calibration"]["agent_preference_verdict"] == "warn"
    assert diffs["cta_calibration"]["human_choice_verdict"] == "pass"
    assert "timing_cadence" not in diffs
    assert len(prov.json_calls) == 1


def test_click_alone_is_capped_at_weak_signal() -> None:
    resp, _ = call(answer(signal_strength="strong"))
    assert resp.json()["inferred_semantic_delta"]["signal_strength"] == "weak"


def test_edit_raises_signal_to_at_most_moderate() -> None:
    edits = [{"kind": "paragraph_edited", "before": "a", "after": "b"}]
    resp, _ = call(answer(signal_strength="strong"), body(edits=edits))
    assert resp.json()["inferred_semantic_delta"]["signal_strength"] == "moderate"


def test_recipient_edit_forces_recipient_class() -> None:
    edits = [{"kind": "recipient_removed", "before": "x", "after": None}]
    resp, _ = call(answer(edit_class=["style"]), body(edits=edits))
    classes = resp.json()["inferred_semantic_delta"]["edit_class"]
    assert "recipient" in classes or "stakeholder" in classes


def test_unknown_offers_no_labels_and_weak_signal() -> None:
    resp, _ = call(answer(unknown=True, semantic_labels=["smaller_ask"], signal_strength="moderate"))
    d = resp.json()["inferred_semantic_delta"]
    assert d["unknown"] is True and d["semantic_labels"] == [] and d["signal_strength"] == "weak"


def test_evidence_the_model_did_not_really_cite_makes_the_inference_unknown() -> None:
    resp, _ = call(answer(evidence_activity_ids=["0e7a1000-0000-4000-8000-00000000ffff"]))
    out = resp.json()
    refs = out["evidence"]["evidence_refs"]
    # the chosen candidate's evidence stays as uncited context (the contract needs a ref) but D5 must read unknown
    assert refs and all(r["activity_id"] in {ACT, ACT2} for r in refs)
    assert out["inferred_semantic_delta"]["unknown"] is True
    assert out["inferred_semantic_delta"]["semantic_labels"] == []


def test_new_classes_and_confidence_are_returned() -> None:
    resp, _ = call(answer(edit_class=["risk", "new_info", "wording", "tone"], confidence=0.8),
                   body(edits=[{"kind": "paragraph_edited", "before": "a", "after": "b"}]))
    d = resp.json()["inferred_semantic_delta"]
    assert d["edit_class"] == ["risk", "new_info", "wording", "tone"]
    assert d["confidence"] == 0.8


def test_confidence_is_clamped_and_low_when_unknown() -> None:
    resp, _ = call(answer(confidence=7.0))
    assert resp.json()["inferred_semantic_delta"]["confidence"] == 1.0
    resp, _ = call(answer(unknown=True, confidence=0.9))
    assert resp.json()["inferred_semantic_delta"]["confidence"] <= 0.3


def test_no_evidence_available_is_refused_not_invented() -> None:
    req = body(chosen=CAND_C)
    req["strategy_set"]["candidates"][0]["evidence_refs"] = []
    req["strategy_set"]["candidates"][1]["evidence_refs"] = []
    resp, _ = call(answer(evidence_activity_ids=[]), req)
    assert resp.status_code == 422


def test_agreement_choice_is_accepted() -> None:
    resp, _ = call(answer(), body(chosen=CAND_A))
    assert resp.status_code == 200


def test_invalid_label_from_model_is_502() -> None:
    resp, _ = call(answer(semantic_labels=["made_up"]))
    assert resp.status_code == 502


def test_chosen_candidate_must_be_in_set() -> None:
    resp, _ = call(answer(), body(chosen="0e7a1000-0000-4000-8000-00000000dead"))
    assert resp.status_code == 422


def test_explicit_note_instruction_with_quote_makes_strong() -> None:
    req = body(human_note="Always cc the champion before pricing.")
    resp, _ = call(answer(signal_strength="strong", explicit_instructions=[
        {"instruction": "cc the champion before pricing", "quote": "Always cc the champion before pricing."}]), req)
    d = resp.json()["inferred_semantic_delta"]
    assert d["signal_strength"] == "strong" and len(d["explicit_instructions"]) == 1


def test_invented_instruction_quote_is_dropped() -> None:
    req = body(human_note="ok")
    resp, _ = call(answer(explicit_instructions=[{"instruction": "never email Fridays",
                                                  "quote": "never email Fridays"}]), req)
    assert resp.json()["inferred_semantic_delta"]["explicit_instructions"] == []


def test_request_is_not_mutated() -> None:
    req = body()
    snap = copy.deepcopy(req)
    call(answer(), req)
    assert req == snap
