"""Transition gold (HAR-126 / WP28): hand-labelled StateTransition scenarios for measuring the detector.

`fixtures/gold/transitions/scenarios.json` is generated from this file (python worker-py/tests/transition_gold.py
--write; test_transition_gold.py fails when it is stale). Every label below was written from the HAR-97 pitch (sections
1-3, 5) and the PR #16 review probes, never from detector output.

What this gold is, honestly: single-author, spec-derived, synthetic account states with no company or person names. It
measures whether the detector reproduces the product spec on the cases we could think of; it is NOT independent
evidence that the detector works on real accounts. The independent gold named in contracts/metrics/har97_metrics.json
(HAR-130 CRMArena deals plus the HAR-131 labelled synthetic layer) does not exist on main: CRMArena has no people
changes at all (docs/data/crmarena-b2b.md) and HAR-131 is an open PR (#19).

Slices: pitch_bundle (the pitch's own evidence bundles), boundary (configurable thresholds: 14, 30, 90 days, one signal
is not a candidate), probe (PR #16 review probes that must never promote), reorg_entry, contradiction, lifecycle and
contested (the pitch can be read two ways; reported apart and left out of the headline numbers, each with both readings).

Labels per step: status (None when the detector must record nothing), to_state, supporting_required (the satisfied
required facts), contradicting (contradiction keys that must be preserved), closed, relationship_state_after."""
from __future__ import annotations

import argparse
import json
import sys
from datetime import timedelta
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))

import transition_builders as tb  # noqa: E402
from transition_reference import ts  # noqa: E402

ROOT = Path(__file__).resolve().parents[2]
OUT = ROOT / "fixtures" / "gold" / "transitions" / "scenarios.json"

ANCHOR = tb.ANCHOR  # 2026-09-01: when the REORG was confirmed
CHAMPION, OTHER = tb.CHAMPION, tb.OTHER
EXPANSION_REQUIRED = ["owner_stabilized", "expansion_need_stated", "additional_stakeholder_engaged", "value_signal_present",
                      "commercial_next_step"]


def at(base: str, days: float = 0, seconds: int = 0) -> str:
    return (ts(base) + timedelta(days=days, seconds=seconds)).strftime("%Y-%m-%dT%H:%M:%SZ")


def after_reorg(days: float) -> str:
    return at(ANCHOR, days)


def reorg_state(**fields: dict) -> dict:
    return tb.state({"value": "REORG", "transition_id": "00000000-0000-4000-8000-0000000000aa", "confirmed_at": ANCHOR}, **fields)


def fresh_state(**fields: dict) -> dict:
    return tb.state(tb.UNKNOWN_STATE, **fields)


def owner_held(days: int, now: str) -> dict:
    since = at(now, -days)
    return {"champion_since": tb.fld(since, since)}


def step(label: str, st: dict, signals: tuple, claims: tuple, now: str, status: str | None, to_state: str | None = None,
         supporting: list[str] | None = None, contradicting: list[str] | None = None, closed: bool = False,
         after: str = "REORG") -> dict:
    gold: dict = {"status": status, "relationship_state_after": after}
    if status is not None:
        gold.update(to_state=to_state, contradicting=sorted(contradicting or []), closed=closed)
        if status != "UNRESOLVED":  # the pitch does not say which facts an UNRESOLVED transition keeps, so they are not scored
            gold["supporting_required"] = sorted(supporting or [])
    return {"label": label, "input": {"state": st, "signals": list(signals), "claims": list(claims), "now": now,
                                      "computed_at": at(now, 0, 3), "open_transition": None}, "gold": gold}


def scenario(sid: str, slice_: str, source: str, rationale: str, steps: list[dict], initial: str = "REORG",
             contested: dict | None = None) -> dict:
    out = {"id": sid, "slice": slice_, "source": source, "rationale": rationale, "initial_state": initial, "steps": steps}
    if contested:
        out["contested"] = contested
    return out


NOW = tb.NOW  # 2026-10-01, 30 days after the reorg
PITCH = "HAR-97 pitch section 3"


def claims_at(days: float, *paths: str, subject: dict | None = None) -> list[dict]:
    subject = subject or {}
    return [tb.claim(p, after_reorg(days), subject.get(p)) for p in paths]


