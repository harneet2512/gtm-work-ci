"""Situation selection (HAR-129 'Required knowledge-uplift experiment'): decision relevance only, never an outcome.

A DISCRIMINATING situation is a test-split decision moment where a learned item applies AND the hidden rules rate the
agent's choice (so the choice matters). An EXCEPTION situation is superficially the same kind of moment where the
learned item's precondition fails, so the planted rule does not apply (it must be retrieved, found not applicable and
not applied). Both are chosen from deals that closed on or after the cutoff-split's test side (current and future
deals), inside the window in which the learned items are still fresh under the lifecycle's stale rule, from the
rendered events the agent will see. No outcome (won/lost) and no arm result is read here.

  price_pushback  discriminating: a price-worded objection email from the buyer while the deal is in Quote, and the
                  generator recorded a pushback there (the planted choice: change the quoted amount, or hold it).
                  exception: a price-worded objection email BEFORE Quote (the same words, no quote to re-price).
  second_quote    discriminating: the deal is in Quote, another quote at the account is open within 30 days (the
                  generator recorded the choice at Quote entry) and the customer's next email is the trigger (the agent
                  is only ever triggered by a customer reply; the planted choice: send a separate quote, or fold it into
                  the open one). exception: the same in Negotiation, past the stage the rule is about.
"""
from __future__ import annotations

import hashlib
import json
import re
from collections.abc import Callable, Iterable, Mapping, Sequence
from dataclasses import dataclass
from datetime import datetime, timedelta, timezone
from pathlib import Path
from typing import Any

from .data import World

SEED = 20261004  # fixed before any run; orders candidates, never picks by outcome
PRICE_TEXT = re.compile(r"the pricing for .+ is higher than what we had planned", re.S)
STALE_DAYS = 180
OPEN_QUOTE_DAYS = 30.0


@dataclass(frozen=True)
class Situation:
    id: str
    kind: str  # discriminating | exception
    point: str
    deal_id: str
    account_id: str
    trigger_object_id: str  # source_object_id of the event that triggers the agent
    trigger_event_key: str
    trigger_time: datetime
    reason: str

    def as_json(self) -> dict:
        return {"id": self.id, "kind": self.kind, "decision_point": self.point, "deal_id": self.deal_id,
                "account_id": self.account_id, "trigger": {"source_object_id": self.trigger_object_id,
                                                           "source_event_key": self.trigger_event_key,
                                                           "occurred_at": self.trigger_time.strftime("%Y-%m-%dT%H:%M:%SZ")},
                "selection_reason": self.reason}


def parse_time(text: str) -> datetime:
    return datetime.strptime(text, "%Y-%m-%dT%H:%M:%SZ").replace(tzinfo=timezone.utc)


def synthetic_events(syn_dir: Path, account_id: str, deal_id: str) -> list[dict[str, Any]]:
    path = syn_dir / "events" / account_id / f"{deal_id}.json"
    return json.loads(path.read_text(encoding="utf-8")) if path.exists() else []


def covered_deals(syn_dir: Path) -> set[str]:
    return set(json.loads((syn_dir / "labels" / "covered_deals.json").read_text(encoding="utf-8"))["deals"])


def last_validated(item: Mapping[str, Any]) -> datetime:
    """When the lifecycle last saw validating evidence (a decision episode, a positive reaction or a won outcome)."""
    times = [parse_time(e["at"]) for e in item["evidence"]
             if e["kind"] == "decision_episode" or (e["kind"] == "customer_reaction" and e["polarity"] == "positive")
             or (e["kind"] == "business_outcome" and e["outcome_type"] == "closed_won")]
    return max(times)


def fresh_until(item: Mapping[str, Any]) -> datetime:
    """The last moment the item is not stale (the lifecycle's stale rule, ADR-0013)."""
    return last_validated(item) + timedelta(days=STALE_DAYS) - timedelta(seconds=1)


def stage_at(events: Sequence[Mapping[str, Any]], t: datetime) -> str | None:
    stages = [(parse_time(e["occurred_at"]), e["source_event_key"].split(":", 2)[2]) for e in events
              if e["source_event_key"].startswith("field:StageName:") and parse_time(e["occurred_at"]) < t]
    return max(stages)[1] if stages else None


