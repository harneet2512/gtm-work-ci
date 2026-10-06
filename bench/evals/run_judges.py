"""Semantic-judge benchmark (WP18, HAR-116): run the judges over the offline gold and report HAR-97 §20 metrics
and the L5 agreement table.

Gold: the 87 cases in fixtures/evals/cases - legacy fixture gold (invented demo accounts; to be replaced by
CRMArena-based gold), single-author. Modes:
  replay    cassettes only (worker-py/cassettes/judges/trial-<n>/), never the network (default)
  record    live calls, every exchange saved as a cassette for later replay
  live      live calls, nothing saved
  baseline  no model: every judge answers "pass" (the floor a real judge must beat)
Live and record read OPENROUTER_API_KEY from the environment (never from a file) and use no fallback model, so a
model comparison is not contaminated:

    python bench/evals/run_judges.py --mode record --model openrouter/deepseek/deepseek-v4-flash --trials 3
"""
from __future__ import annotations

import argparse
import json
import re
import sys
import uuid
from collections import Counter, defaultdict
from collections.abc import Callable, Sequence
from datetime import datetime, timezone
from pathlib import Path
from typing import Any

ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT / "worker-py"))
sys.path.insert(0, str(Path(__file__).resolve().parent))

from judges_md import render  # noqa: E402

from ghost_worker.judges import JudgeContext, JudgeRequest, JudgeRun, judge_many, load_catalog, load_rubrics, route  # noqa: E402
from ghost_worker.judges.baseline import ALWAYS_PASS_MODEL, AlwaysPassProvider  # noqa: E402
from ghost_worker.judges.catalog import EvalCatalog  # noqa: E402
from ghost_worker.judges.meta import GOLD_PROVENANCE, l5_table  # noqa: E402
from ghost_worker.judges.metrics import (Scored, case_level, consistency, group_stats, judgment_stats,  # noqa: E402
                                         slice_agreement)
from ghost_worker.judges.prompt import JUDGE_PROMPT_VERSION  # noqa: E402
from ghost_worker.judges.rubric import Rubric  # noqa: E402
from ghost_worker.llm.factory import build_judge_provider  # noqa: E402
from ghost_worker.llm.fake_provider import FakeProvider, model_family  # noqa: E402
from ghost_worker.llm.provider import LLMProvider  # noqa: E402
from ghost_worker.settings import Settings  # noqa: E402

CASES_DIR = ROOT / "fixtures" / "evals" / "cases"
CASSETTES = ROOT / "worker-py" / "cassettes" / "judges"
REPORTS = ROOT / "bench" / "reports"
RUN_NAMESPACE = uuid.UUID("0b3f6c1e-8d2a-4f57-b0c4-1a9e7d6c5b42")
SLICE_TAGS = ("case_type", "motion", "stage", "champion_status", "economic_buyer", "risk", "candidate", "origin")
DEFAULT_MODEL = Settings(_env_file=None).ghost_model


class RetiredGoldError(RuntimeError):
    """The gold directory carries a RETIRED.json marker: it is not gold, so no agreement number may come from it."""


def retired_notice(directory: Path) -> dict[str, Any] | None:
    marker = directory / "RETIRED.json"
    if not marker.exists():
        return None
    notice = json.loads(marker.read_text(encoding="utf-8"))
    if not notice.get("retired"):
        raise ValueError(f"{marker} does not say what is retired")
    return notice


def load_cases(only: Sequence[str] = (), *, include_retired: bool = False) -> list[dict[str, Any]]:
    """The gold cases. A retired directory (invented accounts) is refused unless include_retired: that is for pipeline
    mechanics (schemas, cassette replay) and never for an agreement number."""
    notice = retired_notice(CASES_DIR)
    if notice and not include_retired:
        raise RetiredGoldError(f"no gold: {CASES_DIR} is retired ({notice['retired']}). {notice.get('reason', '')}")
    cases = [json.loads(p.read_text(encoding="utf-8")) for p in sorted(CASES_DIR.glob("*/*.json"))]
    return [c for c in cases if not only or any(o in c["id"] for o in only)]


def run_id(case: dict[str, Any]) -> str:
    return str(uuid.uuid5(RUN_NAMESPACE, case["id"]))


def semantic_gold(case: dict[str, Any], catalog: EvalCatalog) -> list[dict[str, Any]]:
    return [e for e in case["expected"] if catalog.entry(e["eval_type"]).kind == "semantic"]


def selected_types(case: dict[str, Any], context: JudgeContext, catalog: EvalCatalog,
                   rubrics: dict[str, Rubric], selection: str) -> tuple[str, ...]:
    if selection == "routed":
        return route(context, rubrics, catalog).selected
    return catalog.order({e["eval_type"] for e in semantic_gold(case, catalog)})


