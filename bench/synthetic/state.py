"""Shared state and records of the forward kernel (forward.py, seller.py)."""
from __future__ import annotations

import heapq
import math
from collections.abc import Mapping
from dataclasses import dataclass, field
from typing import Any

from . import plan as P
from .rules import RuleSet, planted_log_odds

STAGES = ("Discovery", "Qualification", "Quote", "Negotiation")
G_PUSH_UNSTABLE = "syn1_push_while_owner_unstable"
G_GONE, G_WITH, G_WITHOUT = "syn1_champion_gone_unreplaced", "syn1_entrant_with_champion", "syn1_entrant_without_champion"
G_HANDOVER, G_QBEB = "syn1_written_handover_exempts", "syn1_quote_before_economic_buyer"
G_THREE, G_PAUSE = "syn1_three_engaged_before_quote", "syn1_requested_pause_ignored"
# Hidden rules: SELLER actions only (user decision 2026-10-02), so the A/B/C test scores the agent's choice.
H_OPS = "syn1_ops_contact_before_quote"
H_REPRICE = "syn1_reprice_after_quote_pushback"
H_CC = "syn1_cc_colleague_on_technical_reply"
H_SECOND_QUOTE = "syn1_second_quote_within_30d"
N_EXEC, N_SIBLING = "syn1_null_executive_outside_group", "syn1_null_sibling_opened_within_30d"


@dataclass(frozen=True)
class Record:
    t: float
    kind: str
    data: tuple[tuple[str, Any], ...]


@dataclass(frozen=True)
class DealResult:
    deal_id: str
    records: tuple[Record, ...]
    features: tuple[tuple[str, float], ...]  # (rule key, first time it held)
    won: bool
    close_t: float
    dormant: bool  # the deal went quiet (exit hazard) and closes lost at its CloseDate
    final_log_odds: float  # planted log-odds at close, without the account offset
    quote_t: float | None  # when the deal entered Quote (siblings read it: syn1_second_quote_within_30d)
    outcome_source: str  # 'synthetic', or 'real' when a visible real terminal event fixes the outcome (Q2)

    def pre_close(self) -> tuple[Record, ...]:
        return tuple(r for r in self.records if r.t < self.close_t and r.kind != "close")


def sigmoid(x: float) -> float:
    return 1.0 / (1.0 + math.exp(-x))


def logit(p: float) -> float:
    return math.log(p / (1.0 - p))


@dataclass
class State:
    """Mutable simulation state, private to one simulate() call."""
    deal: P.DealInput
    plan: P.Plan
    rules: RuleSet
    proc: Mapping[str, float]
    champion: str | None
    policy: Any  # the deal's 'policy' stream: every seller choice draws from it
    stage: int = 0
    stage_t: dict[int, float] = field(default_factory=dict)
    features: dict[str, float] = field(default_factory=dict)
    records: list[Record] = field(default_factory=list)
    heap: list[tuple[float, int, str, tuple[Any, ...]]] = field(default_factory=list)
    seq: int = 0
    engaged: set[str] = field(default_factory=set)
    eb_engaged: bool = False
    tech_answered: bool = False
    pause_until: float = -1.0
    need: bool = False
    value: bool = False
    entrant_after_reorg: bool = False
    owner_t: float | None = None
    acting_until: float = -1.0
    replaced: bool = False
    dormant: bool = False

    def push(self, t: float, kind: str, *args: Any) -> None:
        self.seq += 1
        heapq.heappush(self.heap, (t, self.seq, kind, args))

    def hold(self, key: str, t: float) -> None:
        self.features.setdefault(key, t)

    def record(self, t: float, kind: str, **data: Any) -> None:
        self.records.append(Record(t, kind, tuple(sorted(data.items()))))

    def health(self) -> float:
        return planted_log_odds(self.rules, self.features) - float(self.rules.outcome["base_log_odds"])

    def contact(self, cid: str | None) -> P.Contact | None:
        return next((c for c in self.deal.contacts + self.plan.new_contacts if c.id == cid), None)

    def real_email_near(self, t: float, days: float) -> bool:
        return any(abs(i.t - t) <= days for i in self.deal.inbound)

    def unstable(self, t: float) -> bool:
        if self.plan.reorg_t is None or not self.plan.owner_change or t <= self.plan.reorg_t:
            return False
        if self.owner_t is None:
            return True
        return t < self.owner_t + self.proc["owner_stable_min_days"] or t < self.acting_until
