"""Account-level, seeded train / test / demo split of the CRMArena-Pro B2B org (decision-evals audit, section 5).

    python bench/data/account_split.py --write [--data data/crmarena_b2b]   # (re)build bench/data/account_split.json
    python bench/data/account_split.py --check [--data data/crmarena_b2b]   # rebuild in memory, compare with the file

An account decides its partition, whichever deal a case comes from. The earlier split (`deal_split.json`) cut deals at a
date, so 96 accounts had one deal in the past and another in the present and the same company sat on both sides; this
one cuts accounts. The demo partition (MedTech Advances, EcoLite Innovations) is never in gold, in prompt or rubric
tuning, in knowledge formation or in a backtest: it is what the demo shows.

Eligible accounts are the ones that have at least one previous and one current deal in `deal_split.json` (96 of the
org's 101 accounts). Sorted by id, they are shuffled with `random.Random(SEED)`; the demo accounts leave the pool; of
the rest the first TRAIN_COUNT are train and the others test. The accounts that are not eligible are `unused`: they
have a deal on one side of the cutoff only, so they cannot show knowledge learned earlier being used later.

Reading the snapshot needs the git-ignored export (bench/data/crmarena_export.py); `readers` (below) need only the JSON.
"""
from __future__ import annotations

import argparse
import hashlib
import json
import random
import sys
from pathlib import Path
from typing import Any

ROOT = Path(__file__).resolve().parents[2]
SPLIT_PATH = ROOT / "bench" / "data" / "account_split.json"
DEAL_SPLIT_PATH = ROOT / "bench" / "data" / "deal_split.json"
DEFAULT_DATA = ROOT / "data" / "crmarena_b2b"

SEED = 20261005
TRAIN_COUNT = 56
DEMO_ACCOUNTS = (
    {"account_id": "001Wt00000PHVyfIAH", "name": "MedTech Advances", "role": "demo case 1 (first Play: the correction is made)"},
    {"account_id": "001Wt00000PFsmaIAD", "name": "EcoLite Innovations", "role": "demo case 2 (second Play: the knowledge is used)"},
)
GOLD_SOURCE = {"kind": "model_reference", "model": "openai/gpt-5.6-sol-pro"}
EXCLUDED_FROM = ["gold", "prompt_and_rubric_tuning", "knowledge_formation", "backtests", "agreement_numbers"]


def sha256(path: Path) -> str:
    return hashlib.sha256(path.read_bytes()).hexdigest()


def account_of_deals(data: Path) -> dict[str, str]:
    """Opportunity Id -> Account Id from the snapshot."""
    return {o["Id"]: o["AccountId"] for o in json.loads((data / "Opportunity.json").read_text(encoding="utf-8"))}


def build(data: Path = DEFAULT_DATA) -> dict[str, Any]:
    deals = json.loads(DEAL_SPLIT_PATH.read_text(encoding="utf-8"))
    owner = account_of_deals(data)
    all_accounts = sorted({a["Id"] for a in json.loads((data / "Account.json").read_text(encoding="utf-8"))})
    previous = {owner[d] for d in deals["previous_deal_ids"]}
    current = {owner[d] for d in deals["current_deal_ids"]}
    eligible = sorted(previous & current)
    demo = {a["account_id"] for a in DEMO_ACCOUNTS}
    if not demo <= set(eligible):
        raise ValueError("a demo account has no previous and current deal: " + ", ".join(sorted(demo - set(eligible))))
    pool = [a for a in eligible if a not in demo]
    random.Random(SEED).shuffle(pool)
    train, test = sorted(pool[:TRAIN_COUNT]), sorted(pool[TRAIN_COUNT:])
    unused = sorted(set(all_accounts) - set(eligible))
    demo_deals = sorted(d for d, a in owner.items() if a in demo)
    return {
        "split_version": 1,
        "unit": "account",
        "seed": SEED,
        "description": "Account-level split of the CRMArena-Pro B2B org. An account decides its partition, whichever deal a "
                       "case comes from. The demo partition is never in gold, in tuning, in knowledge formation or in a "
                       "backtest. Gold reference answers are written by a different, stronger model, blind to the judge "
                       "under test; they are not human labels.",
        "gold_source": GOLD_SOURCE,
        "rule": "eligible = accounts with at least one previous and one current deal in deal_split.json; sort by account id; "
                f"random.Random({SEED}).shuffle; remove the demo accounts; first {TRAIN_COUNT} are train, the rest test; "
                "every other account is unused",
        "source": {
            "dataset": "CRMArena-Pro B2B Salesforce org (Salesforce AI Research)",
            "licence": "CC BY-NC 4.0 (non-commercial use only)",
            "account_json_sha256": sha256(data / "Account.json"),
            "opportunity_json_sha256": sha256(data / "Opportunity.json"),
            "deal_split": "bench/data/deal_split.json",
            "deal_split_sha256": sha256(DEAL_SPLIT_PATH),
        },
        "counts": {"accounts": len(all_accounts), "eligible": len(eligible), "demo": len(demo), "train": len(train),
                   "test": len(test), "unused": len(unused)},
        "partitions": {
            "demo": {"accounts": [dict(a) for a in DEMO_ACCOUNTS], "deal_ids": demo_deals, "excluded_from": EXCLUDED_FROM},
            "train": {"accounts": train, "used_for": ["prompt_and_rubric_tuning", "knowledge_formation", "backtests"]},
            "test": {"accounts": test, "used_for": ["gold", "agreement_numbers"], "never_seen_by": ["prompt_authors"]},
            "unused": {"accounts": unused, "reason": "a deal on one side of the cutoff only: no earlier episode to learn from"},
        },
    }


def render(doc: dict[str, Any]) -> str:
    return json.dumps(doc, indent=1, ensure_ascii=False) + "\n"


# ---- readers: they need only the committed JSON -------------------------------------------------------------------
def load(path: Path = SPLIT_PATH) -> dict[str, Any]:
    return json.loads(path.read_text(encoding="utf-8"))


def partition_of(account_id: str, split: dict[str, Any] | None = None) -> str | None:
    """demo, train, test or unused; None for an account the split does not know (it is then not gold)."""
    parts = (split or load())["partitions"]
    if account_id in {a["account_id"] for a in parts["demo"]["accounts"]}:
        return "demo"
    for name in ("train", "test", "unused"):
        if account_id in parts[name]["accounts"]:
            return name
    return None


def demo_deal_ids(split: dict[str, Any] | None = None) -> frozenset[str]:
    return frozenset((split or load())["partitions"]["demo"]["deal_ids"])


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    mode = parser.add_mutually_exclusive_group(required=True)
    mode.add_argument("--write", action="store_true")
    mode.add_argument("--check", action="store_true")
    parser.add_argument("--data", type=Path, default=DEFAULT_DATA)
    args = parser.parse_args(argv)
    text = render(build(args.data))
    if args.write:
        SPLIT_PATH.write_text(text, encoding="utf-8", newline="\n")
        print(f"wrote {SPLIT_PATH.relative_to(ROOT)}")
        return 0
    same = SPLIT_PATH.read_text(encoding="utf-8") == text
    print("account_split.json matches the rebuild" if same else "MISMATCH: account_split.json differs from the rebuild")
    return 0 if same else 1


if __name__ == "__main__":
    sys.exit(main())
