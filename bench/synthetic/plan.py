"""Deal inputs and the exogenous plan of people/org changes (spec §c).

The plan is drawn up front from the deal's own 'exogenous' stream and depends only on base data
(contacts, roles, window, account reorg dates): never on health, stage or outcome. Seller
policies (wait for the economic buyer, pause while ownership is unstable) are drawn here too.
"""
from __future__ import annotations

import json
from collections.abc import Mapping
from dataclasses import dataclass
from pathlib import Path
from typing import Any

from .records import sf_id
from .seeding import Stream

FUNCTIONS = ("finance", "technical", "operations", "business")
_MIX = json.loads((Path(__file__).resolve().parent / "base_profile.v1.json").read_text(encoding="utf-8"))["function_mix"]


def draw_function(s: Stream) -> str:
    """A job function drawn from the real title mix of the WP31 export (base_profile.v1.json)."""
    u, acc = s.uniform(), 0.0
    for name, w in _MIX.items():
        acc += w
        if u < acc:
            return name
    return "business"


@dataclass(frozen=True)
class Contact:
    id: str
    role: str
    function: str


@dataclass(frozen=True)
class Outbound:
    t: float  # days since the deal was created
    recipients: tuple[str, ...]


@dataclass(frozen=True)
class Inbound:
    """A REAL customer email in the base data (WP31: 43.4% of CRMArena-Pro emails are customer-sent)."""
    t: float
    sender: str


@dataclass(frozen=True)
class DealInput:
    deal_id: str
    account_id: str
    days: int
    outbound: tuple[Outbound, ...]
    inbound: tuple[Inbound, ...]  # real customer emails: base data, never duplicated or contradicted
    contacts: tuple[Contact, ...]  # the account's buying group with this deal's roles
    reorg_days: tuple[float, ...]  # account reorganisations relative to the deal's start
    sibling_opened_within_30d: bool  # base data: another opportunity on the account opened in the 30 d before
    account_effect: float  # unobserved per-account log-odds offset (never written)
    account_quote_days: tuple[float, ...] = ()  # other quotes at the account on this deal's clock: real base quotes + earlier synthetic ones
    real_outcome: str | None = None  # 'won' / 'lost' when a visible real terminal event fixes it (spec Q2)


@dataclass(frozen=True)
class Plan:
    champion_id: str | None
    gone_id: str | None
    leave_kind: str | None
    reorg_t: float | None
    owner_change: bool
    seek_eb_first: bool
    pause_unstable: bool
    new_contacts: tuple[Contact, ...]
    events: tuple[tuple[float, str, tuple[Any, ...]], ...]


def _acting(proc: Mapping[str, float], s: Stream) -> float:
    if s.uniform() >= proc["acting_share"]:
        return 0.0
    return proc["acting_min_days"] + s.uniform() * (proc["acting_max_days"] - proc["acting_min_days"])


def _new_contact(deal: DealInput, tag: str, role: str, s: Stream) -> Contact:
    return Contact(sf_id("Contact", deal.account_id, deal.deal_id, tag), role, draw_function(s))


def _leave(deal: DealInput, proc: Mapping[str, float], s: Stream, champ: str | None,
           events: list, contacts: list) -> str | None:
    """E1/E2: the champion leaves or moves away; a replacement is named within the window unless unreplaced.
    Returns 'leaves', 'moves' or None. Draws a fixed number of uniforms whatever happens."""
    u, t = s.uniform(), deal.days * (0.1 + 0.8 * s.uniform())
    unreplaced, delay, acting = s.uniform(), s.uniform(), _acting(proc, s)
    p_leave, p_move = proc["e1_champion_leaves"], proc["e2_champion_moves"]
    if champ is None or u >= p_leave + p_move:
        return None
    events += [(t, "leave", ()), (t + proc["replacement_window_days"], "gone_check", ())]
    if unreplaced >= proc["unreplaced_share"]:
        new = _new_contact(deal, "replacement", "champion", s)
        contacts.append(new)
        events.append((t + 1 + delay * (proc["replacement_window_days"] - 2), "replace", (new.id, acting)))
    return "leaves" if u < p_leave else "moves"


def make_plan(deal: DealInput, proc: Mapping[str, float], s: Stream) -> Plan:
    champ = next((c.id for c in deal.contacts if c.role == "champion"), None)
    seek_eb, pause = s.uniform() < proc["policy_seek_eb_first"], s.uniform() < proc["policy_pause_unstable"]
    events: list = []
    contacts: list = []
    kind = _leave(deal, proc, s, champ, events, contacts)
    reorgs = [r for r in deal.reorg_days if 0.0 <= r < deal.days]
    reorg_t = reorgs[0] if reorgs else None
    owner_change = s.uniform() < proc["reorg_owner_change_share"]
    owner_delay, acting = s.uniform() * 20.0, _acting(proc, s)
    expansion, x_delay = s.uniform() < proc["expansion_after_reorg_share"], 5.0 + 25.0 * s.uniform()
    if reorg_t is not None:
        events.append((reorg_t, "reorg", ()))
        if owner_change:
            owner = _new_contact(deal, "owner", "champion", s)
            contacts.append(owner)
            events.append((reorg_t + owner_delay, "owner", (owner.id, acting)))
            if expansion:  # pitch §3: after the reorg a new business unit shows interest
                lead = _new_contact(deal, "bu_lead", "end_user", s)
                contacts.append(lead)
                events.append((reorg_t + owner_delay + x_delay, "expansion", (lead.id,)))
    if s.uniform() < proc["e4_new_executive"]:
        exe = _new_contact(deal, "executive", "end_user", s)
        contacts.append(exe)
        events.append((deal.days * s.uniform(), "exec", (exe.id,)))
    _entrant(deal, proc, s, events, contacts)
    _ops_note(deal, proc, s, events)
    return Plan(champ, champ if kind else None, kind, reorg_t, owner_change and reorg_t is not None,
                seek_eb, pause, tuple(contacts), tuple(sorted(events, key=lambda e: e[0])))


def _entrant(deal: DealInput, proc: Mapping[str, float], s: Stream, events: list, contacts: list) -> None:
    u, t_e, gap = s.uniform(), deal.days * (0.25 + 0.6 * s.uniform()), 1.0 + 13.0 * s.uniform()
    with_champion, handover, h_at = s.uniform() < proc["outreach_with_champion"], s.uniform(), s.uniform()
    if u >= proc["e5_entrant"]:
        return
    who = _new_contact(deal, "entrant", "technical_evaluator", s)
    contacts.append(who)
    events.append((t_e, "entrant", (who.id,)))
    if not with_champion and handover < proc["written_handover_share"]:
        events.append((t_e + h_at * gap * 0.9, "handover", (who.id,)))
    events.append((t_e + gap, "outreach", (who.id, with_champion)))


def _ops_note(deal: DealInput, proc: Mapping[str, float], s: Stream, events: list) -> None:
    """P1's synthetic arm: the seller writes a short note to an operations-title contact early in the deal."""
    u, at, pick = s.uniform(), deal.days * (0.05 + 0.45 * s.uniform()), s.uniform()
    ops = [c.id for c in deal.contacts if c.function == "operations"]
    if ops and u < proc["ops_note_share"]:
        events.append((at, "ops_note", (ops[int(pick * len(ops)) % len(ops)],)))
