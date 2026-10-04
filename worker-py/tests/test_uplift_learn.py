"""bench/uplift observe + learn: decision-point observations, the promotion rule and the evidence ledger."""
from __future__ import annotations

import random
import sys
from datetime import datetime, timedelta, timezone
from pathlib import Path

import pytest

ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT))

from bench.uplift import catalog  # noqa: E402
from bench.uplift import learn as L  # noqa: E402
from bench.uplift.observe import DealView, Rec, observe  # noqa: E402

START = datetime(2022, 1, 3, tzinfo=timezone.utc)  # a Monday
CUTOFF = datetime(2023, 11, 1, tzinfo=timezone.utc)


def view(deal: str = "D1", *, contacts=None, base_out=(), records=(), close_t: float = 100.0, won: bool = False,
         start: datetime = START) -> DealView:
    return DealView(deal, "A1", start, contacts or {}, tuple(base_out), tuple(Rec(t, k, d) for t, k, d in records), close_t, won)


def by_point(v: DealView) -> dict:
    return {o.point: o for o in observe(v)}


# ---- observe -------------------------------------------------------------------------------------------------------


def test_no_operations_contact_means_no_ops_observation() -> None:
    assert "ops_outreach" not in by_point(view(contacts={"c1": "technical"}))


def test_a_real_email_to_the_operations_contact_before_the_quote_counts_and_one_after_does_not() -> None:
    contacts = {"c1": "operations"}
    stages = [(30.0, "stage", {"stage": "Quote"})]
    before = by_point(view(contacts=contacts, base_out=[(10.0, ("c1",))], records=stages))["ops_outreach"]
    after = by_point(view(contacts=contacts, base_out=[(40.0, ("c1",))], records=stages))["ops_outreach"]
    assert (before.took, before.t) == (True, 10.0)
    assert (after.took, after.t) == (False, 30.0)  # the chance ended when the quote went out


def test_a_synthetic_note_to_the_operations_contact_counts() -> None:
    v = view(contacts={"c1": "operations"}, records=[(5.0, "seller_email", {"to": "c1", "purpose": "ops_note"})])
    assert by_point(v)["ops_outreach"].took is True


def test_price_pushback_is_taken_when_the_amount_is_edited_within_three_days() -> None:
    push = (50.0, "reply", {"contact": "c", "reply_kind": "price_objection"})
    edited = by_point(view(records=[push, (51.0, "amount_edited", {"reason": "pushback"})]))["price_pushback"]
    held = by_point(view(records=[push, (51.0, "seller_email", {"to": "c", "purpose": "value_case"})]))["price_pushback"]
    late = by_point(view(records=[push, (60.0, "amount_edited", {})]))["price_pushback"]
    assert (edited.took, held.took, late.took) == (True, False, False)


def test_technical_reply_and_second_quote_read_the_seller_choice() -> None:
    v = view(records=[(3.0, "seller_email", {"to": "c", "purpose": "technical_reply", "cc_colleague": True}),
                      (9.0, "quote_sent", {"separate": False})])
    got = by_point(v)
    assert got["technical_reply"].took is True and got["second_quote"].took is False


def test_reaction_is_the_next_buyer_reply_inside_the_window() -> None:
    v = view(records=[(3.0, "seller_email", {"to": "c", "purpose": "technical_reply", "cc_colleague": False}),
                      (5.0, "reply", {"contact": "c", "reply_kind": "positive_need"}),
                      (6.0, "reply", {"contact": "c", "reply_kind": "objection"})])
    o = by_point(v)["technical_reply"]
    assert (o.reaction, o.reaction_t) == ("positive", 5.0)
    far = view(records=[(3.0, "seller_email", {"to": "c", "purpose": "technical_reply", "cc_colleague": False}),
                        (40.0, "reply", {"contact": "c", "reply_kind": "objection"})])
    assert by_point(far)["technical_reply"].reaction is None