def bundle(owner_days: int = 26, need: bool = True, stakeholder: bool = True, value: bool = True, commercial: bool = True,
           when: float = 11) -> tuple[dict, list, list]:
    """The pitch's five confirmed-bundle elements, each switchable. Evidence dates 11 days after the reorg."""
    st = reorg_state(**owner_held(owner_days, NOW))
    claims = []
    if need:
        claims.append(tb.claim("expansion_need", after_reorg(when)))
    if stakeholder:
        claims.append(tb.claim("buying_group.member", after_reorg(when), OTHER))
    if value:
        claims.append(tb.claim("business_value", after_reorg(when)))
    signals = [tb.sig("stage_advanced", after_reorg(19))] if commercial else []
    return st, signals, claims


def expansion_scenarios() -> list[dict]:
    out = []
    st, sg, cl = bundle()
    out.append(scenario("confirmed_bundle", "pitch_bundle", PITCH + " (confirmed expansion transition)",
                        "All five elements: new owner stable, need stated, extra stakeholder, value, commercial step.",
                        [step("all five hold", st, tuple(sg), tuple(cl), NOW, "CONFIRMED", "EXPANSION", EXPANSION_REQUIRED, after="EXPANSION")]))
    st = reorg_state(**owner_held(26, NOW), economic_buyer=tb.fld(OTHER), decision_process=tb.fld("budget review"))
    out.append(scenario("confirmed_via_buyer_and_process", "pitch_bundle", PITCH + ": 'concrete next step or commercial process exists'",
                        "A known economic buyer and decision process is a commercial process.",
                        [step("process known", st, (), tuple(bundle(commercial=False)[2]), NOW, "CONFIRMED", "EXPANSION",
                              EXPANSION_REQUIRED, after="EXPANSION")]))
    st, sg, cl = bundle(owner_days=6)
    out.append(scenario("candidate_new_owner_not_yet_stable", "pitch_bundle", PITCH + " (candidate) and the 'ownership stabilized' x example",
                        "A new owner identified 6 days ago has not stabilized: the pitch's own example of why a transition is not confirmed.",
                        [step("owner 6 days", st, tuple(sg), tuple(cl), NOW, "CANDIDATE", "EXPANSION",
                              [k for k in EXPANSION_REQUIRED if k != "owner_stabilized"])]))
    for sid, kwargs, missing in (("candidate_no_commercial_step", {"commercial": False}, "commercial_next_step"),
                                 ("candidate_no_value_signal", {"value": False}, "value_signal_present"),
                                 ("candidate_no_additional_stakeholder", {"stakeholder": False}, "additional_stakeholder_engaged")):
        st, sg, cl = bundle(**kwargs)
        out.append(scenario(sid, "pitch_bundle", PITCH, f"Four of the five confirmed elements; {missing} is missing, so it stays a candidate.",
                            [step(f"no {missing}", st, tuple(sg), tuple(cl), NOW, "CANDIDATE", "EXPANSION",
                                  [k for k in EXPANSION_REQUIRED if k != missing])]))
    st, sg, cl = bundle(need=False)
    out.append(scenario("no_stated_need_is_not_expansion", "pitch_bundle", PITCH + ": 'expansion need explicitly stated'",
                        "Stakeholder, value and a commercial step without any stated need for more is just progress, not expansion.",
                        [step("no need", st, tuple(sg), tuple(cl), NOW, None)]))
    st, sg, cl = bundle(commercial=False, stakeholder=False, value=False)
    out.append(scenario("a_stated_need_alone_is_not_a_candidate", "boundary", "ADR-0012 candidate_min_change_facts = 2 (configurable, pitch: 'exact thresholds are our product design')",
                        "One change alone is never a candidate; the threshold is configuration.",
                        [step("need only", st, tuple(sg), tuple(cl), NOW, None)]))
    st, sg, cl = bundle(owner_days=26)
    st["buying_group"][0]["tenure"] = "interim"
    out.append(scenario("interim_owner_is_not_stable", "pitch_bundle", PITCH + ": 'ownership stabilized' (an acting head is not settled)",
                        "Everything else holds but the owner is acting until a permanent hire.",
                        [step("acting owner", st, tuple(sg), tuple(cl), NOW, "CANDIDATE", "EXPANSION",
                              [k for k in EXPANSION_REQUIRED if k != "owner_stabilized"])]))
    st, sg, cl = bundle()
    st["fields"]["champion_since"] = tb.fld(at(ANCHOR, -12), at(ANCHOR, -12))
    out.append(scenario("owner_from_before_the_reorg_is_not_new", "pitch_bundle", PITCH + ": 'new owner/champion identified'",
                        "A champion who was already there before the reorg is not the new owner the pitch asks for.",
                        [step("old champion", st, tuple(sg), tuple(cl), NOW, "CANDIDATE", "EXPANSION",
                              [k for k in EXPANSION_REQUIRED if k != "owner_stabilized"])]))
    st, sg, cl = bundle(commercial=False, stakeholder=False, value=False, need=False)
    out.append(scenario("crm_motion_alone_is_not_expansion", "pitch_bundle", "HAR-97 pitch section 1: motion is a supporting fact, not the state",
                        "The CRM says motion = expansion and nothing else changed.",
                        [step("crm only", st, (), (), NOW, None)]))
    st, sg, cl = bundle()
    cl = [dict(c, standing="third_party") for c in cl]
    out.append(scenario("third_party_evidence_never_counts", "probe", "ADR-0009 first-party standing",
                        "Enrichment and third-party claims never earn a state.",
                        [step("third party", st, tuple(sg), tuple(cl), NOW, None)]))
    return out


