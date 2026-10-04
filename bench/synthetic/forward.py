"""Forward-in-time deal simulator (spec §d, v1.1): the kernel the generator and power_sim share.

Time runs forward one day at a time. Everything dated t (stage changes, replies, exits, seller
moves) is drawn from the state at or before t, through the deal's running health
h(t) = planted log-odds of the rules holding so far. The outcome is drawn once, at the deal's
close, from the accumulated features. No record dated before the close is a function of the
outcome: `outcome_override` changes only the closing record (tested by shuffling outcomes).
Exogenous people/org changes are planned up front from their own stream (spec §c); the hidden
rules are seller choices (seller.py). A deal with a visible REAL terminal event (a signed contract
or an Accepted / Rejected / Denied quote) keeps its real outcome (spec Q2).
"""
from __future__ import annotations

import heapq
import math
from collections.abc import Mapping
from typing import Any

from . import plan as P
from . import seller
from .rules import RuleSet, planted_log_odds
from .seeding import stream
from .state import (  # noqa: F401  (re-exported for callers and tests)
    G_GONE, G_HANDOVER, G_PAUSE, G_PUSH_UNSTABLE, G_QBEB, G_THREE, G_WITH, G_WITHOUT,
    H_CC, H_OPS, H_REPRICE, H_SECOND_QUOTE, N_EXEC, N_SIBLING, STAGES, DealResult, Record, State, logit, sigmoid,
)


def _covered_by_real_email(st: State, t: float) -> bool:
    """True when a real customer email falls between this outbound and the next one: that period
    already has a buyer response, so no synthetic reply may duplicate or contradict it."""
    nxt = min((o.t for o in st.deal.outbound if o.t > t), default=math.inf)
    return any(t < i.t <= nxt for i in st.deal.inbound)


def _on_outbound(st: State, t: float, recipients: tuple[str, ...], rs: Any) -> None:
    u = [rs.uniform() for _ in range(5)]  # fixed draws per outbound keep the stream aligned
    z = rs.normal(0.0, 1.0)
    if st.pause_until > t:
        st.hold(G_PAUSE, t)
    seller.on_seller_email(st, t, recipients)
    if st.dormant or _covered_by_real_email(st, t):
        return
    p = sigmoid(logit(st.proc["reply_base_prob"]) + st.proc["reply_health_slope"] * st.health())
    pool = [c.id for c in st.deal.contacts + st.plan.new_contacts if c.role != "end_user" and c.id != st.plan.gone_id]
    replier = recipients[0] if recipients else None
    eb = next((c.id for c in st.deal.contacts if c.role == "economic_buyer"), None)
    if eb and st.plan.seek_eb_first and not st.eb_engaged and st.stage <= 1 and u[1] < st.proc["eb_loop_in_when_seeking"]:
        replier = eb  # the seller is working to reach the economic buyer before quoting
    elif pool and u[1] < st.proc["loop_in_prob"]:
        replier = pool[int(u[2] * len(pool)) % len(pool)]
    if replier is None or replier == st.plan.gone_id or u[0] >= p:
        return
    shift = st.proc["kind_health_shift"] * math.tanh(st.health())
    pos, obj = st.proc["positive_share"] + shift, st.proc["objection_share"] - shift
    kind = "positive" if u[3] < pos else ("objection" if u[3] < pos + obj else "timing_request")
    delay = math.exp(math.log(st.proc["reply_delay_median_days"]) + st.proc["reply_delay_sigma"] * z)
    nxt = min((o.t for o in st.deal.outbound if o.t > t and replier in o.recipients), default=math.inf)
    due = max(t + 0.01, min(t + delay, nxt - 0.01))
    span = st.proc["timing_request_max_days"] - st.proc["timing_request_min_days"]
    st.push(due, "reply", replier, kind, round(due + st.proc["timing_request_min_days"] + u[4] * span, 3), u[4])


