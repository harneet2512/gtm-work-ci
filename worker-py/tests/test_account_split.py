"""WP-A item 6, gold hygiene: the account-level split, the demo partition kept out of gold, and the invented gold retired.
The split is bench/data/account_split.json (builder and readers: bench/data/account_split.py)."""
from __future__ import annotations

import json
import sys
from pathlib import Path

import pytest
from test_contracts import load_json

ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT / "bench" / "data"))
import account_split as split  # noqa: E402

MEDTECH, ECOLITE = "001Wt00000PHVyfIAH", "001Wt00000PFsmaIAD"
SPLIT = split.load()
PARTS = SPLIT["partitions"]
SNAPSHOT = split.DEFAULT_DATA


def members(name: str) -> list[str]:
    return [a["account_id"] for a in PARTS[name]["accounts"]] if name == "demo" else PARTS[name]["accounts"]


def test_the_demo_partition_is_medtech_and_ecolite_and_nothing_else() -> None:
    assert sorted(members("demo")) == sorted([MEDTECH, ECOLITE])
    assert {a["name"] for a in PARTS["demo"]["accounts"]} == {"MedTech Advances", "EcoLite Innovations"}
    assert {"gold", "prompt_and_rubric_tuning", "knowledge_formation", "backtests"} <= set(PARTS["demo"]["excluded_from"])


def test_every_account_is_in_exactly_one_partition_and_the_counts_add_up() -> None:
    seen: dict[str, str] = {}
    for name in ("demo", "train", "test", "unused"):
        for account in members(name):
            assert account not in seen, f"{account} is in {seen.get(account)} and {name}"
            seen[account] = name
    c = SPLIT["counts"]
    assert len(seen) == c["accounts"] == 101
    assert (c["demo"], c["train"], c["test"], c["unused"]) == (2, 56, 38, 5) and c["eligible"] == 96
    assert c["demo"] + c["train"] + c["test"] == c["eligible"]


def test_train_and_test_never_contain_a_demo_account() -> None:
    assert not {MEDTECH, ECOLITE} & (set(members("train")) | set(members("test")))


def test_the_split_is_seeded_and_unit_is_the_account() -> None:
    assert SPLIT["unit"] == "account" and isinstance(SPLIT["seed"], int) and str(SPLIT["seed"]) in SPLIT["rule"]
    assert members("train") == sorted(members("train")) and members("test") == sorted(members("test"))


def test_gold_reference_answers_come_from_a_stronger_model_not_humans() -> None:
    assert SPLIT["gold_source"] == {"kind": "model_reference", "model": "openai/gpt-5.6-sol-pro"}
    text = json.dumps(SPLIT).lower()
    assert "not human labels" in text
    assert not [k for k in SPLIT if "human" in k.lower() or "trial" in k.lower()], "no invented human-label or trial fields"


def test_partition_of_reads_the_json_and_does_not_invent() -> None:
    assert split.partition_of(MEDTECH) == split.partition_of(ECOLITE) == "demo"
    assert split.partition_of(members("train")[0]) == "train" and split.partition_of(members("test")[0]) == "test"
    assert split.partition_of(members("unused")[0]) == "unused"
    assert split.partition_of("001NotAnAccount") is None
    assert split.demo_deal_ids() == frozenset(PARTS["demo"]["deal_ids"]) and "006Wt000007BDAnIAO" in split.demo_deal_ids()


def test_no_crmarena_gold_case_comes_from_a_demo_account_or_deal() -> None:
    demo_ids = {MEDTECH, ECOLITE, *split.demo_deal_ids()}
    cases = sorted((ROOT / "fixtures" / "evals" / "crmarena" / "cases").glob("*/*.json"))
    assert len(cases) == 48
    for path in cases:
        text = path.read_text(encoding="utf-8")
        leaked = sorted(i for i in demo_ids if i in text)
        assert not leaked, f"{path.name} is cut from a demo record {leaked}"


def test_the_generator_leaves_the_demo_deal_out_of_its_specs() -> None:
    sys.path.insert(0, str(ROOT / "bench" / "evals"))
    from crmarena_gold.all_specs import EXCLUDED_IDS, SPECS
    assert {s["deal"] for s in SPECS}.isdisjoint(split.demo_deal_ids())
    assert len(SPECS) == 48 and len(EXCLUDED_IDS) == 7


@pytest.mark.skipif(not (SNAPSHOT / "Opportunity.json").exists(), reason="the git-ignored CRMArena snapshot is not on this machine")
def test_the_committed_split_is_what_the_seed_rebuilds() -> None:
    assert split.render(split.build(SNAPSHOT)) == split.SPLIT_PATH.read_text(encoding="utf-8")


def test_the_invented_gold_is_marked_retired() -> None:
    manifest = load_json(ROOT / "fixtures" / "evals" / "cases" / "RETIRED.json")
    assert manifest["retired"] == "invented"
    assert {"Acme Corp", "Beta Inc", "Northstar Health"} <= set(manifest["accounts"])
    assert manifest["cases"] == 87
