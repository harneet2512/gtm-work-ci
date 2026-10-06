"""Bucket 1 model judgments (HAR-97 B1, B3, B5, B8) go through POST /v1/decision-judge, one call per gate with
every model assertion of the gate as a dimension, so the worker's cache or record mode captures them. Rule R1
holds: a verdict that cites no allowed evidence id is unknown."""
from __future__ import annotations

from test_decision_judge import call, dims

KINDS = {
    "b1_inference_boundary": "inference_boundary",
    "b3_prior_context": "supporting_and_conflicting_links",
    "b5_precedent_relevance": "relevance_and_misses",
    "b8_synthesis": "confidence_and_omissions",
}
EV = "0e7a1000-0000-4000-8000-00000000ac01"
PAYLOAD = {"activities": [{"id": EV, "body": "the DPA is signed"}]}


def test_each_bucket1_kind_judges_its_assertion_with_one_call() -> None:
    for kind, dim in KINDS.items():
        resp, prov = call(dims((dim, "pass", [EV])), kind, payload=PAYLOAD)
        assert resp.status_code == 200, resp.text
        out = resp.json()
        assert [d["dimension"] for d in out["dimensions"]] == [dim]
        assert out["dimensions"][0]["verdict"] == "pass" and len(prov.json_calls) == 1


def test_a_bucket1_pass_without_allowed_evidence_is_unknown() -> None:
    for refs in ([], ["made-up"]):
        out = call(dims(("inference_boundary", "pass", refs)), "b1_inference_boundary", payload=PAYLOAD)[0].json()
        assert out["dimensions"][0]["verdict"] == "unknown"


def test_a_missing_bucket1_dimension_is_unknown_not_pass() -> None:
    out = call(dims(), "b8_synthesis", payload=PAYLOAD)[0].json()
    assert out["dimensions"][0]["dimension"] == "confidence_and_omissions" and out["dimensions"][0]["verdict"] == "unknown"


def test_bucket1_judges_keep_the_source_text_that_decision_judges_strip() -> None:
    from ghost_worker.bucket2.decision_judge import BUCKET1_KINDS, JudgeRequest, _prepare

    kept = _prepare(JudgeRequest(kind="b1_inference_boundary", subject_id="e", payload=PAYLOAD, evidence_ids=[EV]))
    assert kept["activities"][0]["body"] == "the DPA is signed"
    stripped = _prepare(JudgeRequest(kind="intent_fit", subject_id="e", payload=PAYLOAD, evidence_ids=[EV]))
    assert "body" not in stripped["activities"][0]
    assert set(BUCKET1_KINDS) == set(KINDS)
