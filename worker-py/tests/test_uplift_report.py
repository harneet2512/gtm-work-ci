"""bench/uplift/report: the A/B/C report is built from per-situation arm records, validates against its contract and is
re-verifiable from its own situations. Inputs are the hand-built fixture of uplift_fixtures.py."""
from __future__ import annotations

import copy
import json
import sys
from pathlib import Path

import pytest

ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT))

import uplift_fixtures as F  # noqa: E402
from bench.uplift import report as RP  # noqa: E402

EXAMPLE = ROOT / "contracts" / "har129" / "abc_report.example.json"


def build_from(f: dict, seed: int = F.provenance()["seed"]) -> dict:
    return RP.build(arms=f["arms"], pack=f["pack"], learning=f["learning"], store=f["store"], guard=f["guard"],
                    provenance=f["provenance"], seed=seed)


def build() -> dict:
    return build_from(F.fresh())


def test_the_fixture_report_validates_and_verifies() -> None:
    assert RP.verify(build()) == []


def test_the_committed_example_is_exactly_what_the_builder_gives() -> None:
    assert json.loads(EXAMPLE.read_text(encoding="utf-8")) == build()


def test_paired_numbers_follow_from_the_situations() -> None:
    paired = build()["paired"]
    ba = paired["B_vs_A"]
    assert (ba["n"], ba["wins"], ba["losses"], ba["ties"]) == (3, 2, 0, 1)
    assert ba["mean_score_first"] == pytest.approx(0.0) and ba["mean_score_second"] == pytest.approx(-2 / 3)
    assert ba["mean_difference"] == pytest.approx(2 / 3)
    assert ba["sign_test_p"] == pytest.approx(0.5)
    ca = paired["C_vs_A"]  # C copies A on the two control situations
    assert (ca["n"], ca["wins"], ca["losses"], ca["mean_difference"], ca["share_same_decision"]) == (2, 0, 0, 0.0, 1.0)
    assert paired["B_vs_C"]["n"] == 0 and paired["B_vs_C"]["sign_test_p"] == 1.0  # no situation has both arms


def test_headline_states_a_negative_result_plainly() -> None:
    h = build()["headline"]
    assert h["b_better_than_a"] is False  # 2 wins of 3 is p = 0.5, not significant
    assert h["c_ignored"] is True and h["exception_respected"] is True
    assert "NOT shown better" in h["text"]


def test_a_significant_win_is_reported_as_one() -> None:
    f = F.fresh()
    for i in range(10):  # ten more situations where B holds the price and A re-prices
        sid = f"S-extra-{i}"
        f["arms"]["situations"].append({"id": sid, "arms": {
            "A": F.arm(sid, "A", F.REPRICE),
            "B": F.arm(sid, "B", F.HOLD, retrieved=[F.K_PRICE], applicable=[F.K_PRICE], cited=[F.K_PRICE])}})
        f["pack"]["situations"].append({"id": sid, "kind": "discriminating", "decision_point": "price_pushback", "deal_id": f"d{i}",
                                        "trigger": {"source_object_id": f"x{i}", "source_event_key": "received",
                                                    "occurred_at": "2023-12-01T10:00:00Z"}, "selection_reason": "extra"})
    r = build_from(f, seed=1)
    assert r["headline"]["b_better_than_a"] is True and r["paired"]["B_vs_A"]["sign_test_p"] < 0.05
    assert RP.verify(r) == []


def test_negative_transfer_counts_all_three_ways_the_knowledge_can_make_things_worse() -> None:
    f = F.fresh()
    for s in f["arms"]["situations"]:
        if s["id"] == "S-price-3":  # B re-prices where A held: worse
            s["arms"]["B"] = F.arm("S-price-3", "B", F.REPRICE, retrieved=[F.K_PRICE], applicable=[F.K_PRICE])
        if s["id"] == "S-price-x":  # B holds the price where there is nothing to hold: misapplied; A did not
            s["arms"]["B"] = F.arm("S-price-x", "B", F.HOLD, retrieved=[F.K_PRICE])
        if s["id"] == "S-quote-c2":  # irrelevant knowledge pushes the agent to a separate quote: C worse than A
            s["arms"]["C"] = F.arm("S-quote-c2", "C", F.SEPARATE, retrieved=[F.K_PRICE])
    r = build_from(f, seed=1)
    nt = r["negative_transfer"]
    assert (nt["discriminating"]["n_worse"], nt["exception"]["n_worse"], nt["control"]["n_worse"]) == (1, 1, 1)
    assert (nt["n_worse"], nt["n"]) == (3, 6) and nt["rate"] == pytest.approx(0.5)
    assert r["exceptions"]["misapplied_by_B"] == 1 and r["exceptions"]["misapplied_by_A"] == 0
    assert r["headline"]["exception_respected"] is False
    assert r["paired"]["C_vs_A"]["losses"] == 1
    assert RP.verify(r) == []


