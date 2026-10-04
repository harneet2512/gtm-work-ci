"""HAR-126 / ADR-0012: the transition rule set (contracts/transitions/rules.v1.json) is data, not code.

Every condition is evaluable deterministically from AccountState fields, claims, the buying group and signals, and every
literal is in its path's vocabulary; thresholds are named configuration; REORG -> EXPANSION starts only from a confirmed
REORG and its candidate gate counts only evidence that is new since the reorg (no static headcounts); CRM `motion` is a
supporting fact, never a requirement; entry into REORG rests on reorg evidence, not risk signals."""
from __future__ import annotations

import copy
from collections.abc import Callable, Iterator

import pytest

from test_contracts import CONTRACTS, SCHEMAS, _all_errors, _set, example, load_json, validator
from transition_rules_lib import RULES, conditions, gate_keys, literal_errors

STATE = load_json(SCHEMAS / "account_state.v1.json")
STATE_FIELDS = STATE["properties"]["fields"]["properties"]
ROLES = set(STATE["properties"]["buying_group"]["items"]["properties"]["roles"]["items"]["enum"])
SIGNALS = set(load_json(SCHEMAS / "signal.v1.json")["properties"]["signal_type"]["enum"])
CLAIM_PATHS = set(load_json(SCHEMAS / "claim.v1.json")["$defs"]["fieldPath"]["enum"])
RELATIONSHIP_STATES = load_json(SCHEMAS / "common.v1.json")["$defs"]["relationshipState"]["enum"]
TRANSITIONS = {t["id"]: t for t in RULES["transitions"]}
REORG_TO_EXPANSION = TRANSITIONS["reorg_to_expansion"]
TO_REORG = TRANSITIONS["to_reorg"]
WINDOW_OPS = {"fired_within_days", "asserted_within_days"}


def _facts(rule: dict) -> list[dict]:
    return rule["facts"] + rule["contradictions"]


def _all_conditions() -> Iterator[tuple[dict, str, dict]]:
    for rule in RULES["transitions"]:
        for fact in _facts(rule):
            for cond in conditions(fact):
                yield rule, fact["key"], cond


def _fact(rule: dict, key: str) -> dict:
    return next(f for f in _facts(rule) if f["key"] == key)


def test_rule_set_instance_validates() -> None:
    errors = list(validator("transition_rules").iter_errors(RULES))
    assert not errors, [f"{list(e.absolute_path)}: {e.message}" for e in errors]


def test_every_path_is_evaluable_from_state_claims_buying_group_or_signals() -> None:
    for rule, key, cond in _all_conditions():
        kind, _, rest = cond["path"].partition(".")
        where = f"{rule['id']}.{key}: {cond['path']}"
        if kind == "state":
            assert rest.split(".")[0] in STATE_FIELDS, where
        elif kind == "claim":
            assert rest in CLAIM_PATHS, where
        elif kind == "buying_group":
            assert rest in ROLES, where
        else:
            assert kind == "diff" and rest in SIGNALS, where


def test_every_literal_is_in_its_vocabulary() -> None:
    assert literal_errors(RULES) == []


@pytest.mark.parametrize(("path", "value"), [
    ("state.champion_status", "actve"), ("state.relationship_risk", "hgh"), ("state.champion.standing", "crm"),
    ("state.motion", "expanson"), ("state.current_commitments", "comercial")])
def test_a_typo_in_a_literal_is_caught(path: str, value: str) -> None:
    rules = copy.deepcopy(RULES)
    op = "has_item" if path == "state.current_commitments" else "eq"
    rules["transitions"][0]["facts"][0]["any_of"][0]["all_of"].append({"path": path, "op": op, "value": value})
    assert literal_errors(rules) == [f"reorg_to_expansion.owner_stabilized: {path} {value!r}"]


