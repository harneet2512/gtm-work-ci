"""Experiment-store guard for the knowledge-uplift run (spec §g, HAR-129 A/B/C).

Knowledge may come ONLY from learning on previous deals. Before that learning starts the
experiment's knowledge store must be empty; afterwards every item must
  - validate against contracts/schemas/knowledge.v1.json;
  - cite only previous-deal decision episodes;
  - not be seed_history or manual; a human_delta item must come from a REAL human decision record;
  - not be seeded demo knowledge, by id or key OR by wording: K17 and every knowledge item in
    contracts/examples and fixtures/evals, plus PR #23's lessons (worker-py/tests/knowledge_lessons.py)
    and knowledge cassettes (worker-py/cassettes/knowledge). K17 and the reorg lesson restate
    eval_known rules, so loading them would pre-teach the answer and steer arm A.

WIRING (not done here): the HAR-128 experiment harness must call assert_empty_before_learning before
previous-deal learning and assert_experiment_store before every A/B/C arm, as a HARD block (the run
aborts), not a warning.
"""
from __future__ import annotations

import json
import re
from collections.abc import Iterable, Mapping, Sequence
from dataclasses import dataclass
from pathlib import Path
from typing import Any

from .leakcheck import SHINGLE, normalize, strings

ROOT = Path(__file__).resolve().parents[2]
FORBIDDEN_ORIGINS = frozenset({"seed_history", "manual"})
_PRINCIPLE = re.compile(r'principle\s*=\s*((?:"(?:[^"\\]|\\.)*"\s*)+)', re.S)


class ExperimentLeak(AssertionError):
    """The experiment store holds knowledge that did not come from previous-deal learning."""


@dataclass(frozen=True)
class LearningRun:
    previous_episode_ids: frozenset[str]  # decision episodes of deals closed before the WP31 cutoff
    human_decided_episode_ids: frozenset[str]  # episodes with a recorded human decision (human_decisions row)


@dataclass(frozen=True)
class Seeded:
    ids: frozenset[str]
    texts: frozenset[str]  # normalised wording of every seeded lesson / knowledge item


def _knowledge_items(node: Any) -> Iterable[Mapping[str, Any]]:
    if isinstance(node, dict):
        if "situation_signature" in node and "guidance" in node:
            yield node
        for v in node.values():
            yield from _knowledge_items(v)
    elif isinstance(node, list):
        for v in node:
            yield from _knowledge_items(v)


def _wording(item: Mapping[str, Any]) -> list[str]:
    g = item.get("guidance") or {}
    return [str(item.get("title", "")), str(g.get("summary", "")), *map(str, g.get("do", [])), *map(str, g.get("dont", []))]


def seeded_knowledge(root: Path = ROOT) -> Seeded:
    ids: set[str] = set()
    texts: set[str] = set()
    for base in (root / "contracts" / "examples", root / "fixtures" / "evals"):
        for path in base.rglob("*.json") if base.exists() else ():
            for item in _knowledge_items(json.loads(path.read_text(encoding="utf-8"))):
                ids.update(str(item[k]) for k in ("id", "key") if item.get(k))
                texts.update(normalize(t) for t in _wording(item) if t)
    lessons = root / "worker-py" / "tests" / "knowledge_lessons.py"
    if lessons.exists():
        for m in _PRINCIPLE.finditer(lessons.read_text(encoding="utf-8")):
            texts.add(normalize("".join(re.findall(r'"((?:[^"\\]|\\.)*)"', m.group(1)))))
    cassettes = root / "worker-py" / "cassettes" / "knowledge"
    for path in sorted(cassettes.glob("*.json")) if cassettes.exists() else ():
        texts.update(normalize(t) for t in strings(json.loads(path.read_text(encoding="utf-8"))) if len(t.split()) >= SHINGLE)
    return Seeded(frozenset(ids), frozenset(t for t in texts if t))


def _shingles(text: str) -> set[str]:
    w = normalize(text).split()
    return {" ".join(w[i:i + SHINGLE]) for i in range(max(len(w) - SHINGLE + 1, 1))} if w else set()


def _copies_seeded_wording(item: Mapping[str, Any], seeded: Seeded) -> bool:
    mine = set().union(*(_shingles(t) for t in _wording(item) if t)) if _wording(item) else set()
    return any(sh and f" {sh} " in f" {t} " for t in seeded.texts for sh in mine)


def _validate(item: Mapping[str, Any], name: str) -> None:
    from jsonschema import Draft202012Validator  # bench-only dependency (worker-py test extra)
    from referencing import Registry, Resource

    schemas = ROOT / "contracts" / "schemas"
    docs = [json.loads(p.read_text(encoding="utf-8")) for p in schemas.glob("*.json")]
    registry = Registry().with_resources([(d["$id"], Resource.from_contents(d)) for d in docs])
    schema = json.loads((schemas / "knowledge.v1.json").read_text(encoding="utf-8"))
    errors = list(Draft202012Validator(schema, registry=registry).iter_errors(item))
    if errors:
        raise ExperimentLeak(f"{name} is not valid knowledge.v1.json: {errors[0].message}")


def assert_empty_before_learning(store: Sequence[Mapping[str, Any]]) -> None:
    if store:
        names = sorted(str(i.get("key") or i.get("id")) for i in store)
        raise ExperimentLeak(f"knowledge store must be empty before previous-deal learning, holds {names}")


def admit(item: Mapping[str, Any], run: LearningRun, seeded: Seeded) -> None:
    """Raise unless the item is valid knowledge learned from previous-deal episodes only."""
    name = str(item.get("key") or item.get("id"))
    if {str(item.get("id")), str(item.get("key"))} & seeded.ids:
        raise ExperimentLeak(f"{name} is seeded demo knowledge; it is excluded from the experiment store")
    _validate(item, name)
    prov = item.get("provenance") or {}
    if prov.get("created_from") in FORBIDDEN_ORIGINS:
        raise ExperimentLeak(f"{name} was not learned (created_from {prov.get('created_from')})")
    if prov.get("created_from") == "human_delta" and prov.get("source_decision_episode_id") not in run.human_decided_episode_ids:
        raise ExperimentLeak(f"{name} claims human_delta without a recorded human decision for its episode")
    episodes = set(item.get("supporting_decision_episode_ids") or [])
    if not episodes or not episodes <= run.previous_episode_ids:
        raise ExperimentLeak(f"{name} cites episodes outside the previous deals: {sorted(episodes - run.previous_episode_ids)}")
    if _copies_seeded_wording(item, seeded):
        raise ExperimentLeak(f"{name} copies seeded wording (an 8-word run of a seeded lesson)")


def assert_experiment_store(store: Sequence[Mapping[str, Any]], run: LearningRun, seeded: Seeded | None = None) -> None:
    """Every item in the store after learning must pass admit()."""
    known = seeded_knowledge() if seeded is None else seeded
    for item in store:
        admit(item, run, known)
