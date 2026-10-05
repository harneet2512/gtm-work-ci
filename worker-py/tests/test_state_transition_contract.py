"""HAR-126 / ADR-0012: StateTransition contract, relationship-state vocabulary and the AccountState additions.

A transition's status must be self-consistent (CONFIRMED has confirmed_at and no unmet required fact, REJECTED has a
decisive contradiction and rejected_at, CANDIDATE has a supporting fact and an unmet required one, the target is null
exactly when UNRESOLVED, only a stale UNRESOLVED closes); contradicting evidence is kept on every status; every
satisfied fact points at the claims and activities it rests on."""
from __future__ import annotations

import copy
from collections.abc import Callable

import pytest

from test_contracts import SCHEMAS, _all_errors, _drop, _set, example, load_json, validator

COMMON = load_json(SCHEMAS / "common.v1.json")["$defs"]
TRANSITION = load_json(SCHEMAS / "state_transition.v1.json")
ACCOUNT_STATE = load_json(SCHEMAS / "account_state.v1.json")
NOW = "2026-10-20T09:00:00Z"


def _contradiction(doc: dict, key: str, rejects: bool) -> dict:
    return {"key": key, "description": "Contradicting evidence.", "required": False, "satisfied": True, "rejects": rejects,
            "evidence_refs": copy.deepcopy(doc["supporting_facts"][0]["evidence_refs"])}


def _confirm(doc: dict) -> None:
    """Turn the CANDIDATE example into a valid CONFIRMED transition."""
    for fact in doc["missing_facts"]:
        if fact["required"]:
            fact.update(satisfied=True, evidence_refs=copy.deepcopy(doc["supporting_facts"][0]["evidence_refs"]))
            doc["supporting_facts"].append(fact)
    doc["missing_facts"] = [f for f in doc["missing_facts"] if not f["required"]]
    doc.update(status="CONFIRMED", confirmed_at=NOW, last_updated_at=NOW, confidence=1.0)


def _reject(doc: dict) -> None:
    """Turn the CANDIDATE example into a valid REJECTED transition."""
    doc["contradicting_facts"] = [_contradiction(doc, "support_risk_high", True)]
    doc.update(status="REJECTED", rejected_at=NOW, last_updated_at=NOW)


def _record(doc: dict) -> None:
    """Add a non-decisive contradiction (preserved evidence, HAR-97 L1)."""
    doc["contradicting_facts"].append(_contradiction(doc, "customer_silent", False))


def _unresolve(doc: dict) -> None:
    doc.update(status="UNRESOLVED", to_state_candidate=None)


def _close(doc: dict) -> None:
    _unresolve(doc)
    doc.update(closed_at=NOW, close_reason="stale")


def _then(*steps: Callable[[dict], None]) -> Callable[[dict], None]:
    def apply(doc: dict) -> None:
        for step in steps:
            step(doc)
    return apply


def _only_optional_missing(doc: dict) -> None:
    doc["missing_facts"] = [f for f in doc["missing_facts"] if not f["required"]]


def _set_contradiction(field: str, value: object) -> Callable[[dict], None]:
    return _then(_record, lambda d: d["contradicting_facts"][0].update({field: value}))