def boundary_scenarios() -> list[dict]:
    out = []
    for days, status in ((14, "CONFIRMED"), (13, "CANDIDATE")):
        st, sg, cl = bundle(owner_days=days)
        needed = EXPANSION_REQUIRED if status == "CONFIRMED" else [k for k in EXPANSION_REQUIRED if k != "owner_stabilized"]
        out.append(scenario(f"owner_held_{days}_days", "boundary", "ADR-0012 owner_stable_min_days = 14",
                            f"The owner has held the role {days} days against the 14-day threshold.",
                            [step(f"{days} days", st, tuple(sg), tuple(cl), NOW, status, "EXPANSION", needed,
                                  after="EXPANSION" if status == "CONFIRMED" else "REORG")]))
    now = at(ANCHOR, 91)  # 2026-12-01
    for label, ago, status in (("inside", 90, "CANDIDATE"), ("outside", 91, None)):
        st = reorg_state(**owner_held(40, now))
        cl = [tb.claim("expansion_need", at(now, -ago)), tb.claim("buying_group.member", at(now, -ago), OTHER)]
        out.append(scenario(f"evidence_{ago}_days_old", "boundary", "ADR-0012 expansion_evidence_window_days = 90",
                            f"Need and stakeholder {ago} days old against the 90-day window.",
                            [step(label, st, (), tuple(cl), now, status, "EXPANSION" if status else None,
                                  ["owner_stabilized", "expansion_need_stated", "additional_stakeholder_engaged"] if status else None)]))
    st = reorg_state(**owner_held(26, NOW))
    for label, when in (("before the reorg", at(ANCHOR, -3)), ("at the reorg instant", ANCHOR)):
        cl = [tb.claim("expansion_need", when), tb.claim("buying_group.member", when, OTHER)]
        out.append(scenario(f"evidence_{label.replace(' ', '_')}", "boundary", "ADR-0012: evidence must be strictly after the reorg that earned the from_state",
                            "Evidence that earned (or predates) the reorg is never reused for the expansion.",
                            [step(label, st, (), tuple(cl), NOW, None)]))
    st, sg, cl = bundle()
    out.append(scenario("open_weakening_signal_unsettles_the_owner", "boundary", "ADR-0012 owner_stabilized: no open weakening or delegation",
                        "A champion_weakened signal is still open.",
                        [step("weakening", st, tuple(sg) + (tb.sig("champion_weakened", after_reorg(25)),), tuple(cl), NOW, "CANDIDATE",
                              "EXPANSION", [k for k in EXPANSION_REQUIRED if k != "owner_stabilized"])]))
    return out


