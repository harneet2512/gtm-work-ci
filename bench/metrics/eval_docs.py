"""Render docs/eval-completeness.md from the eval registry and the completeness matrix (HAR-140 / WP36). Generated."""
from __future__ import annotations

from collections import Counter

import completeness_gate as gate

DOC = "docs/eval-completeness.md"
SYMBOL = {"implemented": "C", "partial": "c", "REQUIRED": "R", "NOT_APPLICABLE": "-"}


def _symbol(cell: dict, by_id: dict) -> str:
    if cell["state"] == "COVERED_BY":
        return SYMBOL["implemented"] if any(by_id[i]["status"] == "implemented" for i in cell["evals"]) else SYMBOL["partial"]
    return SYMBOL[cell["state"]]


def _grid(matrix: dict, by_id: dict) -> list[str]:
    cells = {(c["surface"], c["dimension"]): c for c in matrix["cells"]}
    head = "| # | surface | " + " | ".join(d[:3] for d in matrix["dimensions"]) + " |"
    lines = [head, "|---|---|" + "---|" * len(matrix["dimensions"])]
    for s in matrix["surfaces"]:
        row = [_symbol(cells[(s["id"], d)], by_id) for d in matrix["dimensions"]]
        lines.append(f"| {s['id']} | {s['name']} | " + " | ".join(row) + " |")
    return lines


def _families(registry: dict) -> list[str]:
    mix: dict[str, Counter] = {}
    for e in registry["evals"]:
        mix.setdefault(e["family"], Counter())[e["status"]] += 1
    lines = ["| family | name | class | owner | implemented | partial | planned |", "|---|---|---|---|---|---|---|"]
    for f in registry["families"]:
        c = mix.get(f["id"], Counter())
        lines.append(f"| {f['id']} | {f['name']} | {f['class']} | {f['owner']} | {c['implemented']} | {c['partial']} | {c['planned']} |")
    return lines


def _planned(matrix: dict) -> list[str]:
    lines = ["| surface | dimension | owner | evals | note |", "|---|---|---|---|---|"]
    for c in matrix["cells"]:
        if c["state"] == "REQUIRED":
            lines.append(f"| {c['surface']} | {c['dimension']} | {c['owner']} | {', '.join(c['evals']) or '-'} | {c.get('note', '')} |")
    return lines


def render(matrix: dict, registry: dict) -> str:
    by_id = {e["id"]: e for e in registry["evals"]}
    n = gate.counts(matrix, registry)
    status = Counter(e["status"] for e in registry["evals"])
    parts = [
        "# Eval completeness (HAR-97 canonical sections 1, 12, 13)", "",
        "Generated from `contracts/evals/eval_registry.json` and `contracts/evals/completeness_matrix.json` by "
        "`python bench/metrics/report.py --docs`; do not edit by hand. The construction gate is "
        "`python bench/metrics/completeness_gate.py` (a test in `worker-py/tests/test_completeness_matrix.py`).", "",
        "## Cell counts (20 surfaces x 10 dimensions)", "",
        f"- **{n.cells} cells**: {n.covered} covered ({n.covered_by_implemented} by an implemented eval, {n.covered_partial_only} "
        f"by partial evals only), {n.planned} REQUIRED and planned with an owning Linear issue ({n.planned_with_registered_eval} "
        f"name a registered planned eval), {n.not_applicable} not applicable (each with a reason).",
        f"- **{len(registry['evals'])} registered evals**: {status['implemented']} implemented, {status['partial']} partial, "
        f"{status['planned']} planned.", "",
        "Legend: `C` covered by an implemented eval, `c` covered by partial evals only, `R` REQUIRED and planned, `-` not "
        "applicable. Dimensions in order: INT intended behavior, INV invariants, BOU boundaries, FAI failure modes, AMB "
        "ambiguity/abstention, ADV adversarial/gaming, STO stochastic reliability, CAU causal/trace attribution, REG regression "
        "risk, OBS observability.", "", *_grid(matrix, by_id), "",
        "## Eval families", "", *_families(registry), "",
        "## Planned cells (REQUIRED, status planned)", "", *_planned(matrix), ""]
    return "\n".join(parts)
