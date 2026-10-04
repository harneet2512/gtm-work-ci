"""The frozen inputs of the experiment (committed under bench/uplift/frozen/) and their hashes.

`prepare` writes them from the git-ignored data; everything downstream (the arms, the report, CI replay) reads only
these files and the committed cassettes, so a report is reproducible without the data.
"""
from __future__ import annotations

import hashlib
import json
from collections.abc import Mapping, Sequence
from pathlib import Path
from typing import Any

from bench.synthetic import rules as R

from .learn import canonical_sha256
from .select import Situation, order

ROOT = Path(__file__).resolve().parents[2]
FROZEN = Path(__file__).resolve().parent / "frozen"
CASSETTES = Path(__file__).resolve().parent / "cassettes"
LEARNING_PATH = FROZEN / "learning.v1.json"
SITUATIONS_PATH = FROZEN / "situations.v1.json"
PACK_PATH = FROZEN / "pack.v1.json"
MANIFEST_PATH = FROZEN / "manifest.v1.json"
RUNTIME_PATH = FROZEN / "runtime.v1.json"  # the model and planner budget the cassettes were recorded with
SEED = 20261004


def sha256_file(path: Path) -> str:
    return hashlib.sha256(path.read_bytes()).hexdigest()


def write_json(path: Path, doc: Any, *, indent: int | None = 1) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(json.dumps(doc, indent=indent, ensure_ascii=False, sort_keys=indent is not None) + "\n", encoding="utf-8", newline="\n")


def read_json(path: Path) -> Any:
    return json.loads(path.read_text(encoding="utf-8"))


def cassette_digest(directory: Path = CASSETTES) -> dict[str, Any]:
    """sha256 over the sorted (file name, file sha256) of every cassette, and their count."""
    h = hashlib.sha256()
    files = sorted(directory.glob("*.json")) if directory.exists() else []
    for p in files:
        h.update(p.name.encode("utf-8"))
        h.update(hashlib.sha256(p.read_bytes()).digest())
    return {"count": len(files), "sha256": h.hexdigest()}


def answered_models(directory: Path = CASSETTES) -> list[str]:
    models = set()
    for p in sorted(directory.glob("*.json")) if directory.exists() else []:
        model = read_json(p).get("response", {}).get("model")
        if model:
            models.add(str(model))
    return sorted(models)


def manifest(export_sha: str, synthetic_manifest_sha: str, split_path: Path, learning: Mapping[str, Any]) -> dict[str, Any]:
    doc = R.load_doc()
    return {"version": "uplift_manifest.v1", "seed": SEED, "base_export_sha256": export_sha,
            "synthetic_manifest_sha256": synthetic_manifest_sha, "rules_version": doc["rule_set_version"],
            "rules_sha256": R.content_hash(doc), "split_sha256": sha256_file(split_path),
            "learning_sha256": canonical_sha256(learning)}


def run_ids(situations: Sequence[Mapping[str, Any]], plan: Mapping[tuple[str, str], int] | None) -> list[str]:
    """The situation ids a (possibly call-budget-limited) run covers: per (kind, point) the first n of the fixed seeded order.
    No plan means every situation."""
    if plan is None:
        return [s["id"] for s in situations]
    chosen: list[str] = []
    for key, n in plan.items():
        pool = [s for s in situations if (s["kind"], s["decision_point"]) == key]
        ranked = order([_as_situation(s) for s in pool], SEED)
        chosen += [s.id for s in ranked[:n]]
    return chosen


def _as_situation(s: Mapping[str, Any]) -> Situation:
    from .select import parse_time

    t = s["trigger"]
    return Situation(s["id"], s["kind"], s["decision_point"], s["deal_id"], s["account_id"], t["source_object_id"],
                     t["source_event_key"], parse_time(t["occurred_at"]), s["selection_reason"])