def reorg_scenarios() -> list[dict]:
    out = []
    when = after_reorg(19)
    out.append(scenario("org_change_about_the_champion", "reorg_entry", "HAR-97 pitch section 3 state A: champion changed responsibility",
                        "The customer states a remit change about the champion: a CANDIDATE first, CONFIRMED on the next recompute (round 5: never confirmed in the evaluation that first finds the evidence).",
                        [step("first evaluation", fresh_state(), (), (c1 := tb.claim("org_change", when, CHAMPION),), NOW, "CANDIDATE", "REORG",
                              ["org_change_stated", "owner_responsibility_changed"], after="unknown"),
                         step("next recompute", fresh_state(), (), (c1,), at(NOW, 1), "CONFIRMED", "REORG",
                              ["org_change_stated", "owner_responsibility_changed"], after="REORG")], initial="unknown"))
    out.append(scenario("org_change_and_champion_departed", "reorg_entry", "HAR-97 pitch section 3 state A: champion left",
                        "A stated reorganisation and the champion has left.",
                        [step("first evaluation", fresh_state(champion_status=tb.fld("departed")), (), (c2 := tb.claim("org_change", when),), NOW, "CANDIDATE",
                              "REORG", ["org_change_stated", "owner_responsibility_changed"], after="unknown"),
                         step("next recompute", fresh_state(champion_status=tb.fld("departed")), (), (c2,), at(NOW, 1), "CONFIRMED",
                              "REORG", ["org_change_stated", "owner_responsibility_changed"], after="REORG")], initial="unknown"))
    out.append(scenario("org_change_and_a_pause_is_a_candidate", "reorg_entry", "HAR-97 pitch section 3 state A: commercial motion paused; section 2 candidate",
                        "A stated org change (not about the champion) plus customer silence: meaningful, not complete.",
                        [step("org change + silence", fresh_state(), (tb.sig("customer_went_silent", at(NOW, -5)),), (tb.claim("org_change", when),),
                              NOW, "CANDIDATE", "REORG", ["org_change_stated"], after="unknown")], initial="unknown"))
    out.append(scenario("org_change_stated_long_ago", "boundary", "ADR-0012 reorg_evidence_window_days = 30",
                        "A 45-day-old org change against the 30-day window.",
                        [step("45 days", fresh_state(champion_status=tb.fld("departed")), (), (tb.claim("org_change", at(NOW, -45)),), NOW, None,
                              after="unknown")], initial="unknown"))
    probes = (
        ("technical_hand_off_is_not_a_reorg", "A technical delegation and a new member: routine.",
         dict(), (tb.claim("delegation", when, CHAMPION), tb.claim("buying_group.member", when, OTHER)), ()),
        ("role_label_and_seller_owner_change_are_not_a_reorg", "A role label and a seller-side owner change: routine.",
         dict(), (tb.claim("stakeholder_role", when, CHAMPION), tb.claim("owner", at(when, 1))), ()),
        ("written_delegation_is_not_a_reorg", "A champion who delegated in writing is still the champion.",
         dict(champion_status=tb.fld("delegated")), (tb.claim("delegation", when, CHAMPION),),
         (tb.sig("champion_delegated", when, subject=CHAMPION),)),
        ("a_weakening_champion_is_risk_not_a_reorg", "HAR-97 pitch section 1 treats a weakening champion as a risk signal.",
         dict(champion_status=tb.fld("weakening")), (), (tb.sig("champion_weakened", at(NOW, -3)), tb.sig("customer_went_silent", at(NOW, -2)))),
        ("third_party_org_change_never_earns_reorg", "A third-party alert alone.",
         dict(champion_status=tb.fld("departed")), (tb.claim("org_change", when, CHAMPION, "third_party"),), ()),
    )
    for sid, why, fields, claims, signals in probes:
        out.append(scenario(sid, "probe", "PR #16 review round 2 probe", why,
                            [step("probe", fresh_state(**fields), tuple(signals), tuple(claims), NOW, None, after="unknown")], initial="unknown"))
    st = fresh_state(**owner_held(26, NOW))
    cl = [tb.claim("expansion_need", at(NOW, -20)), tb.claim("buying_group.member", at(NOW, -20), OTHER), tb.claim("business_value", at(NOW, -20))]
    out.append(scenario("a_healthy_account_is_never_an_expansion_candidate", "probe", "HAR-97 pitch section 3: expansion is earned from a reorg",
                        "Expansion signals on an account that never had a reorg.",
                        [step("no reorg", st, (tb.sig("stage_advanced", at(NOW, -10)),), tuple(cl), NOW, None, after="unknown")], initial="unknown"))
    return out