# (schema, mutation, instance path the rejection must point at, why)
BAD_CASES = [
    ("state_transition", _set(["status"], "PROBABLE"), ["status"], "status vocabulary"),
    ("state_transition", _set(["from_state"], "reorg"), ["from_state"], "relationship states are upper case"),
    ("state_transition", _set(["to_state_candidate"], "unknown"), ["to_state_candidate"], "unknown is never a target"),
    ("state_transition", _set(["to_state_candidate"], "REORG"), ["to_state_candidate"], "a transition changes state"),
    ("state_transition", _set(["to_state_candidate"], None), ["to_state_candidate"],
     "only an UNRESOLVED transition may lack a target"),
    ("state_transition", _set(["status"], "UNRESOLVED"), ["to_state_candidate"], "an UNRESOLVED transition has no target"),
    ("state_transition", _set(["trigger_activity_ids"], []), ["trigger_activity_ids"], "a transition is triggered by activity"),
    ("state_transition", _set(["confidence"], 1.5), ["confidence"], "confidence is a probability"),
    ("state_transition", _set(["confidence"], 0.4444), ["confidence"], "confidence has three decimals like numeric(4,3)"),
    ("state_transition", _set(["rule_set_version"], "v1"), ["rule_set_version"], "rule set is versioned"),
    ("state_transition", _drop("last_updated_at"), [], "last_updated_at required"),
    # facts
    ("state_transition", _set(["supporting_facts", 0, "satisfied"], False), ["supporting_facts", 0, "satisfied"],
     "a supporting fact is satisfied"),
    ("state_transition", _set(["missing_facts", 0, "satisfied"], True), ["missing_facts", 0, "satisfied"],
     "a missing fact is unsatisfied"),
    ("state_transition", _set(["supporting_facts", 0, "evidence_refs"], []), ["supporting_facts", 0, "evidence_refs"],
     "a satisfied fact rests on evidence"),
    ("state_transition", _set(["supporting_facts", 0, "key"], "New Owner"), ["supporting_facts", 0, "key"],
     "fact keys are rule-set keys"),
    ("state_transition", _set(["supporting_facts", 0, "evidence_refs", 0], {"claim_id": "0c1a0000-0000-4000-8000-000000000201"}),
     ["supporting_facts", 0, "evidence_refs", 0], "evidence names its activity (common evidenceRef)"),
    ("state_transition", lambda d: d["supporting_facts"][0].pop("required"), ["supporting_facts", 0],
     "a fact says whether the rule set requires it"),
    ("state_transition", _set(["supporting_facts", 0, "rejects"], False), ["supporting_facts", 0],
     "only contradictions carry rejects"),
    ("state_transition", _set_contradiction("required", True), ["contradicting_facts", 0, "required"],
     "a contradiction is never a requirement"),
    ("state_transition", _then(_record, lambda d: d["contradicting_facts"][0].pop("rejects")), ["contradicting_facts", 0],
     "a contradiction says whether it is decisive"),
    ("state_transition", _set_contradiction("evidence_refs", []), ["contradicting_facts", 0, "evidence_refs"],
     "a contradiction rests on evidence"),
    # CANDIDATE
    ("state_transition", _set(["supporting_facts"], []), ["supporting_facts"], "a candidate has supporting evidence"),
    ("state_transition", _only_optional_missing, ["missing_facts"],
     "a candidate misses at least one required fact (else it is confirmed)"),
    ("state_transition", _then(_reject, _set(["status"], "CANDIDATE"), _set(["rejected_at"], None)),
     ["contradicting_facts", 0, "rejects"], "a decisively contradicted candidate is rejected"),
    ("state_transition", _set(["confirmed_at"], NOW), ["confirmed_at"], "only a confirmed transition has confirmed_at"),
    ("state_transition", _set(["rejected_at"], NOW), ["rejected_at"], "only a rejected transition has rejected_at"),
    # CONFIRMED
    ("state_transition", _set(["status"], "CONFIRMED"), ["missing_facts", 0, "required"],
     "a confirmed transition has no unmet required fact"),
    ("state_transition", _then(_confirm, _set(["confirmed_at"], None)), ["confirmed_at"], "a confirmed transition has confirmed_at"),
    ("state_transition", _then(_confirm, _reject, _set(["status"], "CONFIRMED"), _set(["rejected_at"], None)),
     ["contradicting_facts", 0, "rejects"], "a confirmed transition has no decisive contradiction"),
    # REJECTED
    ("state_transition", _then(_set(["status"], "REJECTED"), _set(["rejected_at"], NOW)), ["contradicting_facts"],
     "a rejected transition names the contradiction"),
    ("state_transition", _then(_record, _set(["status"], "REJECTED"), _set(["rejected_at"], NOW)), ["contradicting_facts"],
     "a rejected transition needs a decisive contradiction"),
    ("state_transition", _then(_reject, _set(["rejected_at"], None)), ["rejected_at"], "a rejected transition has rejected_at"),
    # UNRESOLVED and closing
    ("state_transition", _then(_unresolve, _set(["confirmed_at"], NOW)), ["confirmed_at"], "an unresolved transition is not confirmed"),
    ("state_transition", _set(["closed_at"], NOW), ["status"], "only an UNRESOLVED transition is closed"),
    ("state_transition", _then(_close, _set(["close_reason"], None)), ["close_reason"], "a closed transition says why"),
    ("state_transition", _set(["close_reason"], "stale"), ["close_reason"], "an open transition has no close reason"),
    # AccountState additions
    ("account_state", _set(["relationship_state", "value"], "expansion"), ["relationship_state", "value"],
     "relationship_state is the earned vocabulary, not CRM motion"),
    ("account_state", _set(["relationship_state", "transition_id"], None), ["relationship_state", "transition_id"],
     "a known relationship state names the confirmed transition that earned it"),
    ("account_state", _set(["relationship_state", "value"], "unknown"), ["relationship_state", "transition_id"],
     "an unknown relationship state has no transition"),
    ("account_state", _set(["open_transition", "status"], "CONFIRMED"), ["open_transition", "status"],
     "open_transition is non-terminal"),
    ("account_state", _set(["open_transition", "status"], "PROBABLE"), ["open_transition", "status"],
     "open_transition status is a transition status"),
    ("account_state", _set(["open_transition", "missing_facts", 0, "key"], "Owner Stabilized"),
     ["open_transition", "missing_facts", 0, "key"], "missing fact keys are rule-set keys"),
    ("account_state", lambda d: d["open_transition"]["missing_facts"][0].pop("required"),
     ["open_transition", "missing_facts", 0], "each missing fact says whether it is required"),
    ("account_state", lambda d: d["fields"].update(champion_since=d["fields"]["stage"]), ["fields", "champion_since"],
     "champion_since is derived, not won by a claim"),
    ("account_state", _set(["fields", "champion_since", "value"], "two weeks ago"), ["fields", "champion_since", "value"],
     "champion_since is a timestamp"),
    ("claim", lambda d: d.update(field_path="org_change", value="they reorganised"), ["value"],
     "an org_change names its kind and summary"),
    ("claim", lambda d: d.update(field_path="org_change", value={"kind": "vibes", "summary": "s"}), ["value", "kind"],
     "org_change kind vocabulary"),
    ("claim", lambda d: d.update(field_path="expansion_need", value=""), ["value"], "an expansion need is stated text"),
    ("account_state", _set(["buying_group", 0, "tenure"], "acting"), ["buying_group", 0, "tenure"], "tenure vocabulary"),
    ("account_state", _set(["fields", "blockers", "value", 0, "kind"], "pricing"), ["fields", "blockers", "value", 0, "kind"],
     "list item kind vocabulary"),
]