def test_age_ops_target_timestamp_fields() -> None:
    for rule, key, cond in _all_conditions():
        if cond["op"] == "age_days_gte":
            field = cond["path"].split(".")[1]
            assert "timestamp" in str(STATE_FIELDS[field]), f"{rule['id']}.{key}: {field} is not timestamp-valued"


def test_rules_never_read_the_state_they_decide() -> None:
    """No circularity: rules read evidence, never relationship_state or open_transition."""
    assert {"relationship_state", "open_transition"}.isdisjoint(STATE_FIELDS)
    assert not any("relationship_state" in c["path"] or "open_transition" in c["path"] for *_, c in _all_conditions())


def test_thresholds_are_named_data_and_all_used() -> None:
    used = {c["threshold"] for *_, c in _all_conditions() if "threshold" in c}
    used |= {r["candidate"]["min_satisfied"]["threshold"] for r in RULES["transitions"]}
    used.add("unresolved_stale_days")  # the evaluation's stale-close step
    assert used == set(RULES["thresholds"]), "every threshold is referenced and every reference is defined"
    assert "unresolved_stale_days" in " ".join(RULES["evaluation"])
    assert not any(isinstance(c.get("value"), (int, float)) and not isinstance(c.get("value"), bool)
                   for *_, c in _all_conditions()), "numbers live in thresholds, not inline"


def test_since_needs_an_anchor() -> None:
    """'since' names a declared anchor; an account that never confirmed a state ('unknown') has no anchor and no lower bound."""
    for _rule, _key, cond in _all_conditions():
        if "since" in cond:
            assert cond["since"] in RULES["anchors"]
    assert "no lower bound" in RULES["anchors"]["from_state_confirmed_at"]


@pytest.mark.parametrize("rule_id", sorted(TRANSITIONS))
def test_fact_keys_are_unique_and_gates_reference_facts(rule_id: str) -> None:
    rule = TRANSITIONS[rule_id]
    keys = [f["key"] for f in _facts(rule)]
    assert len(keys) == len(set(keys)), keys
    fact_keys = {f["key"] for f in rule["facts"]}
    required = {f["key"] for f in rule["facts"] if f["required"]}
    assert set(rule["confirmed"]["requires_all"]) == required, "confirmed gate = the facts marked required"
    assert gate_keys(rule) <= fact_keys
    assert set(rule["candidate"]["requires_any"]) <= set(rule["candidate"]["min_satisfied"]["of"])
    assert rule["to_state"] not in rule["from_states"]
    assert all(not f["required"] for f in rule["contradictions"]), "contradictions are never requirements"
    assert any(f["rejects"] for f in rule["contradictions"]), "every rule can be rejected"


@pytest.mark.parametrize("rule_id", sorted(TRANSITIONS))
def test_candidate_gate_counts_change_evidence_only(rule_id: str) -> None:
    """CRITICAL review fix: no static headcount, no CRM motion, and at least two change facts."""
    rule = TRANSITIONS[rule_id]
    for key in gate_keys(rule):
        for cond in conditions(_fact(rule, key)):
            assert not cond["path"].startswith("state.motion"), f"{rule_id}.{key}: motion in the gate"
            assert cond["op"] != "count_gte", f"{rule_id}.{key}: static headcount"
    threshold = RULES["thresholds"][rule["candidate"]["min_satisfied"]["threshold"]]["value"]
    assert threshold >= 2, "a candidate needs more than one change fact"


@pytest.mark.parametrize("rule_id", sorted(TRANSITIONS))
def test_crm_motion_is_supporting_never_required(rule_id: str) -> None:
    rule = TRANSITIONS[rule_id]
    motion = {f["key"] for f in rule["facts"] if any(c["path"].startswith("state.motion") for c in conditions(f))}
    assert not motion & set(rule["confirmed"]["requires_all"])