def _inbound(events: Sequence[Mapping[str, Any]], lo: datetime, hi: datetime) -> list[Mapping[str, Any]]:
    return [e for e in events if e["source_system"] == "email" and e["payload"].get("direction") == "inbound"
            and lo < parse_time(e["occurred_at"]) <= hi]


def _window(world: World, deal: str, t: float) -> tuple[datetime, datetime]:
    start = world.by_deal[deal].start.astimezone(timezone.utc)
    return start + timedelta(days=t), start + timedelta(days=t + 1.0)


def _stage_event(events: Sequence[Mapping[str, Any]], stage: str, lo: datetime, hi: datetime) -> Mapping[str, Any] | None:
    return next((e for e in events if e["source_event_key"] == f"field:StageName:{stage}"
                 and lo < parse_time(e["occurred_at"]) <= hi), None)


def _mk(point: str, kind: str, deal: str, account: str, ev: Mapping[str, Any], reason: str) -> Situation:
    return Situation(f"S-{point}-{kind[:4]}-{deal}", kind, point, deal, account, ev["source_object_id"], ev["source_event_key"],
                     parse_time(ev["occurred_at"]), reason)


def candidates(world: World, syn_dir: Path, cutoff: datetime, windows: Mapping[str, datetime]) -> list[Situation]:
    """Every eligible situation of the test side, unordered. windows: point -> last fresh moment of its learned item."""
    covered = covered_deals(syn_dir)
    test = [d for d in world.results if d not in set(world.split["previous_deal_ids"]) | set(world.split["ambiguous_deal_ids"])]
    out: list[Situation] = []
    for deal in sorted(test):
        res, bd = world.results[deal], world.by_deal[deal]
        if deal not in covered or res.outcome_source != "synthetic":
            continue
        events = synthetic_events(syn_dir, bd.deal.account_id, deal)
        for finder in (_price_pushback, _price_early, _second_quote, _negotiation_quote):
            out += finder(world, deal, bd.deal.account_id, events, cutoff, windows)
    return out


def _inside(t: datetime, cutoff: datetime, point: str, windows: Mapping[str, datetime]) -> bool:
    return point in windows and cutoff < t <= windows[point]


def _price_pushback(world: World, deal: str, account: str, events: Sequence[Mapping[str, Any]], cutoff: datetime,
                    windows: Mapping[str, datetime]) -> Iterable[Situation]:
    for rec in world.results[deal].records:
        d = dict(rec.data)
        if rec.kind != "reply" or d["reply_kind"] != "price_objection":
            continue
        lo, hi = _window(world, deal, rec.t)
        for ev in _inbound(events, lo, hi):
            when = parse_time(ev["occurred_at"])
            if PRICE_TEXT.search(ev["payload"]["body_text"]) and stage_at(events, when) == "Quote" \
                    and _inside(when, cutoff, "price_pushback", windows):
                yield _mk("price_pushback", "discriminating", deal, account, ev,
                          "price-worded objection email from the buyer while the deal is in Quote; the generator recorded "
                          "a pushback here, so changing the quoted amount versus holding it is rated by the hidden rule")


def _price_early(world: World, deal: str, account: str, events: Sequence[Mapping[str, Any]], cutoff: datetime,
                 windows: Mapping[str, datetime]) -> Iterable[Situation]:
    for rec in world.results[deal].records:
        d = dict(rec.data)
        if rec.kind != "reply" or d["reply_kind"] != "objection":
            continue
        lo, hi = _window(world, deal, rec.t)
        for ev in _inbound(events, lo, hi):
            when = parse_time(ev["occurred_at"])
            if PRICE_TEXT.search(ev["payload"]["body_text"]) and stage_at(events, when) in (None, "Discovery", "Qualification") \
                    and _inside(when, cutoff, "price_pushback", windows):
                yield _mk("price_pushback", "exception", deal, account, ev,
                          "price-worded objection email BEFORE Quote: the same words as a pushback but no quote exists to "
                          "re-price, so the learned pushback rule's precondition (stage Quote) fails")


FOLLOW_UP_DAYS = 14.0


