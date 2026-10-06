# ruff: noqa: F403,F405,E402
"""Blind gold scenarios, part 2 (see transition_gold_blind.py)."""
import sys  # noqa: F401
from pathlib import Path  # noqa: F401

sys.path.insert(0, str(Path(__file__).resolve().parent))
from blind_world import *  # noqa: F403,E402
from blind_world import (BU_LEAD, EXEC, NEWCOMER, SECURITY, CHAMPION, OWNER2, bp, mk_claim, scenario, one, gold, render, UNK,  # noqa: F401,E402
                         SCENARIOS, baseline, bundle, reorg_world, nid, ts, at)
from blind_cases_a import *  # noqa: F403,E402

NOW_D = "2026-09-20T00:00:00Z"
for sid, owner_at, g, why in [
        ("exp_owner_stable_14d_inside", at(NOW_D, -14, -3600), gold("CONFIRMED", "EXPANSION", "EXPANSION"),
         "has held the role 14 days 1 hour (>= 14 days), so ownership is stabilized -> CONFIRMED"),
        ("exp_owner_stable_13d_outside", at(NOW_D, -14, 3600), gold("CANDIDATE", "REORG", "EXPANSION"),
         "has held the role only 13 days 23 hours (< 14 days), so ownership is not yet stabilized -> CANDIDATE")]:
    w, rel = reorg_world(R_CONF, R_BG)
    bundle(w, owner_at=owner_at, stake_at=at(NOW_D, -10), need_at=at(NOW_D, -10), value_at=at(NOW_D, -8), comm_at=at(NOW_D, -5))
    one(sid, S3 + " + configured owner-stability threshold (14 days)",
        f"Full confirmed bundle in REORG; the new permanent owner {why}.", rel, w, NOW_D, g)

for sid, need_days, g, why in [
        ("exp_need_89d_inside", 89, gold("CONFIRMED", "EXPANSION", "EXPANSION"), "89 days ago (inside the 90-day window) -> CONFIRMED"),
        ("exp_need_91d_outside", 91, gold("CANDIDATE", "REORG", "EXPANSION"),
         "91 days ago (outside the 90-day window), so the need no longer counts; the other four pieces remain -> CANDIDATE")]:
    w, rel = reorg_world(at(NOW_D, -120), at(NOW_D, -123))
    kit_owner(w, at(NOW_D, -100))
    kit_need(w, at(NOW_D, -need_days), speaker=OWNER2)
    kit_stakeholder(w, at(NOW_D, -20))
    kit_value(w, at(NOW_D, -15))
    kit_commercial(w, at(NOW_D, -5))
    one(sid, S3 + " + configured expansion-evidence window (90 days)",
        f"REORG confirmed 120 days ago; stable new owner, new BU stakeholder, value and commercial step are fresh; the expansion need was stated {why}.",
        rel, w, NOW_D, g)

# ================================================================ E. lifecycles
d1 = baseline()
kit_champion_role_change(d1, "2026-06-01T00:00:00Z", with_stage=False)
sg = d1.add(mk_claim("stage", "2026-06-03T00:00:00Z", "on_hold", None, standing="crm_explicit"))
d1.fields["stage"] = scalar(sg)
d1.sig("stage_regressed", "2026-06-03T00:00:00Z", None, sg)
d2 = d1.clone()
d2.sig("customer_replied", "2026-06-18T00:00:00Z", OWNER2)
d3 = d2.clone()
bundle(d3, owner_at="2026-07-01T00:00:00Z", stake_at="2026-07-03T00:00:00Z", need_at="2026-07-03T00:00:00Z",
       value_at="2026-07-05T00:00:00Z", next_at="2026-07-08T00:00:00Z")
