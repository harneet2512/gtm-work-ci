"""WP32 (HAR-131): the hidden rules are SELLER actions (user decision 2026-10-02). Each choice is drawn
from the policy stream, both arms leave an observable seller record, and deals with a visible real
terminal event keep their real outcome (spec Q2)."""
from __future__ import annotations

import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT / "bench"))

from synthetic import forward, rules  # noqa: E402
from synthetic.plan import Contact, DealInput, Inbound, Outbound  # noqa: E402

RS, BASE = rules.load_rules(), dict(rules.load_doc()["process"])
SEED = RS.generation_seed
FAST = {**BASE, "stage_dwell_fraction": 0.005, "policy_seek_eb_first": 0.0, "policy_pause_unstable": 0.0,
        "exit_base_daily": 0.0, "ops_note_share": 0.0, "pushback_at_quote_share": 0.0, "cc_colleague_share": 0.0,
        "e1_champion_leaves": 0.0, "e2_champion_moves": 0.0, "e4_new_executive": 0.0, "e5_entrant": 0.0,
        "reply_base_prob": 0.01}
CONTACTS = (Contact("c-champ", "champion", "business"), Contact("c-eb", "economic_buyer", "finance"),
            Contact("c-ops", "end_user", "operations"), Contact("c-tech", "technical_evaluator", "technical"))


def deal(outbound=(), inbound=(), earlier=(), real=None, days=120) -> DealInput:
    return DealInput("d-s", "a-s", days, tuple(outbound), tuple(inbound), CONTACTS, (), False, 0.0,
                     account_quote_days=tuple(earlier), real_outcome=real)


def run(d: DealInput, **proc: float) -> forward.DealResult:
    return forward.simulate(d, RS, {**FAST, **proc}, SEED)


def kinds(res: forward.DealResult, kind: str) -> list[dict]:
    return [dict(r.data) for r in res.records if r.kind == kind]


def test_p1_writing_to_an_operations_contact_before_quote() -> None:
    before = run(deal(outbound=[Outbound(0.2, ("c-ops",))]))
    assert forward.H_OPS in dict(before.features) and before.quote_t is not None and before.quote_t > 0.2
    note = run(deal(), ops_note_share=1.0)
    assert any(e["purpose"] == "ops_note" for e in kinds(note, "seller_email"))
    nobody = run(deal(outbound=[Outbound(0.2, ("c-champ",))]))
    assert forward.H_OPS not in dict(nobody.features)


def test_p2_reprice_and_hold_are_both_observable_and_only_reprice_holds_the_rule() -> None:
    reprice = run(deal(), pushback_at_quote_share=1.0, reprice_share=1.0)
    hold = run(deal(), pushback_at_quote_share=1.0, reprice_share=0.0)
    assert any(r["reply_kind"] == "price_objection" for r in kinds(reprice, "reply"))
    assert kinds(reprice, "amount_edited") and forward.H_REPRICE in dict(reprice.features)
    assert any(e["purpose"] == "value_case" for e in kinds(hold, "seller_email")) and not kinds(hold, "amount_edited")
    assert forward.H_REPRICE not in dict(hold.features)


def test_p2_never_puts_a_synthetic_pushback_next_to_a_real_customer_email() -> None:
    real = [Inbound(float(t), "c-champ") for t in range(0, 120, 5)]
    res = run(deal(inbound=real), pushback_at_quote_share=1.0)
    assert not [r for r in kinds(res, "reply") if r["reply_kind"] == "price_objection"]


def test_p3_first_technical_buyer_email_gets_a_reply_with_or_without_a_colleague() -> None:
    cc = run(deal(inbound=[Inbound(3.0, "c-tech"), Inbound(9.0, "c-tech")]), cc_colleague_share=1.0)
    no = run(deal(inbound=[Inbound(3.0, "c-tech")]), cc_colleague_share=0.0)
    replies = [e for e in kinds(cc, "seller_email") if e["purpose"] == "technical_reply"]
    assert len(replies) == 1 and replies[0]["cc_colleague"] and forward.H_CC in dict(cc.features)
    assert [e for e in kinds(no, "seller_email") if e["purpose"] == "technical_reply"] and forward.H_CC not in dict(no.features)


def test_neutral_cc_noise_lands_on_other_seller_emails_and_never_holds_p3() -> None:
    """User decision 2026-10-03: colleague ccs also appear on non-P3 seller emails, with no planted effect."""
    res = run(deal(), ops_note_share=1.0, pushback_at_quote_share=1.0, reprice_share=0.0, cc_noise_share=1.0)
    noisy = [e for e in kinds(res, "seller_email") if e["purpose"] in ("ops_note", "value_case")]
    assert noisy and all(e["cc_colleague"] for e in noisy)
    assert forward.H_CC not in dict(res.features)
    quiet = run(deal(), ops_note_share=1.0, cc_noise_share=0.0)
    assert not any(e.get("cc_colleague") for e in kinds(quiet, "seller_email"))


def test_p4_second_quote_only_with_another_open_quote_in_the_last_30_days() -> None:
    first = run(deal())
    assert first.quote_t is not None and not kinds(first, "quote_sent")
    near = first.quote_t - 10.0
    separate = run(deal(earlier=[near]), separate_quote_share=1.0)
    fold = run(deal(earlier=[near]), separate_quote_share=0.0)
    old = run(deal(earlier=[first.quote_t - 45.0]), separate_quote_share=1.0)
    assert forward.H_SECOND_QUOTE in dict(separate.features) and kinds(separate, "quote_sent")[0]["separate"]
    assert forward.H_SECOND_QUOTE not in dict(fold.features) and not kinds(fold, "quote_sent")[0]["separate"]
    assert not kinds(old, "quote_sent") and forward.H_SECOND_QUOTE not in dict(old.features)


def test_q2_a_visible_real_terminal_event_keeps_the_real_outcome() -> None:
    won = forward.simulate(deal(real="won"), RS, {**FAST, "exit_base_daily": 1.0}, SEED, outcome_override=False)
    lost = forward.simulate(deal(real="lost"), RS, FAST, SEED, outcome_override=True)
    assert won.won and won.outcome_source == "real" and not lost.won and lost.outcome_source == "real"
    assert run(deal()).outcome_source == "synthetic"


def test_seller_choices_do_not_depend_on_the_outcome() -> None:
    d = deal(outbound=[Outbound(0.2, ("c-ops",))], inbound=[Inbound(3.0, "c-tech")])
    proc = {"pushback_at_quote_share": 1.0, "cc_colleague_share": 0.5, "reprice_share": 0.5}
    a = forward.simulate(d, RS, {**FAST, **proc}, SEED, outcome_override=True)
    b = forward.simulate(d, RS, {**FAST, **proc}, SEED, outcome_override=False)
    assert a.pre_close() == b.pre_close() and a.features == b.features
