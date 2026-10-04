"""WP32 (HAR-131) realism pass, Opus review HIGH 2: the validator must check MEANING, not just surface tokens.

Every probe below was ACCEPTED by the token-only validator (intent flips, a dropped negation, relative-date shifts,
role swaps, removed numbers and durations, a month change outside the slots) and must now be rejected. The real
templates are used, with slot values filled in.
"""
from __future__ import annotations

import sys
from pathlib import Path

import pytest

ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT / "bench"))

from synthetic.paraphrase_check import check  # noqa: E402
from synthetic.paraphrase_meaning import classify, meaning_reasons  # noqa: E402
from synthetic.render import TEMPLATES  # noqa: E402
from synthetic.render_all import MESSAGES  # noqa: E402

S = dict(seller_first="Dana", buyer_full="Priya Shah", company="Acme Corp", product="Smart Hub", date="March 14",
         other_team="another team", other_name="Marcus Lee", buyer_first="Priya", seller_full="Dana Kim")
SIGN = "\n\nBest regards,\n\nPriya Shah\nAcme Corp"
TIMING = TEMPLATES["timing_request"][0].format(**S)
OUTREACH = MESSAGES["seller_outreach"]["bodies"][0].format(**S)
REORG = MESSAGES["reorg_announced"]["bodies"][0].format(**S)
HANDOVER = MESSAGES["written_handover"]["bodies"][0].format(**S)
INTRO = MESSAGES["stakeholder_intro"]["bodies"][0].format(**S)

FAITHFUL_TIMING = (
    "Hi Dana,\n\nThanks for getting in touch again. We are in the middle of a busy period with several internal "
    "commitments, and I do not think we can give Smart Hub the attention it deserves until after March 14.\n\n"
    "Could we pick this up again after that date? I would rather revisit it when the right people have time to engage "
    "than rush a decision now. I will contact you once things settle down." + SIGN)


def _bad(text: str, template: str, needle: str) -> None:
    reasons = check(text, template, S)
    assert any(needle in r for r in reasons), reasons


def test_a_faithful_paraphrase_of_a_real_template_is_accepted() -> None:
    assert check(FAITHFUL_TIMING, TIMING, S) == []


def test_intent_flip_pause_until_a_date_becomes_go_ahead_now() -> None:
    flip = ("Hi Dana,\n\nThanks for reaching out. We are ready to go ahead with Smart Hub now, so there is no need to "
            "wait for March 14." + SIGN)
    assert classify(flip) != classify(TIMING)
    _bad(flip, TIMING, "intent changed")
    _bad(flip, TIMING, "intent cues added")


def test_negation_dropped_is_rejected() -> None:
    _bad(FAITHFUL_TIMING.replace("I do not think we can", "I think we can"), TIMING, "negation changed")


def test_negation_added_is_rejected() -> None:
    _bad(OUTREACH.replace("I would be glad", "I would not be glad"), OUTREACH, "negation changed")


@pytest.mark.parametrize("swap", ["next month", "this week", "tomorrow"])
def test_relative_date_shift_is_rejected(swap: str) -> None:
    assert "next week" in OUTREACH
    _bad(OUTREACH.replace("next week", swap), OUTREACH, "time words")


def test_handover_role_swap_is_rejected() -> None:
    swapped = REORG.replace("who your main contact will be", "who your executive sponsor will be")
    _bad(swapped, REORG, "role phrases changed")
    taken_back = HANDOVER.replace("will take the lead on", "will assist me on").replace("full support to make decisions",
                                                                                         "my review of every decision")
    _bad(taken_back, HANDOVER, "role phrases changed")


def test_handover_role_synonym_is_not_a_swap() -> None:
    assert not any("role phrases" in r for r in meaning_reasons(REORG.replace("main contact", "point of contact"), REORG, S))


def test_removing_three_and_thirty_days_is_rejected() -> None:
    template = "Hi Dana,\n\nThree stakeholders joined the review and we expect a decision within 30 days." + SIGN
    paraphrase = "Hi Dana,\n\nSeveral stakeholders joined the review and we expect a decision soon." + SIGN
    reasons = check(paraphrase, template, S)
    assert any("removed numbers or time words" in r and "num:30" in r and "w:three" in r for r in reasons), reasons
    assert check(template.replace("Hi Dana", "Hello Dana"), template, S) == []


@pytest.mark.parametrize("new_month", ["May", "June"])
def test_a_month_change_outside_the_slots_is_rejected(new_month: str) -> None:
    template = "Hi Dana,\n\nWe will revisit this in April, after the audit." + SIGN
    _bad(template.replace("April", new_month), template, "time words")


def test_a_slot_date_is_protected_but_a_changed_slot_is_still_a_drop() -> None:
    _bad(FAITHFUL_TIMING.replace("March 14", "March 15"), TIMING, "slot date")


def test_reclassification_matches_the_template_kind_for_every_reply_template() -> None:
    for kind, bodies in TEMPLATES.items():
        if kind == "description":
            continue
        for body in bodies:
            text = body.format(**S)
            assert classify(text) == classify(text.replace("Hi ", "Hello "))
            assert meaning_reasons(text.replace("Hi ", "Hello "), text, S) == []


def test_an_introduction_stays_an_introduction() -> None:
    flat = "Hi Dana,\n\nPlease send the latest pricing for Smart Hub." + SIGN
    assert classify(INTRO)[0] == "stakeholder_intro"
    _bad(flat, INTRO, "intent")