d4 = d3.clone()
kit_commercial(d4, "2026-07-20T00:00:00Z")
scenario("lifecycle_demo_unknown_reorg_candidate_confirmed_expansion", "pitch sections 3 and 4 (the demo story end to end)",
         "The pitch's demo path. Step 1: champion states a move away from the rollout + opportunity on hold -> REORG CONFIRMED. Step 2: only a "
         "routine reply; the reorg evidence is already used -> nothing. Step 3: new owner (9 days, not yet stable), new BU stakeholder, stated "
         "need, value, next-step call; no commercial process -> CANDIDATE EXPANSION. Step 4: commercial process starts and the owner has held "
         "the role 24 days -> CONFIRMED EXPANSION. Step 5: nothing new -> no repeat transition.",
         UNK, [("champion moved, motion paused", d1, "2026-06-05T00:00:00Z", gold("CONFIRMED", "REORG", "REORG")),
               ("routine reply only", d2, "2026-06-20T00:00:00Z", gold(None, "REORG")),
               ("expansion signals, owner not yet stable", d3, "2026-07-10T00:00:00Z", gold("CANDIDATE", "REORG", "EXPANSION")),
               ("commercial process + stable owner", d4, "2026-07-25T00:00:00Z", gold("CONFIRMED", "EXPANSION", "EXPANSION")),
               ("nothing new", d4, "2026-08-05T00:00:00Z", gold(None, "EXPANSION"))])

r1, rel = reorg_world(R_CONF, R_BG)
bundle(r1, owner_at="2026-08-15T00:00:00Z", stake_at="2026-08-25T00:00:00Z", need_at="2026-08-25T00:00:00Z")
r2 = r1.clone()
r2.sig("customer_replied", "2026-09-18T00:00:00Z", OWNER2)
r3 = r2.clone()
kit_champion_role_change(r3, "2026-09-28T00:00:00Z", person=OWNER2, with_stage=False)
r4 = r3.clone()
kit_owner(r4, "2026-10-05T00:00:00Z", person=BU_LEAD, title="Head of Regional Finance")
kit_need(r4, "2026-10-10T00:00:00Z", speaker=BU_LEAD)
kit_value(r4, "2026-10-12T00:00:00Z", speaker=BU_LEAD)
scenario("lifecycle_expansion_candidate_rejected_new_owner_moves", S2 + " (REJECTED) + section 3",
         "Step 1: new stable owner, new BU stakeholder and a stated need, no value or commercial step -> CANDIDATE EXPANSION. Step 2: nothing "
         "material -> stays CANDIDATE. Step 3: the new owner states first-hand they are moving to another group and will not own the rollout: "
         "the suspected transition's foundation (a stabilized owner) is contradicted by later first-party evidence -> REJECTED, account stays "
         "REORG. Step 4: fresh evidence after the rejection (the BU lead becomes owner, 20 days in role; restated need; value) -> a new CANDIDATE.",
         rel, [("candidate opens", r1, "2026-09-10T00:00:00Z", gold("CANDIDATE", "REORG", "EXPANSION")),
               ("nothing material", r2, "2026-09-20T00:00:00Z", gold("CANDIDATE", "REORG", "EXPANSION")),
               ("new owner moves away", r3, "2026-10-01T00:00:00Z", gold("REJECTED", "REORG", "EXPANSION")),
               ("fresh evidence after rejection", r4, "2026-10-25T00:00:00Z", gold("CANDIDATE", "REORG", "EXPANSION"))])

c1 = baseline()
oc = kit_restructure(c1, "2026-09-08T00:00:00Z")
c2 = c1.clone()
for c in c2.claims:
    if c["id"] == oc["id"]:
        c["status"] = "rejected"
c2.lists.pop("org_changes")
sg = c2.add(mk_claim("stage", "2026-09-25T00:00:00Z", "evaluation", None, standing="crm_explicit"))
c2.fields["stage"] = scalar(sg)
c2.sig("stage_advanced", "2026-09-25T00:00:00Z", None, sg)
st = c2.add(mk_claim("champion_status", "2026-09-25T00:00:00Z", "active",
                     "The reorganisation has been called off; I'm still owning this, let's pick the rollout back up.", CHAMPION, speaker=CHAMPION))
