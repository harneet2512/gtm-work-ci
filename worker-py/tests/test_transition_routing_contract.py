"""HAR-126 / ADR-0012: eval routing keyed on the StateTransition's (to_state, status) (contracts/transitions/routing.v1.json).

HAR-97 pitch §11: the router selects graders "based on transition status/state/action", so a CONFIRMED REORG never gets
the expansion suite; the EXPANSION suites are exactly pitch §5 (plus Scene 5's non-blocking expansion readiness)."""
from __future__ import annotations

import copy
from collections.abc import Callable

import pytest

from test_contracts import CONTRACTS, SCHEMAS, _all_errors, _set, example, load_json, validator
from transition_rules_lib import RULES

ROUTING = load_json(CONTRACTS / "transitions" / "routing.v1.json")
CATALOG_TYPES = load_json(CONTRACTS / "evals" / "eval_catalog.json")["eval_types"]
COMMON = load_json(SCHEMAS / "common.v1.json")["$defs"]
STATUSES = COMMON["transitionStatus"]["enum"]
PITCH_CANDIDATE = ["evidence sufficiency / abstention", "state-transition support", "buyer readiness",
                   "stakeholder correctness", "relationship continuity", "premature CTA", "customer-risk sensitivity"]
PITCH_CONFIRMED = ["champion strength / mobilization", "economic-buyer coverage", "stakeholder / multithreading coverage",
                   "business-case strength", "decision-process coverage", "compelling event / reason to act",
                   "next-step quality", "CTA calibration", "expansion-readiness consistency"]


def route(status: str, to_state: str | None, from_state: str = "REORG") -> str | None:
    """Resolve the suite for a transition: exact to_state before '*'; suite_of_state falls back to state_suites."""
    matches = [r for r in ROUTING["routes"] if r["status"] == status and r["to_state"] in (to_state, "*")]
    best = sorted(matches, key=lambda r: r["to_state"] == "*")[0]
    if "suite_of_state" in best:
        return ROUTING["state_suites"][from_state] or best["fallback_suite"]
    return best["suite"]


def evals(suite: str) -> dict[str, list[str]]:
    return {e["har97_eval"]: e["eval_types"] for e in ROUTING["suites"][suite]["evals"]}


def test_routing_instance_validates() -> None:
    errors = list(validator("transition_routing").iter_errors(ROUTING))
    assert not errors, [f"{list(e.absolute_path)}: {e.message}" for e in errors]


def test_every_rule_target_and_status_has_a_route() -> None:
    for rule in RULES["transitions"]:
        for status in ("CANDIDATE", "CONFIRMED"):
            assert route(status, rule["to_state"]) in ROUTING["suites"], (rule["id"], status)
    assert route("UNRESOLVED", None) == "conservative"
    assert {r["status"] for r in ROUTING["routes"]} == set(STATUSES)


def test_routing_keys_on_target_and_status() -> None:
    """A CONFIRMED REORG is not an expansion (review HIGH)."""
    assert route("CANDIDATE", "EXPANSION") == "expansion_candidate"
    assert route("CONFIRMED", "EXPANSION") == "expansion_confirmed"
    assert route("CONFIRMED", "REORG") == "reorg_disruption"
    assert route("CANDIDATE", "REORG") == "conservative"


def test_rejected_falls_back_to_the_from_state_suite() -> None:
    assert route("REJECTED", "EXPANSION", from_state="REORG") == "reorg_disruption"
    assert route("REJECTED", "REORG", from_state="EXPANSION") == "expansion_confirmed"
    assert route("REJECTED", "REORG", from_state="unknown") == "conservative", "a from_state without a suite has a defined fallback"


def test_routes_are_unique_per_status_and_target() -> None:
    pairs = [(r["status"], r["to_state"]) for r in ROUTING["routes"]]
    assert len(pairs) == len(set(pairs)), "duplicate (status, to_state) routes make the choice order-dependent"


def test_state_suites_cover_the_vocabulary_and_agree_with_confirmed_routes() -> None:
    assert set(ROUTING["state_suites"]) == set(COMMON["relationshipState"]["enum"]) | {"unknown"}
    for r in ROUTING["routes"]:
        if r["status"] == "CONFIRMED":
            assert ROUTING["state_suites"][r["to_state"]] == r["suite"], r


def test_every_suite_is_used_and_every_routed_eval_type_exists() -> None:
    used = {r["suite"] for r in ROUTING["routes"] if "suite" in r} | {s for s in ROUTING["state_suites"].values() if s}
    assert used == set(ROUTING["suites"])
    for suite in ROUTING["suites"]:
        for name, types in evals(suite).items():
            assert types and set(types) <= set(CATALOG_TYPES), (suite, name, types)


def test_expansion_suites_are_exactly_har97_pitch_section_5() -> None:
    assert list(evals("conservative")) == PITCH_CANDIDATE
    assert list(evals("expansion_candidate")) == PITCH_CANDIDATE + ["expansion readiness"]
    assert list(evals("expansion_confirmed")) == PITCH_CONFIRMED