def build_requests(cases: list[dict[str, Any]], catalog: EvalCatalog, rubrics: dict[str, Rubric], *,
                   selection: str, trials: int) -> list[JudgeRequest]:
    requests = []
    for case in cases:
        context = JudgeContext.from_eval_case(case)
        for trial in range(1, trials + 1):
            run = JudgeRun(agent_run_id=run_id(case), trial=trial)
            requests += [JudgeRequest(key=case["id"], context=context, eval_type=name, run=run)
                         for name in selected_types(case, context, catalog, rubrics, selection)]
    return requests


def provider_factory(mode: str, model: str, rubrics: dict[str, Rubric],
                     cassettes: Path = CASSETTES) -> Callable[[int], LLMProvider]:
    if mode == "baseline":
        return lambda trial: AlwaysPassProvider(rubrics)
    if mode == "replay":
        return lambda trial: FakeProvider(cassettes / f"trial-{trial}", model_family(model))

    def live(trial: int) -> LLMProvider:
        settings = Settings(_env_file=None, ghost_model=model, ghost_fallback_model=None, ghost_llm_mode=mode,
                            cassette_dir=cassettes / f"trial-{trial}")
        return build_judge_provider(settings)

    return live


def score(cases: list[dict[str, Any]], outcomes: dict[tuple[str, str, int], Any], catalog: EvalCatalog,
          trials: int) -> list[Scored]:
    scored = []
    for case in cases:
        slices = {"case_type": case["case_type"], **{k: v for k, v in case["slice_tags"].items() if k != "extra"}}
        for gold in semantic_gold(case, catalog):
            for trial in range(1, trials + 1):
                scored.append(Scored(case_id=case["id"], eval_type=gold["eval_type"], trial=trial, gold=gold,
                                     slices=slices, should_pass=case["should_pass"],
                                     outcome=outcomes.get((case["id"], gold["eval_type"], trial))))
    return scored


def routing_report(cases: list[dict[str, Any]], catalog: EvalCatalog, rubrics: dict[str, Rubric]) -> dict[str, Any]:
    """How the §7 router would treat the gold: recall of gold judgments, fails and blocks; judges per action."""
    counts, per_action, missed = Counter(), defaultdict(list), []
    for case in cases:
        context = JudgeContext.from_eval_case(case)
        decision = route(context, rubrics, catalog)
        per_action[context.action_type].append(len(decision.selected))
        for gold in semantic_gold(case, catalog):
            hit = gold["eval_type"] in decision.selected
            counts["gold"] += 1
            counts["hit"] += hit
            counts["fail"] += gold["verdict"] == "fail"
            counts["fail_hit"] += hit and gold["verdict"] == "fail"
            counts["block"] += gold["blocking"]
            counts["block_hit"] += hit and gold["blocking"]
            if not hit and gold["verdict"] == "fail":
                missed.append({"case_id": case["id"], "eval_type": gold["eval_type"], "gold_verdict": "fail",
                               "blocking": gold["blocking"], "reason": decision.skipped[gold["eval_type"]]})

    def share(a: str, b: str) -> float | None:
        return round(counts[a] / counts[b], 3) if counts[b] else None

    by_action = {a: {"cases": len(v), "mean_selected": round(sum(v) / len(v), 1), "max_selected": max(v)}
                 for a, v in sorted(per_action.items())}
    return {"semantic_types": len(rubrics), "recall_gold": share("hit", "gold"), "recall_fail": share("fail_hit", "fail"),
            "recall_block": share("block_hit", "block"), "counts": dict(counts), "by_action": by_action,
            "missed_fails": missed}


def error_reason(error: str | None) -> str:
    """'Type: why', without ids or cassette keys, so the same cause groups together."""
    kind, _, why = (error or "").partition(": ")
    why = why.split(" in ", 1)[0] if kind == "CassetteNotFoundError" else why  # drop the local cassette path
    return f"{kind}: {re.sub(r'[0-9a-f]{8}-[0-9a-f-]{27}|[0-9a-f]{64}', '<id>', why)[:100]}" if why else kind


