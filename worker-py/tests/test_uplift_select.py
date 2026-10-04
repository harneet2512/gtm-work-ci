"""bench/uplift/select: situations are chosen by decision relevance and a fixed seeded order, never by outcome."""
from __future__ import annotations

import sys
from datetime import datetime, timedelta, timezone
from pathlib import Path
from types import SimpleNamespace

ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT))

from bench.uplift import select as S  # noqa: E402

START = datetime(2023, 10, 1, tzinfo=timezone.utc)
CUTOFF = datetime(2023, 11, 1, tzinfo=timezone.utc)
WINDOWS = {"price_pushback": datetime(2024, 4, 28, tzinfo=timezone.utc), "second_quote": datetime(2023, 12, 28, tzinfo=timezone.utc)}
PRICE = "Thank you for the proposal. I have to be candid that the pricing for Orbit is higher than what we had planned for this year."
FIT = "Before we can go further, we need to understand how it would fit alongside the tools we already rely on."


def iso(t: datetime) -> str:
    return t.strftime("%Y-%m-%dT%H:%M:%SZ")


def stage(t: datetime, name: str) -> dict:
    return {"source_system": "crm", "source_object_id": "opp:D1", "source_event_key": f"field:StageName:{name}", "occurred_at": iso(t),
            "payload": {}}


def inbound(t: datetime, oid: str, body: str) -> dict:
    return {"source_system": "email", "source_object_id": oid, "source_event_key": "received", "occurred_at": iso(t),
            "payload": {"direction": "inbound", "body_text": body}}


def world(records, *, emails=(), quote_days=()) -> SimpleNamespace:
    res = SimpleNamespace(records=tuple(SimpleNamespace(t=t, kind=k, data=tuple(sorted(d.items()))) for t, k, d in records))
    bd = SimpleNamespace(start=START, emails=tuple(emails), deal=SimpleNamespace(account_quote_days=tuple(quote_days)))
    return SimpleNamespace(results={"D1": res}, by_deal={"D1": bd}, split={"previous_deal_ids": [], "ambiguous_deal_ids": []})


def find(fn, w, events):
    return list(fn(w, "D1", "A1", events, CUTOFF, WINDOWS))


# ---- freshness -----------------------------------------------------------------------------------------------------


def test_an_item_is_fresh_for_180_days_after_its_last_validating_evidence() -> None:
    item = {"evidence": [{"kind": "decision_episode", "at": "2023-07-01T10:00:00Z"},
                         {"kind": "business_outcome", "outcome_type": "closed_lost", "at": "2023-09-01T10:00:00Z"},
                         {"kind": "customer_reaction", "polarity": "negative", "at": "2023-10-01T10:00:00Z"},
                         {"kind": "business_outcome", "outcome_type": "closed_won", "at": "2023-07-10T10:00:00Z"}]}
    assert S.last_validated(item) == datetime(2023, 7, 10, 10, tzinfo=timezone.utc)  # losses and negatives do not validate
    assert S.fresh_until(item) == datetime(2023, 7, 10, 10, tzinfo=timezone.utc) + timedelta(days=180) - timedelta(seconds=1)


def test_stage_at_is_the_last_stage_event_before_the_time() -> None:
    events = [stage(START + timedelta(days=1), "Qualification"), stage(START + timedelta(days=10), "Quote")]
    assert S.stage_at(events, START) is None
    assert S.stage_at(events, START + timedelta(days=5)) == "Qualification"
    assert S.stage_at(events, START + timedelta(days=11)) == "Quote"


# ---- price pushback ------------------------------------------------------------------------------------------------


def test_a_price_worded_pushback_in_quote_inside_the_window_is_a_discriminating_situation() -> None:
    t0 = CUTOFF + timedelta(days=10)
    w = world([((t0 - START).days + 0.0, "reply", {"reply_kind": "price_objection", "contact": "c"})])
    events = [stage(t0 - timedelta(days=3), "Quote"), inbound(t0 + timedelta(hours=5), "02sX", PRICE)]
    got = find(S._price_pushback, w, events)
    assert [(s.kind, s.point, s.trigger_object_id) for s in got] == [("discriminating", "price_pushback", "02sX")]
    assert "hidden rule" in got[0].reason and got[0].id == "S-price_pushback-disc-D1"


