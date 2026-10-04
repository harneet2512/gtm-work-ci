"""bench/uplift/guard: the hard block before every arm admits only knowledge learned from previous deals."""
from __future__ import annotations

import copy
import json
import subprocess
import sys
from pathlib import Path

import pytest

ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT))

from bench.synthetic import experiment as X  # noqa: E402
from bench.synthetic import rules  # noqa: E402
from bench.uplift import guard as G  # noqa: E402

EP1, EP2 = "5a000000-0000-4000-8000-000000000001", "5a000000-0000-4000-8000-000000000002"
LEARNING = {"previous_episode_ids": [EP1, EP2]}
K17 = json.loads((ROOT / "contracts" / "examples" / "knowledge.example.json").read_text(encoding="utf-8"))


def learned(**over: object) -> dict:
    item = copy.deepcopy(K17)
    item.update(id="22222222-0000-4000-8000-000000000001", key="K202", title="Hold the quoted amount after a price objection",
                guidance={"summary": "Learned from 198 previous deals at this decision point.",
                          "do": ["Keep the quoted amount and make the case for the value."], "dont": []},
                provenance={"created_from": "human_delta", "source_decision_episode_id": EP1},
                supporting_decision_episode_ids=[EP1, EP2], status="provisional")
    item.update(over)
    return item


def test_a_learned_item_from_previous_deals_is_admitted() -> None:
    record = G.check_store([learned()], LEARNING, empty_before_learning=True)
    assert record == {"empty_before_learning": True, "admitted": 1, "hidden_rule_text_found": 0,
                      "seeded_or_manual_found": 0, "hard_block_passed": True}


def test_a_store_that_was_not_empty_before_learning_is_refused() -> None:
    with pytest.raises(X.ExperimentLeak):
        G.check_store([], LEARNING, empty_before_learning=False)


def test_seeded_k17_is_refused() -> None:
    with pytest.raises(X.ExperimentLeak):
        G.check_store([K17], LEARNING, empty_before_learning=True)


def test_manual_and_seed_history_origins_are_refused() -> None:
    for origin in ("manual", "seed_history"):
        with pytest.raises(X.ExperimentLeak):
            G.check_store([learned(provenance={"created_from": origin, "source_decision_episode_id": None})], LEARNING,
                          empty_before_learning=True)


def test_an_episode_outside_the_previous_deals_is_refused() -> None:
    with pytest.raises(X.ExperimentLeak):
        G.check_store([learned(supporting_decision_episode_ids=[EP1, "5a000000-0000-4000-8000-0000000000ff"])], LEARNING,
                      empty_before_learning=True)


def test_hidden_rule_text_in_an_item_is_refused() -> None:
    statement = rules.load_rules().rules[8].statement  # a hidden rule's statement
    leaky = learned(guidance={"summary": statement, "do": ["x"], "dont": []})
    with pytest.raises(X.ExperimentLeak):
        G.check_store([leaky], LEARNING, empty_before_learning=True)
    keyed = learned(title="see syn1_reprice_after_quote_pushback")
    with pytest.raises(X.ExperimentLeak):
        G.check_store([keyed], LEARNING, empty_before_learning=True)


def test_the_cli_exits_nonzero_on_a_leak_and_zero_otherwise(tmp_path: Path) -> None:
    learning = tmp_path / "learning.json"
    learning.write_text(json.dumps(LEARNING), encoding="utf-8")
    def run(items: list, empty: bool = True) -> subprocess.CompletedProcess:
        store = tmp_path / "store.json"
        store.write_text(json.dumps({"empty_before_learning": empty, "items": items}), encoding="utf-8")
        return subprocess.run([sys.executable, "-m", "bench.uplift.guard", str(store), str(learning)], cwd=ROOT,
                              capture_output=True, text=True, check=False)
    ok, bad = run([learned()]), run([K17])
    assert ok.returncode == 0 and json.loads(ok.stdout)["admitted"] == 1
    assert bad.returncode == 1 and "experiment guard" in bad.stderr
    assert run([], empty=False).returncode == 1
    assert subprocess.run([sys.executable, "-m", "bench.uplift.guard"], cwd=ROOT, capture_output=True, check=False).returncode == 2
