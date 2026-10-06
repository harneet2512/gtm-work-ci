"""Two labelling passes, their agreement and the merged gold (HAR-114 gold v2, part B).

Both passes were written by the same author (one model), in two separate sittings with the second made from blind sheets
(render.py: no case type, no title, no design intent, no best action) and without reading the first. That is
self-consistency of one labeller, NOT inter-rater agreement and NOT human agreement; every number computed here says so.

A pass file is {case id: {eval type: [verdict, label | null, blocking, rationale?]}}. Where the two passes differ on
verdict, label or blocking, resolutions.json carries the final judgment and the reason; where they agree, the gold is the
agreed judgment (pass 1's wording).
"""
from __future__ import annotations

import json
from collections import Counter
from pathlib import Path
from typing import Any

ROOT = Path(__file__).resolve().parents[3]
LABELS = ROOT / "bench" / "labels" / "crmarena"
CATALOG = json.loads((ROOT / "contracts" / "evals" / "eval_catalog.json").read_text(encoding="utf-8"))["eval_types"]
DIAGNOSTICS = {
    ("buyer_readiness", "TOO_EARLY"): ["too_early"], ("timing_cadence", "TOO_EARLY"): ["too_early"],
    ("cta_calibration", "TOO_STRONG"): ["ask_too_strong"], ("cta_calibration", "TOO_WEAK"): ["ask_too_weak"],
    ("stakeholder_coverage", "SINGLE_THREADED_RISK"): ["single_threaded"], ("stakeholder_coverage", "UNDER_THREADED"): ["under_threaded"],
    ("stakeholder_coverage", "OVER_THREADED_TOO_EARLY"): ["over_threaded_too_early"],
    ("champion_continuity", "CHAMPION_BYPASSED"): ["champion_bypassed"], ("momentum", "COOLING"): ["engagement_cooling"],
}


def sheet_map() -> dict[str, dict]:
    """Neutral sheet id -> {case, evals} of the blind pass-2 sheets (random ids, shuffled order)."""
    path = LABELS / "pass_2_map.json"
    return {e["sheet"]: e for e in json.loads(path.read_text(encoding="utf-8"))["sheets"]} if path.exists() else {}


def load_pass(name: str) -> dict[str, dict[str, list]]:
    """A pass as {case id: {eval type: judgment}}. Pass 2 is keyed by neutral sheet ids and mapped back here."""
    mapping = sheet_map() if name == "pass_2" else {}
    out: dict[str, dict[str, list]] = {}
    for path in sorted((LABELS / name).glob("*.json")):
        for key, evals in json.loads(path.read_text(encoding="utf-8")).items():
            case_id = mapping[key]["case"] if mapping else key
            if case_id in out:
                raise ValueError(f"{case_id} appears twice in {name}")
            out[case_id] = evals
    return out


def load_resolutions() -> dict[str, dict[str, list]]:
    path = LABELS / "resolutions.json"
    return json.loads(path.read_text(encoding="utf-8")) if path.exists() else {}


def differs(a: list, b: list) -> bool:
    return tuple(a[:3]) != tuple(b[:3])


def disagreements(p1: dict, p2: dict) -> list[tuple[str, str, list, list]]:
    return [(c, e, p1[c][e], p2[c][e]) for c in sorted(p1) for e in p1[c] if differs(p1[c][e], p2[c][e])]


def agreement(p1: dict, p2: dict) -> dict[str, Any]:
    """Self-consistency of one author across two passes (not inter-rater agreement)."""
    n = Counter()
    for c in p1:
        for e, a in p1[c].items():
            b = p2[c][e]
            n["judgments"] += 1
            n["verdict_agree"] += a[0] == b[0]
            n["fail_agree"] += (a[0] == "fail") == (b[0] == "fail")
            if a[1] is not None or b[1] is not None:
                n["label_judgments"] += 1
                n["label_agree"] += a[1] == b[1]
            n["blocking_agree"] += a[2] == b[2]
            n["exact_agree"] += not differs(a, b)
    return dict(n)