def test_an_exception_where_the_knowledge_is_not_retrieved_is_not_respected() -> None:
    f = F.fresh()
    for s in f["arms"]["situations"]:
        if s["id"] == "S-price-x":
            s["arms"]["B"]["knowledge"]["retrieved"] = []
    r = build_from(f, seed=1)
    assert r["exceptions"]["retrieved"] == 0 and r["headline"]["exception_respected"] is False


def test_irrelevant_knowledge_that_is_applied_is_not_ignored() -> None:
    f = F.fresh()
    for s in f["arms"]["situations"]:
        if s["id"] == "S-quote-c1":
            s["arms"]["C"]["knowledge"]["cited"] = [F.K_PRICE]
    r = build_from(f, seed=1)
    assert r["controls"]["applied"] == 1 and r["headline"]["c_ignored"] is False


def test_control_summary_counts_retrieval_applicability_and_sameness() -> None:
    c = build()["controls"]
    assert c == {"n": 2, "retrieved": 2, "marked_not_applicable": 2, "applied": 0, "c_equals_a": 2, "mean_score_c_minus_a": 0.0}


def test_corrections_are_counted_per_arm_from_the_deterministic_evals() -> None:
    c = build()["corrections"]
    assert (c["A"]["total"], c["B"]["total"], c["C"]["total"]) == (1, 0, 0)
    assert c["A"]["mean"] == pytest.approx(1 / 6, abs=1e-5) and c["B_minus_A"] == pytest.approx(-0.25)
    assert (c["B_vs_A"]["n"], c["B_vs_A"]["first_better"], c["B_vs_A"]["second_better"]) == (4, 1, 0)
    assert "proxy" in c["proxy"]


def test_the_lifecycle_note_says_what_happened_to_each_promoted_item() -> None:
    note = build()["learning"]["lifecycle_note"]
    assert "K202 price_pushback: provisional" in note
    assert "K204 second_quote: disputed (disputed: 3 negative reactions" in note


def test_every_report_carries_the_four_synthetic_caveats_and_the_har131_statement() -> None:
    from bench.synthetic.caveats import assert_carries_caveats

    caveats = build()["caveats"]
    assert_carries_caveats("\n".join(caveats))
    assert any("does not prove the rules hold in real selling" in c for c in caveats)


def test_verify_refuses_a_tampered_aggregate_and_a_schema_violation() -> None:
    r = build()
    r["paired"]["B_vs_A"]["wins"] = 5
    assert any("paired does not match" in p for p in RP.verify(r))
    broken = build()
    del broken["headline"]
    assert RP.verify(broken)
    wrong_count = build()
    wrong_count["experiment"]["n_exception"] = 9
    assert any("exception situations" in p for p in RP.verify(wrong_count))
    wrong_control = build()
    wrong_control["experiment"]["n_control"] = 0
    assert any("control situations" in p for p in RP.verify(wrong_control))


def test_the_draft_hash_changes_with_the_recipients_and_the_text() -> None:
    a = F.candidate("s", "A", F.HOLD)
    b = copy.deepcopy(a)
    assert RP.draft_sha256(a) == RP.draft_sha256(b)
    b["to"][0]["person_id"] = "someone-else"
    assert RP.draft_sha256(a) != RP.draft_sha256(b)
    c = copy.deepcopy(a)
    c["full_action_artifact"]["body"] += "!"
    assert RP.draft_sha256(a) != RP.draft_sha256(c)


def test_leakage_flag_follows_the_dates() -> None:
    assert build()["learning"]["no_leakage"]["passed"] is True
    f = F.fresh()
    f["store"][0]["created_at"] = "2024-01-01T00:00:00Z"
    assert build_from(f, seed=1)["learning"]["no_leakage"]["passed"] is False


def test_times_from_the_database_are_normalised_to_utc_before_they_are_compared() -> None:
    f = F.fresh()
    f["store"][0]["created_at"] = "2023-10-31T20:00:00-04:00"  # = 2023-11-01T00:00:00Z
    r = build_from(f, seed=1)
    assert r["learning"]["knowledge"][0]["created_at"] == "2023-11-01T00:00:00Z"
    assert r["learning"]["no_leakage"]["latest_knowledge_created_at"] == "2023-11-01T00:00:00Z"
    f["store"][0]["created_at"] = "2023-11-10T21:00:00-04:00"  # = 2023-11-11T01:00:00Z, after the first trigger
    assert build_from(f, seed=1)["learning"]["no_leakage"]["passed"] is False
