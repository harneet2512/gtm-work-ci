"""HAR-126 / ADR-0012: the reference evaluator (transition_reference.py) on synthetic states.

One case per status and per boundary (threshold minus one, evidence before / at the reorg or outside the window, a
decisive vs a recorded contradiction, stale vs fresh UNRESOLVED, ambiguity between rules) plus the PR #16 probe
regressions. The Go detector must reproduce these outcomes."""
from __future__ import annotations

import uuid

import pytest

from test_contracts import validator
from transition_builders import (OPEN_REORG, ACT, ANCHOR, CANDIDATE_CLAIMS, CHAMPION, NEED, NOW, OPEN_EXPANSION, OTHER, STABLE_OWNER,  # noqa: F401
                                 STAKEHOLDER, UNKNOWN_STATE, VALUE, claim, confirmed_case, days_before, fld, keys, rival_gate_moved, rival_rules, run, sig, state)
from transition_reference import Outcome
from transition_rules_lib import RULES

# ---------- REORG -> EXPANSION ----------
def test_candidate_from_a_stated_need_and_a_new_stakeholder() -> None:
    out = run(state())
    assert (out.status, out.to_state, out.confidence) == ("CANDIDATE", "EXPANSION", 0.4)
    assert {"expansion_need_stated", "additional_stakeholder_engaged", "new_owner_identified"} <= keys(out.supporting)
    assert keys(out.missing) == {"owner_stabilized", "value_signal_present", "commercial_next_step",
                                 "economic_buyer_known", "decision_process_known"}


@pytest.mark.parametrize("claims", [(NEED,), (STAKEHOLDER, VALUE)], ids=["need_alone", "no_stated_need"])
def test_below_the_gate(claims: tuple) -> None:
    assert run(state(), claims=claims).status is None


@pytest.mark.parametrize("at", ["2026-08-20T00:00:00Z", ANCHOR], ids=["before_the_reorg", "at_the_anchor"])
def test_evidence_must_be_strictly_after_the_reorg(at: str) -> None:
    claims = (claim("expansion_need", at), claim("buying_group.member", at, OTHER))
    assert run(state(), claims=claims).status is None


def test_evidence_outside_the_window_does_not_count() -> None:
    assert run(state(), now="2026-12-15T00:00:00Z").status is None


def test_the_new_owners_own_introduction_is_not_an_additional_stakeholder() -> None:
    out = run(state(), claims=(NEED, claim("buying_group.member", subject=CHAMPION)))
    assert out.status is None


def test_third_party_evidence_never_counts() -> None:
    assert run(state(), claims=(claim("expansion_need", standing="third_party"), STAKEHOLDER)).status is None


def test_confirmed_when_all_five_requirements_hold() -> None:
    st, signals, claims = confirmed_case()
    out = run(st, signals, claims, computed_at="2026-10-01T00:00:05Z")
    assert (out.status, out.confidence, out.confirmed_at) == ("CONFIRMED", 1.0, NOW), "confirmed_at is the version's as_of"
    assert keys(out.missing) == {"economic_buyer_known", "decision_process_known"}


@pytest.mark.parametrize(("held_days", "status"), [(13, "CANDIDATE"), (14, "CONFIRMED")])
def test_owner_stability_threshold_boundary(held_days: int, status: str) -> None:
    st, signals, claims = confirmed_case()
    st["fields"]["champion_since"] = fld(days_before(held_days), days_before(held_days))
    assert run(st, signals, claims).status == status


def test_an_owner_from_before_the_reorg_is_not_stable() -> None:
    st, signals, claims = confirmed_case()
    st["fields"]["champion_since"] = fld("2026-08-01T00:00:00Z", "2026-08-01T00:00:00Z")
    out = run(st, signals, claims)
    assert out.status == "CANDIDATE" and "owner_stabilized" in keys(out.missing)