def test_controls_read_weekday_volume_and_cc() -> None:
    v = view(base_out=[(0.0, ("c1",)), (2.0, ("c1",))], records=[(4.0, "seller_outreach", {"to": "c2", "cc_colleague": True})])
    got = by_point(v)
    assert got["first_send_early_week"].took is True  # day 0 is a Monday
    assert got["high_outbound_volume"].value == 3.0 and got["high_outbound_volume"].took is None
    assert got["any_colleague_cc"].took is True
    assert by_point(view(base_out=[(2.0, ("c1",))]))["first_send_early_week"].took is False  # Wednesday


def test_a_deal_with_no_seller_activity_exercises_nothing() -> None:
    assert observe(view()) == []


# ---- statistics ----------------------------------------------------------------------------------------------------


def test_fisher_exact_matches_the_textbook_table() -> None:
    assert L.fisher_exact_p(3, 1, 1, 3) == pytest.approx(0.485714, abs=1e-5)
    assert L.fisher_exact_p(10, 0, 0, 10) == pytest.approx(2 / 184756)
    assert L.fisher_exact_p(5, 5, 5, 5) == 1.0
    assert L.fisher_exact_p(0, 0, 0, 0) == 1.0


def test_benjamini_hochberg_keeps_the_stepped_up_prefix_and_skips_untestable() -> None:
    p = [0.001, 0.2, None, 0.012, 0.9]
    assert L.benjamini_hochberg(p, 0.10) == [True, False, False, True, False]
    assert L.benjamini_hochberg([None, None], 0.10) == [False, False]


# ---- learn ---------------------------------------------------------------------------------------------------------


def synthetic_previous(n: int = 160, seed: int = 11) -> list[DealView]:
    """Deals where writing to the operations contact wins 70% vs 30%, and a control (cc) with no effect."""
    rng = random.Random(seed)
    out = []
    for i in range(n):
        took = i % 2 == 0
        won = rng.random() < (0.7 if took else 0.3)
        recs = [(30.0, "stage", {"stage": "Quote"}), (31.0, "reply", {"contact": "c", "reply_kind": "positive" if won else "objection"})]
        base_out = [(10.0, ("c1",))] if took else [(10.0, ("c9",))]
        if i % 3 == 0:
            recs.append((12.0, "seller_email", {"to": "c9", "purpose": "technical_reply", "cc_colleague": rng.random() < 0.5}))
        out.append(view(f"D{i:03d}", contacts={"c1": "operations", "c9": "business"}, base_out=base_out, records=recs,
                        close_t=200.0, won=won, start=START + timedelta(days=i)))
    return out


def test_learning_promotes_the_real_effect_and_not_the_control() -> None:
    result = L.learn(synthetic_previous(), CUTOFF)
    promoted = {h["decision_point"] for h in result["hypotheses"] if h["promoted"]}
    assert "ops_outreach" in promoted
    assert "any_colleague_cc" not in promoted
    item = next(k for k in result["knowledge"] if k["decision_point"] == "ops_outreach")
    assert item["direction"] == "better" and item["recommended_option_taken"] is True
    assert item["key"] == "K201" and item["created_at"] == "2023-11-01T00:00:00Z"
    assert "Fisher exact p" in item["guidance"]["summary"]


def test_a_harmful_option_is_recommended_in_reverse() -> None:
    views = []
    rng = random.Random(5)
    for i in range(160):
        took = i % 2 == 0
        won = rng.random() < (0.2 if took else 0.6)
        recs = [(50.0, "reply", {"contact": "c", "reply_kind": "price_objection"})]
        if took:
            recs.append((51.0, "amount_edited", {}))
        views.append(view(f"P{i:03d}", records=recs, close_t=120.0, won=won, start=START + timedelta(days=i)))
    item = next(k for k in L.learn(views, CUTOFF)["knowledge"] if k["decision_point"] == "price_pushback")
    assert item["direction"] == "worse" and item["recommended_option_taken"] is False
    assert "Keep the quoted amount" in item["guidance"]["do"][0]


