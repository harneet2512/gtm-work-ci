"""HAR-114 gold v2: review-round checks on the CRMArena gold's labels, blocking, inferred roles and agreement reporting."""
from __future__ import annotations

import json

from crmarena_gold_lib import CR_CASES, LABELS, ROOT
from eval_cases_lib import EVAL_TYPES


def gold(cid: str, eval_type: str) -> dict:
    return next(e for e in CR_CASES[cid]["expected"] if e["eval_type"] == eval_type)


def test_inferred_roles_are_marked_in_the_context_judges_receive() -> None:
    """The schema has no role-confidence field and the judge code is unchanged, so the marker rides in the member title."""
    for cid, case in CR_CASES.items():
        for m in case["context"]["state"]["buying_group"]:
            if "champion" in m["roles"]:
                assert "champion role inferred, not recorded" in m["title"], cid
            elif m["roles"] != ["unknown"]:
                assert "role inferred from department" in m["title"], cid


def test_inferred_role_judgments_are_listed_as_debatable_for_humans() -> None:
    needed = json.loads((LABELS / "labels_needed.json").read_text(encoding="utf-8"))
    listed = {(j["case"], j["eval_type"]): j for j in needed["judgments"]}
    for cid, case in CR_CASES.items():
        inferred = any("inferred" in m["title"] for m in case["context"]["state"]["buying_group"])
        for e in case["expected"]:
            if inferred and e["eval_type"] in ("champion_continuity", "stakeholder_selection", "stakeholder_coverage"):
                assert any("depends on a role" in w for w in listed[(cid, e["eval_type"])]["why"]), (cid, e["eval_type"])
    assert needed["legacy_contested"][0]["case"] == "beta_cp4_nonmaterial_fyi_no_action"


def test_blocking_follows_the_catalog_rule_text_review() -> None:
    review = json.loads((LABELS / "blocking_rule_review.json").read_text(encoding="utf-8"))
    assert len(review["decisions"]) == 8
    for item in review["decisions"]:
        assert gold(item["case"], item["eval_type"])["blocking"] is item["blocking"] and item["derivation"]
    for item in review["not_blocking_by_rule_text"]:
        assert gold(item["case"], item["eval_type"])["blocking"] is False
    catalog_rules = {e["blocking_rule"] for e in EVAL_TYPES.values() if "blocking_rule" in e}
    assert set(review["rule_text"].values()) <= catalog_rules, "the quoted rule text is the catalog's"


def test_exception_awareness_blocks_only_on_an_explicit_written_instruction() -> None:
    judgment = gold("cr_d9_k104_check_in_sent_despite_the_buyers_date", "exception_awareness")
    assert judgment["verdict"] == "fail" and judgment["blocking"] is False


def test_pass_2_was_written_from_neutral_shuffled_sheets() -> None:
    plan = json.loads((LABELS / "pass_2_map.json").read_text(encoding="utf-8"))["sheets"]
    assert len(plan) == len(CR_CASES) and {e["case"] for e in plan} == set(CR_CASES)
    assert all(e["sheet"].startswith("S") and e["sheet"][1:] not in e["case"] for e in plan)
    assert [e["case"] for e in plan] != sorted(CR_CASES), "case order is shuffled"
    assert any(e["evals"] != [x["eval_type"] for x in CR_CASES[e["case"]]["expected"]] for e in plan), "eval order is shuffled"


def test_agreement_is_reported_per_deal_cluster_and_not_as_blind_independent() -> None:
    report = json.loads((ROOT / "bench" / "reports" / "crmarena-gold-agreement-2026-10-02.json").read_text(encoding="utf-8"))
    assert report["deal_clusters"] == len({c["based_on"]["deal_id"] for c in CR_CASES.values()})
    assert sum(r["judgments"] for r in report["per_deal"].values()) == report["judgments"]
    low, high = report["cluster_bootstrap_95ci"]["exact_agree"]
    assert 0 <= low <= report["exact_agree"] / report["judgments"] <= high <= 1
    assert "NOT blind independent" in report["passes"]["pass_2"] and "not independent" in report["caveat"]


def test_kappa_caveat_says_pass_1_was_revised_before_pass_2() -> None:
    report = json.loads((ROOT / "bench" / "reports" / "crmarena-gold-agreement-2026-10-02.json").read_text(encoding="utf-8"))
    assert "revised in review before pass 2" in report["kappa_caveat"] and "says little" in report["kappa_caveat"]