c2.fields["champion_status"] = scalar(st)
c2.add(mk_claim("summary", "2026-09-25T00:00:00Z", "Planned reorganisation called off; structure unchanged; rollout discussion resumed",
                "The reorganisation has been called off; I'm still owning this, let's pick the rollout back up.", speaker=CHAMPION))
scenario("lifecycle_reorg_candidate_rejected_restructure_called_off", S2 + " (REJECTED) + State A",
         "Step 1: announced restructure + paused discussion, champion still in place -> CANDIDATE REORG (see contested). Step 2: the champion "
         "states the reorganisation was called off, they still own the rollout, and the opportunity moves forward again; the restructure claim "
         "is rejected. The suspected transition is contradicted by later first-party evidence -> REJECTED; the account stays 'unknown'.",
         UNK, [("restructure announced", c1, "2026-09-15T00:00:00Z", gold("CANDIDATE", "unknown", "REORG")),
               ("restructure called off", c2, "2026-09-30T00:00:00Z", gold("REJECTED", "unknown", "REORG"))],
         contested={"reading_a": "Step 1 is CANDIDATE REORG (structure change + pause, champion still in place); step 2 REJECTED.",
                    "reading_b": "Step 1 is already CONFIRMED REORG (two first-party State A facts); then step 2 would not be a REJECTED candidate.",
                    "your_label_follows": "a"})

u = unresolved_world()
scenario("lifecycle_unresolved_stale_closure", S2 + " (UNRESOLVED) + configured stale closure (30 days)",
         "Step 1: UNRESOLVED (see contested). Step 2: 29 days after the last supporting evidence (2026-08-03), still open. Step 3: 31 days "
         "with nothing new supporting it -> closed as stale (status stays UNRESOLVED, closed=true; never REJECTED). Step 4: the same old "
         "evidence must not reopen a new transition -> nothing.",
         UNK, [("graph changed, target unclear", u, "2026-08-05T00:00:00Z", gold("UNRESOLVED", "unknown")),
               ("29 days without support", u, "2026-09-01T00:00:00Z", gold("UNRESOLVED", "unknown")),
               ("31 days without support", u, "2026-09-03T00:00:00Z", gold("UNRESOLVED", "unknown", closed=True)),
               ("same stale evidence later", u, "2026-09-20T00:00:00Z", gold(None, "unknown"))],
         contested=UNRESOLVED_CONTEST)

u1 = unresolved_world()
u2 = u1.clone()
st = u2.add(mk_claim("champion_status", "2026-08-10T00:00:00Z", "active", "Back from a hectic stretch, let's pick things up.", CHAMPION, speaker=CHAMPION))
u2.fields["champion_status"] = scalar(st)
u2.sig("champion_reactivated", "2026-08-10T00:00:00Z", CHAMPION, st)
u2.members[CHAMPION].update(status="active", last_engaged_at="2026-08-10T00:00:00Z")
scenario("lifecycle_unresolved_contradicted_never_rejected", S2 + " (REJECTED is for a suspected transition; UNRESOLVED has no target)",
         "Step 1: UNRESOLVED. Step 2: the champion re-engages, contradicting the fading signal. An UNRESOLVED transition has no suspected "
         "target to contradict, so it is never REJECTED; it stays UNRESOLVED (open). Step 3: 33 days after the last supporting evidence -> "
         "closed as stale.",
         UNK, [("graph changed, target unclear", u1, "2026-08-05T00:00:00Z", gold("UNRESOLVED", "unknown")),
               ("champion re-engages", u2, "2026-08-12T00:00:00Z", gold("UNRESOLVED", "unknown")),
               ("stale", u2, "2026-09-05T00:00:00Z", gold("UNRESOLVED", "unknown", closed=True))],
         contested={"reading_a": "Step 1 UNRESOLVED; after the contradiction it stays UNRESOLVED (open) until the 30-day stale rule closes it.",
                    "reading_b": "Step 1 could be CANDIDATE REORG or nothing (see the UNRESOLVED scenario); and after the contradiction the open UNRESOLVED might simply be dropped rather than kept open.",
                    "your_label_follows": "a"})

