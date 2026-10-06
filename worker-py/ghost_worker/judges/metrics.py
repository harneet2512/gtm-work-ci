"""Judge-vs-gold metrics (HAR-97 §20) and the L5 per-eval agreement table.

§20 measures: agreement with gold labels, precision/recall for fail states, false-block rate, false-pass rate,
consistency across repeated trials, agreement by account-state slice. Every rate is None when its denominator
is empty (no fake precision); counts travel with every rate.
"""
from __future__ import annotations

from collections import Counter, defaultdict
from collections.abc import Callable, Iterable, Mapping, Sequence
from itertools import combinations
from typing import Any

from pydantic import BaseModel, ConfigDict

from .context import Frozen
from .engine import JudgeOutcome

VERDICTS = ("pass", "warn", "fail", "abstain")


class Scored(BaseModel):
    """One gold judgment of one case and what the judge said in one trial (outcome None = not run/routed out)."""

    model_config = ConfigDict(frozen=True)

    case_id: str
    eval_type: str
    trial: int
    gold: Frozen
    slices: Frozen
    should_pass: bool
    outcome: JudgeOutcome | None = None

    @property
    def judged(self) -> Mapping[str, Any] | None:
        return self.outcome.result if self.outcome is not None and self.outcome.ok else None

    @property
    def judged_verdict(self) -> str:
        """The judge's verdict in the gold's vocabulary: gold says abstain where a result says unknown."""
        verdict = self.judged["verdict"]
        return "abstain" if verdict == "unknown" else verdict


def rate(numerator: int, denominator: int) -> float | None:
    return round(numerator / denominator, 3) if denominator else None


def _kappa(pairs: Sequence[tuple[str, str]]) -> float | None:
    """Cohen's kappa of judge vs gold verdicts (chance-corrected agreement)."""
    n = len(pairs)
    if not n:
        return None
    observed = sum(g == j for g, j in pairs) / n
    gold, judge = Counter(g for g, _ in pairs), Counter(j for _, j in pairs)
    expected = sum(gold[v] * judge[v] for v in VERDICTS) / (n * n)
    return None if expected == 1 else round((observed - expected) / (1 - expected), 3)


def _fail_stats(judged: Sequence[Scored]) -> dict[str, Any]:
    gold_fail = [s for s in judged if s.gold["verdict"] == "fail"]
    judge_fail = [s for s in judged if s.judged_verdict == "fail"]
    both = sum(s.judged_verdict == "fail" for s in gold_fail)
    return {"gold_fail": len(gold_fail), "judge_fail": len(judge_fail),
            "fail_precision": rate(both, len(judge_fail)), "fail_recall": rate(both, len(gold_fail)),
            "false_pass_rate": rate(sum(s.judged_verdict == "pass" for s in gold_fail), len(gold_fail))}


def _block_stats(judged: Sequence[Scored]) -> dict[str, Any]:
    gold_block = [s for s in judged if s.gold["blocking"]]
    gold_clear = [s for s in judged if not s.gold["blocking"]]
    judge_block = [s for s in judged if s.judged["blocking"]]
    return {"gold_blocking": len(gold_block), "judge_blocking": len(judge_block),
            "false_block_rate": rate(sum(s.judged["blocking"] for s in gold_clear), len(gold_clear)),
            "block_precision": rate(sum(s.gold["blocking"] for s in judge_block), len(judge_block)),
            "block_recall": rate(sum(s.judged["blocking"] for s in gold_block), len(gold_block))}


def _diagnostic_recall(judged: Sequence[Scored]) -> float | None:
    wanted = [(s, d) for s in judged for d in s.gold.get("diagnostics", ())]
    return rate(sum(d in s.judged["diagnostics"] for s, d in wanted), len(wanted))


def _errors(scored: Sequence[Scored]) -> int:
    return sum(s.outcome is not None and not s.outcome.ok for s in scored)


def _skipped(scored: Sequence[Scored]) -> int:
    return sum(s.outcome is None for s in scored)


def judgment_stats(scored: Sequence[Scored]) -> dict[str, Any]:
    """Pooled over every (case, gold judgment, trial) in `scored`."""
    judged = [s for s in scored if s.judged is not None]
    labelled = [s for s in judged if s.gold.get("label") is not None]
    return {
        "n_gold": len(scored), "n_judged": len(judged),
        "error_count": _errors(scored), "skipped_count": _skipped(scored),
        "insufficient_data": not judged,
        "coverage": rate(len(judged), len(scored)),
        "verdict_agreement": rate(sum(s.gold["verdict"] == s.judged_verdict for s in judged), len(judged)),
        "verdict_kappa": _kappa([(s.gold["verdict"], s.judged_verdict) for s in judged]),
        "n_labelled": len(labelled),
        "label_agreement": rate(sum(s.gold["label"] == s.judged["label"] for s in labelled), len(labelled)),
        "abstain_rate": rate(sum(s.judged_verdict == "abstain" for s in judged), len(judged)),
        "diagnostic_recall": _diagnostic_recall(judged),
        **_fail_stats(judged), **_block_stats(judged),
    }


