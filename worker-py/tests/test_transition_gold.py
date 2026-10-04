"""The transition gold (fixtures/gold/transitions/*.json) is well-formed and the authored set is current.

The Go measurement (core-go/internal/transitioneval, `ghostctl transitions-eval`) replays these scenarios through the
detector; this file keeps the gold itself honest: labels use the contract's vocabulary, scenarios cite their source and
every contested one states both readings."""
from __future__ import annotations

import json

import pytest

from test_contracts import CONTRACTS, load_json
from transition_gold import OUT, render
from transition_rules_lib import RULES

GOLD_DIR = OUT.parent
FILES = sorted(GOLD_DIR.glob("*.json"))
STATUSES = set(load_json(CONTRACTS / "schemas" / "common.v1.json")["$defs"]["transitionStatus"]["enum"])
STATES = set(load_json(CONTRACTS / "schemas" / "common.v1.json")["$defs"]["relationshipState"]["enum"]) | {"unknown"}
REQUIRED_FACTS = {k for r in RULES["transitions"] for k in r["confirmed"]["requires_all"]}
CONTRADICTION_KEYS = {c["key"] for r in RULES["transitions"] for c in r["contradictions"]}


def scenarios() -> list[tuple[str, dict]]:
    return [(f.name, s) for f in FILES for s in json.loads(f.read_text(encoding="utf-8"))["scenarios"]]


def test_the_authored_gold_is_current() -> None:
    assert OUT.read_text(encoding="utf-8") == render(), "run: python worker-py/tests/transition_gold.py --write"


def test_scenario_ids_are_unique_across_files() -> None:
    ids = [s["id"] for _, s in scenarios()]
    assert len(ids) == len(set(ids)) and len(ids) >= 40


@pytest.mark.parametrize(("file", "scenario"), scenarios(), ids=lambda v: v if isinstance(v, str) else v["id"])
def test_every_scenario_is_labelled_with_the_contract_vocabulary(file: str, scenario: dict) -> None:
    assert scenario["source"] and scenario["rationale"], "a label without a cited source is not gold"
    assert scenario["initial_state"] in STATES
    last_now = ""
    for step in scenario["steps"]:
        gold, inp = step["gold"], step["input"]
        assert gold["status"] is None or gold["status"] in STATUSES
        assert gold["relationship_state_after"] in STATES
        assert inp["open_transition"] is None, "the harness chains the open transition itself"
        assert inp["now"] >= last_now, "steps must not go back in time"
        last_now = inp["now"]
        if gold["status"] is None:
            continue
        if gold["status"] == "UNRESOLVED":
            assert gold.get("to_state") is None, "an UNRESOLVED transition has no target"
        else:
            assert gold["to_state"] in STATES - {"unknown"}
        assert set(gold.get("supporting_required", [])) <= REQUIRED_FACTS
        assert set(gold.get("contradicting", [])) <= CONTRADICTION_KEYS
        if gold["status"] != "UNRESOLVED" and file == OUT.name:
            assert "supporting_required" in gold, "the authored gold labels required facts on every non-UNRESOLVED step"


def test_contested_scenarios_state_both_readings() -> None:
    contested = [s for _, s in scenarios() if s["slice"] == "contested" or s.get("contested")]
    assert contested, "contested cases are reported apart, not hidden"
    for s in contested:
        readings = s["contested"]
        assert len(readings) >= 2 and all(isinstance(v, str) and v for v in readings.values()), s["id"]


def test_the_gold_has_no_company_or_person_names() -> None:
    text = " ".join(f.read_text(encoding="utf-8") for f in FILES).lower()
    for name in ("acme", "northstar", "beta corp", "priya", "marco", "dana kim"):
        assert name not in text, f"invented name {name!r} in the transition gold"


def test_the_authored_gold_covers_the_slices_and_every_status() -> None:
    authored = json.loads(OUT.read_text(encoding="utf-8"))["scenarios"]
    assert {"pitch_bundle", "boundary", "probe", "reorg_entry", "contradiction", "lifecycle", "contested"} <= {s["slice"] for s in authored}
    labelled = {st["gold"]["status"] for s in authored for st in s["steps"]}
    assert labelled == {None, "CONFIRMED", "CANDIDATE", "UNRESOLVED", "REJECTED"}


def test_the_blind_gold_is_reproducible_from_its_script() -> None:
    import subprocess
    import sys

    before = (GOLD_DIR / "blind.json").read_bytes()
    subprocess.run([sys.executable, str(GOLD_DIR.parents[2] / "worker-py" / "tests" / "transition_gold_blind.py")], check=True, capture_output=True)
    assert (GOLD_DIR / "blind.json").read_bytes() == before