def test_reorg_to_expansion_follows_har97_pitch_section_3() -> None:
    rule = REORG_TO_EXPANSION
    assert (rule["from_states"], rule["to_state"]) == (["REORG"], "EXPANSION"), "only out of a confirmed REORG"
    assert rule["confirmed"]["requires_all"] == [
        "owner_stabilized", "expansion_need_stated", "additional_stakeholder_engaged",
        "value_signal_present", "commercial_next_step"]
    assert rule["candidate"]["requires_any"] == ["expansion_need_stated"]
    assert rule["candidate"]["min_satisfied"]["of"] == ["expansion_need_stated", "additional_stakeholder_engaged", "value_signal_present"]
    optional = {f["key"] for f in rule["facts"] if not f["required"]}
    assert {"new_owner_identified", "economic_buyer_known", "decision_process_known", "crm_motion_expansion"} <= optional
    decisive = {f["key"]: f["rejects"] for f in rule["contradictions"]}
    assert decisive == {"expansion_withdrawn": True, "support_risk_high": True, "stage_regressed": False, "customer_silent": False}


def test_expansion_gate_evidence_is_new_since_the_reorg_and_windowed() -> None:
    """Every gate condition is a windowed signal anchored at the REORG's confirmed_at."""
    for key in gate_keys(REORG_TO_EXPANSION):
        for cond in conditions(_fact(REORG_TO_EXPANSION, key)):
            assert cond["op"] in WINDOW_OPS and cond.get("since") == "from_state_confirmed_at", f"{key}: {cond}"


def test_commercial_next_step_is_commercial() -> None:
    fact = _fact(REORG_TO_EXPANSION, "commercial_next_step")
    paths = {c["path"] for c in conditions(fact)}
    assert "state.next_meeting" not in paths, "a meeting alone is not a commercial step"
    assert {"diff.stage_advanced", "diff.pricing_interest", "state.current_commitments"} <= paths
    commitment = next(c for c in conditions(fact) if c["path"] == "state.current_commitments")
    assert (commitment["op"], commitment["value"]) == ("has_item", "commercial")


def test_owner_stability_needs_a_new_champion_held_long_enough() -> None:
    """A human-approved champion alone does not stabilize ownership; champion_since >= threshold does."""
    owner = _fact(REORG_TO_EXPANSION, "owner_stabilized")
    assert len(owner["any_of"]) == 1
    conds = owner["any_of"][0]["all_of"]
    assert {"path": "state.champion_since", "op": "age_days_gte", "threshold": "owner_stable_min_days"} in conds
    assert {"path": "state.champion_since", "op": "known", "since": "from_state_confirmed_at"} in conds
    assert not any(c["path"].endswith(".standing") for c in conds)
    assert {"path": "buying_group.champion", "op": "not_exists", "where": {"tenure": "interim"}} in conds, \
        "an interim (acting) owner is not stable; unknown tenure does not block"


def test_expansion_facts_read_first_class_evidence_claims() -> None:
    """Round 3: need, value and stakeholder read the claims made for them, never borrowed fields."""
    claim_routes = {key: {c["path"] for c in conditions(_fact(REORG_TO_EXPANSION, key)) if c["path"].startswith("claim.")}
                    for key in ("expansion_need_stated", "value_signal_present", "additional_stakeholder_engaged")}
    assert claim_routes == {"expansion_need_stated": {"claim.expansion_need"}, "value_signal_present": {"claim.business_value"},
                            "additional_stakeholder_engaged": {"claim.buying_group.member"}}
    borrowed = {"state.product_use_case", "state.decision_criteria"}
    assert not borrowed & {c["path"] for f in REORG_TO_EXPANSION["facts"] for c in conditions(f)}


def test_the_new_owner_is_not_the_additional_stakeholder() -> None:
    for cond in conditions(_fact(REORG_TO_EXPANSION, "additional_stakeholder_engaged")):
        assert cond.get("about") == "not_champion", cond