v1 = unresolved_world()
v2 = v1.clone()
kit_champion_role_change(v2, "2026-08-10T00:00:00Z", with_stage=False)
scenario("lifecycle_unresolved_resolves_to_reorg", S2 + " + State A",
         "Step 1: UNRESOLVED. Step 2: the champion now states first-hand that they moved to another group and no longer own the rollout "
         "(champion changed responsibility, owner unclear) while a new participant is already present -> REORG CONFIRMED.",
         UNK, [("graph changed, target unclear", v1, "2026-08-05T00:00:00Z", gold("UNRESOLVED", "unknown")),
               ("champion states role change", v2, "2026-08-12T00:00:00Z", gold("CONFIRMED", "REORG", "REORG"))],
         contested=UNRESOLVED_CONTEST)

t1, rel = reorg_world(R_CONF, R_BG)
bundle(t1, owner_at="2026-08-15T00:00:00Z", stake_at="2026-08-25T00:00:00Z", need_at="2026-08-25T00:00:00Z")
t2 = t1.clone()
toc = t2.add(mk_claim("org_change", "2026-09-18T00:00:00Z", {"kind": "role_change", "summary": "Enrichment lists the new owner at a different company"},
                      None, OWNER2, "third_party"))
t2.list_item("org_changes", toc, toc["value"]["summary"], kind="relationship", subject=OWNER2)
tst = t2.add(mk_claim("champion_status", "2026-09-18T00:00:00Z", "departed", None, OWNER2, "third_party"))
win = [c for c in t2.claims if c["field_path"] == "champion_status" and c["subject_person_id"] == OWNER2][0]
t2.fields["champion_status"] = scalar(win, conflicts=[(tst, "Enrichment says the owner left; first-party says active")])
t2.sig("field_contradicted", "2026-09-18T00:00:00Z", OWNER2, tst,
       details={"field": "champion_status", "winning_claim_id": win["id"], "winning_standing": win["standing"],
                "contradicting_claim_id": tst["id"], "contradicting_standing": "third_party"})
scenario("contradiction_third_party_does_not_reject_candidate", S2 + " + L1 'Did we preserve contradictory evidence?'",
         "Step 1: CANDIDATE EXPANSION (stable new owner, new BU stakeholder, stated need). Step 2: an enrichment feed claims the new owner left; "
         "first-party evidence still has them active and the first-party claim wins the field. The contradiction is preserved but is not "
         "decisive -> stays CANDIDATE.",
         rel, [("candidate opens", t1, "2026-09-10T00:00:00Z", gold("CANDIDATE", "REORG", "EXPANSION")),
               ("third-party contradiction", t2, "2026-09-20T00:00:00Z", gold("CANDIDATE", "REORG", "EXPANSION"))],
         contested={"reading_a": "A third-party, outranked contradiction is kept as contradicting evidence but does not reject.",
                    "reading_b": "Any later evidence that the owner left contradicts the candidate's basis and REJECTS it.",
                    "your_label_follows": "a"})

w, rel = reorg_world(R_CONF, R_BG)
bundle(w, owner_at="2026-08-15T00:00:00Z", stake_at="2026-08-25T00:00:00Z", need_at="2026-08-25T00:00:00Z", value_at="2026-09-01T00:00:00Z")
cm, _ = kit_commercial(w, "2026-09-10T00:00:00Z")
dp = w.add(mk_claim("decision_process", "2026-09-10T00:00:00Z", "VP Finance signs off, then procurement review", None, standing="crm_explicit"))
lo = w.add(mk_claim("decision_process", "2026-09-12T00:00:00Z", "Legal review may also be needed before procurement",
                    "I think legal might want a look too before procurement.", speaker=BU_LEAD))
w.fields["decision_process"] = scalar(dp, conflicts=[(lo, "Newer AI-extracted claim adds a legal step")])
w.sig("field_contradicted", "2026-09-12T00:00:00Z", BU_LEAD, lo,
      details={"field": "decision_process", "winning_claim_id": dp["id"], "winning_standing": "crm_explicit",
               "contradicting_claim_id": lo["id"], "contradicting_standing": "first_party_ai"})
