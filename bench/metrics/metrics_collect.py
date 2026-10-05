"""Collectors that recompute every `measured` registry metric from repo sources (HAR-132 / WP33).

One collector per measured metric id; the harness test asserts the set equals the registry's measured ids and that
each recomputed value matches the registry's current_value."""
from __future__ import annotations

from collections.abc import Callable
from dataclasses import dataclass
from pathlib import Path
from typing import Any

import metrics_slices as ms
import metrics_sources as src

DECIMALS = 3
GATE_MIN_PR = 0.95  # HAR-96 §5 target for resolution precision/recall


@dataclass(frozen=True)
class Collected:
    value: Any
    display: str
    source: str
    n: int | None = None


@dataclass(frozen=True)
class Sources:
    """Parsed inputs; `wp` holds the WP5/WP6 numbers parsed from the committed `go test -v` artifact `go_source`."""
    extract: dict
    agent: dict
    wp: dict
    go_source: str
    transitions: dict

    @classmethod
    def from_repo(cls, date: str, root: Path = src.ROOT, run_go: bool = False,
                  runner: src.Runner | None = None) -> Sources:
        rel = src.run_go(date, runner or src.subprocess_runner, root) if run_go else src.go_artifact(date)
        return cls(extract=src.extract_summary(root / src.EXTRACT_REPORT),
                   agent=src.agent_report(root / src.AGENT_REPORT), wp=src.read_go_artifact(rel, root), go_source=rel,
                   transitions=src.transitions_report(root / src.TRANSITIONS_REPORT))


def _round(x: float) -> float:
    return round(x, DECIMALS)


def same_value(a: Any, b: Any) -> bool:
    if isinstance(a, bool) or isinstance(b, bool):
        return type(a) is type(b) and a == b
    if isinstance(a, dict) and isinstance(b, dict):
        return a.keys() == b.keys() and all(same_value(a[k], b[k]) for k in a)
    if isinstance(a, (int, float)) and isinstance(b, (int, float)):
        return _round(a) == _round(b)
    return a == b


def matches(got: Collected, current: dict) -> bool:
    """A recomputed value matches the registry only if value, sample size and source file all agree."""
    return (same_value(got.value, current["value"]) and got.n == current.get("n")
            and got.source == current["source"])


def _share(pair: tuple[int, int]) -> float:
    num, den = pair
    return _round(num / den)


def _pr(s: Sources, key: str) -> Collected:
    pr = s.wp[key]
    return Collected({"precision": pr.precision, "recall": pr.recall},
                     f"P {pr.precision:.3f} / R {pr.recall:.3f} (n={pr.n})", s.go_source, pr.n)


def _state_fields(s: Sources) -> Collected:
    w = s.wp
    value = {"all_fields": _share(w["all_fields"]), "required_fields": _share(w["required_fields"]),
             "knowable_fields": _share(w["knowable"])}
    text = (f"all {w['all_fields'][0]}/{w['all_fields'][1]}, required {w['required_fields'][0]}/"
            f"{w['required_fields'][1]}, knowable {w['knowable'][0]}/{w['knowable'][1]}")
    return Collected(value, text, s.go_source, w["all_fields"][1])


def _pair(pair: tuple[int, int], source: str) -> Collected:
    return Collected(_share(pair), f"{pair[0]}/{pair[1]}", source, pair[1])


def _trace_gate(s: Sources) -> Collected:
    prs = [s.wp[k] for k in ("identity", "about", "involves")]
    ok = all(p.precision >= GATE_MIN_PR and p.recall >= GATE_MIN_PR for p in prs)
    return Collected(ok, f"identity, about, involves P/R >= {GATE_MIN_PR} on the legacy fixture world: {ok}",
                     s.go_source)


def _case_count(case_type: str) -> Callable[[Sources], Collected]:
    def collect(_: Sources) -> Collected:
        count = len(list((ms.CASES_DIR / case_type).glob("*.json")))
        return Collected(count, f"{count} gold cases", f"fixtures/evals/cases/{case_type}", count)
    return collect


def _transition_ratio(key: str) -> Callable[[Sources], Collected]:
    """A ratio of the transitions report's headline (every uncontested step of both gold sets)."""
    def collect(s: Sources) -> Collected:
        r = s.transitions["headline_uncontested"][key]
        return Collected(r["value"], f"{r['num']}/{r['den']}", src.TRANSITIONS_REPORT, r["den"])
    return collect


def _transition_support(s: Sources) -> Collected:
    """Strict (status and target) precision: the share of recorded transitions whose status matches the gold."""
    d = s.transitions["headline_uncontested"]["transition_detection_status_and_target"]
    return Collected(d["precision"], f"{d['tp']}/{d['tp'] + d['fp']}", src.TRANSITIONS_REPORT, d["tp"] + d["fp"])


COLLECTORS: dict[str, Callable[[Sources], Collected]] = {
    "l1.relationship_state_accuracy": _transition_ratio("relationship_state_accuracy"),
    "l1.transition_support": _transition_support,
    "l1.uncertainty_marking": _transition_ratio("uncertainty_marking"),
    "l1.premature_promotion": _transition_ratio("premature_promotion"),
    "l1.entity_attachment": lambda s: _pr(s, "identity"),
    "l1.account_association": lambda s: _pr(s, "about"),
    "l1.state_field_accuracy": _state_fields,
    "l1.stakeholder_state_accuracy": lambda s: _pair(s.wp["buying_group"], s.go_source),
    "l1.critical_context_recall": lambda s: Collected(
        s.extract["fact_recall"], f"fact recall {s.extract['fact_recall']} over {s.extract['facts']} facts",
        src.EXTRACT_REPORT, s.extract["facts"]),
    "abstention.correctness": lambda s: _pair(src.check_totals(s.agent, src.ABSTENTION_CHECKS), src.AGENT_REPORT),
    "l3.correct_recipients": lambda s: _pair(src.check_totals(s.agent, src.RECIPIENT_CHECKS), src.AGENT_REPORT),
    "online_product.workflow_completion": lambda s: _pair(src.completion(s.agent), src.AGENT_REPORT),
    "gate.trace_raw_activity": _trace_gate,
}
COLLECTORS.update({f"bench.{ct}": _case_count(ct) for ct in ms.case_types()})