def test_reorg_entry_rests_on_a_stated_org_change() -> None:
    """Round 3: a first-party org_change is required; routine delegation, role labels, seller-side owner changes,
    a delegated or weakening champion never enter REORG."""
    assert TO_REORG["confirmed"]["requires_all"] == ["org_change_stated", "owner_responsibility_changed"]
    assert TO_REORG["candidate"]["requires_any"] == ["org_change_stated"]
    stated = _fact(TO_REORG, "org_change_stated")
    assert [c["path"] for c in conditions(stated)] == ["claim.org_change"]
    paths = {c["path"] for f in TO_REORG["facts"] for c in conditions(f)}
    assert not paths & {"claim.delegation", "claim.stakeholder_role", "claim.owner", "diff.champion_delegated",
                        "diff.champion_weakened", "diff.support_risk_spike"}
    status = [c for f in TO_REORG["facts"] for c in conditions(f) if c["path"] == "state.champion_status"]
    assert status == [{"path": "state.champion_status", "op": "eq", "value": "departed"}], "delegated is not a reorg (Northstar)"


def test_only_first_party_claims_are_evidence() -> None:
    standings = load_json(SCHEMAS / "common.v1.json")["$defs"]["standing"]["enum"]
    assert RULES["claim_standings"] == [s for s in standings if s != "third_party"]


def test_new_claim_paths_fold_into_optional_state_lists() -> None:
    claim_defs = load_json(SCHEMAS / "claim.v1.json")["$defs"]
    pending = claim_defs["fieldPathPendingExtraction"]["enum"]
    assert pending == ["org_change", "expansion_need", "business_value"]
    assert set(pending) <= set(claim_defs["fieldPath"]["enum"])
    for name in ("org_changes", "expansion_needs", "business_value"):
        assert STATE_FIELDS[name]["$ref"] == "#/$defs/listField" and name not in STATE["properties"]["fields"]["required"]


@pytest.mark.parametrize("rule_id", sorted(TRANSITIONS))
def test_every_required_fact_is_reachable_without_signals(rule_id: str) -> None:
    """WP29 review: signals come from WP8 (not built yet), so every required fact needs a route over what extraction,
    CRM rules and the reducer already produce (state fields, claims, buying group); 'not_exists' on a signal holds
    vacuously without signals."""
    for fact in TRANSITIONS[rule_id]["facts"]:
        if not fact["required"]:
            continue
        routes = [g for g in fact["any_of"]
                  if all(not c["path"].startswith("diff.") or c["op"] == "not_exists" for c in g["all_of"])]
        assert routes, f"{rule_id}.{fact['key']} is reachable only through signals"


def test_every_condition_group_cites_evidence() -> None:
    """A satisfied fact must cite evidence, so every group has a positive (evidence-producing) condition."""
    negative = {"not_exists", "is_unknown"}
    for rule in RULES["transitions"]:
        for fact in _facts(rule):
            for group in fact["any_of"]:
                assert any(c["op"] not in negative for c in group["all_of"]), f"{rule['id']}.{fact['key']}"


def test_buying_group_predicate_is_data() -> None:
    assert RULES["buying_group_member_statuses"] == ["active", "new"]
    assert RULES["vocabularies"]["motion"] == ["new_business", "expansion", "renewal"]


def test_rule_set_example_is_an_excerpt_of_the_instance() -> None:
    ex = example("transition_rules")
    for key in ("rule_set_version", "confidence", "anchors", "buying_group_member_statuses", "vocabularies", "thresholds"):
        assert ex[key] == RULES[key], key
    assert all(TRANSITIONS[t["id"]] == t for t in ex["transitions"])