one("contradiction_outranked_detail_confirmed_stands", S2 + " + L1 'Did we preserve contradictory evidence?'",
    "Full confirmed bundle in REORG, plus a newer lower-standing claim that contradicts a detail of the decision process (adds a legal step); "
    "the CRM record wins the field. The contradiction is preserved but does not undo any bundle element -> CONFIRMED.",
    rel, w, NOW, gold("CONFIRMED", "EXPANSION", "EXPANSION"))

e1, rel = confirmed_bundle()
e2 = e1.clone()
for item in e2.lists["current_commitments"]:
    item["status"] = "overdue"
e2.sig("commitment_overdue", "2026-09-25T00:00:00Z", OWNER2, [c for c in e2.claims if c["field_path"] == "commitment"][0])
scenario("lifecycle_confirmed_expansion_then_overdue_commitment", S2 + " (REJECTED applies to suspected transitions only)",
         "Step 1: full bundle -> CONFIRMED EXPANSION. Step 2: the order-form commitment goes overdue. A confirmed state is not 'rejected' by a "
         "later setback, and a single risk fact is not a new candidate -> no transition; the account stays EXPANSION.",
         rel, [("full bundle", e1, NOW, gold("CONFIRMED", "EXPANSION", "EXPANSION")),
               ("commitment overdue", e2, "2026-10-05T00:00:00Z", gold(None, "EXPANSION"))])

w, rel = reorg_world(R_CONF, R_BG)
dl = w.add(mk_claim("delegation", "2026-09-10T00:00:00Z", "Security review taken over by the security reviewer",
                    "I'm taking over the security review. Can you keep me looped in on commercial timing?", SECURITY, speaker=SECURITY))
w.add(mk_claim("stakeholder_role", "2026-09-10T00:00:00Z", "Security", "I'm taking over the security review.", SECURITY, speaker=SECURITY))
w.sig("new_stakeholder_entered", "2026-09-10T00:00:00Z", SECURITY, dl)
pa = w.add(mk_claim("buying_group.member", "2026-09-11T00:00:00Z", "Security reviewer added to the meeting", None, SECURITY, "first_party_record"))
w.member(SECURITY, ["security"], "new", "2026-09-11T00:00:00Z", pa, "Security reviewer", "unknown")
nm = w.add(mk_claim("next_milestone", "2026-09-10T00:00:00Z", "Share commercial timing with the security reviewer",
                    "Can you keep me looped in on commercial timing?", speaker=SECURITY))
w.fields["next_milestone"] = scalar(nm)
mo = w.add(mk_claim("motion", "2026-09-12T00:00:00Z", "expansion", None, standing="crm_explicit"))
sg = w.add(mk_claim("stage", "2026-09-12T00:00:00Z", "discovery", None, standing="crm_explicit"))
w.fields.update(motion=scalar(mo), stage=scalar(sg))
one("pitch_activity_example_security_takeover", "pitch section 1 (A217-A219 evidence trail)",
    "The pitch's own activity trail in a REORG account: a security reviewer takes over the security review and asks about commercial timing, "
    "is added to a meeting, and the CRM gets an expansion opportunity. Pitch: 'something material changed' but it 'should not immediately "
    "jump to Expansion Ready'. New stakeholder + commercial-timing next step point at expansion; owner, stated need and value are missing -> CANDIDATE.",
    rel, w, "2026-09-13T00:00:00Z", gold("CANDIDATE", "REORG", "EXPANSION"),
    contested={"reading_a": "Material change pointing at expansion (new stakeholder + commercial timing + expansion opportunity), not confirmed: CANDIDATE EXPANSION.",
               "reading_b": "The expansion direction rests mainly on the CRM opportunity/motion, which is only a supporting fact, so the target cannot be told yet: UNRESOLVED.",
               "your_label_follows": "a"})