@pytest.mark.parametrize(("tenure", "status"), [("interim", "CANDIDATE"), ("permanent", "CONFIRMED"), ("unknown", "CONFIRMED"), (None, "CONFIRMED")])
def test_an_interim_owner_is_not_stable(tenure: str | None, status: str) -> None:
    st, signals, claims = confirmed_case()
    if tenure is not None:
        st["buying_group"][0]["tenure"] = tenure
    out = run(st, signals, claims)
    assert out.status == status and ("owner_stabilized" in keys(out.missing)) == (tenure == "interim")


def test_an_open_weakening_signal_unsettles_the_owner() -> None:
    st, signals, claims = confirmed_case()
    assert run(st, signals + (sig("champion_weakened", "2026-09-28T00:00:00Z"),), claims).status == "CANDIDATE"


def test_commercial_commitment_counts_only_when_commercial() -> None:
    item = {"text": "Send order form", "claim_id": str(uuid.uuid4()), "status": "open", "kind": "commercial",
            "evidence_refs": [{"activity_id": ACT, "occurred_at": "2026-09-18T00:00:00Z"}]}
    claims = (NEED, STAKEHOLDER, VALUE)
    assert run(state(**STABLE_OWNER, current_commitments=fld([item])), claims=claims).status == "CONFIRMED"
    technical = {**item, "kind": "technical"}
    assert run(state(**STABLE_OWNER, current_commitments=fld([technical])), claims=claims).status == "CANDIDATE"


def test_a_recorded_contradiction_is_kept_and_does_not_block() -> None:
    st, signals, claims = confirmed_case()
    out = run(st, signals + (sig("customer_went_silent", "2026-09-29T00:00:00Z"),), claims)
    assert out.status == "CONFIRMED" and [(c.key, c.rejects) for c in out.contradicting] == [("customer_silent", False)]


def test_a_decisive_contradiction_rejects_the_open_candidate() -> None:
    out = run(state(relationship_risk=fld("high")), open_transition=OPEN_EXPANSION)
    assert (out.status, out.to_state, [(c.key, c.rejects) for c in out.contradicting]) == (
        "REJECTED", "EXPANSION", [("support_risk_high", True)])


def test_a_decisive_contradiction_without_an_open_transition_creates_nothing() -> None:
    st, signals, claims = confirmed_case()
    st["fields"]["relationship_risk"] = fld("high")
    assert run(st, signals, claims).status is None


def test_an_unresolved_transition_is_never_rejected() -> None:
    previous = {"status": "UNRESOLVED", "to_state_candidate": None, "last_updated_at": "2026-09-25T00:00:00Z"}
    out = run(state(relationship_risk=fld("high")), open_transition=previous)
    assert out.status == "UNRESOLVED" and all(c.rejects is False for c in out.contradicting)


def test_a_candidate_that_loses_its_gate_becomes_unresolved_and_keeps_its_facts() -> None:
    out = run(state(), claims=(NEED,), open_transition=OPEN_EXPANSION)
    assert (out.status, out.to_state, out.closed) == ("UNRESOLVED", None, False)
    assert "expansion_need_stated" in keys(out.supporting) and "owner_stabilized" in keys(out.missing)


@pytest.mark.parametrize(("idle_days", "closed"), [(29, False), (30, True)])
def test_stale_unresolved_closes_after_the_window(idle_days: int, closed: bool) -> None:
    previous = {"status": "UNRESOLVED", "to_state_candidate": None, "last_updated_at": days_before(idle_days)}
    out = run(state(), claims=(), open_transition=previous)
    assert (out.status, out.closed) == ("UNRESOLVED", closed)


def test_two_gates_at_once_are_unresolved_with_their_facts() -> None:
    out = run(state(), rules=rival_rules())
    assert (out.status, out.to_state) == ("UNRESOLVED", None) and "expansion_need_stated" in keys(out.supporting)