def contested_scenarios() -> list[dict]:
    when = after_reorg(19)
    return [
        scenario("org_change_alone_with_the_champion_unaffected", "contested",
                 "HAR-97 pitch section 2 (candidate: 'meaningful signals ... one or more required pieces are still missing')",
                 "A stated org change that does not touch the champion.",
                 [step("org change only", fresh_state(), (), (tb.claim("org_change", when),), NOW, "CANDIDATE", "REORG", ["org_change_stated"],
                       after="unknown")], initial="unknown",
                 contested={"gold_reading": "CANDIDATE: the org change is the meaningful signal, the champion's change is the missing piece",
                            "detector_design": "ADR-0012: one change alone is never a candidate, so nothing is recorded"}),
        scenario("champion_departed_with_no_org_change_claim", "contested", "HAR-97 pitch section 3 state A: 'champion moved / left'",
                 "The champion left; no org_change has been stated, and the extractor cannot emit one yet.",
                 [step("departed only", fresh_state(champion_status=tb.fld("departed")), (), (), NOW, "CANDIDATE", "REORG", [], after="unknown")],
                 initial="unknown",
                 contested={"gold_reading": "CANDIDATE: 'champion left' is the pitch's first state-A fact",
                            "detector_design": "ADR-0012 round 3: REORG entry needs a first-party org_change, so nothing is recorded"}),
        scenario("economic_buyer_unknown_but_bundle_complete", "contested", PITCH + " vs the pitch's candidate example UI",
                 "All five confirmed-bundle elements hold; the economic buyer and budget are unknown.",
                 [step("buyer unknown", *bundle()[:1], tuple(bundle()[1]), tuple(bundle()[2]), NOW, "CONFIRMED", "EXPANSION",
                       EXPANSION_REQUIRED, after="EXPANSION")],
                 contested={"gold_reading": "CONFIRMED: section 3's normative confirmed list does not require the economic buyer",
                            "detector_design": "same; the example UI in section 2 lists 'economic buyer / budget signal unknown' as unconfirmed"}),
    ]


def contradiction_scenarios() -> list[dict]:
    out = []
    st, sg, cl = bundle(commercial=False, value=False)
    needed = ["owner_stabilized", "expansion_need_stated", "additional_stakeholder_engaged"]
    risky = dict(st, fields=dict(st["fields"], relationship_risk=tb.fld("high")))
    out.append(scenario("support_risk_rejects_an_open_candidate", "contradiction", "HAR-97 pitch section 2 (REJECTED: contradicted by later evidence) and section 1 (recovery before expansion)",
                        "A candidate expansion, then high support risk.",
                        [step("candidate", st, tuple(sg), tuple(cl), at(NOW, 0), "CANDIDATE", "EXPANSION", needed),
                         step("risk turns high", risky, tuple(sg), tuple(cl), at(NOW, 2), "REJECTED", "EXPANSION", needed, ["support_risk_high"])]))
    out.append(scenario("a_contradiction_without_an_open_transition_creates_nothing", "contradiction", "HAR-97 pitch section 2: REJECTED needs a previously suspected transition",
                        "High risk and thin evidence with nothing open.",
                        [step("risk, no candidate", risky, (), (tb.claim("expansion_need", after_reorg(11)),), NOW, None)]))
    st, sg, cl = bundle(commercial=False)
    sg = list(sg) + [tb.sig("stage_regressed", after_reorg(25))]
    out.append(scenario("a_recorded_regression_is_preserved_not_decisive", "contradiction", "HAR-97 L1: 'Did we preserve contradictory evidence?'",
                        "A stage regression after the reorg is kept on the candidate but does not reject it.",
                        [step("regression", st, tuple(sg), tuple(cl), NOW, "CANDIDATE", "EXPANSION",
                              ["owner_stabilized", "expansion_need_stated", "additional_stakeholder_engaged", "value_signal_present"], ["stage_regressed"])]))
    st, sg, cl = bundle()
    out.append(scenario("a_confirmed_transition_keeps_a_recorded_contradiction", "contradiction", "HAR-97 L1: 'Did we preserve contradictory evidence?'",
                        "All five elements hold and the customer went quiet; the silence stays visible.",
                        [step("confirmed with silence", st, tuple(sg) + (tb.sig("customer_went_silent", after_reorg(28)),), tuple(cl), NOW,
                              "CONFIRMED", "EXPANSION", EXPANSION_REQUIRED, ["customer_silent"], after="EXPANSION")]))
    return out


