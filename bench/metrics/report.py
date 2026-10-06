"""HAR-97 metrics harness (HAR-132 / WP33).

Reads contracts/metrics/metric_registry.json, recomputes every measured metric from repo sources, attaches gold-case
evidence to blocked/computable ones and writes bench/reports/metrics-<date>.{json,md}. Offline: no model calls.

    python bench/metrics/report.py --date 2026-10-02            # reproduce from the committed artifacts
    python bench/metrics/report.py --date D --run-go            # re-run the WP5/WP6 Go acceptance tests (embedded
                                                                # Postgres) and write bench/reports/go-acceptance-D.txt
    python bench/metrics/report.py --date D --go-date G         # report dated D from the go-acceptance artifact of G
    python bench/metrics/report.py --docs                       # regenerate docs/metrics*.md and docs/eval-completeness.md

The report is a pure function of the committed inputs and --date, so re-running reproduces it byte for byte.
"""
from __future__ import annotations

import argparse
import json
import sys
from collections import Counter, defaultdict
from collections.abc import Callable
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))

import completeness_gate as gate  # noqa: E402
import eval_docs  # noqa: E402
import metrics_collect as mc  # noqa: E402
import metrics_docs  # noqa: E402
import metrics_slices as ms  # noqa: E402
import metrics_sources as src  # noqa: E402

REPORTS = src.ROOT / "bench" / "reports"


def _runs_at_cap(sources: mc.Sources, _: dict) -> tuple[str, str]:
    at_cap, done = src.runs_at_tool_cap(sources.agent)
    return f"runs_at_{src.TOOL_CAP}_call_cap", f"{at_cap}/{done}"


# registry `harness_evidence` key -> (sources, gold) -> (evidence name, value)
EVIDENCE: dict[str, Callable[[mc.Sources, dict], tuple[str, object]]] = {
    "gold_case_summary": lambda _, gold: ("gold_cases", gold["cases"]),
    "runs_at_tool_cap": _runs_at_cap,
}


def _evidence(metric: dict, sources: mc.Sources, gold: dict) -> dict:
    ev: dict = {}
    if metric.get("eval_types"):
        ev["gold_judgments"] = sum(gold["eval_types"].get(t, 0) for t in metric["eval_types"])
    if metric["id"] in gold["slices"]:
        ev["gold_cases_per_value"] = gold["slices"][metric["id"]]
    for key in metric.get("harness_evidence", []):
        name, value = EVIDENCE[key](sources, gold)
        ev[name] = value
    return ev


def _row(metric: dict, sources: mc.Sources, gold: dict) -> dict:
    row = {k: metric[k] for k in ("id", "name", "kind", "layer", "status", "owner_wp", "blocker")}
    row["har97_ref"] = f"{metric['har97_ref']['section']}: {metric['har97_ref']['quote']}"
    collector = mc.COLLECTORS.get(metric["id"])
    if metric["status"] == "measured":
        got = collector(sources) if collector else None
        current = metric["current_value"]
        row.update(value=got.value if got else None, display=got.display if got else "no collector",
                   n=got.n if got else None, source=got.source if got else None,
                   data_basis=current["data_basis"], matches_registry=bool(got) and mc.matches(got, current))
        row["caveat"] = current.get("caveat")
    else:
        row["tracked_by"] = metric["tracked_by"]
        if metric["status"] == "computable":
            row["notes"] = metric.get("notes")
    row["evidence"] = _evidence(metric, sources, gold)
    return row


def build_report(registry: dict, sources: mc.Sources, date: str) -> dict:
    cases = ms.load_cases()
    gold = {"eval_types": ms.eval_type_counts(cases), "slices": ms.slice_coverage(cases),
            "cases": ms.case_summary(cases)}
    rows = [_row(m, sources, gold) for m in registry["metrics"]]
    by_layer: dict[str, Counter] = defaultdict(Counter)
    for r in rows:
        by_layer[r["layer"]][r["status"]] += 1
    summary = {"by_layer_status": {layer: dict(by_layer[layer]) for layer in registry["layer_order"]},
               "by_status": dict(Counter(r["status"] for r in rows)), "by_kind": dict(Counter(r["kind"] for r in rows)),
               "stale": [r["id"] for r in rows if r["status"] == "measured" and not r["matches_registry"]]}
    return {"date": date, "registry_source": registry["source"], "go_acceptance_source": sources.go_source,
            "summary": summary, "metrics": rows, "gold": {"cases": gold["cases"]}}