def test_two_rules_confirming_at_once_are_unresolved_not_file_order() -> None:
    st, signals, claims = confirmed_case()
    assert run(st, signals, claims, rules=rival_rules()).status == "UNRESOLVED"


def test_a_candidate_whose_gate_moves_to_another_target_goes_unresolved() -> None:
    out = run(state(), rules=rival_gate_moved(), open_transition=OPEN_EXPANSION)  # the EXPANSION gate no longer holds
    assert (out.status, out.to_state) == ("UNRESOLVED", None), "no direct re-target from EXPANSION to RECOVERY"


def test_a_healthy_account_is_never_an_expansion_candidate() -> None:
    st, signals, claims = confirmed_case()
    st["relationship_state"] = UNKNOWN_STATE
    healthy = signals + tuple(sig(k, "2026-09-20T00:00:00Z") for k in ("expansion_interest", "new_stakeholder_entered", "product_usage_increased"))
    assert run(st, healthy, claims).status is None


# ---------- entry into REORG ----------
REORG_STATED = claim("org_change", "2026-09-20T00:00:00Z")
REMIT_CHANGED = claim("org_change", "2026-09-20T00:00:00Z", CHAMPION)


def fresh(**fields: dict) -> dict:
    return state(UNKNOWN_STATE, **fields)


@pytest.mark.parametrize(("st", "claims"), [
    (fresh(champion_status=fld("departed")), (REORG_STATED,)),
    (fresh(), (REMIT_CHANGED,)),
], ids=["org_change_and_champion_departed", "org_change_about_the_champion"])
def test_reorg_is_confirmed_by_a_stated_org_change_and_an_owner_change(st: dict, claims: tuple) -> None:
    first = run(st, claims=claims)
    assert (first.status, first.to_state) == ("CANDIDATE", "REORG"), "never confirmed in the evaluation that first finds the evidence"
    out = run(st, claims=claims, open_transition=OPEN_REORG)
    assert (out.status, out.to_state, out.confirmed_at) == ("CONFIRMED", "REORG", NOW)


def test_reorg_candidate_from_an_org_change_and_a_pause() -> None:
    out = run(fresh(), (sig("customer_went_silent", "2026-09-25T00:00:00Z"),), claims=(REORG_STATED,))
    assert (out.status, out.to_state) == ("CANDIDATE", "REORG") and "owner_responsibility_changed" in keys(out.missing)


def test_reorg_candidate_rejected_when_the_champion_reactivates() -> None:
    previous = {"status": "CANDIDATE", "to_state_candidate": "REORG", "last_updated_at": "2026-09-25T00:00:00Z"}
    out = run(fresh(), (sig("customer_went_silent", "2026-09-25T00:00:00Z"), sig("champion_reactivated", "2026-09-28T00:00:00Z")),
              claims=(REORG_STATED,), open_transition=previous)
    assert (out.status, out.to_state, [c.key for c in out.contradicting]) == ("REJECTED", "REORG", ["champion_reactivated"])


def test_third_party_org_change_never_earns_reorg() -> None:
    claims = (claim("org_change", "2026-09-20T00:00:00Z", standing="third_party"),)
    assert run(fresh(champion_status=fld("departed")), claims=claims).status is None


@pytest.mark.parametrize(("st", "signals", "claims"), [
    (fresh(), (), (claim("delegation", "2026-09-20T00:00:00Z", CHAMPION), claim("buying_group.member", "2026-09-20T00:00:00Z", OTHER))),
    (fresh(), (), (claim("stakeholder_role", "2026-09-20T00:00:00Z", CHAMPION), claim("owner", "2026-09-21T00:00:00Z"))),
    (fresh(champion_status=fld("delegated")), (sig("champion_delegated", "2026-09-20T00:00:00Z", subject=CHAMPION),),
     (claim("delegation", "2026-09-20T00:00:00Z", CHAMPION), claim("buying_group.member", "2026-09-20T00:00:00Z", OTHER))),
    (fresh(champion_status=fld("weakening")), (sig("champion_weakened", "2026-09-28T00:00:00Z"), sig("customer_went_silent", "2026-09-29T00:00:00Z")), ()),
], ids=["technical_hand_off", "role_label_and_seller_owner_change", "northstar_written_delegation", "weakening_is_risk"])
def test_routine_claims_never_enter_reorg(st: dict, signals: tuple, claims: tuple) -> None:
    """PR #16 round-2 probes: routine delegation, role labels, seller-side owner changes and risk are not a reorg."""
    assert run(st, signals, claims).status is None