def _engage(st: State, t: float, sender: str) -> None:
    """Structural effects of any buyer email, real or synthetic."""
    who = st.contact(sender)
    st.engaged.add(sender)
    if who is not None and who.role == "economic_buyer":
        st.eb_engaged = True
    seller.on_buyer_email(st, t, sender)


def _on_reply(st: State, t: float, replier: str, kind: str, until: float, u: float) -> None:
    st.record(t, "reply", contact=replier, reply_kind=kind,
              timing_until=until if kind == "timing_request" else None)
    _engage(st, t, replier)
    if kind == "timing_request":
        st.pause_until = max(st.pause_until, until)
    if kind == "positive" and st.plan.reorg_t is not None and t > st.plan.reorg_t:
        st.need = st.need or u < st.proc["need_after_reorg_share"]
        st.value = st.value or u > 1.0 - st.proc["value_after_reorg_share"]


def _on_people(st: State, t: float, kind: str, args: tuple[Any, ...]) -> None:
    pl = st.plan
    if kind == "leave":
        st.record(t, "champion_leaves", contact=pl.gone_id, move=pl.leave_kind)
        st.champion = None
    elif kind == "replace":
        st.record(t, "champion_named", contact=args[0], acting=args[1] > 0)
        st.champion, st.replaced, st.acting_until = args[0], True, t + args[1]
    elif kind == "gone_check":
        if not st.replaced:
            st.hold(G_GONE, t)
    elif kind == "reorg":
        st.record(t, "reorg_announced", owner_change=pl.owner_change)
        if pl.owner_change:
            st.champion = None
    elif kind == "owner":
        st.record(t, "champion_named", contact=args[0], acting=args[1] > 0)
        st.champion, st.owner_t, st.acting_until = args[0], t, t + args[1]
    elif kind == "exec":
        st.record(t, "executive_joins", contact=args[0])
        st.hold(N_EXEC, t)


def _on_exogenous(st: State, t: float, kind: str, args: tuple[Any, ...]) -> None:
    pl = st.plan
    if kind == "entrant":
        st.record(t, "stakeholder_enters", contact=args[0])
        st.entrant_after_reorg = st.entrant_after_reorg or (pl.reorg_t is not None and t > pl.reorg_t)
    elif kind == "handover":
        if st.champion is not None:
            st.record(t, "written_handover", contact=st.champion, to=args[0])
            st.hold(G_HANDOVER, t)
    elif kind == "outreach":
        _on_outreach(st, t, args[0], args[1])
    elif kind == "expansion":
        st.record(t, "expansion_interest", contact=args[0])  # a new business-unit lead states need and value
        st.need = st.value = st.entrant_after_reorg = True
        _engage(st, t, args[0])
    elif kind == "ops_note":
        seller.on_ops_note(st, t, args[0])
    elif kind == "pushback":
        seller.on_pushback(st, t, args[0])
    elif kind == "tech_ack":
        seller.on_tech_ack(st, t, args[0], args[1])
    else:
        _on_people(st, t, kind, args)


def _on_outreach(st: State, t: float, entrant: str, with_champion: bool) -> None:
    if st.champion is None:
        st.record(t, "seller_outreach", to=entrant, cc_champion=False, cc_colleague=seller.noise_cc(st))
        return
    st.record(t, "seller_outreach", to=entrant, cc_champion=with_champion, cc_colleague=seller.noise_cc(st))
    st.hold(G_WITH if with_champion else G_WITHOUT, t)
    seller.on_seller_email(st, t, (entrant,))


def _advance(st: State, t: float) -> None:
    st.stage += 1
    st.stage_t[st.stage] = t
    st.record(t, "stage", stage=STAGES[st.stage])
    if st.stage == 2:
        if not st.eb_engaged:
            st.hold(G_QBEB, t)
        if len(st.engaged) >= 3:
            st.hold(G_THREE, t)
        seller.on_quote_entry(st, t)
    if st.stage >= 2 and st.plan.reorg_t is not None and st.plan.owner_change and t > st.plan.reorg_t:
        if st.unstable(t):
            st.hold(G_PUSH_UNSTABLE, t)
        # syn1_push_after_owner_stable was dropped 2026-10-03 (sign recovered in 0.72 of seeds; user decision)


