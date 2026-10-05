"""Seed-history counts behind the CRMArena gold knowledge (HAR-114 gold v2).

    python bench/evals/crmarena_gold/knowledge_stats.py            # print the counts
    python bench/evals/crmarena_gold/knowledge_stats.py --check    # fail when knowledge.py disagrees

A knowledge item's `counts` come from PREVIOUS deals only (bench/data/deal_split.json: deals whose last event is before
the 2023-11-01 cutoff): `decisions` = previous deals with at least one customer email matching the item's pattern,
`outcomes_advanced` = those that have a signed contract (a weak, undated-by-source label; no causal claim),
`positive_reactions` and `negative_reactions` are 0 because CRMArena records no reactions. The patterns are the text
signals the item is about, not a model of why a deal advanced.
"""
from __future__ import annotations

import re
import sys
from pathlib import Path

if __package__ in (None, ""):  # run as a script: python bench/evals/crmarena_gold/<module>.py
    sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
    __package__ = "crmarena_gold"

from .source import Snapshot

PATTERNS = {
    "K101": re.compile(r"additional costs?|hidden (?:fees|costs)|breakdown of the pricing|total cost of ownership|whether they come at any", re.I),
    "K102": re.compile(r"legal (?:team|department)[^.]{0,80}(?:review|check|finali)", re.I),
    "K103": re.compile(r"(?:includ\w*|involv\w*|invit\w*|bring in|ensure)[^.]{0,40}(?:our|my) (?:CTO|CFO|lead engineer|technical team|legal team|security team)", re.I),
    "K104": re.compile(r"get back to you (?:by|early|next|shortly)|(?:by|before) the end of (?:this|the) week|early next week", re.I),
}


def stats(snap: Snapshot) -> dict[str, dict[str, int]]:
    contracts = {o["Id"] for o in snap.opportunities.values() if o.get("ContractId__c")}
    out = {}
    for key, pattern in PATTERNS.items():
        deals = {d for d in snap.previous_deals()
                 if any(e.kind == "email" and e.inbound and pattern.search(e.body) for e in snap.deal_events(d))}
        out[key] = {"decisions": len(deals), "outcomes_advanced": len(deals & contracts)}
    out["_base"] = {"previous_deals": len(snap.previous_deals()),
                    "with_contract": len(set(snap.previous_deals()) & contracts)}
    return out


def main(argv: list[str]) -> int:
    got = stats(Snapshot())
    if "--check" in argv:
        from .knowledge import KNOWLEDGE
        bad = [k for k, v in KNOWLEDGE.items()
               if (v["counts"]["decisions"], v["counts"]["outcomes_advanced"]) != (got[v["key"]]["decisions"], got[v["key"]]["outcomes_advanced"])]
        print("knowledge counts differ from the snapshot:", bad) if bad else print("knowledge counts match the snapshot")
        return 1 if bad else 0
    for key, value in got.items():
        print(key, value)
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