def test_evidence_covers_every_episode_that_followed_the_recommendation_whatever_the_outcome() -> None:
    result = L.learn(synthetic_previous(), CUTOFF)
    item = next(k for k in result["knowledge"] if k["decision_point"] == "ops_outreach")
    followed = [e for e in item["evidence"] if e["kind"] == "decision_episode"]
    outcomes = [e for e in item["evidence"] if e["kind"] == "business_outcome"]
    assert len(followed) == len(outcomes) == len(item["supporting_episode_ids"]) == 80
    assert {e["outcome_type"] for e in outcomes} == {"closed_won", "closed_lost"}  # losses are on the ledger too
    assert not [e for e in item["evidence"] if e["kind"] == "counterexample"]
    assert [e["at"] for e in item["evidence"]] == sorted(e["at"] for e in item["evidence"])


def test_nothing_dated_on_or_after_the_cutoff_is_used() -> None:
    views = synthetic_previous()
    late = view("LATE", contacts={"c1": "operations"}, base_out=[(10.0, ("c1",))], records=[(30.0, "stage", {"stage": "Quote"})],
                close_t=(CUTOFF - START).days + 5.0, won=True)
    result = L.learn(views + [late], CUTOFF)
    assert result["excluded_closing_after_cutoff"] == 1
    assert result["previous_deals"] == len(views)
    ids = {e["ref_id"] for k in result["knowledge"] for e in k["evidence"]}
    assert L.det_uuid("episode", "LATE", "ops_outreach") not in ids
    assert all(e["at"] < "2023-11-01T00:00:00Z" for k in result["knowledge"] for e in k["evidence"])


def test_a_reaction_after_the_cutoff_is_not_recorded() -> None:
    near_end = view("NEAR", contacts={"c1": "operations"}, base_out=[(10.0, ("c1",))],
                    records=[(12.0, "stage", {"stage": "Quote"}), (31.0, "reply", {"reply_kind": "positive", "contact": "c"})],
                    close_t=29.0, won=True, start=CUTOFF - timedelta(days=30))
    views = synthetic_previous() + [near_end]
    item = next(k for k in L.learn(views, CUTOFF)["knowledge"] if k["decision_point"] == "ops_outreach")
    mine = [e for e in item["evidence"] if e["ref_id"] == L.det_uuid("reaction", "NEAR", "ops_outreach")]
    assert mine == []  # the reply is dated after the cutoff


def test_too_few_deals_per_option_is_untestable_and_never_promoted() -> None:
    result = L.learn(synthetic_previous(n=30), CUTOFF)
    ops = next(h for h in result["hypotheses"] if h["decision_point"] == "ops_outreach")
    assert ops["p_value"] is None and ops["promoted"] is False and result["knowledge"] == []


def test_learning_is_deterministic_and_hashable() -> None:
    a, b = L.learn(synthetic_previous(), CUTOFF), L.learn(synthetic_previous(), CUTOFF)
    assert L.canonical_sha256(a) == L.canonical_sha256(b)
    assert len(L.canonical_sha256(a)) == 64


def test_volume_control_is_split_at_the_median() -> None:
    views = [view(f"V{i}", base_out=[(float(j), ("c",)) for j in range(1 + (i % 4))], close_t=50.0, won=i % 2 == 0,
                  start=START + timedelta(days=i)) for i in range(80)]
    h = next(h for h in L.learn(views, CUTOFF)["hypotheses"] if h["decision_point"] == "high_outbound_volume")
    assert h["n_option"] + h["n_other"] == 80 and h["n_option"] > 0 and h["n_other"] > 0


def test_every_catalog_point_has_a_unique_knowledge_key_outside_the_seeded_ones() -> None:
    keys = [p.key for p in catalog.POINTS]
    assert len(set(keys)) == len(keys) and all(k.startswith("K2") for k in keys)
