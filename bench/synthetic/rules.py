"""Loader for the planted-rule registry (rules.v1.json) and the planted outcome model."""
from __future__ import annotations

import hashlib
import json
import math
from collections.abc import Iterable, Mapping
from dataclasses import dataclass
from pathlib import Path
from types import MappingProxyType
from typing import Any

HERE = Path(__file__).resolve().parent
RULES_PATH = HERE / "rules.v1.json"
SCHEMA_PATH = HERE / "rules.schema.json"
KINDS = ("effect", "null", "exception")
GROUPS = ("eval_known", "hidden_company_specific", "control")


@dataclass(frozen=True)
class Rule:
    key: str
    kind: str
    group: str
    statement: str
    feature: str
    log_odds: float
    odds_ratio: float
    prevalence_target: float
    modifies: str | None
    evals: tuple[str, ...]
    transitions: tuple[Mapping[str, Any], ...]
    timescale: tuple[str, ...]


@dataclass(frozen=True)
class RuleSet:
    version: str
    provenance: str
    status: str
    generation_seed: int
    outcome: Mapping[str, Any]
    gates: Mapping[str, Any]
    rules: tuple[Rule, ...]


def _rule(raw: Mapping[str, Any]) -> Rule:
    ex = raw["exercises"]
    return Rule(
        key=raw["key"], kind=raw["kind"], group=raw["group"], statement=raw["statement"], feature=raw["feature"],
        log_odds=float(raw["log_odds"]), odds_ratio=float(raw["odds_ratio"]),
        prevalence_target=float(raw["prevalence_target"]), modifies=raw.get("modifies"),
        evals=tuple(ex["evals"]), transitions=tuple(MappingProxyType(dict(t)) for t in ex["transitions"]),
        timescale=tuple(ex["timescale"]),
    )


def _check(rules: tuple[Rule, ...]) -> None:
    """Invariants the JSON Schema cannot express. Raises ValueError naming the rule."""
    keys = [r.key for r in rules]
    if len(set(keys)) != len(keys):
        raise ValueError("duplicate rule keys")
    effects = {r.key for r in rules if r.kind == "effect"}
    for r in rules:
        if r.kind not in KINDS or r.group not in GROUPS:
            raise ValueError(f"{r.key}: unknown kind {r.kind!r} or group {r.group!r}")
        if (r.kind == "null") != (r.group == "control"):
            raise ValueError(f"{r.key}: null rules, and only they, are the control group")
        if not math.isclose(r.odds_ratio, math.exp(r.log_odds), rel_tol=0.01):
            raise ValueError(f"{r.key}: odds_ratio {r.odds_ratio} != exp(log_odds)")
        if r.kind == "null" and r.log_odds != 0.0:
            raise ValueError(f"{r.key}: a null rule has log_odds 0")
        if (r.modifies is not None) != (r.kind == "exception"):
            raise ValueError(f"{r.key}: only exceptions modify a rule")
        if r.modifies is not None and r.modifies not in effects:
            raise ValueError(f"{r.key}: modifies unknown effect rule {r.modifies!r}")


NEVER_WAIVED = ("null_incremental_auc", "cc_incremental_auc")  # the null gates are never waived


def check_exceptions(doc: Mapping[str, Any], rules: tuple[Rule, ...]) -> None:
    """A gate exception may never waive a failure that names a hidden rule (only hidden rules back learning
    claims) or a null gate (null_incremental_auc, cc_incremental_auc)."""
    hidden = [r.key for r in rules if r.group == "hidden_company_specific"]
    for e in doc.get("gate_exceptions", []):
        for text in ([e["failure"]] if "failure" in e else []) + list(e.get("failures", ())):
            if any(k in text for k in hidden):
                raise ValueError(f"gate exception {text!r} names a hidden rule: it can never be waived")
            if any(k in text for k in NEVER_WAIVED):
                raise ValueError(f"gate exception {text!r} names a null gate: it can never be waived")


def load_doc(path: Path = RULES_PATH) -> dict[str, Any]:
    return json.loads(path.read_text(encoding="utf-8"))


def load_rules(path: Path = RULES_PATH) -> RuleSet:
    doc = load_doc(path)
    rules = tuple(_rule(r) for r in doc["rules"])
    _check(rules)
    check_exceptions(doc, rules)
    return RuleSet(
        version=doc["rule_set_version"], provenance=doc["provenance"], status=doc["status"],
        generation_seed=int(doc["generation_seed"]),
        outcome=MappingProxyType(dict(doc["outcome"])), gates=MappingProxyType(dict(doc["learnability_gates"])),
        rules=rules,
    )


def planted_log_odds(rule_set: RuleSet, holding: Iterable[str]) -> float:
    """Log-odds of a win from the rules that hold for a deal, without the account offset.

    An exception contributes only together with the rule it modifies. Unknown keys raise KeyError
    so a typo in the generator can never silently drop an effect.
    """
    by_key = {r.key: r for r in rule_set.rules}
    active = set(holding)
    unknown = active - by_key.keys()
    if unknown:
        raise KeyError(f"unknown rule keys: {sorted(unknown)}")
    total = float(rule_set.outcome["base_log_odds"])
    for key in sorted(active):
        rule = by_key[key]
        if rule.kind == "exception" and rule.modifies not in active:
            continue
        total += rule.log_odds
    return total


HASH_EXCLUDED = ("frozen_sha256", "changelog")  # notes never stale the pinned report or the freeze


def content_hash(doc: Mapping[str, Any]) -> str:
    """sha256 of the canonical rule file, excluding the pinned hash itself and the changelog (notes)."""
    body = {k: v for k, v in doc.items() if k not in HASH_EXCLUDED}
    canonical = json.dumps(body, sort_keys=True, separators=(",", ":"), ensure_ascii=False)
    return hashlib.sha256(canonical.encode("utf-8")).hexdigest()
