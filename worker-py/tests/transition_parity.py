"""Parity cases for the transition detector (ADR-0012): inputs plus the Python reference evaluator's outcome.

`fixtures/transitions/parity_cases.json` holds every case. The Go detector (core-go/internal/transitions) must produce
the same outcome for each input (parity_test.go), and test_transition_parity.py fails when the committed file differs
from what this module generates, so neither side can drift alone.

    python worker-py/tests/transition_parity.py --write      # regenerate the committed file

Cases: the named scenarios of the reference-evaluator tests, then a seeded grid (SEED) that varies the relationship
state, champion facts, signals, claims, standings, open transition and rule set across the status space."""
from __future__ import annotations

import argparse
import json
import random
import re
import sys
import uuid
from collections import Counter
from datetime import datetime, timedelta, timezone
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))

import transition_builders as tb  # noqa: E402
from transition_reference import Context, Outcome, evaluate, ts  # noqa: E402
from transition_rules_lib import RULES  # noqa: E402

ROOT = Path(__file__).resolve().parents[2]
OUT = ROOT / "fixtures" / "transitions" / "parity_cases.json"
SEED, GRID_CASES, LEANING_CASES = 126, 450, 200
BASE = ts(tb.NOW)

SIGNAL_TYPES = ["expansion_interest", "new_stakeholder_entered", "product_usage_increased", "stage_advanced", "stage_regressed",
                "pricing_interest", "support_risk_spike", "customer_went_silent", "champion_weakened", "champion_delegated",
                "champion_reactivated"]
CLAIM_PATHS = ["expansion_need", "business_value", "org_change", "buying_group.member", "delegation", "owner"]
STANDINGS = ["first_party_ai", "crm_explicit", "human_approved", "third_party", "first_party_record"]