def test_state_transition_example_agrees_with_the_rule_set() -> None:
    doc = example("state_transition")
    assert doc["rule_set_version"] == RULES["rule_set_version"]
    rule = next(r for r in RULES["transitions"]
                if doc["from_state"] in r["from_states"] and r["to_state"] == doc["to_state_candidate"])
    defs = {f["key"]: f for f in rule["facts"]}
    listed = doc["supporting_facts"] + doc["missing_facts"]
    assert {f["key"] for f in listed} == set(defs), "every rule fact is reported, as supporting or missing"
    assert all(f["required"] == defs[f["key"]]["required"] for f in listed)
    contradictions = {f["key"]: f["rejects"] for f in rule["contradictions"]}
    assert all(contradictions[f["key"]] == f["rejects"] for f in doc["contradicting_facts"])
    satisfied = {f["key"] for f in doc["supporting_facts"]}
    of = rule["candidate"]["min_satisfied"]["of"]
    assert satisfied & set(rule["candidate"]["requires_any"]), "the example passes the candidate gate"
    assert len(satisfied & set(of)) >= RULES["thresholds"][rule["candidate"]["min_satisfied"]["threshold"]]["value"]
    required = len(rule["confirmed"]["requires_all"])
    met = sum(f["required"] for f in doc["supporting_facts"])
    assert doc["confidence"] == round(met / required, 3), "confidence = satisfied required / required"