def _base_inbound(world: World, deal: str) -> list[dict[str, Any]]:
    """The deal's REAL customer emails (WP31 base data), as the minimal event the pack builder finds by id."""
    return [{"source_system": "email", "source_object_id": e.id, "source_event_key": "received",
             "occurred_at": e.at.astimezone(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ"), "payload": {"direction": "inbound"}}
            for e in world.by_deal[deal].emails if not e.outbound]


def _first_reply_after(world: World, deal: str, events: Sequence[Mapping[str, Any]], stage: Mapping[str, Any],
                       stage_name: str) -> Mapping[str, Any] | None:
    """The first inbound customer email (rendered or real) within FOLLOW_UP_DAYS after the stage event while the deal is
    still in that stage: the agent is triggered by a customer reply (eligible_customer_replied), never by a stage change."""
    start = parse_time(stage["occurred_at"])
    pool = list(_inbound(events, start, start + timedelta(days=FOLLOW_UP_DAYS)))
    pool += [e for e in _base_inbound(world, deal) if start < parse_time(e["occurred_at"]) <= start + timedelta(days=FOLLOW_UP_DAYS)]
    for ev in sorted(pool, key=lambda e: (parse_time(e["occurred_at"]), e["source_object_id"])):
        if stage_at(events, parse_time(ev["occurred_at"])) == stage_name:
            return ev
    return None


def _second_quote(world: World, deal: str, account: str, events: Sequence[Mapping[str, Any]], cutoff: datetime,
                  windows: Mapping[str, datetime]) -> Iterable[Situation]:
    for rec in world.results[deal].records:
        if rec.kind != "quote_sent":
            continue
        lo, hi = _window(world, deal, rec.t)
        # the generator records quote_sent at Quote entry; the customer's next email in Quote is the trigger
        stage = _stage_event(events, "Quote", lo - timedelta(days=1), hi)
        reply = None if stage is None else _first_reply_after(world, deal, events, stage, "Quote")
        if reply is not None and _inside(parse_time(reply["occurred_at"]), cutoff, "second_quote", windows):
            yield _mk("second_quote", "discriminating", deal, account, reply,
                      "the deal is in Quote with another quote at the account open within 30 days and the customer has just "
                      "written; sending a separate quote versus folding it into the open one is rated by the hidden rule")


def _negotiation_quote(world: World, deal: str, account: str, events: Sequence[Mapping[str, Any]], cutoff: datetime,
                       windows: Mapping[str, datetime]) -> Iterable[Situation]:
    res, bd = world.results[deal], world.by_deal[deal]
    start = bd.start.astimezone(timezone.utc)
    for rec in res.records:
        d = dict(rec.data)
        if rec.kind != "stage" or d["stage"] != "Negotiation":
            continue
        t = rec.t
        if not any(t - OPEN_QUOTE_DAYS < q < t for q in bd.deal.account_quote_days):
            continue
        stage = _stage_event(events, "Negotiation", start + timedelta(days=t) - timedelta(days=1), start + timedelta(days=t + 1.0))
        reply = None if stage is None else _first_reply_after(world, deal, events, stage, "Negotiation")
        if reply is not None and _inside(parse_time(reply["occurred_at"]), cutoff, "second_quote", windows):
            yield _mk("second_quote", "exception", deal, account, reply,
                      "the deal is in Negotiation with another quote at the account open within 30 days and the customer has "
                      "just written: the same account facts as a second quote, but the stage is past Quote, so the learned "
                      "rule's precondition fails")


def order(items: Sequence[Situation], seed: int = SEED) -> list[Situation]:
    """A fixed pseudo-random order that depends only on the seed and the id."""
    return sorted(items, key=lambda s: hashlib.sha256(f"{seed}|{s.id}".encode()).hexdigest())


def pick(items: Sequence[Situation], plan: Mapping[tuple[str, str], int], seed: int = SEED) -> list[Situation]:
    """Per (kind, point) the first n of the seeded order; one situation per deal and kind."""
    chosen: list[Situation] = []
    seen: set[tuple[str, str]] = set()
    for key, n in plan.items():
        taken = 0
        for s in order([x for x in items if (x.kind, x.point) == key], seed):
            if taken == n:
                break
            if (s.kind, s.deal_id) in seen:
                continue
            seen.add((s.kind, s.deal_id))
            chosen.append(s)
            taken += 1
    return chosen


def windows_from_learning(learning: Mapping[str, Any]) -> dict[str, datetime]:
    return {k["decision_point"]: fresh_until(k) for k in learning["knowledge"]}
