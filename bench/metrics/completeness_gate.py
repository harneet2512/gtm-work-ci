"""Construction gate of the 20 x 10 completeness matrix (HAR-97 CANONICAL EVAL STRATEGY sections 1 and 13; HAR-140 / WP36).

A cell is REQUIRED, NOT_APPLICABLE (with a reason) or COVERED_BY (eval ids). The gate fails on any cell that is none of:
covered by registered evals that exist (implemented or partial) and protect that surface and dimension; REQUIRED and
planned with BOTH an owning Linear issue known to the registries AND a registered planned eval id; or not applicable
with a reason that cites a surface or an eval. It also fails when the registry contradicts the
matrix (an eval tagged to a cell the matrix calls not applicable, or an existing eval the cell does not credit).

    python bench/metrics/completeness_gate.py        # prints the cell counts; exit 1 on any violation
"""
from __future__ import annotations

import json
import re
import sys
from collections import Counter
from pathlib import Path
from typing import NamedTuple

ROOT = Path(__file__).resolve().parents[2]
MATRIX_PATH = ROOT / "contracts" / "evals" / "completeness_matrix.json"
REGISTRY_PATH = ROOT / "contracts" / "evals" / "eval_registry.json"
EXISTING = ("implemented", "partial")
OWNER = re.compile(r"^HAR-[0-9]+$")
CITES = re.compile(r"\b(E\d+(\.\d+)?|M[1-5]|surfaces? \d+)\b")
METRIC_PATH = ROOT / "contracts" / "metrics" / "metric_registry.json"
EXTRA_TICKETS = frozenset({"HAR-97", "HAR-129", "HAR-132", "HAR-140"})  # parents and the WPs that own registries/traces
MIN_REASON = 20
SURFACE_COUNT, DIMENSION_COUNT = 20, 10


class Counts(NamedTuple):
    cells: int
    covered: int
    covered_by_implemented: int
    covered_partial_only: int
    planned: int
    planned_with_registered_eval: int
    not_applicable: int


def load(path: Path) -> dict:
    return json.loads(path.read_text(encoding="utf-8"))


def known_tickets(metric_registry: dict | None = None) -> frozenset[str]:
    """Linear issues the repo already names as owners or trackers (the pytest check, now in the gate itself)."""
    reg = metric_registry if metric_registry is not None else load(METRIC_PATH)
    named = {m["owner_wp"].split(" ")[0] for m in reg["metrics"]}
    named |= {t for m in reg["metrics"] for t in m.get("tracked_by", [])}
    return frozenset(named) | EXTRA_TICKETS


def _cell_name(cell: dict) -> str:
    return f"surface {cell.get('surface')} x {cell.get('dimension')}"


def _structure(matrix: dict) -> list[str]:
    cells = matrix["cells"]
    keys = [(c.get("surface"), c.get("dimension")) for c in cells]
    expected = {(s, d) for s in range(1, SURFACE_COUNT + 1) for d in matrix["dimensions"]}
    problems = [f"duplicate cell {k}" for k, n in Counter(keys).items() if n > 1]
    problems += [f"missing cell {k}" for k in sorted(expected - set(keys), key=str)]
    problems += [f"unknown cell {k}" for k in sorted(set(keys) - expected, key=str)]
    if len(matrix["dimensions"]) != DIMENSION_COUNT or len(matrix["surfaces"]) != SURFACE_COUNT:
        problems.append("the matrix must have 20 surfaces and 10 dimensions")
    return problems


def _evals_check(cell: dict, by_id: dict[str, dict], want_existing: bool) -> list[str]:
    problems: list[str] = []
    for eval_id in cell.get("evals", []):
        e = by_id.get(eval_id)
        if e is None:
            problems.append(f"{_cell_name(cell)}: eval {eval_id} is not in the registry")
            continue
        exists = e["status"] in EXISTING
        if exists != want_existing:
            kind = "covered cells list only existing evals" if want_existing else "planned cells list only planned evals"
            problems.append(f"{_cell_name(cell)}: {eval_id} is {e['status']} ({kind})")
        if cell["surface"] not in e["surfaces"] or cell["dimension"] not in e["dimensions"]:
            problems.append(f"{_cell_name(cell)}: {eval_id} does not protect this surface and dimension")
    return problems


