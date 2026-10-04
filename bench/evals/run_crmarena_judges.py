"""Run the WP18 judge benchmark over the CRMArena gold in REPLAY mode (HAR-114 gold v2, part C).

    python bench/evals/run_crmarena_judges.py --dry-run          # every case loads, routes and builds its requests; no model
    python bench/evals/run_crmarena_judges.py                    # replay: cassettes only, never the network
    python bench/evals/run_crmarena_judges.py --selection routed # what the section-7 router would run

This is a thin wrapper around bench/evals/run_judges.py (WP18, HAR-116): it points the same benchmark at
fixtures/evals/crmarena/cases and at its own cassette folder worker-py/cassettes/judges-crmarena/trial-<n>/. It has no
`--mode`: there is no way to make a live call from here. Recording the live run is the lead's job; until the cassettes
exist, a replay reports every judgment as a missing cassette (that is the expected state, not a failure).

The labels being scored are model labels (single author, two passes), not human labels; the report says so.
Needs the WP18 judges (worker-py/ghost_worker/judges and bench/evals/run_judges.py); on a tree without them it exits 2.
"""
from __future__ import annotations

import argparse
import logging
import sys
from collections.abc import Sequence
from datetime import datetime, timezone
from pathlib import Path
from typing import Any

HERE = Path(__file__).resolve().parent
ROOT = HERE.parents[1]
CASES_DIR = ROOT / "fixtures" / "evals" / "crmarena" / "cases"
CASSETTES = ROOT / "worker-py" / "cassettes" / "judges-crmarena"
REPORTS = ROOT / "bench" / "reports"
PROVENANCE = ("CRMArena gold v2: real CRMArena-Pro B2B deals (CC BY-NC 4.0, Salesforce AI Research), authored candidates, "
              "model labels from two passes by one author, NOT human labels")
NO_JUDGES = 2


class JudgesMissing(RuntimeError):
    """The WP18 judge benchmark is not part of this tree."""


def load_benchmark() -> Any:
    sys.path.insert(0, str(HERE))
    try:
        import run_judges
    except ImportError as exc:
        raise JudgesMissing(f"the WP18 judge benchmark (bench/evals/run_judges.py) is not in this tree: {exc}") from exc
    return run_judges


def dry_run(bench: Any, only: Sequence[str], selection: str) -> dict[str, Any]:
    """Load every case, build every judge context and request; report counts. No provider is created."""
    bench.CASES_DIR = CASES_DIR
    catalog = bench.load_catalog()
    rubrics = dict(bench.load_rubrics(catalog))
    cases = bench.load_cases(only)
    requests = bench.build_requests(cases, catalog, rubrics, selection=selection, trials=1)
    gold = sum(len(bench.semantic_gold(c, catalog)) for c in cases)
    return {"cases": len(cases), "requests": len(requests), "semantic_gold_judgments": gold, "selection": selection}


def replay(bench: Any, *, model: str, trials: int, selection: str, only: Sequence[str], max_workers: int) -> dict[str, Any]:
    bench.CASES_DIR = CASES_DIR
    bench.GOLD_PROVENANCE = PROVENANCE
    logging.disable(logging.ERROR)  # a missing cassette is reported in the table, once per eval type, not once per call
    return bench.run(mode="replay", model=model, trials=trials, selection=selection, only=only,
                     max_workers=max_workers, cassettes=CASSETTES)


def parse_args(argv: Sequence[str] | None) -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__.split("\n\n")[0], allow_abbrev=False)  # '--mode' must not abbreviate '--model'
    parser.add_argument("--dry-run", action="store_true", help="build every request, call nothing, write nothing")
    parser.add_argument("--model", action="append", help="repeatable; names the cassette family (default: the benchmark's default)")
    parser.add_argument("--trials", type=int, default=1)
    parser.add_argument("--selection", choices=["gold", "routed"], default="gold")
    parser.add_argument("--case", action="append", default=[], help="only cases whose id contains this")
    parser.add_argument("--max-workers", type=int, default=8)
    parser.add_argument("--out", type=Path, default=REPORTS)
    parser.add_argument("--tag", default=datetime.now(timezone.utc).strftime("%Y-%m-%d"))
    return parser.parse_args(argv)


def main(argv: Sequence[str] | None = None) -> int:
    args = parse_args(argv)
    try:
        bench = load_benchmark()
    except JudgesMissing as exc:
        sys.stderr.write(f"{exc}\n")
        return NO_JUDGES
    if args.dry_run:
        sys.stdout.write(f"{dry_run(bench, args.case, args.selection)}\n")
        return 0
    for model in args.model or [bench.DEFAULT_MODEL]:
        report = replay(bench, model=model, trials=args.trials, selection=args.selection, only=args.case,
                        max_workers=args.max_workers)
        report = {**report, "title": f"CRMArena gold v2, replay, {model}"}
        stem = f"judges-crmarena-{args.tag}-replay-{bench.model_family(model)}"
        sys.stdout.write(f"{bench.write(report, args.out, args.tag, stem)}\n")
        missing = sum(report.get("missing_cassettes", {}).values())
        if missing:
            sys.stdout.write(f"{missing} judgments have no cassette yet (record live to fill them; none was called here)\n")
    return 0


if __name__ == "__main__":
    sys.exit(main())
