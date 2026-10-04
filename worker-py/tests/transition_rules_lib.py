"""Shared helpers for the transition rule set (contracts/transitions/rules.v1.json, ADR-0012): condition iteration,
gate keys and literal-vocabulary checks. The reference evaluator is transition_reference.py."""
from __future__ import annotations

from collections.abc import Iterator

from test_contracts import CONTRACTS, SCHEMAS, load_json

RULES = load_json(CONTRACTS / "transitions" / "rules.v1.json")
_STATE_DEFS = load_json(SCHEMAS / "account_state.v1.json")["$defs"]
_STANDING = load_json(SCHEMAS / "common.v1.json")["$defs"]["standing"]["enum"]


def conditions(fact: dict) -> Iterator[dict]:
    for group in fact["any_of"]:
        yield from group["all_of"]


def gate_keys(rule: dict) -> set[str]:
    """Facts the CANDIDATE gate reads."""
    return set(rule["candidate"]["requires_any"]) | set(rule["candidate"]["min_satisfied"]["of"])


def vocabulary(rules: dict, cond: dict) -> list[str] | None:
    """The allowed literals for a condition's value, or None when the path has no declared vocabulary."""
    path = cond["path"]
    if cond["op"] == "has_item":
        return _STATE_DEFS["itemKind"]["enum"]
    if path.endswith(".standing"):
        return _STANDING
    return {
        "state.champion_status": _STATE_DEFS["championStatus"]["enum"],
        "state.relationship_risk": _STATE_DEFS["relationshipRisk"]["enum"],
        "state.motion": rules["vocabularies"]["motion"],
    }.get(path)


def literal_errors(rules: dict) -> list[str]:
    """Every eq/neq/in/has_item literal must belong to its path's vocabulary (catches typos such as 'actve')."""
    errors = []
    for rule in rules["transitions"]:
        for fact in rule["facts"] + rule["contradictions"]:
            for cond in conditions(fact):
                if "value" not in cond:
                    continue
                allowed = vocabulary(rules, cond)
                values = cond["value"] if isinstance(cond["value"], list) else [cond["value"]]
                for value in values:
                    if allowed is None or value not in allowed:
                        errors.append(f"{rule['id']}.{fact['key']}: {cond['path']} {value!r}")
    return errors
