"""bench/uplift/scoring: the lexicon classifier of a chosen action and the hidden-only decision score.

The corpus below was written before any model output was seen; it is the classifier's specification."""
from __future__ import annotations

import sys
from pathlib import Path

import pytest

ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT))

from bench.uplift import scoring as S  # noqa: E402


def cand(strategy_type: str, title: str, description: str) -> dict:
    return {"strategy_type": strategy_type, "title": title, "description": description}


REPRICE_CASES = [
    cand("offer_price_concession", "Offer a price concession", "Reduce the quoted amount by 10% to keep the deal moving"),
    cand("revise_pricing", "Revise the pricing", "Propose a phased rollout with a smaller initial scope"),
    cand("discount_offer", "Discount for annual commitment", "Offer a discount if they sign this quarter"),
    cand("renegotiate_price", "Renegotiate the price", "Lower the price to meet their budget"),
]
HOLD_CASES = [
    cand("reinforce_value_case", "Reinforce the value case", "Hold the quoted price and walk through the return on investment"),
    cand("roi_justification", "Share ROI evidence", "Provide customer results that justify the investment"),
    cand("defend_pricing", "Defend the pricing", "Keep the quote as is and explain what is included"),
]
NEITHER_CASES = [
    cand("schedule_call", "Schedule a call with the economic buyer", "Align on budget and timing with the buyer"),
    cand("ask_internal_owner", "Ask the account owner", "Check what the buyer's budget cycle looks like"),
]
SEPARATE_CASES = [
    cand("send_separate_quote", "Send a separate quote", "Issue a new quote for the additional scope"),
    cand("second_quote_proposal", "Second quote for the new team", "Prepare a second quote alongside the first"),
    cand("standalone_quote", "Standalone quote", "Send a standalone quote to the new business unit"),
]
FOLD_CASES = [
    cand("fold_into_open_quote", "Consolidate into the open quote", "Update the existing quote rather than sending a new one"),
    cand("amend_quote", "Amend the earlier quote", "Merge the new line items into the quote that is still outstanding"),
    cand("single_quote", "One combined quote", "Combine both requests into a single quote"),
]


@pytest.mark.parametrize("c", REPRICE_CASES)
def test_a_price_change_is_the_option_and_not_the_recommendation(c: dict) -> None:
    got = S.classify("price_pushback", c)
    assert got["option"] and not got["recommendation"], got


@pytest.mark.parametrize("c", HOLD_CASES)
def test_holding_the_price_is_the_recommendation_and_not_the_option(c: dict) -> None:
    got = S.classify("price_pushback", c)
    assert got["recommendation"] and not got["option"], got


@pytest.mark.parametrize("c", NEITHER_CASES)
def test_an_unrelated_move_is_neither(c: dict) -> None:
    for point in ("price_pushback", "second_quote"):
        got = S.classify(point, c)
        assert not got["option"] and not got["recommendation"], got


@pytest.mark.parametrize("c", SEPARATE_CASES)
def test_a_separate_quote_is_the_option(c: dict) -> None:
    got = S.classify("second_quote", c)
    assert got["option"] and not got["recommendation"], got


@pytest.mark.parametrize("c", FOLD_CASES)
def test_folding_into_the_open_quote_is_the_recommendation(c: dict) -> None:
    got = S.classify("second_quote", c)
    assert got["recommendation"] and not got["option"], got


def test_a_candidate_that_mixes_both_courses_is_not_classified() -> None:
    mixed = cand("hybrid", "Hold the price, offer a phased scope", "Keep the quoted price but propose a phased rollout")
    got = S.classify("price_pushback", mixed)
    assert not got["option"] and not got["recommendation"]
    assert any(e.startswith("option:") for e in got["evidence"]) and any(e.startswith("opposite:") for e in got["evidence"])


def test_the_body_is_never_read() -> None:
    c = {**cand("schedule_call", "Schedule a call", "Align on timing"), "full_action_artifact": {"body": "We can discount the price"}}
    assert not S.classify("price_pushback", c)["option"]


def test_hidden_rule_values_are_the_frozen_hidden_only_log_odds() -> None:
    assert S.rule_value("price_pushback") == -1.0
    assert S.rule_value("second_quote") == -1.1
    assert S.rule_value("ops_outreach") == 0.9 and S.rule_value("technical_reply") == 0.9


def test_decision_score_and_regret_for_a_harmful_option() -> None:
    assert S.decision_score("price_pushback", True) == (-1.0, 1.0)
    assert S.decision_score("price_pushback", False) == (0.0, 0.0)
    assert S.decision_score("second_quote", True) == (-1.1, 1.1)


def test_decision_score_and_regret_for_a_helpful_option() -> None:
    assert S.decision_score("ops_outreach", True) == (0.9, 0.0)
    assert S.decision_score("ops_outreach", False) == (0.0, 0.9)


def test_an_eval_known_rule_is_never_a_decision_score() -> None:
    from bench.synthetic import rules as R

    eval_known = {r.key for r in R.load_rules().rules if r.group != "hidden_company_specific"}
    assert not eval_known & set(S.RULE_OF.values())
