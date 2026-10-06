"""A stand-in base world with the measured shape of the CRMArena-Pro B2B org, for power_sim only.

The record-level WP31 export is git-ignored, so power_sim cannot depend on it. Its aggregate
profile is committed (base_profile.v1.json, profile_base.py): the title seniority and function
mixes (titles are filled on 100% of contacts), the customer-email share (43.4%) and the emails per
deal. The world draws 101 accounts, 9 contacts each, 8 to 15 opportunities per account over
2020-2024 from those mixes. Deal windows, account structure and reorg timing remain stand-ins.
Base-data null features (industry, amount band, rep, weekday) have no planted effect.
"""
from __future__ import annotations

import json
import math
from collections.abc import Mapping
from pathlib import Path
from dataclasses import dataclass

from . import roles as R
from .plan import Contact, DealInput, Inbound, Outbound, draw_function
from .seeding import Stream, stream

PROFILE = json.loads((Path(__file__).resolve().parent / "base_profile.v1.json").read_text(encoding="utf-8"))
SPAN_DAYS = 1640  # 2020-01-01 .. mid-2024 deal starts
CUSTOMER_EMAIL_SHARE = PROFILE["customer_email_share"]  # WP31: 2,245 of 5,177 emails were sent by customers
SENIORITY_WEIGHTS = tuple((int(k), w) for k, w in PROFILE["seniority_mix"].items())



@dataclass(frozen=True)
class WorldDeal:
    deal: DealInput
    start: float
    nulls: tuple[tuple[str, int], ...]  # industry, amount_band, owner, weekday
    base_quote: float | None  # absolute day of this deal's REAL base quote, if it has one (0.60 per opportunity)


def _weighted(s: Stream, pairs: tuple[tuple[int, float], ...]) -> int:
    u, acc = s.uniform(), 0.0
    for value, w in pairs:
        acc += w
        if u < acc:
            return value
    return pairs[-1][0]


def _poisson(s: Stream, lam: float) -> int:
    k, p, limit = 0, s.uniform(), math.exp(-lam)
    while p > limit:
        k += 1
        p *= s.uniform()
    return k


def _people(account: str, s: Stream) -> list[R.Person]:
    return [R.Person(f"{account}-c{i}", _weighted(s, SENIORITY_WEIGHTS), draw_function(s)) for i in range(9)]


def _reorgs(proc: Mapping[str, float], s: Stream) -> list[float]:
    rate, t, out = proc["reorg_rate_per_year"] / 365.0, 0.0, []
    while True:
        t += -math.log(1.0 - s.uniform()) / rate
        if t > SPAN_DAYS + 200:
            return out
        out.append(t)


def _deal(account: str, idx: int, people: list[R.Person], acct_roles: Mapping[str, str], start: float,
          ctx: tuple[list[float], list[float], float], s: Stream) -> DealInput:
    reorgs, starts, effect = ctx
    days = s.integer(30, 180)
    mid = [p.id for p in people if 2 <= p.seniority <= 4]
    primary = s.choice(mid) if mid else None
    n = 1 + _poisson(s, 3.4)
    times = sorted(0.85 * days * s.uniform() for _ in range(n))
    champ = R.deal_champion(people, acct_roles, primary, {})
    outbound, inbound = [], []
    for t in times:  # each email is customer-sent with the measured share (WP31: 43.4%)
        customer, who = s.uniform() < CUSTOMER_EMAIL_SHARE, champ if champ and s.uniform() < 0.65 else s.choice(people).id
        (inbound if customer else outbound).append((round(t, 3), who))
    if not outbound:
        outbound, inbound = [inbound[0]], inbound[1:]
    by_id = {p.id: p for p in people}
    contacts = tuple(Contact(pid, "champion" if pid == champ else role, by_id[pid].function)
                     for pid, role in sorted(acct_roles.items()))
    sibling = any(start - 30 <= o < start for o in starts)
    u, won, lost = s.uniform(), PROFILE["real_terminal_won_share"], PROFILE["real_terminal_lost_share"]
    real = "won" if u < won else ("lost" if u < won + lost else None)  # visible real terminal event (spec Q2)
    return DealInput(f"{account}-d{idx}", account, days, tuple(Outbound(t, (w,)) for t, w in outbound),
                     tuple(Inbound(t, w) for t, w in inbound), contacts, tuple(r - start for r in reorgs), sibling, effect,
                     real_outcome=real)


def build_world(seed: int, proc: Mapping[str, float], sd: float, n_accounts: int = 101) -> tuple[WorldDeal, ...]:
    out: list[WorldDeal] = []
    for a in range(n_accounts):
        account = f"acct{a:03d}"
        s = stream(seed, "world", account)
        people = _people(account, s)
        acct_roles = R.account_roles(people, s.uniform() < 0.5, s)
        industry, effect = s.integer(0, 7), s.normal(0.0, sd)
        reorgs = _reorgs(proc, s)
        starts = sorted(SPAN_DAYS * s.uniform() for _ in range(s.integer(8, 15)))
        for i, start in enumerate(starts):
            deal = _deal(account, i, people, acct_roles, start, (reorgs, starts, effect), s)
            weekday = int(start + deal.outbound[0].t) % 7
            nulls = (("industry", industry), ("amount_band", s.integer(0, 3)), ("owner", s.integer(0, 19)),
                     ("weekday", weekday))
            has_quote, back = s.uniform() < PROFILE["quotes_per_opportunity"], 30.0 * s.uniform()
            out.append(WorldDeal(deal, start, nulls, start - back if has_quote else None))  # base quotes are mis-dated early
    return tuple(out)