def group_stats(scored: Sequence[Scored], key: Callable[[Scored], str]) -> dict[str, dict[str, Any]]:
    groups: dict[str, list[Scored]] = defaultdict(list)
    for s in scored:
        groups[key(s)].append(s)
    return {name: judgment_stats(groups[name]) for name in sorted(groups)}


def slice_agreement(scored: Sequence[Scored], tags: Iterable[str]) -> dict[str, dict[str, dict[str, Any]]]:
    """Agreement by account-state slice: per tag, per value, the core rates."""
    keep = ("n_judged", "verdict_agreement", "label_agreement", "false_pass_rate", "false_block_rate")
    out: dict[str, dict[str, dict[str, Any]]] = {}
    for tag in tags:
        stats = group_stats(scored, lambda s, t=tag: str(s.slices.get(t)))
        out[tag] = {value: {k: row[k] for k in keep} for value, row in stats.items()}
    return out


def _signature(result: Mapping[str, Any]) -> tuple[Any, ...]:
    return result["verdict"], result["label"], result["blocking"]


def consistency(scored: Sequence[Scored]) -> dict[str, Any]:
    """Repeat consistency (§20): over (case, eval type) judged in two or more trials, the share where every
    trial agrees on verdict, label and blocking, and the mean pairwise agreement."""
    trials: dict[tuple[str, str], list[tuple[Any, ...]]] = defaultdict(list)
    for s in scored:
        if s.judged is not None:
            trials[(s.case_id, s.eval_type)].append(_signature(s.judged))
    repeated = [sigs for sigs in trials.values() if len(sigs) >= 2]
    pairs = [a == b for sigs in repeated for a, b in combinations(sigs, 2)]
    return {"n_repeated": len(repeated), "unanimous_rate": rate(sum(len(set(s)) == 1 for s in repeated), len(repeated)),
            "pairwise_agreement": rate(sum(pairs), len(pairs))}


def case_level(scored: Sequence[Scored]) -> dict[str, Any]:
    """Per (case, trial): a should-pass case any judge blocked is a false block; a case whose semantic gold
    has a fail that some judge flagged (fail) is caught, otherwise a false pass. Only judged rows count: a run
    enters a denominator only if a judge actually answered for it (false block: any judged row; false pass: a
    judged row for a gold-fail judgment), so errored or skipped judges can never look like "not flagged". With
    no judged row at all the rates are None and `insufficient_data` is True."""
    runs: dict[tuple[str, int], list[Scored]] = defaultdict(list)
    for s in scored:
        runs[(s.case_id, s.trial)].append(s)
    groups = list(runs.values())
    good = [g for g in groups if g[0].should_pass and any(s.judged is not None for s in g)]
    flawed = [g for g in groups if any(s.gold["verdict"] == "fail" and s.judged is not None for s in g)]
    unjudged = sum(not any(s.judged is not None for s in g) for g in groups)

    def blocked(group: list[Scored]) -> bool:
        return any(s.judged is not None and s.judged["blocking"] for s in group)

    def flagged(group: list[Scored]) -> bool:
        """Caught: the judge failed a dimension the gold also judged as fail."""
        return any(s.judged is not None and s.judged_verdict == "fail" and s.gold["verdict"] == "fail"
                   for s in group)

    def flagged_any(group: list[Scored]) -> bool:
        """Looser view: the judge failed any dimension, even one the gold passed."""
        return any(s.judged is not None and s.judged_verdict == "fail" for s in group)

    return {"insufficient_data": not any(s.judged is not None for s in scored),
            "good_runs": len(good), "false_block_rate": rate(sum(map(blocked, good)), len(good)),
            "flawed_runs": len(flawed), "false_pass_rate": rate(sum(not flagged(g) for g in flawed), len(flawed)),
            "false_pass_rate_any_flag": rate(sum(not flagged_any(g) for g in flawed), len(flawed)),
            "error_count": _errors(scored), "skipped_count": _skipped(scored), "unjudged_runs": unjudged}
