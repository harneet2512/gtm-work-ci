"""Skeleton + merged labels -> the committed case (HAR-114 gold v2, part B)."""
from __future__ import annotations

from typing import Any

from . import labels as L

TRIGGER_TAG = {"inbound_email": "inbound_email", "outbound_email": "outbound_email", "task_due": "task_trigger"}
ORDER = ("id", "origin", "case_type", "title", "should_pass", "difficulty", "slice_tags", "based_on", "source", "context",
         "candidate_action", "expected", "expected_best_action", "labelling", "evidence_gap", "notes")


def slice_tags(skeleton: dict[str, Any], spec: dict[str, Any], should_pass: bool) -> dict[str, Any]:
    fields = skeleton["context"]["state"]["fields"]
    extra = sorted({*spec.get("extra", []), TRIGGER_TAG[skeleton["based_on"]["trigger_kind"]]})
    return {"account": skeleton["based_on"]["account_id"], "origin": "crmarena", "motion": "unknown", "stage": "unknown",
            "champion_status": fields["champion_status"], "economic_buyer": "unknown", "risk": "unknown",
            "candidate": "good" if should_pass else "flawed", "expected_action": skeleton["expected_best_action"]["action"],
            "extra": extra}


def finalize(skeleton: dict[str, Any], spec: dict[str, Any], p1: dict, p2: dict, resolutions: dict) -> dict[str, Any]:
    case_id = skeleton["id"]
    expected, notes, agree = [], [], {"judgments": 0, "verdict_agree": 0, "fail_agree": 0}
    for eval_type in spec["evals"]:
        a, b = p1[case_id][eval_type], p2[case_id][eval_type]
        judgment, reason = L.final_label(case_id, eval_type, p1, p2, resolutions)
        problems = L.check_judgment(eval_type, judgment)
        if problems:
            raise ValueError(f"{case_id}: {problems}")
        extra = {}
        if reason:
            notes.append({"eval_type": eval_type, "pass_1": "/".join(str(x) for x in a[:3]),
                          "pass_2": "/".join(str(x) for x in b[:3]), "resolution": reason})
            extra = {"debatable": True, "debate_note": reason}
        expected.append(L.expected_item(eval_type, judgment, extra))
        agree["judgments"] += 1
        agree["verdict_agree"] += a[0] == b[0]
        agree["fail_agree"] += (a[0] == "fail") == (b[0] == "fail")
    should_pass = not any(e["verdict"] == "fail" for e in expected)
    case = {**skeleton, "should_pass": should_pass, "expected": expected,
            "slice_tags": slice_tags(skeleton, spec, should_pass),
            "labelling": {"label_kind": "model labels, single author, not human labels", "passes": ["pass_1", "pass_2"],
                          "agreement": agree, "disagreements": notes}}
    if not case.get("evidence_gap"):
        case.pop("evidence_gap", None)
    return {k: case[k] for k in ORDER if k in case}
