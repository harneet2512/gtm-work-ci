"""WP32 (HAR-131) v1.1: the forward-in-time kernel. No record dated before a deal's close may depend
on its outcome; real customer emails are base data that synthetic replies never duplicate; rendered
events carry the synthetic marker only on the envelope."""
from __future__ import annotations

import json
import random
import sys
from pathlib import Path

import pytest

ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT / "bench"))

from synthetic import forward, leakcheck, render, roles, rules, seeding  # noqa: E402
from synthetic.plan import Contact, DealInput, Inbound, Outbound  # noqa: E402
from synthetic.world import build_world  # noqa: E402

RS, PROC = rules.load_rules(), dict(rules.load_doc()["process"])
SEED = RS.generation_seed
WORLD = build_world(SEED, PROC, 0.5, n_accounts=12)


def results(override=None):
    return [forward.simulate(w.deal, RS, PROC, SEED, outcome_override=override) for w in WORLD]


def test_simulation_is_deterministic() -> None:
    assert results() == results()


def test_shuffling_outcomes_across_deals_changes_no_record_before_any_close() -> None:
    base = results()
    shuffled = [o for o in (r.won for r in base)]
    random.Random(7).shuffle(shuffled)
    for w, r, won in zip(WORLD, base, shuffled):
        other = forward.simulate(w.deal, RS, PROC, SEED, outcome_override=won)
        assert other.pre_close() == r.pre_close() and other.close_t == r.close_t
        assert other.features == r.features


def test_both_outcomes_share_every_pre_close_record() -> None:
    for w in WORLD[:40]:
        won = forward.simulate(w.deal, RS, PROC, SEED, outcome_override=True)
        lost = forward.simulate(w.deal, RS, PROC, SEED, outcome_override=False)
        assert won.pre_close() == lost.pre_close()


def _deal(inbound: tuple[Inbound, ...]) -> DealInput:
    contacts = (Contact("c-champ", "champion", "operations"), Contact("c-eb", "economic_buyer", "finance"))
    outbound = tuple(Outbound(float(t), ("c-champ",)) for t in (5, 20, 35, 50, 65))
    return DealInput("d-1", "a-1", 90, outbound, inbound, contacts, (), False, 0.0)


def test_real_customer_email_suppresses_synthetic_replies_in_its_period() -> None:
    proc = {**PROC, "reply_base_prob": 0.99, "loop_in_prob": 0.0, "eb_loop_in_when_seeking": 0.0}
    real = tuple(Inbound(t, "c-champ") for t in (7.0, 22.0, 37.0, 52.0, 67.0))
    with_real = forward.simulate(_deal(real), RS, proc, SEED)
    without = forward.simulate(_deal(()), RS, proc, SEED)
    def gap_fill(res):  # the price pushback at Quote (seller.py) has its own real-email guard
        return [r for r in res.records if r.kind == "reply" and dict(r.data)["reply_kind"] != "price_objection"]
    assert not gap_fill(with_real) and gap_fill(without)


def test_real_customer_email_still_shapes_the_planted_features() -> None:
    real = (Inbound(0.3, "c-eb"),)  # the economic buyer writes: engaged before any Quote
    res = forward.simulate(_deal(real), RS, {**PROC, "reply_base_prob": 0.01, "stage_dwell_fraction": 0.01}, SEED)
    assert forward.G_QBEB not in dict(res.features)


def test_no_kernel_record_carries_the_marker() -> None:
    """Kernel records feed rendering; rendered events are checked in test_synthetic_render.py."""
    recs = [r for res in results() for r in res.records]
    assert recs and not [r for r in recs if leakcheck.MARKER_TEXT.search(json.dumps(r.data))]


def test_seller_mapping_matches_wp31_tenant_address() -> None:
    assert render.tenant_address("Kim@TechDomain.com") == "kim@ghostvendor.com"
    with pytest.raises(ValueError):
        render.tenant_address("@acme.com")


@pytest.mark.parametrize(("title", "want"), [
    ("Chief Financial Officer", (5, "finance")), ("VP of Engineering", (4, "technical")),
    ("Director of Operations", (3, "operations")), ("Marketing Manager", (2, "business")), (None, (1, "business")),
])
def test_titles_parse_to_seniority_and_function(title: str | None, want: tuple[int, str]) -> None:
    assert roles.parse_title(title) == want


def test_account_roles_pick_finance_as_economic_buyer_and_keep_champion_separate() -> None:
    people = [roles.Person("cfo", 5, "finance"), roles.Person("vp", 4, "technical"), roles.Person("eng", 2, "technical"),
              roles.Person("mgr", 2, "business")]
    acct = roles.account_roles(people, technical_line=True, stream=seeding.stream(1, "roles"))
    assert acct["cfo"] == "economic_buyer" and acct["vp"] == "decision_maker" and acct["eng"] == "technical_evaluator"
    assert roles.deal_champion(people, acct, "cfo", {"mgr": 3}) == "mgr"
