"""The hard block on the experiment's knowledge store (bench/synthetic/experiment.py), run before every arm.

`ghostctl abc-arms` exports the store it learned (knowledge.v1 objects) and calls this module as a subprocess; a
non-zero exit aborts the run (a hard block, not a warning). The checks:
  - the store was empty before previous-deal learning (the Go side asserts it and says so in the export);
  - every item validates against knowledge.v1, cites only previous-deal episodes, is not seeded or manual, a
    human_delta item comes from a recorded decision of a previous deal, and no item copies seeded wording;
  - no item's text contains a hidden-rule key, statement or 8-word run of one (bench/synthetic/leakcheck).
Returns the counts the report's `learning.guard` records.
"""
from __future__ import annotations

import json
import sys
from collections.abc import Mapping, Sequence
from pathlib import Path
from typing import Any

from bench.synthetic import experiment as X
from bench.synthetic import leakcheck, rules


def check_store(items: Sequence[Mapping[str, Any]], learning: Mapping[str, Any], *, empty_before_learning: bool) -> dict[str, Any]:
    """Raise X.ExperimentLeak unless the store is admissible; else the guard record of the report."""
    if not empty_before_learning:
        raise X.ExperimentLeak("the knowledge store was not empty before previous-deal learning")
    episodes = frozenset(learning["previous_episode_ids"])
    # the seller's recorded choice in each previous deal is the human decision the knowledge is learned from
    run = X.LearningRun(previous_episode_ids=episodes, human_decided_episode_ids=episodes)
    X.assert_experiment_store(items, run)
    needles = leakcheck.markers(rules.load_rules())
    found = 0
    for item in items:
        views = [f" {leakcheck.normalize(s)} " for s in leakcheck.strings(item)]
        found += sum(1 for m in needles if any(f" {m} " in v for v in views))
    if found:
        raise X.ExperimentLeak(f"hidden-rule text found in the knowledge store ({found} markers)")
    return {"empty_before_learning": True, "admitted": len(items), "hidden_rule_text_found": 0,
            "seeded_or_manual_found": 0, "hard_block_passed": True}


def main(argv: Sequence[str]) -> int:
    if len(argv) != 2:
        print("usage: python -m bench.uplift.guard <store.json> <learning.json>", file=sys.stderr)
        return 2
    store = json.loads(Path(argv[0]).read_text(encoding="utf-8"))
    learning = json.loads(Path(argv[1]).read_text(encoding="utf-8"))
    try:
        record = check_store(store["items"], learning, empty_before_learning=bool(store["empty_before_learning"]))
    except X.ExperimentLeak as err:
        print(f"experiment guard: {err}", file=sys.stderr)
        return 1
    print(json.dumps(record))
    return 0


if __name__ == "__main__":
    raise SystemExit(main(sys.argv[1:]))