def _one_cell(cell: dict, by_id: dict[str, dict], tickets: frozenset[str]) -> list[str]:
    state = cell.get("state")
    name = _cell_name(cell)
    tagged = [e for e in by_id.values() if cell["surface"] in e["surfaces"] and cell["dimension"] in e["dimensions"]]
    existing = [e["id"] for e in tagged if e["status"] in EXISTING]
    if state == "COVERED_BY":
        problems = _evals_check(cell, by_id, True)
        listed = set(cell.get("evals", []))
        problems += [f"{name}: existing eval {i} protects this cell but is not credited" for i in existing if i not in listed]
        return problems or ([] if cell.get("evals") else [f"{name}: COVERED_BY without evals"])
    if state == "NOT_APPLICABLE":
        reason = cell.get("reason", "")
        problems = [] if len(reason) >= MIN_REASON else [f"{name}: NOT_APPLICABLE needs a reason"]
        if not CITES.search(reason):
            problems.append(f"{name}: NOT_APPLICABLE reason must cite a surface or an eval")
        return problems + [f"{name}: not applicable but eval {e['id']} is tagged to it" for e in tagged]
    if state == "REQUIRED":
        problems = []
        owner = cell.get("owner", "")
        if cell.get("status") != "planned" or not OWNER.match(owner) or not cell.get("evals"):
            problems.append(f"{name}: REQUIRED with no eval registered and not planned with an owning Linear issue "
                            "and a registered planned eval")
        elif owner not in tickets:
            problems.append(f"{name}: owner {owner} is not a Linear issue the registries know")
        problems += _evals_check(cell, by_id, False)
        return problems + [f"{name}: existing eval {i} protects this cell, mark it COVERED_BY" for i in existing]
    return [f"{name}: unknown state {state!r}"]


def entries_by_id(registry: dict) -> dict[str, dict]:
    """Every registry entry a matrix cell may name: the evals and the operational metrics (M1-M5, kept apart from the
    evals but still protecting surface 20)."""
    return {e["id"]: e for e in (*registry["evals"], *registry.get("metrics", []))}


def check(matrix: dict, registry: dict, tickets: frozenset[str] | None = None) -> list[str]:
    """Every violation of the construction gate; empty means the gate passes."""
    by_id = entries_by_id(registry)
    tickets = known_tickets() if tickets is None else tickets
    problems = _structure(matrix)
    for cell in matrix["cells"]:
        if {"surface", "dimension"} <= cell.keys():
            problems += _one_cell(cell, by_id, tickets)
    return problems


def counts(matrix: dict, registry: dict) -> Counts:
    by_id = entries_by_id(registry)
    cells = matrix["cells"]
    covered = [c for c in cells if c["state"] == "COVERED_BY"]
    full = [c for c in covered if any(by_id[i]["status"] == "implemented" for i in c["evals"])]
    planned = [c for c in cells if c["state"] == "REQUIRED"]
    return Counts(len(cells), len(covered), len(full), len(covered) - len(full), len(planned),
                  sum(1 for c in planned if c["evals"]), sum(1 for c in cells if c["state"] == "NOT_APPLICABLE"))


def main() -> int:
    matrix, registry = load(MATRIX_PATH), load(REGISTRY_PATH)
    problems = check(matrix, registry)
    for p in problems:
        print("GATE FAIL:", p)
    c = counts(matrix, registry)
    print(f"{c.cells} cells: {c.covered} covered ({c.covered_by_implemented} by an implemented eval, "
          f"{c.covered_partial_only} by partial evals only), {c.planned} planned ({c.planned_with_registered_eval} with a "
          f"registered planned eval), {c.not_applicable} not applicable")
    return 1 if problems else 0


if __name__ == "__main__":
    sys.exit(main())