def test_the_reorgs_own_evidence_is_not_reused_after_it() -> None:
    """The anchor is the REORG's confirmed_at, the as_of of the version that confirmed it: evidence at or before it is never reused."""
    as_of, computed = "2026-09-20T00:00:00Z", "2026-09-20T00:00:03Z"
    reorg = run(fresh(), claims=(claim("org_change", as_of, CHAMPION),), now=as_of, computed_at=computed, open_transition=OPEN_REORG)
    assert (reorg.status, reorg.confirmed_at) == ("CONFIRMED", as_of)
    after = state({"value": "REORG", "transition_id": str(uuid.uuid4()), "confirmed_at": reorg.confirmed_at})
    same_call = (claim("expansion_need", as_of), claim("buying_group.member", as_of, OTHER))
    assert run(after, claims=same_call, now=NOW).status is None
    later = (claim("expansion_need", "2026-09-20T00:00:04Z"), claim("buying_group.member", "2026-09-21T00:00:00Z", OTHER))
    assert run(after, claims=later, now=NOW).status == "CANDIDATE"


# ---------- outcomes are valid contract objects ----------
def _as_transition(out: Outcome) -> dict:
    return {
        "id": str(uuid.uuid4()), "account_id": str(uuid.uuid4()), "from_state": "REORG", "to_state_candidate": out.to_state,
        "status": out.status, "trigger_activity_ids": [ACT], "supporting_facts": [f.as_contract() for f in out.supporting],
        "missing_facts": [f.as_contract() for f in out.missing], "contradicting_facts": [f.as_contract() for f in out.contradicting],
        "confidence": out.confidence, "state_version": 7, "rule_set_version": RULES["rule_set_version"],
        "first_observed_at": "2026-09-10T00:00:00Z", "last_updated_at": NOW,
        "confirmed_at": out.confirmed_at, "rejected_at": NOW if out.status == "REJECTED" else None,
    }


def test_outcomes_are_valid_state_transitions() -> None:
    st, signals, claims = confirmed_case()
    unresolved_from = {"status": "UNRESOLVED", "to_state_candidate": None, "last_updated_at": "2026-09-25T00:00:00Z"}
    outcomes = [run(state()), run(st, signals + (sig("customer_went_silent", "2026-09-29T00:00:00Z"),), claims),
                run(state(relationship_risk=fld("high")), open_transition=OPEN_EXPANSION),
                run(state(), claims=(NEED,), open_transition=OPEN_EXPANSION),
                run(state(relationship_risk=fld("high")), open_transition=unresolved_from)]
    assert [o.status for o in outcomes] == ["CANDIDATE", "CONFIRMED", "REJECTED", "UNRESOLVED", "UNRESOLVED"]
    for out in outcomes:
        errors = list(validator("state_transition").iter_errors(_as_transition(out)))
        assert not errors, (out.status, [f"{list(e.absolute_path)}: {e.message}" for e in errors])


# ---------- PR #16 round 4 probes: each must never promote ----------
@pytest.mark.parametrize("status", ["weakening", "unknown", "inactive", "disengaged"])
def test_a_quiet_champion_plus_an_unrelated_org_change_is_not_a_reorg(status: str) -> None:
    st = fresh()
    st["buying_group"][0]["status"] = status
    assert run(st, claims=(claim("org_change", "2026-09-20T00:00:00Z", OTHER),)).status is None