def test_a_pushback_that_does_not_read_as_price_or_is_not_in_quote_or_is_outside_the_window_is_skipped() -> None:
    t0 = CUTOFF + timedelta(days=10)
    rec = [((t0 - START).days + 0.0, "reply", {"reply_kind": "price_objection", "contact": "c"})]
    quote = stage(t0 - timedelta(days=3), "Quote")
    assert find(S._price_pushback, world(rec), [quote, inbound(t0 + timedelta(hours=5), "02sX", FIT)]) == []
    assert find(S._price_pushback, world(rec), [stage(t0 - timedelta(days=3), "Qualification"),
                                                inbound(t0 + timedelta(hours=5), "02sX", PRICE)]) == []
    assert find(S._price_pushback, world(rec), [quote, stage(t0 - timedelta(days=1), "Negotiation"),
                                                inbound(t0 + timedelta(hours=5), "02sX", PRICE)]) == []
    late = CUTOFF + timedelta(days=200)
    far = [(((late - START).days) + 0.0, "reply", {"reply_kind": "price_objection", "contact": "c"})]
    assert find(S._price_pushback, world(far), [stage(late - timedelta(days=3), "Quote"), inbound(late + timedelta(hours=5), "02sY", PRICE)]) == []


def test_a_price_worded_objection_before_quote_is_an_exception_situation() -> None:
    t0 = CUTOFF + timedelta(days=12)
    w = world([((t0 - START).days + 0.0, "reply", {"reply_kind": "objection", "contact": "c"})])
    events = [stage(t0 - timedelta(days=4), "Qualification"), inbound(t0 + timedelta(hours=6), "02sE", PRICE)]
    got = find(S._price_early, w, events)
    assert [(s.kind, s.point) for s in got] == [("exception", "price_pushback")]
    assert find(S._price_early, w, [stage(t0 - timedelta(days=4), "Quote"), inbound(t0 + timedelta(hours=6), "02sE", PRICE)]) == []


# ---- second quote --------------------------------------------------------------------------------------------------


def base_email(t: datetime, oid: str):
    return SimpleNamespace(id=oid, at=t, outbound=False)


def test_a_second_quote_situation_is_triggered_by_the_customers_next_email_not_by_the_stage_change() -> None:
    t0 = CUTOFF + timedelta(days=5)
    w = world([((t0 - START).days + 0.0, "quote_sent", {"separate": True})], emails=[base_email(t0 + timedelta(days=2), "02sREAL")])
    events = [stage(t0 + timedelta(hours=3), "Quote")]
    got = find(S._second_quote, w, events)
    assert [(s.kind, s.trigger_object_id, s.trigger_event_key) for s in got] == [("discriminating", "02sREAL", "received")]
    assert find(S._second_quote, world([((t0 - START).days + 0.0, "quote_sent", {"separate": True})]), events) == []  # no email


def test_the_same_moment_in_negotiation_is_an_exception() -> None:
    t0 = CUTOFF + timedelta(days=5)
    w = world([((t0 - START).days + 0.0, "stage", {"stage": "Negotiation"})], emails=[base_email(t0 + timedelta(days=2), "02sN")],
              quote_days=[(t0 - START).days - 10.0])
    got = find(S._negotiation_quote, w, [stage(t0 + timedelta(hours=3), "Negotiation")])
    assert [(s.kind, s.point) for s in got] == [("exception", "second_quote")]
    no_quote = world([((t0 - START).days + 0.0, "stage", {"stage": "Negotiation"})], emails=[base_email(t0 + timedelta(days=2), "02sN")])
    assert find(S._negotiation_quote, no_quote, [stage(t0 + timedelta(hours=3), "Negotiation")]) == []


# ---- ordering ------------------------------------------------------------------------------------------------------


def sit(i: int, kind: str = "discriminating", point: str = "price_pushback", deal: str | None = None) -> S.Situation:
    return S.Situation(f"S-{point}-{kind[:4]}-{i}", kind, point, deal or f"D{i}", "A", f"o{i}", "received", START, "r")


def test_order_depends_only_on_the_seed_and_the_id() -> None:
    items = [sit(i) for i in range(20)]
    assert S.order(items) == S.order(list(reversed(items)))
    assert S.order(items, seed=1) != S.order(items, seed=2)


def test_pick_takes_the_first_n_per_kind_and_point_and_one_per_deal() -> None:
    items = [sit(i) for i in range(10)] + [sit(i, point="second_quote") for i in range(10)] + [sit(20, "exception", "second_quote", "D1")]
    plan = {("discriminating", "price_pushback"): 4, ("discriminating", "second_quote"): 3, ("exception", "second_quote"): 5}
    chosen = S.pick(items, plan)
    assert [(s.kind, s.point) for s in chosen].count(("discriminating", "price_pushback")) == 4
    assert [(s.kind, s.point) for s in chosen].count(("discriminating", "second_quote")) == 3
    assert [s.deal_id for s in chosen if s.kind == "exception"] == ["D1"]  # a different kind may reuse a deal
    assert chosen == S.pick(list(reversed(items)), plan)


def test_selection_never_reads_an_outcome() -> None:
    assert "won" not in S.Situation.__dataclass_fields__
    source = Path(S.__file__).read_text(encoding="utf-8")
    assert ".won" not in source and "close_t" not in source