def _stage_step(st: State, t: float, ss: Any) -> None:
    u = ss.uniform()
    if st.stage >= len(STAGES) - 1:
        return
    if st.plan.pause_unstable and st.unstable(t):
        return
    qual = st.stage_t.get(1, t)
    if st.stage == 1 and st.plan.seek_eb_first and not st.eb_engaged and t - qual < st.proc["seek_eb_max_wait_days"]:
        return
    rate = math.exp(st.proc["stage_health_slope"] * st.health()) / (st.proc["stage_dwell_fraction"] * st.deal.days)
    if u < 1.0 - math.exp(-rate):
        _advance(st, t)


def _exit_step(st: State, es: Any) -> bool:
    """Daily exit hazard from the state so far. A deal that exits goes dormant: no more stage advances or
    synthetic replies, and it closes lost at its CloseDate, so no close date depends on the outcome."""
    u = es.uniform()
    if st.dormant:
        return False
    p = 1.0 - math.exp(-st.proc["exit_base_daily"] * math.exp(-st.proc["exit_health_slope"] * st.health()))
    return u < p


def _dispatch(st: State, t: float, kind: str, args: tuple[Any, ...], rs: Any) -> None:
    if kind == "outbound":
        _on_outbound(st, t, args[0], rs)
    elif kind == "reply":
        _on_reply(st, t, *args)
    elif kind == "real_email":
        _engage(st, t, args[0])  # base data: shapes features, writes no synthetic record
    else:
        _on_exogenous(st, t, kind, args)


def simulate(deal: P.DealInput, rules: RuleSet, proc: Mapping[str, float], root_seed: int,
             outcome_override: bool | None = None) -> DealResult:
    plan = P.make_plan(deal, proc, stream(root_seed, "deal", deal.deal_id, "exogenous"))
    st = State(deal=deal, plan=plan, rules=rules, proc=proc, champion=plan.champion_id,
               policy=stream(root_seed, "deal", deal.deal_id, "policy"))
    if deal.sibling_opened_within_30d:
        st.hold(N_SIBLING, 0.0)
    for o in deal.outbound:
        st.push(o.t, "outbound", o.recipients)
    for i in deal.inbound:
        st.push(i.t, "real_email", i.sender)
    for t, kind, args in plan.events:
        st.push(t, kind, *args)
    rs, ss, es = (stream(root_seed, "deal", deal.deal_id, s) for s in ("replies", "stage", "exit"))
    for day in range(deal.days):
        while st.heap and st.heap[0][0] < day + 1:
            t, _, kind, args = heapq.heappop(st.heap)
            _dispatch(st, t, kind, args, rs)
        if not st.dormant:
            _stage_step(st, day + 0.9, ss)
        st.dormant = st.dormant or _exit_step(st, es)
    u = stream(root_seed, "deal", deal.deal_id, "outcome").uniform()
    if deal.real_outcome is not None:  # Q2: a visible real terminal event fixes the outcome
        return _close(st, float(deal.days), won=deal.real_outcome == "won", source="real")
    if st.dormant:
        return _close(st, float(deal.days), won=False)
    won = outcome_override if outcome_override is not None else u < sigmoid(
        float(rules.outcome["base_log_odds"]) + st.health() + deal.account_effect)
    return _close(st, float(deal.days), won=won)


def _close(st: State, t: float, won: bool, source: str = "synthetic") -> DealResult:
    st.record(t, "close", stage="Closed Won" if won else "Closed Lost")
    return DealResult(
        deal_id=st.deal.deal_id, records=tuple(st.records), features=tuple(sorted(st.features.items())),
        won=won, close_t=t, dormant=st.dormant, final_log_odds=planted_log_odds(st.rules, st.features),
        quote_t=st.stage_t.get(2), outcome_source=source,
    )