# (mutation of the rule-set example, instance path the rejection must point at, why)
BAD_RULES: list[tuple[Callable[[dict], None], list, str]] = [
    (_set(["rule_set_version"], "v1"), ["rule_set_version"], "rule set is versioned like transition_rules:v1"),
    (_set(["transitions", 0, "to_state"], "unknown"), ["transitions", 0, "to_state"], "unknown is never a target"),
    (_set(["transitions", 0, "from_states"], []), ["transitions", 0, "from_states"], "a rule starts somewhere"),
    (_set(["transitions", 0, "facts", 0, "any_of", 0, "all_of", 0], {"path": "vibes.mood", "op": "eq", "value": "x"}),
     ["transitions", 0, "facts", 0, "any_of", 0, "all_of", 0, "path"], "paths are state./claim./buying_group./diff."),
    (_set(["transitions", 0, "facts", 0, "any_of", 0, "all_of", 0], {"path": "state.champion_status", "op": "exists"}),
     ["transitions", 0, "facts", 0, "any_of", 0, "all_of", 0, "op"], "state paths take value ops, not signal ops"),
    (_set(["transitions", 0, "facts", 0, "any_of", 0, "all_of", 0], {"path": "buying_group.champion", "op": "count_gte"}),
     ["transitions", 0, "facts", 0, "any_of", 0, "all_of", 0, "op"], "no static headcount op"),
    (_set(["transitions", 0, "facts", 0, "any_of", 0, "all_of", 0],
          {"path": "buying_group.champion", "op": "exists", "since": "from_state_confirmed_at"}),
     ["transitions", 0, "facts", 0, "any_of", 0, "all_of", 0], "buying-group membership has no 'since'"),
    (_set(["transitions", 0, "facts", 0, "any_of", 0, "all_of", 0], {"path": "claim.owner", "op": "exists"}),
     ["transitions", 0, "facts", 0, "any_of", 0, "all_of", 0, "op"], "claim paths take asserted_within_days"),
    (_set(["transitions", 0, "facts", 0, "any_of", 0, "all_of", 0], {"path": "diff.expansion_interest", "op": "fired_within_days"}),
     ["transitions", 0, "facts", 0, "any_of", 0, "all_of", 0], "window ops name a threshold"),
    (_set(["transitions", 0, "facts", 0, "any_of", 0, "all_of", 0], {"path": "state.champion_status", "op": "eq"}),
     ["transitions", 0, "facts", 0, "any_of", 0, "all_of", 0], "value ops carry a value"),
    (_set(["transitions", 0, "facts", 0, "any_of", 0, "all_of", 0], {"path": "diff.expansion_interest", "op": "exists", "since": "yesterday"}),
     ["transitions", 0, "facts", 0, "any_of", 0, "all_of", 0, "since"], "since names an anchor"),
    (_set(["transitions", 0, "facts", 0, "any_of", 0, "all_of", 0],
          {"path": "state.champion_status", "op": "eq", "value": "active", "about": "champion"}),
     ["transitions", 0, "facts", 0, "any_of", 0, "all_of", 0], "'about' qualifies claims and signals only"),
    (_set(["transitions", 0, "facts", 0, "any_of", 0, "all_of", 0],
          {"path": "state.champion_status", "op": "eq", "value": "active", "threshold": "owner_stable_min_days"}),
     ["transitions", 0, "facts", 0, "any_of", 0, "all_of", 0], "a threshold on an op that ignores it"),
    (_set(["transitions", 0, "facts", 0, "any_of", 0, "all_of", 0], {"path": "state.champion", "op": "known", "value": "x"}),
     ["transitions", 0, "facts", 0, "any_of", 0, "all_of", 0], "a value on an op that ignores it"),
    (_set(["transitions", 0, "facts", 0, "any_of", 0, "all_of", 0],
          {"path": "diff.champion_weakened", "op": "not_exists", "since": "from_state_confirmed_at"}),
     ["transitions", 0, "facts", 0, "any_of", 0, "all_of", 0], "since on an absence"),
    (_set(["transitions", 0, "facts", 0, "any_of", 0, "all_of", 0], {"path": "state.champion.standing", "op": "known"}),
     ["transitions", 0, "facts", 0, "any_of", 0, "all_of", 0, "op"], "standing paths compare values"),
    (lambda d: d.pop("claim_standings"), [], "claim standings are declared"),
    (_set(["transitions", 0, "facts", 0, "any_of", 0, "all_of", 0],
          {"path": "state.champion_status", "op": "eq", "value": "active", "where": {"tenure": "interim"}}),
     ["transitions", 0, "facts", 0, "any_of", 0, "all_of", 0], "'where' filters buying-group members only"),
    (_set(["transitions", 0, "facts", 0, "any_of", 0, "all_of", 0],
          {"path": "buying_group.champion", "op": "exists", "where": {"tenure": "acting"}}),
     ["transitions", 0, "facts", 0, "any_of", 0, "all_of", 0, "where", "tenure"], "tenure vocabulary"),
    (_set(["transitions", 0, "facts", 0, "key"], "Owner"), ["transitions", 0, "facts", 0, "key"], "fact keys are snake case"),
    (_set(["transitions", 0, "facts", 0, "rejects"], True), ["transitions", 0, "facts", 0], "only contradictions reject"),
    (_set(["transitions", 0, "contradictions", 0, "required"], True), ["transitions", 0, "contradictions", 0, "required"],
     "a contradiction is never a requirement"),
    (lambda d: d["transitions"][0]["contradictions"][0].pop("rejects"), ["transitions", 0, "contradictions", 0],
     "a contradiction says whether it is decisive"),
    (lambda d: d["transitions"][0]["candidate"]["min_satisfied"].pop("of"), ["transitions", 0, "candidate", "min_satisfied"],
     "the gate names the change facts it counts"),
    (_set(["thresholds", "owner_stable_min_days", "unit"], "fortnights"), ["thresholds", "owner_stable_min_days", "unit"],
     "threshold units"),
    (_set(["buying_group_member_statuses"], ["vibing"]), ["buying_group_member_statuses", 0], "member statuses vocabulary"),
    (_set(["transitions", 0, "confirmed", "requires_all"], []), ["transitions", 0, "confirmed", "requires_all"],
     "confirmation needs requirements"),
]


@pytest.mark.parametrize(("mutate", "path", "why"), BAD_RULES, ids=[c[2] for c in BAD_RULES])
def test_known_bad_rule_set_rejected_for_the_right_reason(mutate: Callable[[dict], None], path: list, why: str) -> None:
    doc = copy.deepcopy(example("transition_rules"))
    mutate(doc)
    errors = list(validator("transition_rules").iter_errors(doc))
    assert errors, f"expected rejection: {why}"
    paths = [list(e.absolute_path) for e in _all_errors(errors)]
    assert path in paths, f"{why}: rejected at {paths}, expected {path}"


def test_rule_files_live_under_contracts_transitions() -> None:
    assert (CONTRACTS / "transitions" / "rules.v1.json").exists()