def test_expansion_readiness_in_a_candidate_is_non_blocking() -> None:
    """Scene 5 shows 'Expansion readiness NOT YET' for a candidate: present, never blocking."""
    entry = next(e for e in ROUTING["suites"]["expansion_candidate"]["evals"] if e["har97_eval"] == "expansion readiness")
    assert entry["eval_types"] == ["expansion_readiness"] and "NOT YET" in entry["note"]
    assert CATALOG_TYPES["expansion_readiness"]["can_block"] is False


def test_reorg_disruption_suite_is_the_reviewed_list() -> None:
    types = [t for ts in evals("reorg_disruption").values() for t in ts]
    assert types == ["evidence_sufficiency", "champion_continuity", "stakeholder_selection",
                     "customer_risk_sensitivity", "cta_calibration", "buyer_readiness"]


def test_named_mappings_follow_the_review() -> None:
    candidate, confirmed = evals("expansion_candidate"), evals("expansion_confirmed")
    assert candidate["state-transition support"] == ["state_transition_support"]
    assert candidate["premature CTA"] == ["cta_calibration", "buyer_readiness"]
    assert candidate["relationship continuity"] == ["champion_continuity"]
    assert candidate["stakeholder correctness"] == ["stakeholder_selection"]
    assert confirmed["expansion-readiness consistency"] == ["expansion_readiness"]


def test_product_decisions_are_marked() -> None:
    for r in ROUTING["routes"]:
        if not (r["to_state"] == "EXPANSION" and r["status"] in ("CANDIDATE", "CONFIRMED")):
            assert r["basis"].startswith("Product decision"), r


def test_action_routes_refine_only_to_known_suites_with_a_stated_basis() -> None:
    assert ROUTING["action_routes"], "HAR-128: routing is by transition status, state and action"
    for r in ROUTING["action_routes"]:
        assert r["suite"] in ROUTING["suites"] and r["basis"].startswith("Product decision"), r
    expansion = next(r for r in ROUTING["action_routes"] if r["action_class"] == "EXPANSION_MOTION")
    assert set(expansion["statuses"]) == {"CANDIDATE", "UNRESOLVED"} and expansion["suite"] == "expansion_candidate"


def test_routing_example_is_an_excerpt_of_the_instance() -> None:
    ex = example("transition_routing")
    assert (ex["routes"], ex["action_routes"], ex["state_suites"]) == (ROUTING["routes"], ROUTING["action_routes"], ROUTING["state_suites"])
    assert all(ROUTING["suites"][k] == v for k, v in ex["suites"].items())


BAD_ROUTING: list[tuple[Callable[[dict], None], list, str]] = [
    (_set(["routes", 0, "status"], "PROBABLE"), ["routes", 0, "status"], "routes key on transition statuses"),
    (_set(["routes", 0, "to_state"], "expansion"), ["routes", 0, "to_state"], "routes key on relationship states"),
    (lambda d: d["routes"][0].pop("suite"), ["routes", 0], "a route names a suite or a state fallback"),
    (lambda d: d["routes"][0].pop("basis"), ["routes", 0], "a route states its basis"),
    (lambda d: d["routes"][-1].pop("fallback_suite"), ["routes", 5], "a state fallback names its default suite"),
    (_set(["action_routes", 0, "action_class"], "PUSH"), ["action_routes", 0, "action_class"], "an action route keys on a decision class"),
    (_set(["action_routes", 0, "statuses"], []), ["action_routes", 0, "statuses"], "an action route names the statuses it refines"),
    (_set(["action_routes", 0, "suite"], "Vibes"), ["action_routes", 0, "suite"], "an action route names a suite"),
    (lambda d: d["action_routes"][0].pop("basis"), ["action_routes", 0], "an action route states its basis"),
    (_set(["state_suites", "SOMEWHERE"], None), ["state_suites"], "state suites key on relationship states"),
    (_set(["suites", "reorg_disruption", "evals", 0, "eval_types"], ["vibes"]),
     ["suites", "reorg_disruption", "evals", 0, "eval_types", 0], "routed evals are catalog eval types"),
    (_set(["suites", "reorg_disruption", "evals", 0, "eval_types"], []), ["suites", "reorg_disruption", "evals", 0],
     "an unmapped eval names its gap"),
]


@pytest.mark.parametrize(("mutate", "path", "why"), BAD_ROUTING, ids=[c[2] for c in BAD_ROUTING])
def test_known_bad_routing_rejected_for_the_right_reason(mutate: Callable[[dict], None], path: list, why: str) -> None:
    doc = copy.deepcopy(example("transition_routing"))
    mutate(doc)
    errors = list(validator("transition_routing").iter_errors(doc))
    assert errors, f"expected rejection: {why}"
    paths = [list(e.absolute_path) for e in _all_errors(errors)]
    assert path in paths, f"{why}: rejected at {paths}, expected {path}"