def test_a_confirmed_expansion_does_not_flip_back_to_reorg_on_the_evidence_that_earned_the_earlier_reorg() -> None:
    expansion = state({"value": "EXPANSION", "transition_id": str(uuid.uuid4()), "confirmed_at": "2026-09-17T00:00:00Z"}, champion_status=fld("departed"))
    old = (claim("org_change", "2026-09-01T00:00:00Z", CHAMPION), claim("org_change", "2026-09-02T00:00:00Z", CHAMPION))
    assert run(expansion, claims=old).status is None
    new = (claim("org_change", "2026-09-25T00:00:00Z", CHAMPION),)
    assert run(expansion, claims=new).status == "CANDIDATE"
    assert run(expansion, claims=new, open_transition=OPEN_REORG).status == "CONFIRMED"


@pytest.mark.parametrize("status", ["outranked", "expired", "rejected"])
def test_claims_the_adjudicator_discarded_never_earn_a_state(status: str) -> None:
    discarded = dict(claim("org_change", "2026-09-20T00:00:00Z", CHAMPION), status=status)
    assert run(fresh(), claims=(discarded,)).status is None
    assert run(fresh(), claims=(dict(discarded, status="superseded"),), open_transition=OPEN_REORG).status == "UNRESOLVED", "a superseded claim no longer holds the candidate up"
    assert run(fresh(), claims=(dict(discarded, status="active"),), open_transition=OPEN_REORG).status == "CONFIRMED"


def test_third_party_fields_never_earn_a_state() -> None:
    assert run(fresh(champion_status=fld("departed", standing="third_party")), claims=(claim("org_change", "2026-09-20T00:00:00Z"),)).status is None
    owner = {k: fld(v, standing="third_party") for k, v in (("champion_status", "active"), ("champion", CHAMPION))}
    st, signals, claims = confirmed_case()
    st["fields"].update(owner)
    assert run(st, signals, claims).status != "CONFIRMED"
    st, signals, claims = confirmed_case()
    st["fields"]["economic_buyer"], st["fields"]["decision_process"] = fld(OTHER, standing="third_party"), fld("review", standing="third_party")
    assert run(st, (), claims).status == "CANDIDATE", "a third-party buyer and process are not a commercial step"


@pytest.mark.parametrize("status", ["active", "new", "unknown", "inactive", "weakening"])
def test_an_acting_owner_is_not_stable_whatever_their_engagement_status(status: str) -> None:
    st, signals, claims = confirmed_case()
    st["buying_group"][0].update(tenure="interim", status=status)
    assert run(st, signals, claims).status == "CANDIDATE"


def test_a_fact_that_cites_no_evidence_does_not_hold() -> None:
    silent = dict(sig("customer_went_silent", "2026-09-25T00:00:00Z"), evidence_refs=[])
    out = run(fresh(), (silent,), claims=(claim("org_change", "2026-09-20T00:00:00Z"),))
    assert out.status is None, "silence with no evidence reference must not hold a CANDIDATE up"


# ---------- PR #16 round 5: signal clock, claim polarity, per-deal facts ----------
from transition_builders import DEAL_A, DEAL_B, deal  # noqa: E402
from transition_reference import Context, evaluate  # noqa: E402


def run_deals(deals: tuple, claims: tuple, signals: tuple = (), rel: dict | None = None, **kw) -> Outcome:
    st = state(rel)
    return evaluate(RULES, Context(st, tuple(signals), tuple(claims), NOW, kw.get("open_transition"), None, tuple(deals)))


def scoped(c: dict, opportunity: str | None) -> dict:
    return {**c, "opportunity_id": opportunity}