def build_report(cases: list[dict[str, Any]], scored: list[Scored], errors: Counter, reasons: Counter, *, catalog: EvalCatalog,
                 rubrics: dict[str, Rubric], mode: str, model: str, trials: int, selection: str) -> dict[str, Any]:
    classes = {name: catalog.entry(name).evidence_class for name in rubrics}
    return {
        "generated_at": datetime.now(timezone.utc).isoformat(timespec="seconds"), "gold": GOLD_PROVENANCE,
        "mode": mode, "model": ALWAYS_PASS_MODEL if mode == "baseline" else model, "trials": trials,
        "selection": selection, "n_cases": len(cases), "prompt_version": JUDGE_PROMPT_VERSION,
        "catalog_version": catalog.version, "rubric_versions": {n: r.eval_version for n, r in rubrics.items()},
        "evidence_class": classes, "overall": judgment_stats(scored), "case_level": case_level(scored),
        "consistency": consistency(scored),
        "per_eval": {n: s for n, s in group_stats(scored, lambda s: s.eval_type).items()},
        "by_evidence_class": group_stats(scored, lambda s: classes[s.eval_type]),
        "slices": slice_agreement(scored, SLICE_TAGS), "l5": l5_table(scored),
        "routing": routing_report(cases, catalog, rubrics), "errors": dict(errors.most_common()),
        "error_reasons": dict(reasons.most_common()),
    }


def run(*, mode: str, model: str, trials: int, selection: str, only: Sequence[str] = (), max_workers: int = 8,
        cassettes: Path = CASSETTES, include_retired: bool = False) -> dict[str, Any]:
    catalog = load_catalog()
    rubrics = dict(load_rubrics(catalog))
    cases = load_cases(only, include_retired=include_retired)
    requests = build_requests(cases, catalog, rubrics, selection=selection, trials=trials)
    batch = judge_many(requests, provider_factory(mode, model, rubrics, cassettes), rubrics, catalog,
                       max_workers=max_workers)
    outcomes = {(r.key, r.eval_type, r.run.trial): o for r, o in batch.pairs}
    errors = Counter((o.error or "").split(":", 1)[0] for _, o in batch.pairs if not o.ok)
    reasons = Counter(error_reason(o.error) for _, o in batch.pairs if not o.ok)
    scored = score(cases, outcomes, catalog, trials)
    report = build_report(cases, scored, errors, reasons, catalog=catalog, rubrics=rubrics, mode=mode, model=model,
                          trials=trials, selection=selection)
    missing = Counter(r.eval_type for r, o in batch.pairs if (o.error or "").startswith("CassetteNotFoundError"))
    return {**report, "missing_cassettes": dict(sorted(missing.items()))}


def write(report: dict[str, Any], out: Path, tag: str, stem: str | None = None) -> Path:
    out.mkdir(parents=True, exist_ok=True)
    name = stem or f"judges-{tag}-{report['mode']}-{model_family(report['model'])}"
    (out / f"{name}.json").write_text(json.dumps(report, indent=1, ensure_ascii=False) + "\n", encoding="utf-8")
    (out / f"{name}.md").write_text(render(report), encoding="utf-8")
    return out / f"{name}.md"


def parse_args(argv: Sequence[str] | None) -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__.split("\n\n")[0])
    parser.add_argument("--mode", choices=["replay", "record", "live", "baseline"], default="replay")
    parser.add_argument("--model", action="append", help="repeatable; default GHOST_MODEL's default")
    parser.add_argument("--trials", type=int, default=1)
    parser.add_argument("--selection", choices=["gold", "routed"], default="gold",
                        help="gold: judge every gold eval type (per-judge agreement); routed: what §7 routing runs")
    parser.add_argument("--case", action="append", default=[], help="only cases whose id contains this")
    parser.add_argument("--include-retired", action="store_true",
                        help="run the retired legacy gold (invented accounts) as pipeline mechanics only; "
                             "its numbers are not agreement with anything real")
    parser.add_argument("--max-workers", type=int, default=8)
    parser.add_argument("--out", type=Path, default=REPORTS)
    parser.add_argument("--title", help="heading of the markdown report (default: the benchmark/mode/model line)")
    parser.add_argument("--stem", help="output file name without extension (default judges-<tag>-<mode>-<model>)")
    parser.add_argument("--tag", default=datetime.now(timezone.utc).strftime("%Y-%m-%d"))
    return parser.parse_args(argv)


def main(argv: Sequence[str] | None = None) -> list[dict[str, Any]]:
    args = parse_args(argv)
    reports = []
    for model in args.model or [DEFAULT_MODEL]:
        report = run(mode=args.mode, model=model, trials=args.trials, selection=args.selection, only=args.case,
                     max_workers=args.max_workers, include_retired=args.include_retired)
        report = {**report, "title": args.title} if args.title else report
        sys.stdout.write(f"{write(report, args.out, args.tag, args.stem)}\n")
        reports.append(report)
    return reports


if __name__ == "__main__":
    main()