def kappa(p1: dict, p2: dict) -> float | None:
    """Cohen's kappa on fail vs non-fail between the two passes."""
    pairs = [((a[0] == "fail"), (p2[c][e][0] == "fail")) for c in p1 for e, a in p1[c].items()]
    total = len(pairs)
    if not total:
        return None
    observed = sum(x == y for x, y in pairs) / total
    px, py = sum(x for x, _ in pairs) / total, sum(y for _, y in pairs) / total
    expected = px * py + (1 - px) * (1 - py)
    return None if expected == 1 else round((observed - expected) / (1 - expected), 3)


def final_label(case_id: str, eval_type: str, p1: dict, p2: dict, resolutions: dict) -> tuple[list, str | None]:
    """(final judgment [verdict, label, blocking, rationale], resolution reason or None)."""
    a, b = p1[case_id][eval_type], p2[case_id][eval_type]
    if not differs(a, b):
        return list(a), None
    res = resolutions[case_id][eval_type]
    return list(res[:4]), res[4]


def expected_item(eval_type: str, judgment: list, extra: dict[str, Any]) -> dict[str, Any]:
    verdict, label, blocking, rationale = judgment[:4]
    entry = CATALOG[eval_type]
    diagnostics = DIAGNOSTICS.get((eval_type, label), []) if verdict != "pass" else []
    out = {"eval_type": eval_type, "verdict": verdict, "label": label, "diagnostics": diagnostics, "blocking": bool(blocking),
           "rationale": rationale, "evidence_class": entry["evidence_class"]}
    return {**out, **extra}


def check_judgment(eval_type: str, judgment: list) -> list[str]:
    """Catalog problems with one judgment (label set, blocking permission)."""
    verdict, label, blocking = judgment[:3]
    entry = CATALOG[eval_type]
    problems = []
    if entry["labels"] and label not in entry["labels"]:
        problems.append(f"{eval_type}: label {label!r} not in {entry['labels']}")
    if not entry["labels"] and label is not None:
        problems.append(f"{eval_type}: has no labels but got {label!r}")
    if blocking and not entry["can_block"]:
        problems.append(f"{eval_type} never blocks")
    if blocking and verdict != "fail":
        problems.append(f"{eval_type}: blocking needs fail")
    return problems


def per_cluster(p1: dict, p2: dict, cluster_of: dict[str, str]) -> dict[str, dict[str, int]]:
    """Judgment and agreement counts per cluster (a deal): the judgments of one deal are not independent."""
    out: dict[str, dict[str, int]] = {}
    for case_id, evals in p1.items():
        row = out.setdefault(cluster_of[case_id], {"cases": 0, "judgments": 0, "exact_agree": 0, "fail_agree": 0})
        row["cases"] += 1
        for eval_type, a in evals.items():
            b = p2[case_id][eval_type]
            row["judgments"] += 1
            row["exact_agree"] += not differs(a, b)
            row["fail_agree"] += (a[0] == "fail") == (b[0] == "fail")
    return out


def cluster_bootstrap(p1: dict, p2: dict, cluster_of: dict[str, str], resamples: int = 4000, seed: int = 7) -> dict[str, list[float]]:
    """95% percentile intervals of exact and fail/non-fail agreement, resampling whole deals with replacement."""
    import random

    rows = per_cluster(p1, p2, cluster_of)
    keys = sorted(rows)
    rng = random.Random(seed)
    draws: dict[str, list[float]] = {"exact_agree": [], "fail_agree": []}
    for _ in range(resamples):
        pick = [rows[rng.choice(keys)] for _ in keys]
        total = sum(r["judgments"] for r in pick)
        for name in draws:
            draws[name].append(sum(r[name] for r in pick) / total)
    out = {}
    for name, values in draws.items():
        values.sort()
        out[name] = [round(values[int(0.025 * resamples)], 3), round(values[int(0.975 * resamples) - 1], 3)]
    return out
