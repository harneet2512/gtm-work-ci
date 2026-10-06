# ruff: noqa: F403,F405,E402
"""Blind gold set for the state-transition detector, labelled from the HAR-97 pitch excerpt only.

Run: python worker-py/tests/transition_gold_blind.py -> writes fixtures/gold/transitions/blind.json. Authored by a separate agent from the pitch alone (it never saw the rules or the detector); only the three path lines were edited.
Validates every claim, signal (minus the harness-only `open` flag) and AccountState against schemas/.
"""
from __future__ import annotations

import json
import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))
from blind_world import *  # noqa: F403,E402
import blind_cases_a  # noqa: F401,E402
import blind_cases_b  # noqa: F401,E402

# ---------------------------------------------------------------- validate + write
def validate(doc):
    from jsonschema import Draft202012Validator
    from referencing import Registry, Resource

    schemas = {p.name: json.loads(p.read_text(encoding="utf-8")) for p in (HERE.parents[1] / "contracts" / "schemas").glob("*.json")}
    reg = Registry()
    for name, sch in schemas.items():
        res = Resource.from_contents(sch)
        reg = reg.with_resource(sch["$id"], res).with_resource(name, res)
    v = {n: Draft202012Validator(schemas[n], registry=reg) for n in ("claim.v1.json", "signal.v1.json", "account_state.v1.json")}
    errors = []
    for s in doc["scenarios"]:
        for i, step in enumerate(s["steps"]):
            inp = step["input"]
            for c in inp["claims"]:
                errors += [f"{s['id']}[{i}] claim {c['field_path']}: {e.message}" for e in v["claim.v1.json"].iter_errors(c)]
            for sg_ in inp["signals"]:
                body = {k: val for k, val in sg_.items() if k != "open"}
                errors += [f"{s['id']}[{i}] signal {sg_['signal_type']}: {e.message}" for e in v["signal.v1.json"].iter_errors(body)]
            errors += [f"{s['id']}[{i}] state: {e.message}" for e in v["account_state.v1.json"].iter_errors(inp["state"])]
    return errors


def check(doc):
    ids = [s["id"] for s in doc["scenarios"]]
    assert len(ids) == len(set(ids)), "duplicate scenario ids"
    for s in doc["scenarios"]:
        nows = [ts(st["input"]["now"]) for st in s["steps"]]
        assert nows == sorted(nows), f"{s['id']}: now decreases"
        assert s["steps"][0]["input"]["state"]["relationship_state"]["value"] == s["initial_state"]
        for st in s["steps"]:
            g = st["gold"]
            assert g["status"] in (None, "CONFIRMED", "CANDIDATE", "UNRESOLVED", "REJECTED")
            assert ("to_state" in g) == (g["status"] is not None)
            assert not g["closed"] or g["status"] == "UNRESOLVED"


if __name__ == "__main__":
    doc = {"gold_version": 1, "scenarios": SCENARIOS}
    check(doc)
    errs = validate(doc)
    for e in errs[:40]:
        print("SCHEMA:", e)
    out = HERE.parents[1] / "fixtures" / "gold" / "transitions" / "blind.json"
    out.write_text(json.dumps(doc, indent=2), encoding="utf-8", newline="\n")
    steps = sum(len(s["steps"]) for s in SCENARIOS)
    print(f"wrote {out.name}: {len(SCENARIOS)} scenarios, {steps} steps, "
          f"{sum(1 for s in SCENARIOS if 'contested' in s)} contested, "
          f"{sum(1 for s in SCENARIOS if s['rationale'].startswith('NEGATIVE'))} negative, schema errors: {len(errs)}")
