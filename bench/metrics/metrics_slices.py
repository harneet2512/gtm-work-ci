"""Gold-case evidence for blocked or computable metrics (HAR-132 / WP33): judgments per eval type and the HAR-97
regression-slice value of every gold case (fixtures/evals). Counts only: no evaluator output exists yet."""
from __future__ import annotations

import json
from collections import Counter
from collections.abc import Callable
from pathlib import Path

from metrics_sources import ROOT, load_json

CASES_DIR = ROOT / "fixtures" / "evals" / "cases"
EARLY_STAGES = frozenset({"discovery"})
WEAK_CHAMPION = frozenset({"weakening", "departed", "inactive", "unknown"})
SECURITY_WORDS = ("security", "soc2", "soc 2", "residency", "hipaa", "baa", "phi")
PRICING_WORDS = ("pric", "discount", "budget")
MULTITHREAD_MIN_ACTIVE = 2


def load_cases(directory: Path = CASES_DIR) -> list[dict]:
    return [load_json(p) for p in sorted(directory.glob("*/*.json"))]


def case_types(directory: Path = CASES_DIR) -> list[str]:
    return sorted(p.name for p in directory.iterdir() if p.is_dir())


def eval_type_counts(cases: list[dict]) -> Counter:
    return Counter(e["eval_type"] for case in cases for e in case["expected"])


def contested(case: dict) -> set[str]:
    """Eval types whose adjudicated correction is flagged contested (a human has not decided yet)."""
    return {c["eval_type"] for rev in case.get("gold_revision", []) for c in rev["changes"] if c.get("contested")}


def case_summary(cases: list[dict]) -> dict:
    """Counts with the contested judgments included, and again without them until a human decides."""
    blocking = lambda c, skip: sum(1 for e in c["expected"] if e["verdict"] == "fail" and e["blocking"] and e["eval_type"] not in skip)
    clean = [c for c in cases if not contested(c)]
    return {"cases": len(cases),
            "should_pass": sum(1 for c in cases if c["should_pass"]),
            "flawed": sum(1 for c in cases if not c["should_pass"]),
            "blocking_fails": sum(blocking(c, set()) for c in cases),
            "with_expected_best_action": sum(1 for c in cases if c.get("expected_best_action")),
            "contested_judgments": sum(len(contested(c)) for c in cases),
            "cases_without_contested": len(clean),
            "should_pass_without_contested": sum(1 for c in clean if c["should_pass"]),
            "blocking_fails_without_contested": sum(blocking(c, contested(c)) for c in cases)}


def _stage(case: dict) -> str:
    return "early" if case["slice_tags"]["stage"] in EARLY_STAGES else "late"


def _champion(case: dict) -> str:
    status = case["slice_tags"]["champion_status"]
    if status == "active":
        return "active"
    return "delegated" if status == "delegated" else "weak_or_none" if status in WEAK_CHAMPION else status


def _threading(case: dict) -> str:
    active = sum(1 for m in case["context"]["state"].get("buying_group", []) if m.get("status") == "active")
    return "multithreaded" if active >= MULTITHREAD_MIN_ACTIVE else "single_threaded"


def _blocker_type(case: dict) -> str:
    blockers = json.dumps(case["context"]["state"]["fields"].get("blockers", {})).lower()
    if any(w in blockers for w in SECURITY_WORDS):
        return "security"
    return "pricing" if any(w in blockers for w in PRICING_WORDS) else "none"


def _evidence(case: dict) -> str:
    conflict = case["case_type"] == "conflicting_account_state" or "standing_conflict" in case["slice_tags"]["extra"]
    return "contradictory" if conflict else "clean"


def _knowledge_available(case: dict) -> str:
    return "knowledge_available" if case["context"].get("offered_knowledge") else "no_knowledge"


def _knowledge_applicability(case: dict) -> str:
    labels = {e.get("label") for e in case["expected"] if e["eval_type"] == "knowledge_applicability"}
    if not labels:
        return "not_judged"
    return "applicable" if labels == {"APPLIES"} else "non_applicable"


SLICE_RULES: dict[str, Callable[[dict], str]] = {
    "motion": lambda c: c["slice_tags"]["motion"],
    "stage": _stage,
    "champion": _champion,
    "threading": _threading,
    "economic_buyer": lambda c: c["slice_tags"]["economic_buyer"],
    "blocker_type": _blocker_type,
    "support_risk": lambda c: c["slice_tags"]["risk"],
    "evidence_conflict": _evidence,
    "knowledge_available": _knowledge_available,
    "knowledge_applicability": _knowledge_applicability,
}


def slice_coverage(cases: list[dict]) -> dict[str, dict[str, int]]:
    return {f"slices.{name}": dict(Counter(rule(c) for c in cases)) for name, rule in SLICE_RULES.items()}
