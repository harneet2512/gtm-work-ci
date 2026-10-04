"""bench/uplift/blind: unlabelled randomised pairs, with the arm key kept apart."""
from __future__ import annotations

import json
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT))

import uplift_fixtures as F  # noqa: E402
from bench.uplift import blind as B  # noqa: E402

CONTEXT = {"state_header": "Acme (motion new_business, stage Quote)", "trigger_summary": "Inbound email: pricing concern",
           "people": [{"person_id": "p-buyer", "name": "Dana Buyer", "title": "Finance Manager", "side": "buyer", "email": "dana@orbit.example"},
               {"person_id": "p-rep", "name": "Sam Rep", "title": "", "side": "seller", "email": "sam@ghostvendor.com"}]}


def arms_with_context() -> dict:
    doc = F.arms_doc()
    for s in doc["situations"]:
        s["context"] = dict(CONTEXT)
    return doc


def pairs() -> tuple[list, dict]:
    return B.build_pairs(arms_with_context(), F.pack(), seed=7)


def test_pairs_cover_b_vs_a_on_discriminating_and_c_vs_a_on_control_situations() -> None:
    ps, key = pairs()
    comparisons = [k["comparison"] for k in key["pairs"]]
    assert comparisons.count("B_vs_A") == 3 and comparisons.count("C_vs_A") == 2 and "B_vs_C" not in comparisons
    assert [p["pair_id"] for p in ps] == [k["pair_id"] for k in key["pairs"]]
    assert B.validate(ps, key) == []


def test_exception_situations_have_no_pairs() -> None:
    _, key = pairs()
    assert not [k for k in key["pairs"] if k["situation_id"].endswith("-x")]


def test_the_blind_file_carries_no_arm_label_knowledge_model_or_score() -> None:
    ps, key = pairs()
    text = json.dumps(ps).lower()
    for forbidden in ("knowledge", "arm", "decision_score", "qwen", "counterfactual", "b_vs", F.K_PRICE, "realized", "regret"):
        assert forbidden not in text, forbidden
    assert set(ps[0]) == {"pair_id", "situation", "left", "right"}
    assert set(key["pairs"][0]) == {"pair_id", "situation_id", "comparison", "left_arm", "right_arm"}


def test_the_reviewer_sees_names_and_titles_but_never_an_address() -> None:
    ps, _ = pairs()
    assert "@" not in json.dumps(ps)
    people = ps[0]["situation"]["people"]
    assert people[1] == {"person_id": "p-rep", "name": "Sam Rep", "title": None, "side": "seller"}


def test_the_drafts_are_full_and_use_display_names() -> None:
    ps, _ = pairs()
    left = ps[0]["left"]
    assert left["to"] == ["Dana Buyer"] and left["cc"] == [] and left["body"].startswith("Hi,")
    assert left["subject"].startswith("Re: ")


def test_sides_are_randomised_deterministically_and_the_key_matches_the_drafts() -> None:
    ps, key = pairs()
    sides = [k["left_arm"] for k in key["pairs"]]
    assert "A" in sides and ("B" in sides or "C" in sides)  # both orientations occur
    assert (ps, key) == pairs()
    other_ps, other_key = B.build_pairs(arms_with_context(), F.pack(), seed=8)
    assert [k["left_arm"] for k in other_key["pairs"]] != sides
    arms = arms_with_context()["situations"]
    first = key["pairs"][0]
    cand = next(s for s in arms if s["id"] == first["situation_id"])["arms"][first["left_arm"]]["candidate"]
    assert ps[0]["left"]["strategy_type"] == cand["strategy_type"]


def test_write_produces_jsonl_and_a_separate_key_file(tmp_path: Path) -> None:
    ps, key = pairs()
    B.write(tmp_path, ps, key)
    lines = (tmp_path / "pairs_blind.jsonl").read_text(encoding="utf-8").splitlines()
    assert [json.loads(line)["pair_id"] for line in lines] == [p["pair_id"] for p in ps]
    assert json.loads((tmp_path / "pairs_key.json").read_text(encoding="utf-8"))["version"] == "abc_pairs_key.v1"
    assert "left_arm" not in "\n".join(lines)


def test_validate_catches_a_mismatched_key() -> None:
    ps, key = pairs()
    key["pairs"] = key["pairs"][:-1]
    assert any("exactly the blind pairs" in p for p in B.validate(ps, key))