@pytest.mark.parametrize(("name", "mutate", "path", "why"), BAD_CASES, ids=[c[3] for c in BAD_CASES])
def test_known_bad_instance_rejected_for_the_right_reason(
        name: str, mutate: Callable[[dict], None], path: list, why: str) -> None:
    doc = copy.deepcopy(example(name))
    mutate(doc)
    errors = list(validator(name).iter_errors(doc))
    assert errors, f"expected rejection: {why}"
    paths = [list(e.absolute_path) for e in _all_errors(errors)]
    assert path in paths, f"{why}: rejected at {paths}, expected {path}"


GOOD_SHAPES = {
    "confirmed": _confirm,
    "confirmed_with_recorded_contradiction": _then(_confirm, _record),
    "candidate_with_recorded_contradiction": _record,
    "rejected": _reject,
    "rejected_keeps_recorded_contradiction": _then(_reject, _record),
    "unresolved_without_target": _unresolve,
    "unresolved_with_recorded_contradiction": _then(_unresolve, _record),
    "closed_stale": _close,
}


@pytest.mark.parametrize("make", list(GOOD_SHAPES.values()), ids=list(GOOD_SHAPES))
def test_every_status_has_a_valid_shape(make: Callable[[dict], None]) -> None:
    doc = copy.deepcopy(example("state_transition"))
    make(doc)
    errors = list(validator("state_transition").iter_errors(doc))
    assert not errors, [f"{list(e.absolute_path)}: {e.message}" for e in errors]


