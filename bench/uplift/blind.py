"""Blind pairwise review support: pairs_blind.jsonl (unlabelled, randomised) and pairs_key.json (apart).

Only the files and their schemas (contracts/har129/abc_pairs_blind.v1.json, abc_pairs_key.v1.json); the web review page
is a later item. A pair holds the FULL first drafts of two arms of one situation, left and right chosen by a seeded
coin that depends only on the pair id, so the side says nothing about the arm. The blind file carries no arm label,
no knowledge id, no model and no score; the key file says which arm is on which side and must not reach the reviewer.
"""
from __future__ import annotations

import hashlib
import json
from collections.abc import Mapping, Sequence
from pathlib import Path
from typing import Any

ROOT = Path(__file__).resolve().parents[2]
# which comparisons a situation kind yields: discriminating B vs A (and B vs C where C ran), control C vs A; an exception
# situation yields none (nothing to prefer: neither draft should apply the knowledge)
COMPARISONS = {"discriminating": (("B", "A", "B_vs_A"), ("B", "C", "B_vs_C")), "control": (("C", "A", "C_vs_A"),)}


def _people(context: Mapping[str, Any]) -> list[dict[str, Any]]:
    """What the reviewer may see of a person: id, name, title and side. Never an address."""
    return [{"person_id": p["person_id"], "name": p["name"], "title": p.get("title") or None, "side": p["side"]}
            for p in context["people"]]


def _names(context: Mapping[str, Any]) -> dict[str, str]:
    return {p["person_id"]: p["name"] for p in context["people"]}


def draft_view(candidate: Mapping[str, Any], names: Mapping[str, str]) -> dict[str, Any]:
    art = candidate.get("full_action_artifact") or {}
    who = lambda rs: [names.get(r["person_id"], "unknown person") for r in rs or []]  # noqa: E731
    return {"strategy_type": candidate["strategy_type"], "title": candidate.get("title", ""),
            "description": candidate.get("description", ""), "action_type": candidate["action_type"],
            "to": who(candidate.get("to")), "cc": who(candidate.get("cc")), "channel": art.get("channel", ""),
            "subject": art.get("subject"), "body": art.get("body", "")}


def _left_is_first(seed: int, pair_id: str) -> bool:
    return hashlib.sha256(f"{seed}|{pair_id}".encode()).digest()[0] % 2 == 0


def build_pairs(arms: Mapping[str, Any], pack: Mapping[str, Any], seed: int) -> tuple[list[dict[str, Any]], dict[str, Any]]:
    """(pairs, key): B vs A and B vs C on discriminating situations, C vs A on control situations."""
    by_id = {s["id"]: s for s in arms["situations"]}
    pairs: list[dict[str, Any]] = []
    key: list[dict[str, Any]] = []
    for ps in pack["situations"]:
        if ps["kind"] not in COMPARISONS:
            continue
        got = by_id[ps["id"]]
        context = got["context"]
        names = _names(context)
        for first, second, comparison in COMPARISONS[ps["kind"]]:
            if not got["arms"].get(first) or not got["arms"].get(second):
                continue
            pair_id = f"P{len(pairs) + 1:03d}"
            left, right = (first, second) if _left_is_first(seed, pair_id) else (second, first)
            pairs.append({"pair_id": pair_id,
                          "situation": {"state_header": context["state_header"], "trigger_summary": context["trigger_summary"],
                                        "people": _people(context)},
                          "left": draft_view(got["arms"][left]["candidate"], names),
                          "right": draft_view(got["arms"][right]["candidate"], names)})
            key.append({"pair_id": pair_id, "situation_id": ps["id"], "comparison": comparison, "left_arm": left,
                        "right_arm": right})
    return pairs, {"version": "abc_pairs_key.v1", "seed": seed, "pairs": key}


def write(out_dir: Path, pairs: Sequence[Mapping[str, Any]], key: Mapping[str, Any]) -> None:
    out_dir.mkdir(parents=True, exist_ok=True)
    (out_dir / "pairs_blind.jsonl").write_text("".join(json.dumps(p, ensure_ascii=False) + "\n" for p in pairs),
                                               encoding="utf-8", newline="\n")
    (out_dir / "pairs_key.json").write_text(json.dumps(key, indent=2, ensure_ascii=False) + "\n", encoding="utf-8", newline="\n")


def validate(pairs: Sequence[Mapping[str, Any]], key: Mapping[str, Any]) -> list[str]:
    from jsonschema import Draft202012Validator

    base = ROOT / "contracts" / "har129"
    pair_schema = json.loads((base / "abc_pairs_blind.v1.json").read_text(encoding="utf-8"))
    key_schema = json.loads((base / "abc_pairs_key.v1.json").read_text(encoding="utf-8"))
    problems = [f"pair {p.get('pair_id')}: {e.message}" for p in pairs for e in Draft202012Validator(pair_schema).iter_errors(p)]
    problems += [f"key: {e.message}" for e in Draft202012Validator(key_schema).iter_errors(key)]
    ids = [p["pair_id"] for p in pairs]
    if ids != [k["pair_id"] for k in key["pairs"]]:
        problems.append("the key does not list exactly the blind pairs, in order")
    return problems