def test_a_signal_is_timed_by_when_it_happened_not_when_it_was_recorded() -> None:
    """A replay records every signal 'now' (created_at in the future of the activity clock); occurred_at is the world time."""
    reactivated = dict(sig("champion_reactivated", "2026-09-28T00:00:00Z"), created_at="2027-01-01T00:00:00Z")
    previous = {"status": "CANDIDATE", "to_state_candidate": "REORG", "last_updated_at": "2026-09-25T00:00:00Z"}
    out = run(fresh(), (sig("customer_went_silent", "2026-09-25T00:00:00Z"), reactivated), claims=(REORG_STATED,), open_transition=previous)
    assert out.status == "REJECTED", "the re-engagement happened before now, whatever created_at says"


@pytest.mark.parametrize("path", ["expansion_need", "business_value"])
def test_a_withdrawn_claim_is_not_the_need_it_withdraws(path: str) -> None:
    st, signals, claims = confirmed_case()
    kept = tuple(c for c in claims if c["field_path"] != path)
    withdrawn = dict(claim(path, "2026-09-25T00:00:00Z"), withdrawn=True)
    assert run(st, signals, kept + (withdrawn,)).status != "CONFIRMED"
    assert run(st, signals, claims).status == "CONFIRMED"


def test_a_retraction_rejects_an_open_expansion_candidate() -> None:
    withdrawn = dict(claim("expansion_need", "2026-09-28T00:00:00Z"), withdrawn=True)
    out = run(state(), claims=(STAKEHOLDER, withdrawn), open_transition=OPEN_EXPANSION)
    assert (out.status, [c.key for c in out.contradicting]) == ("REJECTED", ["expansion_withdrawn"])
    assert run(state(), claims=(NEED, STAKEHOLDER, withdrawn), open_transition=None).status != "CONFIRMED"


def test_a_withdrawn_reorg_is_not_a_reorg_and_rejects_the_candidate() -> None:
    withdrawn = dict(REORG_STATED, withdrawn=True, occurred_at="2026-09-28T00:00:00Z")
    assert run(fresh(), claims=(withdrawn,)).status is None
    out = run(fresh(), (sig("customer_went_silent", "2026-09-25T00:00:00Z"),), claims=(REORG_STATED, withdrawn), open_transition=OPEN_REORG)
    assert out.status == "REJECTED"


def test_a_change_of_primary_deal_cannot_reverse_a_transition() -> None:
    """The headline is the primary deal; the rules read every open deal, so which deal is primary does not matter."""
    st, signals, claims = confirmed_case()
    ok = deal(DEAL_A, **STABLE_OWNER)
    other = deal(DEAL_B, champion_status=fld("weakening"), champion_since=fld("2026-09-29T00:00:00Z", "2026-09-29T00:00:00Z"))
    for deals in ((ok, other), (other, ok)):
        assert run_deals(deals, claims, signals).status == "CONFIRMED"
    assert run_deals((other,), claims, signals).status == "CANDIDATE", "with only the unsettled deal open the owner is not stable"


def test_a_closed_deal_does_not_count_and_a_deal_scoped_claim_stays_in_its_deal() -> None:
    st, signals, claims = confirmed_case()
    closed = deal(DEAL_A, is_open=False, **STABLE_OWNER)
    open_unsettled = deal(DEAL_B)
    assert run_deals((closed, open_unsettled), claims, signals).status == "CANDIDATE"
    in_a = tuple(scoped(c, DEAL_A) for c in claims)
    assert run_deals((deal(DEAL_A, **STABLE_OWNER), deal(DEAL_B, **STABLE_OWNER)), in_a, signals).status == "CONFIRMED"
    only_b = (deal(DEAL_B, **STABLE_OWNER),)
    assert run_deals(only_b, in_a, signals).status != "CONFIRMED", "claims about deal A are not evidence on deal B"


def test_reorg_reads_every_claim_of_the_account_whatever_deal_it_names() -> None:
    claims = (scoped(REMIT_CHANGED, DEAL_A),)
    assert run_deals((deal(DEAL_B),), claims, rel=UNKNOWN_STATE).status == "CANDIDATE"