def test_not_to_self_covers_every_relationship_state() -> None:
    """Adding a relationship state must add its notToSelf branch (ADR-0012 checklist)."""
    branches = TRANSITION["$defs"]["notToSelf"]["allOf"]
    covered = [b["if"]["properties"]["from_state"]["const"] for b in branches]
    assert covered == COMMON["relationshipState"]["enum"]
    assert all(b["then"]["properties"]["to_state_candidate"]["not"]["const"] == s for b, s in zip(branches, covered))


def test_account_state_without_transition_fields_still_validates() -> None:
    """Backward compatibility: the WP6 reducer emits neither relationship_state nor open_transition yet."""
    doc = copy.deepcopy(example("account_state"))
    for key in ("relationship_state", "open_transition"):
        doc.pop(key)
    doc["fields"].pop("champion_since")
    assert not list(validator("account_state").iter_errors(doc))
    assert {"relationship_state", "open_transition"}.isdisjoint(ACCOUNT_STATE["required"])
    assert "champion_since" not in ACCOUNT_STATE["properties"]["fields"]["required"]


def test_no_open_transition_and_unknown_state_are_legal() -> None:
    doc = copy.deepcopy(example("account_state"))
    doc["relationship_state"] = {"value": "unknown", "transition_id": None, "confirmed_at": None}
    doc["open_transition"] = None
    assert not list(validator("account_state").iter_errors(doc))


def test_relationship_vocabulary_is_shared_and_described() -> None:
    states = COMMON["relationshipState"]
    assert states["enum"] == ["NEW_LOGO", "EXPANSION", "RENEWAL", "RECOVERY", "REORG"]
    assert set(states["descriptions"]) == set(states["enum"]), "every relationship state is described"
    assert COMMON["transitionStatus"]["enum"] == ["CONFIRMED", "CANDIDATE", "UNRESOLVED", "REJECTED"]
    props = TRANSITION["properties"]
    assert "unknown" in str(props["from_state"]) and "unknown" not in str(props["to_state_candidate"])
    assert props["rule_set_version"] == {"$ref": "common.v1.json#/$defs/ruleSetVersion"}


def test_motion_is_documented_as_a_supporting_fact_not_the_state() -> None:
    motion = ACCOUNT_STATE["properties"]["fields"]["properties"]["motion"]["description"]
    assert "supporting fact" in motion and "ADR-0012" in motion
    assert "ADR-0012" in ACCOUNT_STATE["properties"]["relationship_state"]["description"]


def test_examples_agree_with_each_other() -> None:
    """The AccountState example's open transition is the StateTransition example, summarized."""
    state, transition = example("account_state"), example("state_transition")
    summary = state["open_transition"]
    assert summary["transition_id"] == transition["id"] and state["account_id"] == transition["account_id"]
    assert (summary["from_state"], summary["to_state_candidate"], summary["status"]) == (
        transition["from_state"], transition["to_state_candidate"], transition["status"])
    assert summary["missing_facts"] == [{"key": f["key"], "required": f["required"]} for f in transition["missing_facts"]]
    assert any(not f["required"] for f in summary["missing_facts"]), "optional gaps (economic buyer) reach the agent"
    assert state["relationship_state"]["value"] == transition["from_state"]
    assert transition["state_version"] == state["version"]
    assert state["relationship_state"]["confirmed_at"] < state["fields"]["champion_since"]["value"], \
        "the example's champion took over after the reorg"