def lifecycle_scenarios() -> list[dict]:
    out = []
    # earned in steps: candidate, a quiet re-evaluation, then confirmed
    d10, d12, d41 = after_reorg(10), after_reorg(12), after_reorg(41)
    early_claims = (tb.claim("expansion_need", after_reorg(9)), tb.claim("buying_group.member", after_reorg(9), OTHER))
    st_early = reorg_state(champion_since=tb.fld(after_reorg(8), after_reorg(8)))
    late_claims = early_claims + (tb.claim("business_value", after_reorg(30)),)
    st_late = reorg_state(champion_since=tb.fld(after_reorg(8), after_reorg(8)), economic_buyer=tb.fld(OTHER, after_reorg(30)),
                          decision_process=tb.fld("budget review", after_reorg(30)))
    out.append(scenario("earned_in_steps", "lifecycle", PITCH + " (candidate then confirmed expansion)", "The pitch's Scenes 3 and 7: candidate, then confirmed.",
                        [step("day 10", st_early, (), early_claims, d10, "CANDIDATE", "EXPANSION", ["expansion_need_stated", "additional_stakeholder_engaged"]),
                         step("day 12, nothing new", st_early, (), early_claims, d12, "CANDIDATE", "EXPANSION", ["expansion_need_stated", "additional_stakeholder_engaged"]),
                         step("day 41", st_late, (), late_claims, d41, "CONFIRMED", "EXPANSION", EXPANSION_REQUIRED, after="EXPANSION")]))
    # a candidate whose evidence ages out becomes UNRESOLVED, regains its gate, then is rejected by the champion's return... (REORG-side)
    old = (tb.claim("expansion_need", after_reorg(2)), tb.claim("buying_group.member", after_reorg(2), OTHER))
    renewed = (tb.claim("expansion_need", after_reorg(110)), tb.claim("buying_group.member", after_reorg(110), OTHER))
    st = reorg_state(champion_since=tb.fld(after_reorg(1), after_reorg(1)))
    out.append(scenario("a_lost_gate_is_unresolved_then_recovers", "lifecycle", "HAR-97 pitch section 2 (UNRESOLVED: cannot tell where the account is heading)",
                        "Evidence ages out of the window; later evidence restores the candidate.",
                        [step("day 20", st, (), old, after_reorg(20), "CANDIDATE", "EXPANSION", ["owner_stabilized", "expansion_need_stated", "additional_stakeholder_engaged"]),
                         step("day 100: evidence aged out", st, (), old, after_reorg(100), "UNRESOLVED", None, []),
                         step("day 112: new evidence", st, (), old + renewed, after_reorg(112), "CANDIDATE", "EXPANSION",
                              ["owner_stabilized", "expansion_need_stated", "additional_stakeholder_engaged"])]))
    out.append(scenario("a_stale_unresolved_transition_is_closed", "lifecycle", "ADR-0012 unresolved_stale_days = 30",
                        "UNRESOLVED with no gate for 30 days is closed rather than left open forever.",
                        [step("day 20", st, (), old, after_reorg(20), "CANDIDATE", "EXPANSION", ["owner_stabilized", "expansion_need_stated", "additional_stakeholder_engaged"]),
                         step("day 100", st, (), old, after_reorg(100), "UNRESOLVED", None, []),
                         step("day 131", st, (), old, after_reorg(131), "UNRESOLVED", None, [], closed=True)]))
    risky = dict(st, fields=dict(st["fields"], relationship_risk=tb.fld("high")))
    out.append(scenario("an_unresolved_transition_is_never_rejected", "lifecycle", "HAR-97 pitch section 2: REJECTED contradicts a suspected target; UNRESOLVED has none",
                        "High risk arrives while the transition is UNRESOLVED.",
                        [step("day 20", st, (), old, after_reorg(20), "CANDIDATE", "EXPANSION", ["owner_stabilized", "expansion_need_stated", "additional_stakeholder_engaged"]),
                         step("day 100", st, (), old, after_reorg(100), "UNRESOLVED", None, []),
                         step("day 105: risk high", risky, (), old, after_reorg(105), "UNRESOLVED", None, [], ["support_risk_high"])]))
    reactivation = [tb.sig("customer_went_silent", at(NOW, -5))]
    org = (tb.claim("org_change", after_reorg(19)),)
    out.append(scenario("a_reorg_candidate_is_rejected_when_the_champion_returns", "lifecycle", "HAR-97 pitch section 2 (REJECTED: contradicted by later evidence)",
                        "A suspected reorg; the previous champion re-engages, so the relationship was not disrupted.",
                        [step("org change + silence", fresh_state(), tuple(reactivation), org, NOW, "CANDIDATE", "REORG", ["org_change_stated"], after="unknown"),
                         step("champion re-engages", fresh_state(), tuple(reactivation) + (tb.sig("champion_reactivated", at(NOW, 2)),), org, at(NOW, 3),
                              "REJECTED", "REORG", ["org_change_stated"], ["champion_reactivated"], after="unknown")], initial="unknown"))
    return out


