"""Readers and parsers for the sources behind measured HAR-97 metrics (HAR-132 / WP33). Pure functions, no model calls.

Sources: the live benchmark reports in bench/reports and the committed raw `go test -v` output of the WP5/WP6
acceptance tests (bench/reports/go-acceptance-<date>.txt; `--run-go` regenerates it)."""
from __future__ import annotations

import json
import re
import subprocess
from collections.abc import Callable, Iterable
from pathlib import Path
from typing import Any, NamedTuple

ROOT = Path(__file__).resolve().parents[2]
REGISTRY_PATH = ROOT / "contracts" / "metrics" / "metric_registry.json"
EXTRACT_REPORT = "bench/reports/live-extract-2026-10-02-v4.json"
AGENT_REPORT = "bench/reports/live-agent-2026-10-02-v3.json"
GO_ARTIFACT = "bench/reports/go-acceptance-{date}.txt"
TRANSITIONS_REPORT = "bench/reports/transitions-gold-2026-10-03.json"
RECIPIENT_CHECKS = ("priya_included", "marco_included", "sam_included", "elena_not_forced")
ABSTENTION_CHECKS = ("waited", "no_action")
TOOL_CAP = 6
GO_TESTS = ("TestWorldIdentityClusteringMatchesGold|TestWorldActivityEdgesMatchAnIndependentExpectation|"
            "TestGoldAcceptance")
GO_PACKAGES = ("./internal/graph", "./internal/coalesce")

Runner = Callable[[list[str], Path], str]


class SourceError(ValueError):
    """A source file or tool output does not have the expected shape."""


class PR(NamedTuple):
    precision: float
    recall: float
    n: int


def load_json(path: Path) -> Any:
    return json.loads(Path(path).read_text(encoding="utf-8"))


def load_registry(path: Path = REGISTRY_PATH) -> dict:
    return load_json(path)


def ratio_of(text: str) -> tuple[int, int]:
    match = re.fullmatch(r"(\d+)/(\d+)", text.strip())
    if not match:
        raise SourceError(f"not a ratio: {text!r}")
    num, den = int(match.group(1)), int(match.group(2))
    if num > den:
        raise SourceError(f"ratio above 1: {text!r}")
    return num, den


def _search(pattern: str, text: str, what: str) -> re.Match[str]:
    match = re.search(pattern, text)
    if not match:
        raise SourceError(f"{what}: pattern not found")
    return match


# ---- Go acceptance output (go test -v)

def parse_go_output(text: str) -> dict:
    ident = _search(r"identity clustering: .*?gold pairs (\d+), predicted pairs \d+, precision ([\d.]+), "
                    r"recall ([\d.]+)", text, "identity")
    out: dict = {"identity": PR(float(ident.group(2)), float(ident.group(3)), int(ident.group(1)))}
    for edge in ("about", "involves"):
        m = _search(rf"\b{edge}: expected (\d+), predicted \d+, precision ([\d.]+), recall ([\d.]+)", text, edge)
        out[edge] = PR(float(m.group(2)), float(m.group(3)), int(m.group(1)))
    total = _search(r"TOTAL\s+(\d+/\d+) = [\d.]+%\s+(\d+/\d+) = [\d.]+%\s+(\d+/\d+) = [\d.]+%", text, "state TOTAL")
    out["knowable"], out["all_fields"], out["required_fields"] = (ratio_of(total.group(i)) for i in (1, 2, 3))
    group = _search(r"buying group \+ coverage gaps \(all facts\): (\d+/\d+)", text, "buying group")
    out["buying_group"] = ratio_of(group.group(1))
    return out


def go_artifact(date: str) -> str:
    return GO_ARTIFACT.format(date=date)


def subprocess_runner(cmd: list[str], cwd: Path) -> str:
    done = subprocess.run(cmd, cwd=cwd, capture_output=True, text=True, check=False, timeout=1800)
    if done.returncode != 0:
        raise SourceError(f"{' '.join(cmd)} failed ({done.returncode}): {done.stdout[-2000:]}{done.stderr[-2000:]}")
    return done.stdout


def run_go(date: str, runner: Runner = subprocess_runner, root: Path = ROOT) -> str:
    """Run the WP5/WP6 acceptance tests (embedded Postgres, about a minute) save their -v output; returns its path."""
    cmd = ["go", "test", "-p", "1", "-count=1", "-v", "-run", GO_TESTS, *GO_PACKAGES]
    output = runner(cmd, root / "core-go")
    parse_go_output(output)  # fail before writing an artifact the report cannot read
    rel = go_artifact(date)
    (root / rel).write_text(output, encoding="utf-8", newline="\n")
    return rel


def read_go_artifact(rel: str, root: Path = ROOT) -> dict:
    path = root / rel
    if not path.is_file():
        raise SourceError(f"{rel} missing: run with --run-go to create it")
    return parse_go_output(path.read_text(encoding="utf-8"))


# ---- transition detector vs the transition gold (HAR-126; `ghostctl transitions-eval`)

TRANSITION_KEYS = ("headline_uncontested", "state_transition_support_check")


def transitions_report(path: Path) -> dict:
    doc = load_json(path)
    missing = [k for k in TRANSITION_KEYS if k not in doc]
    if missing:
        raise SourceError(f"{path}: not a transitions-eval report (missing {missing})")
    return doc


# ---- live benchmark reports

def extract_summary(path: Path) -> dict:
    summary = load_json(path).get("summary")
    if not isinstance(summary, dict) or "fact_recall" not in summary:
        raise SourceError(f"{path}: no summary.fact_recall")
    return summary


def agent_report(path: Path) -> dict:
    doc = load_json(path)
    if "situations" not in doc.get("summary", {}) or "raw" not in doc:
        raise SourceError(f"{path}: not a live-agent report")
    return doc


def completion(report: dict) -> tuple[int, int]:
    situations = report["summary"]["situations"].values()
    return sum(sum(s["actions"].values()) for s in situations), sum(s["runs"] for s in situations)


def check_totals(report: dict, names: Iterable[str]) -> tuple[int, int]:
    passed = total = 0
    for situation in report["summary"]["situations"].values():
        for name, value in situation["checks"].items():
            if name in names:
                num, den = ratio_of(value)
                passed, total = passed + num, total + den
    return passed, total


def runs_at_tool_cap(report: dict, cap: int = TOOL_CAP) -> tuple[int, int]:
    done = [r for runs in report["raw"].values() for r in runs if r.get("action")]
    return sum(1 for r in done if r.get("tool_calls") == cap), len(done)
