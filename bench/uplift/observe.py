"""What a learner can see of a deal's seller decisions: observations of each catalog decision point.

Input is a DealView: the deal's observable facts (contacts with their functions, the real seller emails, the seller
and customer records the generator rendered into events). It carries no outcome-dependent field and no rule key; the
outcome (won/lost and when) is the only label. Clean mode: the learner reads the records the events were rendered
from, i.e. perfect extraction; the noisy-mode caveat (synthetic caveats) says what that leaves out.
"""
from __future__ import annotations

from dataclasses import dataclass
from datetime import datetime, timedelta
from typing import Any

REACTION_WINDOW_DAYS = 21.0
PRICE_OBJECTION = "price_objection"
POSITIVE_KINDS = frozenset({"positive", "positive_need", "positive_value"})
NEGATIVE_KINDS = frozenset({"objection", PRICE_OBJECTION, "timing_request"})
SELLER_EMAIL_KINDS = frozenset({"seller_email", "seller_outreach"})
AMOUNT_EDIT_WINDOW_DAYS = 3.0


@dataclass(frozen=True)
class Rec:
    t: float
    kind: str
    data: dict[str, Any]


@dataclass(frozen=True)
class DealView:
    deal_id: str
    account_id: str
    start: datetime
    contacts: dict[str, str]  # contact id -> job function (technical, operations, finance, business)
    base_outbound: tuple[tuple[float, tuple[str, ...]], ...]  # (t, recipient contact ids) of real seller emails
    records: tuple[Rec, ...]
    close_t: float
    won: bool

    def at(self, t: float) -> datetime:
        return self.start + timedelta(days=t)


@dataclass(frozen=True)
class Obs:
    deal_id: str
    point: str
    t: float  # decision time in days on the deal's clock
    took: bool | None  # None: the point's option is decided later (volume control)
    value: float  # volume control: the number of outbound emails; 0 otherwise
    won: bool
    close_t: float
    reaction: str | None  # positive | negative | None: how the buyer's next reply read
    reaction_t: float | None


def _first(view: DealView, kind: str, pred=lambda d: True) -> Rec | None:
    return next((r for r in sorted(view.records, key=lambda r: r.t) if r.kind == kind and pred(r.data)), None)


def _reaction(view: DealView, t: float) -> tuple[str | None, float | None]:
    for r in sorted(view.records, key=lambda r: r.t):
        if r.kind == "reply" and t < r.t <= t + REACTION_WINDOW_DAYS:
            kind = r.data.get("reply_kind")
            if kind in POSITIVE_KINDS:
                return "positive", r.t
            if kind in NEGATIVE_KINDS:
                return "negative", r.t
    return None, None


def _obs(view: DealView, point: str, t: float, took: bool | None, value: float = 0.0) -> Obs:
    reaction, reaction_t = _reaction(view, t)
    return Obs(view.deal_id, point, t, took, value, view.won, view.close_t, reaction, reaction_t)


def _ops_outreach(view: DealView) -> Obs | None:
    ops = {c for c, f in view.contacts.items() if f == "operations"}
    if not ops:
        return None
    quote = _first(view, "stage", lambda d: d.get("stage") == "Quote")
    end = quote.t if quote is not None else view.close_t
    times = [t for t, rcpts in view.base_outbound if t < end and ops & set(rcpts)]
    times += [r.t for r in view.records if r.kind in SELLER_EMAIL_KINDS and r.t < end and r.data.get("to") in ops]
    return _obs(view, "ops_outreach", min(times) if times else end, bool(times))


def _price_pushback(view: DealView) -> Obs | None:
    push = _first(view, "reply", lambda d: d.get("reply_kind") == PRICE_OBJECTION)
    if push is None:
        return None
    edited = any(r.kind == "amount_edited" and push.t < r.t <= push.t + AMOUNT_EDIT_WINDOW_DAYS for r in view.records)
    return _obs(view, "price_pushback", push.t, edited)


def _technical_reply(view: DealView) -> Obs | None:
    rec = _first(view, "seller_email", lambda d: d.get("purpose") == "technical_reply")
    return None if rec is None else _obs(view, "technical_reply", rec.t, bool(rec.data.get("cc_colleague")))


def _second_quote(view: DealView) -> Obs | None:
    rec = _first(view, "quote_sent")
    return None if rec is None else _obs(view, "second_quote", rec.t, bool(rec.data.get("separate")))


def _outbound_times(view: DealView) -> list[float]:
    return sorted([t for t, _ in view.base_outbound] + [r.t for r in view.records if r.kind in SELLER_EMAIL_KINDS])


def _controls(view: DealView) -> list[Obs]:
    out = []
    times = _outbound_times(view)
    if times:
        weekday = view.at(times[0]).weekday()
        out.append(_obs(view, "first_send_early_week", times[0], weekday in (0, 1)))
        out.append(_obs(view, "high_outbound_volume", times[0], None, float(len(times))))
    sent = [r for r in view.records if r.kind in SELLER_EMAIL_KINDS]
    if sent:
        out.append(_obs(view, "any_colleague_cc", sent[0].t, any(bool(r.data.get("cc_colleague")) for r in sent)))
    return out


def observe(view: DealView) -> list[Obs]:
    """Every catalog decision point the deal exercised, in catalog order."""
    found = [_ops_outreach(view), _price_pushback(view), _technical_reply(view), _second_quote(view)]
    return [o for o in found if o is not None] + _controls(view)