def review_probe_scenarios() -> list[dict]:
    """Cases written after PR #16 round-4 review probes found a promotion path; each is a regression the detector must hold."""
    out = []
    when = after_reorg(19)
    for status in ("weakening", "unknown"):
        st = fresh_state()
        st["buying_group"][0]["status"] = status
        out.append(scenario(f"a_{status}_champion_and_an_unrelated_org_change_is_not_a_reorg", "probe", "PR #16 round 4 review probe; HAR-97 pitch section 1",
                            "An org change about someone else while the champion merely went quiet: not a reorg.",
                            [step("probe", st, (), (tb.claim("org_change", when, OTHER),), NOW, None, after="unknown")], initial="unknown"))
    expansion = tb.state({"value": "EXPANSION", "transition_id": "00000000-0000-4000-8000-0000000000aa", "confirmed_at": after_reorg(16)},
                         champion_status=tb.fld("departed"))
    old = (tb.claim("org_change", after_reorg(0), CHAMPION), tb.claim("org_change", after_reorg(1), CHAMPION))
    out.append(scenario("a_confirmed_expansion_does_not_flip_back_to_reorg_on_old_evidence", "probe", "PR #16 round 4 review probe; rules anchors: evidence is never reused",
                        "EXPANSION was confirmed; the org changes that earned the earlier REORG must not confirm REORG again.",
                        [step("old org changes", expansion, (), old, after_reorg(18), None, after="EXPANSION")], initial="REORG"))
    out.append(scenario("third_party_champion_status_never_earns_a_reorg", "probe", "ADR-0009: enrichment never earns a state",
                        "A third-party record says the champion departed; the customer stated an org change about nobody in particular.",
                        [step("probe", fresh_state(champion_status=tb.fld("departed", standing="third_party")), (), (tb.claim("org_change", when),), NOW, None,
                              after="unknown")], initial="unknown"))
    st, sg, cl = bundle()
    st["buying_group"][0].update(tenure="interim", status="unknown")
    out.append(scenario("an_acting_owner_who_never_wrote_directly_is_not_stable", "probe", PITCH + ": 'ownership stabilized'",
                        "The acting owner's engagement status is unknown, but the role is still held on an interim basis.",
                        [step("acting, status unknown", st, tuple(sg), tuple(cl), NOW, "CANDIDATE", "EXPANSION",
                              [k for k in EXPANSION_REQUIRED if k != "owner_stabilized"])]))
    return out


def build() -> dict:
    scenarios = (expansion_scenarios() + boundary_scenarios() + reorg_scenarios() + contradiction_scenarios() + lifecycle_scenarios()
                 + review_probe_scenarios() + contested_scenarios())
    ids = [s["id"] for s in scenarios]
    assert len(ids) == len(set(ids)), "duplicate scenario ids"
    return {"gold_version": 1, "labelling": __doc__, "scenarios": scenarios}


def render() -> str:
    text = json.dumps(build(), sort_keys=True, indent=1) + "\n"
    return _stable_ids(text)


def _stable_ids(text: str) -> str:
    """Builders mint random uuids; replace each by one derived from its order of appearance so the file is reproducible."""
    import re
    import uuid

    fixed = {tb.ACT, tb.CHAMPION, tb.OTHER, "00000000-0000-4000-8000-0000000000aa"}
    seen: dict[str, str] = {}

    def swap(m: re.Match) -> str:
        if m.group(0) in fixed:
            return m.group(0)
        seen.setdefault(m.group(0), str(uuid.uuid5(uuid.NAMESPACE_URL, f"transition-gold/{len(seen)}")))
        return seen[m.group(0)]

    return re.sub(r"[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}", swap, text)


if __name__ == "__main__":
    ap = argparse.ArgumentParser()
    ap.add_argument("--write", action="store_true")
    args = ap.parse_args()
    text = render()
    if args.write:
        OUT.parent.mkdir(parents=True, exist_ok=True)
        OUT.write_text(text, encoding="utf-8", newline="\n")
    doc = json.loads(text)
    print(len(doc["scenarios"]), "scenarios,", sum(len(s["steps"]) for s in doc["scenarios"]), "steps")