def _row_md(r: dict) -> str:
    if r["status"] == "measured":
        flag = "" if r["matches_registry"] else " **STALE vs registry**"
        detail = f"**{r['display']}**{flag} ({r['source']}; n={r['n']})"
    elif r["status"] == "blocked":
        detail = f"blocker: {r['blocker']} (tracked by {', '.join(r['tracked_by'])})"
    else:
        detail = f"{r.get('notes') or 'computable'} (tracked by {', '.join(r['tracked_by'])})"
    if r["evidence"]:
        detail += "; evidence: " + ", ".join(f"{k}={v}" for k, v in r["evidence"].items())
    cells = (f"`{r['id']}`", r["kind"], r["status"], r.get("data_basis", "-"), detail, r["owner_wp"])
    return "| " + " | ".join(metrics_docs.cell(c) for c in cells) + " |"


def render_markdown(rep: dict) -> str:
    s = rep["summary"]
    lines = [f"# HAR-97 metrics ({rep['date']})", "",
             f"Registry: `contracts/metrics/metric_registry.json` (HAR-97 as of {rep['registry_source']['updated_at']}). "
             f"WP5/WP6 numbers parsed from the committed `go test -v` output `{rep['go_acceptance_source']}` "
             "(`--run-go` re-runs the Go acceptance tests). Generated by "
             f"`python bench/metrics/report.py --date {rep['date']}`; data_basis says what each measured value was "
             "measured on (legacy_fixture_world = invented Acme/Beta/Northstar accounts).", "", "## Summary", "",
             "| Layer | measured | computable | blocked |", "|---|---|---|---|"]
    for layer, counts in s["by_layer_status"].items():
        lines.append(f"| {layer} | " + " | ".join(str(counts.get(k, 0)) for k in metrics_docs.STATUSES) + " |")
    lines += ["", f"By status: {s['by_status']}. By kind: {s['by_kind']}. Stale measured values: "
              f"{s['stale'] or 'none'}.", "", f"Gold cases (fixtures/evals): {rep['gold']['cases']}.", ""]
    for layer in s["by_layer_status"]:
        lines += [f"## {layer}", "| id | kind | status | data_basis | value / blocker | owner |",
                  "|---|---|---|---|---|---|"]
        lines += [_row_md(r) for r in rep["metrics"] if r["layer"] == layer]
        lines.append("")
    return "\n".join(lines)


def dump_report(rep: dict) -> str:
    """JSON with one metric row per line (diff-friendly, small files)."""
    head = {k: v for k, v in rep.items() if k != "metrics"}
    lines = ["{"] + [f"  {json.dumps(k)}: {json.dumps(v, ensure_ascii=False)}," for k, v in head.items()]
    rows = ",\n".join("    " + json.dumps(r, ensure_ascii=False) for r in rep["metrics"])
    return "\n".join([*lines, '  "metrics": [', rows, "  ]", "}"]) + "\n"


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[1])
    parser.add_argument("--date", help="report date (YYYY-MM-DD); required unless --docs. Names the outputs and the "
                                       "go-acceptance artifact read (or written with --run-go)")
    parser.add_argument("--go-date", help="date of the go-acceptance artifact to read (default: --date); lets a registry "
                                          "refresh get a new report date without re-running the Go acceptance tests")
    parser.add_argument("--out-dir", type=Path, default=REPORTS)
    parser.add_argument("--run-go", action="store_true", help="re-run the WP5/WP6 Go acceptance tests")
    parser.add_argument("--docs", action="store_true", help="regenerate docs/metrics.md and exit")
    args = parser.parse_args(argv)
    registry = src.load_registry()
    if args.docs:
        for rel, text in metrics_docs.render_docs(registry).items():
            (src.ROOT / rel).write_text(text, encoding="utf-8", newline="\n")
        matrix, evals = gate.load(gate.MATRIX_PATH), gate.load(gate.REGISTRY_PATH)
        (src.ROOT / eval_docs.DOC).write_text(eval_docs.render(matrix, evals), encoding="utf-8", newline="\n")
        return 0
    if not args.date:
        parser.error("--date is required (the report must be reproducible from the committed artifacts)")
    rep = build_report(registry, mc.Sources.from_repo(args.go_date or args.date, run_go=args.run_go), args.date)
    args.out_dir.mkdir(parents=True, exist_ok=True)
    stem = args.out_dir / f"metrics-{args.date}"
    stem.with_suffix(".json").write_text(dump_report(rep), encoding="utf-8", newline="\n")
    stem.with_suffix(".md").write_text(render_markdown(rep), encoding="utf-8", newline="\n")
    print(f"wrote {stem}.json and .md: {rep['summary']['by_status']}; stale: {rep['summary']['stale'] or 'none'}")
    return 1 if rep["summary"]["stale"] else 0


if __name__ == "__main__":
    raise SystemExit(main())