def iso(moment: datetime) -> str:
    return moment.astimezone(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")


def fact_json(f) -> dict:
    return {"key": f.key, "required": f.required, "rejects": f.rejects,
            "evidence": [[r.get("activity_id"), r.get("claim_id")] for r in f.evidence_refs],
            "signal_ids": list(dict.fromkeys(f.signal_ids))}


def outcome_json(o: Outcome) -> dict:
    return {"status": o.status, "to_state": o.to_state, "rule_id": o.rule_id, "confidence": o.confidence, "closed": o.closed,
            "confirmed_at": o.confirmed_at, "supporting": [fact_json(f) for f in o.supporting],
            "missing": [fact_json(f) for f in o.missing], "contradicting": [fact_json(f) for f in o.contradicting]}


def case(name: str, rules_key: str, st: dict, signals, claims, now: str, open_transition, computed_at, deals=()) -> dict:
    ctx = {"state": st, "signals": list(signals), "claims": list(claims), "now": now, "computed_at": computed_at,
           "open_transition": open_transition, "deals": list(deals)}
    return {"name": name, "rules": rules_key, "input": ctx}


RULESETS = {"v1": lambda: RULES, "rival": tb.rival_rules, "rival_gate_moved": tb.rival_gate_moved}


def named_cases() -> list[dict]:
    st, signals, claims = tb.confirmed_case()
    unresolved_from = {"status": "UNRESOLVED", "to_state_candidate": None, "last_updated_at": "2026-09-25T00:00:00Z"}
    stale = {"status": "UNRESOLVED", "to_state_candidate": None, "last_updated_at": tb.days_before(31)}
    fresh = {"status": "UNRESOLVED", "to_state_candidate": None, "last_updated_at": tb.days_before(29)}
    high = tb.state(relationship_risk=tb.fld("high"))
    return [
        case("candidate", "v1", tb.state(), (), tb.CANDIDATE_CLAIMS, tb.NOW, None, None),
        case("confirmed", "v1", st, signals, claims, tb.NOW, None, "2026-10-01T00:00:07Z"),
        case("confirmed_two_rules", "rival", st, signals, claims, tb.NOW, None, None),
        case("candidate_two_gates", "rival", tb.state(), (), tb.CANDIDATE_CLAIMS, tb.NOW, None, None),
        case("gate_moves_to_another_target", "rival_gate_moved", tb.state(), (), tb.CANDIDATE_CLAIMS, tb.NOW, tb.OPEN_EXPANSION, None),
        case("rejected", "v1", high, (), tb.CANDIDATE_CLAIMS, tb.NOW, tb.OPEN_EXPANSION, None),
        case("no_open_no_reject", "v1", high, (), tb.CANDIDATE_CLAIMS, tb.NOW, None, None),
        case("lost_gate", "v1", tb.state(), (), (tb.NEED,), tb.NOW, tb.OPEN_EXPANSION, None),
        case("unresolved_never_rejected", "v1", high, (), tb.CANDIDATE_CLAIMS, tb.NOW, unresolved_from, None),
        case("stale_closes", "v1", tb.state(), (), (), tb.NOW, stale, None),
        case("fresh_stays", "v1", tb.state(), (), (), tb.NOW, fresh, None),
        case("healthy_account", "v1", tb.state(tb.UNKNOWN_STATE), (), (tb.NEED, tb.STAKEHOLDER), tb.NOW, None, None),
    ]


def _random_state(rng: random.Random) -> dict:
    relationship = rng.choice(["unknown", "REORG", "REORG", "EXPANSION", "RECOVERY"])
    anchor_days = rng.randint(20, 60)
    anchor = iso(BASE - timedelta(days=anchor_days))
    if relationship == "unknown":
        rel = {"value": "unknown", "transition_id": None, "confirmed_at": None}
    else:
        rel = {"value": relationship, "transition_id": str(uuid.UUID(int=rng.getrandbits(128), version=4)), "confirmed_at": anchor}
    since = iso(BASE - timedelta(days=rng.randint(0, 70)))
    fields = {"champion_status": tb.fld(rng.choice(["active", "active", "weakening", "delegated", "departed"])),
              "champion_since": tb.fld(since, since) if rng.random() < 0.8 else tb.fld("unknown"),
              "relationship_risk": tb.fld(rng.choice(["low", "low", "medium", "high"])),
              "economic_buyer": tb.fld(tb.OTHER, standing=rng.choice(["first_party_ai", "crm_explicit", "third_party"]))
              if rng.random() < 0.4 else tb.fld("unknown"),
              "decision_process": tb.fld("budget review", standing=rng.choice(["first_party_ai", "crm_explicit", "third_party"]))
              if rng.random() < 0.4 else tb.fld("unknown"),
              "motion": tb.fld(rng.choice(["expansion", "renewal", "new_business", "unknown"]), standing="crm_explicit")}
    if rng.random() < 0.2:
        fields["champion"] = tb.fld("unknown")
    if rng.random() < 0.35:
        at = iso(BASE - timedelta(days=rng.randint(0, 60)))
        item = {"kind": rng.choice(["commercial", "technical"]), "status": rng.choice(["open", "overdue", "fulfilled", None]),
                "text": "x", "claim_id": str(uuid.UUID(int=rng.getrandbits(128), version=4)),
                "evidence_refs": [{"activity_id": tb.ACT, "occurred_at": at}]}
        fields["current_commitments"] = {"value": [item], "known": True, "standing": "first_party_ai", "as_of": at,
                                         "evidence_refs": [{"activity_id": tb.ACT, "occurred_at": at}]}
    st = tb.state(rel, **fields)
    if rng.random() < 0.3:
        st["buying_group"][0]["tenure"] = rng.choice(["interim", "permanent", "unknown"])
    if rng.random() < 0.15:
        st["buying_group"][0]["status"] = rng.choice(["departed", "weakening", "inactive", "new"])
    if rng.random() < 0.2:
        st["buying_group"][0]["roles"] = ["user"]
    return st


def _anchor_days(st: dict) -> int:
    confirmed = st["relationship_state"]["confirmed_at"]
    return (BASE - ts(confirmed)).days if confirmed else 40


def _random_signals(rng: random.Random, span: int) -> list[dict]:
    out = []
    for _ in range(rng.choice([0, 0, 1, 2, 3, 4])):
        created = iso(BASE - timedelta(days=rng.randint(-2, span + 10)))
        s = tb.sig(rng.choice(SIGNAL_TYPES), created, rng.random() < 0.8, rng.choice([None, tb.CHAMPION, tb.OTHER]))
        if rng.random() < 0.2:
            s["first_party"] = rng.random() < 0.3  # assessed: mostly third-party-derived, sometimes explicitly first-party
        if rng.random() < 0.15:
            s["created_at"] = iso(BASE + timedelta(days=rng.randint(1, 400)))  # recorded later than it happened (a replay)
        if rng.random() < 0.3:
            s["opportunity_id"] = rng.choice([tb.DEAL_A, tb.DEAL_B])
        out.append(s)
    return out


def _random_claims(rng: random.Random, span: int) -> list[dict]:
    out = []
    for _ in range(rng.choice([0, 1, 2, 3, 4, 5, 6])):
        occurred = iso(BASE - timedelta(days=rng.randint(-1, span + 10), seconds=rng.randint(0, 86399)))
        c = tb.claim(rng.choice(CLAIM_PATHS), occurred, rng.choice([None, tb.CHAMPION, tb.OTHER]),
                     rng.choice(STANDINGS[:1] * 5 + STANDINGS))
        if rng.random() < 0.1:
            c["status"] = rng.choice(["rejected", "superseded", "outranked", "expired"])
        if rng.random() < 0.12:
            c["withdrawn"] = True
        if rng.random() < 0.3:
            c["opportunity_id"] = rng.choice([tb.DEAL_A, tb.DEAL_B])
        out.append(c)
    return out


def _random_open(rng: random.Random) -> dict | None:
    kind = rng.choice([None, None, None, None, "cand_exp", "cand_exp", "cand_reorg", "cand_reorg", "cand_reorg", "unresolved", "cand_recovery"])
    updated = iso(BASE - timedelta(days=rng.randint(0, 50)))
    return {None: None,
            "cand_exp": {"status": "CANDIDATE", "to_state_candidate": "EXPANSION", "last_updated_at": updated},
            "cand_reorg": {"status": "CANDIDATE", "to_state_candidate": "REORG", "last_updated_at": updated},
            "cand_recovery": {"status": "CANDIDATE", "to_state_candidate": "RECOVERY", "last_updated_at": updated},
            "unresolved": {"status": "UNRESOLVED", "to_state_candidate": None, "last_updated_at": updated}}[kind]


def _random_deals(rng: random.Random, st: dict) -> list[dict]:
    """Per-deal states derived from the account headline with random differences (champion, tenure, buyer, risk, openness)."""
    out = []
    for oid in rng.sample([tb.DEAL_A, tb.DEAL_B], rng.choice([1, 2, 2])):
        d = tb.deal(oid)
        d["fields"] = {k: dict(v) for k, v in st["fields"].items()}
        for name in ("champion_status", "economic_buyer", "decision_process", "relationship_risk"):
            if rng.random() < 0.35:
                d["fields"][name] = rng.choice([tb.fld("unknown"), tb.fld("active"), tb.fld("weakening"), tb.fld("high"), tb.fld(tb.OTHER)])
        if rng.random() < 0.4:
            since = iso(BASE - timedelta(days=rng.randint(0, 70)))
            d["fields"]["champion_since"] = tb.fld(since, since)
        d["buying_group"] = [dict(m) for m in st["buying_group"]]
        if rng.random() < 0.25:
            d["buying_group"][0]["tenure"] = rng.choice(["interim", "permanent", "unknown"])
        d["is_open"] = rng.random() < 0.8
        out.append(d)
    return out


def grid_cases() -> list[dict]:
    rng = random.Random(SEED)
    cases = []
    for i in range(GRID_CASES):
        now = iso(BASE + timedelta(days=rng.randint(-3, 5)))
        computed = iso(ts(now) + timedelta(seconds=rng.randint(0, 600))) if rng.random() < 0.7 else None
        st = _random_state(rng)
        span = _anchor_days(st)
        deals = _random_deals(rng, st) if rng.random() < 0.4 else []
        cases.append(case(f"grid_{i:04d}", rng.choice(["v1", "v1", "v1", "v1", "v1", "rival", "rival_gate_moved"]), st,
                          _random_signals(rng, span), _random_claims(rng, span), now, _random_open(rng), computed, deals))
    return cases


FIXED_IDS = {tb.ACT, tb.CHAMPION, tb.OTHER}
UUID_RE = re.compile(r"[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}")


def stabilize(c: dict) -> dict:
    """Replace the builders' random uuids with ones derived from the case name and order of appearance."""
    text, seen = json.dumps(c["input"], sort_keys=True), {}

    def swap(m: re.Match) -> str:
        if m.group(0) in FIXED_IDS:
            return m.group(0)
        seen.setdefault(m.group(0), str(uuid.uuid5(uuid.NAMESPACE_URL, f"transition-parity/{c['name']}/{len(seen)}")))
        return seen[m.group(0)]

    return {**c, "input": json.loads(UUID_RE.sub(swap, text))}


def leaning_cases() -> list[dict]:
    """Starts from the confirmed REORG -> EXPANSION bundle and perturbs it, so the status space near the boundaries is covered."""
    rng = random.Random(SEED + 1)
    cases = []
    for i in range(LEANING_CASES):
        owner = {"champion_since": tb.fld(iso(BASE - timedelta(days=rng.choice([3, 13, 14, 15, 30]))), None)}
        owner["champion_since"]["as_of"] = owner["champion_since"]["value"]
        st = tb.state(**owner)
        if rng.random() < 0.15:
            st["buying_group"][0]["tenure"] = "interim"
        if rng.random() < 0.1:
            st["fields"]["relationship_risk"] = tb.fld("high")
        when = lambda: iso(BASE - timedelta(days=rng.choice([1, 5, 12, 25, 29, 31, 80, 95])))  # noqa: E731
        claims = [c for c in (tb.claim("expansion_need", when()), tb.claim("buying_group.member", when(), tb.OTHER),
                              tb.claim("business_value", when())) if rng.random() < 0.8]
        signals = [s for s in (tb.sig("stage_advanced", when()), tb.sig("pricing_interest", when()),
                               tb.sig("stage_regressed", when()), tb.sig("customer_went_silent", when(), rng.random() < 0.5),
                               tb.sig("champion_weakened", when()), tb.sig("expansion_interest", when()))
                   if rng.random() < 0.35]
        open_transition = rng.choice([None, None, tb.OPEN_EXPANSION, {**tb.OPEN_EXPANSION, "status": "UNRESOLVED", "to_state_candidate": None}])
        deals = _random_deals(rng, st) if rng.random() < 0.4 else []
        cases.append(case(f"lean_{i:04d}", rng.choice(["v1", "v1", "v1", "rival"]), st, signals, claims, tb.NOW, open_transition,
                          iso(BASE + timedelta(seconds=rng.randint(0, 600))), deals))
    return cases


def build() -> dict:
    cases = [stabilize(c) for c in named_cases() + grid_cases() + leaning_cases()]
    for c in cases:
        i = c["input"]
        ctx = Context(i["state"], tuple(i["signals"]), tuple(i["claims"]), i["now"], i["open_transition"], i["computed_at"], tuple(i["deals"]))
        c["expected"] = outcome_json(evaluate(RULESETS[c["rules"]](), ctx))
    rulesets = {name: make() for name, make in RULESETS.items() if name != "v1"}  # v1 is contracts/transitions/rules.v1.json
    return {"rule_set_version": RULES["rule_set_version"], "seed": SEED, "rulesets": rulesets, "cases": cases}


def render() -> str:
    return json.dumps(build(), indent=1, sort_keys=True) + "\n"


if __name__ == "__main__":
    ap = argparse.ArgumentParser()
    ap.add_argument("--write", action="store_true")
    args = ap.parse_args()
    text = render()
    if args.write:
        OUT.parent.mkdir(parents=True, exist_ok=True)
        OUT.write_text(text, encoding="utf-8", newline="\n")
    print(sorted(Counter((c["expected"]["status"], c["expected"]["to_state"]) for c in json.loads(text)["cases"]).items(),
                 key=lambda kv: str(kv[0])))
