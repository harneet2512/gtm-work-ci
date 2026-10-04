"""Deterministic access to the frozen synthetic world: the base export and the kernel, as DealViews.

Needs the git-ignored CRMArena export and the frozen rule file: it runs where the data exists (the 'prepare' step).
CI never calls it; CI replays the committed pack and learning evidence.
"""
from __future__ import annotations

import hashlib
import json
from dataclasses import dataclass
from datetime import timezone
from pathlib import Path

from bench.synthetic import rules as R
from bench.synthetic.base import Base, BaseDeal, load
from bench.synthetic.forward import DealResult
from bench.synthetic.pipeline import simulate_base

from .observe import DealView, Rec

ROOT = Path(__file__).resolve().parents[2]
SPLIT_PATH = ROOT / "bench" / "data" / "deal_split.json"


@dataclass(frozen=True)
class World:
    base: Base
    results: dict[str, DealResult]
    by_deal: dict[str, BaseDeal]
    rule_set: R.RuleSet
    split: dict


def sha256_file(path: Path) -> str:
    return hashlib.sha256(path.read_bytes()).hexdigest()


def load_world(export: Path, split_path: Path = SPLIT_PATH) -> World:
    """The base export, the frozen rule file's kernel run (seed = the frozen generation seed), the deal split."""
    rule_set, doc = R.load_rules(), R.load_doc()
    seed = rule_set.generation_seed
    base = load(export, doc["process"], seed, float(rule_set.outcome["account_effect_sd"]))
    results = {r.deal_id: r for r in simulate_base(base, rule_set, doc["process"], seed)}
    split = json.loads(split_path.read_text(encoding="utf-8"))
    return World(base, results, {b.deal.deal_id: b for b in base.deals}, rule_set, split)


def view_of(bd: BaseDeal, res: DealResult) -> DealView:
    """What a learner may know of a deal: contacts and functions, real seller emails, rendered seller/customer records,
    and the outcome. No rule key and no outcome-dependent record time other than the close."""
    return DealView(
        deal_id=res.deal_id, account_id=bd.deal.account_id, start=bd.start.astimezone(timezone.utc),
        contacts={c.id: c.function for c in bd.deal.contacts},
        base_outbound=tuple((o.t, tuple(o.recipients)) for o in bd.deal.outbound),
        records=tuple(Rec(r.t, r.kind, dict(r.data)) for r in res.records),
        close_t=res.close_t, won=res.won)


def previous_views(world: World) -> list[DealView]:
    prev = set(world.split["previous_deal_ids"])
    return [view_of(world.by_deal[d], world.results[d]) for d in sorted(prev) if d in world.results]
