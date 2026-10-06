# ruff: noqa: F403,F405,E402
"""Blind gold scenarios, part 1 (see transition_gold_blind.py)."""
import sys  # noqa: F401
from pathlib import Path  # noqa: F401

sys.path.insert(0, str(Path(__file__).resolve().parent))
from blind_world import *  # noqa: F403,E402
from blind_world import (BU_LEAD, EXEC, NEWCOMER, SECURITY, CHAMPION, OWNER2, bp, mk_claim, scenario, one, gold, render, UNK,  # noqa: F401,E402
                         SCENARIOS, baseline, bundle, reorg_world, nid, ts, at)

NOW = "2026-09-15T00:00:00Z"


def confirmed_bundle(eb=True, skip=()):
    w, rel = reorg_world(R_CONF, R_BG, eb=eb)
    bundle(w,
           owner_at=None if "owner" in skip else "2026-08-15T00:00:00Z",
           stake_at=None if "stakeholder" in skip else "2026-08-25T00:00:00Z",
           need_at=None if "need" in skip else "2026-08-25T00:00:00Z",
           value_at=None if "value" in skip else "2026-09-01T00:00:00Z",
           comm_at=None if "commercial" in skip else "2026-09-10T00:00:00Z")
    return w, rel


w, rel = reorg_world(R_CONF, R_BG, eb=False)
bundle(w, owner_at="2026-09-05T00:00:00Z", stake_at="2026-09-08T00:00:00Z", need_at="2026-09-08T00:00:00Z",
       value_at="2026-09-06T00:00:00Z", next_at="2026-09-12T00:00:00Z")
one("pitch_candidate_bundle_reorg_to_expansion", S3 + ", 'Candidate expansion transition' + section 2 UI example",
    "The pitch's own candidate bundle: a new owner identified (10 days in role, so ownership not yet stabilized), explicit interest "
    "from another BU, a value signal and a next-step call, but economic buyer unknown and no budget/decision process or commercial step. "
    "Pitch shows exactly this as REORG -> EXPANSION CANDIDATE.", rel, w, NOW, gold("CANDIDATE", "REORG", "EXPANSION"))

w, rel = confirmed_bundle()
one("pitch_confirmed_bundle_reorg_to_expansion", S3 + ", 'Confirmed expansion transition'",
    "All five elements of the pitch's confirmed bundle, first-party and new since REORG was confirmed: new owner stable 31 days, expansion "
    "need stated by a new BU lead, that BU stakeholder engaged, business value stated, and a commercial process (order form + decision process). "
    "Pitch: 'Now the state can move to EXPANSION CONFIRMED'.", rel, w, NOW, gold("CONFIRMED", "EXPANSION", "EXPANSION"))

for part, why in [("owner", "no new owner/champion: the old champion departed and nobody has taken over (owner unclear)"),
                  ("need", "no explicitly stated expansion need"),
                  ("stakeholder", "no additional stakeholder or business unit engaged (the need is stated by the existing user only)"),
                  ("value", "no business value / usage signal"),
                  ("commercial", "no concrete next step or commercial process (no order form, no decision process, no stage advance)")]:
    w, rel = confirmed_bundle(skip=(part,))
    one(f"confirmed_bundle_minus_{part}", S3 + ", 'Confirmed expansion transition' with one element removed",
        f"The pitch's confirmed bundle with one element removed: {why}. Four first-party, new pieces of expansion evidence remain "
        "(>= 2), so the expansion target is clear but a required piece is missing -> CANDIDATE, not CONFIRMED.",
        rel, w, NOW, gold("CANDIDATE", "REORG", "EXPANSION"))

w, rel = confirmed_bundle(eb=False)
one("confirmed_bundle_economic_buyer_unknown", S3 + " confirmed bundle vs section 2 UI example",
    "Full confirmed bundle (all five listed elements) but the economic buyer is unknown. The pitch's confirmed bundle does not list "
    "an economic buyer; a commercial process (order form, decision process) exists. Labelled CONFIRMED by the confirmed-bundle list.",
    rel, w, NOW, gold("CONFIRMED", "EXPANSION", "EXPANSION"),
    contested={"reading_a": "The section 3 confirmed bundle is the definition and does not include an economic buyer, so CONFIRMED.",
               "reading_b": "The section 2 UI lists 'economic buyer / budget signal unknown' as a 'why not confirmed?' item, so an unknown economic buyer keeps it CANDIDATE.",
               "your_label_follows": "a"})

# ================================================================ B. State A: entering REORG from unknown
w = baseline()
kit_champion_role_change(w, "2026-09-08T00:00:00Z")
one("reorg_confirmed_champion_role_change_stated", S3A + " + section 1",
    "The champion states first-hand that they moved to another group and no longer own the rollout (champion changed responsibility, "
    "owner now unclear) and the CRM opportunity goes on hold (commercial motion paused). Two first-party State A facts, both fresh -> "
    "REORG CONFIRMED.", UNK, w, NOW, gold("CONFIRMED", "REORG", "REORG"))

w = baseline()
kit_restructure(w, "2026-09-08T00:00:00Z")
one("reorg_candidate_restructure_announced_champion_in_place", S3A,
    "The champion states that operations is being reorganised under a new VP (new team structure) and the rollout discussion is paused "
    "(commercial motion paused): two fresh first-party pieces pointing at REORG. But the champion is still in place and active and no "
    "ownership change has happened yet, so the relationship itself is not yet shown to be disrupted -> CANDIDATE REORG.",
    UNK, w, NOW, gold("CANDIDATE", "unknown", "REORG"),
    contested={"reading_a": "A stated future restructure plus a pause signals REORG, but the defining disruption (champion moved/left/changed responsibility, owner unclear) is missing, so CANDIDATE.",
               "reading_b": "Both are first-party State A facts ('new team structure appeared', 'commercial motion paused'), which is enough first-party evidence to CONFIRM REORG.",
               "your_label_follows": "a"})

w = baseline()
kit_restructure(w, "2026-09-08T00:00:00Z", pause=False, kind="restructure",
                summary="Finance and procurement are merging under a new COO next quarter",
                quote="Finance and procurement are merging under a new COO next quarter; it doesn't change anything for this project.")
one("neg_reorg_single_structure_change", S3A + " + configured candidate minimum (2 pieces)",
    "NEGATIVE: one stated organisational change elsewhere in the company (a merger of two departments), with the champion active and the "
    "motion unchanged. One piece of change evidence alone is not a candidate -> no transition.", UNK, w, NOW, gold(None, "unknown"))

w = baseline()
kit_champion_weakening(w, "2026-09-10T00:00:00Z")
one("neg_reorg_champion_simply_weakened", S3A + " + section 1 ('champion becomes inactive' is a graph fact, not a state)",
    "NEGATIVE: the champion only became slower to respond (status weakening). The champion did not move, leave or change responsibility; "
    "a single engagement fact is a graph event, not a state, and one piece is not a candidate -> no transition.",
    UNK, w, NOW, gold(None, "unknown"))

w = baseline()
oc = w.add(mk_claim("org_change", "2026-09-08T00:00:00Z", {"kind": "role_change", "summary": "Enrichment provider lists the champion under a new title in another department"},
                    None, CHAMPION, "third_party"))
w.list_item("org_changes", oc, oc["value"]["summary"], kind="relationship", subject=CHAMPION)
tp = w.add(mk_claim("champion_status", "2026-09-08T00:00:00Z", "departed", None, CHAMPION, "third_party"))
win = [c for c in w.claims if c["field_path"] == "champion_status"][0]
w.fields["champion_status"] = scalar(win, conflicts=[(tp, "Enrichment says the champion left the role; first-party says active")])
w.sig("field_contradicted", "2026-09-08T00:00:00Z", CHAMPION, tp,
      details={"field": "champion_status", "winning_claim_id": win["id"], "winning_standing": win["standing"],
               "contradicting_claim_id": tp["id"], "contradicting_standing": "third_party"})
one("neg_reorg_third_party_enrichment", S2 + " (CONFIRMED needs first-party evidence) + State A",
    "NEGATIVE: only a third-party enrichment feed says the champion changed role; first-party evidence still says the champion is active. "
    "Third-party enrichment never earns a state and here it is the only change evidence -> no transition.",
    UNK, w, NOW, gold(None, "unknown"),
    contested={"reading_a": "Third-party evidence must not earn or start a transition; nothing first-party changed, so no transition.",
               "reading_b": "Third-party data cannot CONFIRM, but two third-party pieces (role change + departed) could still raise a provisional CANDIDATE REORG.",
               "your_label_follows": "a"})


def unresolved_world(t0="2026-08-01T00:00:00Z", t1="2026-08-03T00:00:00Z"):
    w = baseline()
    kit_champion_weakening(w, t0)
    kit_newcomer(w, t1)
    return w


UNRESOLVED_CONTEST = {
    "reading_a": "Champion fading plus an unidentified new participant is a material graph change whose direction (replacement/reorg, added team/expansion, or noise) cannot be told: UNRESOLVED.",
    "reading_b": "Champion weakening plus a new person is a replacement pattern pointing at REORG, so CANDIDATE REORG (or, since 'champion simply weakened' is not a reorg, no transition at all).",
    "your_label_follows": "a"}

one("unresolved_champion_fading_new_participant", S2 + " (UNRESOLVED) + section 1",
    "The champion is fading (weakening) and an unidentified new person was added to the thread; nobody stated a reorganisation, role change "
    "or expansion need. The graph changed (two distinct pieces) but the evidence does not say which state the account is moving into -> UNRESOLVED.",
    UNK, unresolved_world(), "2026-08-05T00:00:00Z", gold("UNRESOLVED", "unknown"), contested=UNRESOLVED_CONTEST)

NOW_B = "2026-09-20T00:00:00Z"
w = baseline()
kit_champion_role_change(w, at(NOW_B, -29))
one("reorg_window_29d_inside", S3A + " + configured reorg-evidence window (30 days)",
    "Same first-party champion role change + paused motion as the confirmed REORG case, stated 29 days ago: still inside the 30-day "
    "reorg-evidence window -> REORG CONFIRMED.", UNK, w, NOW_B, gold("CONFIRMED", "REORG", "REORG"))

w = baseline()
kit_champion_role_change(w, at(NOW_B, -31))
one("neg_reorg_window_31d_outside", S3A + " + configured reorg-evidence window (30 days)",
    "NEGATIVE / time boundary: the same REORG evidence, but stated 31 days ago, outside the 30-day window; nothing fresh. Stale evidence "
    "cannot earn REORG -> no transition.", UNK, w, NOW_B, gold(None, "unknown"),
    contested={"reading_a": "All reorg evidence is older than the 30-day window, so nothing counts: no transition.",
               "reading_b": "The champion_status 'departed' field still holds in the current state, so a persisting fact could keep at least a CANDIDATE REORG.",
               "your_label_follows": "a"})

# ================================================================ C. expansion-side negatives (from REORG)
w, rel = reorg_world(R_CONF, R_BG)
kit_need(w, "2026-09-08T00:00:00Z", speaker=OWNER2)
one("neg_exp_single_expansion_need", S3 + " + configured candidate minimum (2 pieces)",
    "NEGATIVE: in REORG, an existing user states an expansion need, and nothing else is new (no new owner, no new stakeholder, no value, "
    "no commercial step). One piece alone is not a candidate -> no transition.", rel, w, NOW, gold(None, "REORG"))

w, rel = reorg_world(R_CONF, R_BG)
mo = w.add(mk_claim("motion", "2026-09-10T00:00:00Z", "expansion", None, standing="crm_explicit"))
w.fields["motion"] = scalar(mo)
one("neg_exp_crm_motion_alone", S2 + " + section 3 (state explained by evidence, not a label)",
    "NEGATIVE: in REORG, the only change is the CRM opportunity motion flipped to 'expansion'. The CRM motion field is a supporting fact, "
    "never the state -> no transition.", rel, w, NOW, gold(None, "REORG"))

w = baseline(motion="expansion")
for kind, t in [("product_usage_increased", "2026-09-18T00:00:00Z"), ("customer_replied", "2026-09-20T00:00:00Z"),
                ("meeting_accepted", "2026-09-21T00:00:00Z")]:
    w.sig(kind, t, CHAMPION)
w.add(mk_claim("summary", "2026-09-20T00:00:00Z", "Champion mentioned that other teams have seen the dashboards",
               "A couple of other teams have seen our dashboards and think they're neat.", speaker=CHAMPION))
w.add(mk_claim("product_use_case", "2026-09-20T00:00:00Z", "Month-end reconciliation",
               "We mostly use it for month-end reconciliation.", speaker=CHAMPION))
one("neg_healthy_account_expansion_chatter", S2 + " + section 1 (graph facts are not state labels)",
    "NEGATIVE: healthy account (stable champion since February, low risk, CRM motion already 'expansion'), usage up, friendly reply, "
    "a meeting accepted and the champion casually mentions other teams have seen the dashboards. No stated expansion need, no new "
    "stakeholder, no org change: chatter, not change evidence -> no transition.", UNK, w, "2026-09-25T00:00:00Z", gold(None, "unknown"))

w = baseline()
for path, val, q in [("next_meeting", "Quarterly check-in on 2026-10-01", "Let's keep our usual quarterly check-in."),
                     ("objections", "Report export is slow", "The report export is still a bit slow."),
                     ("decision_criteria", "Must keep SSO", "Whatever we do has to keep SSO."),
                     ("stakeholder_role", "Operations manager", "Still running ops on our side.")]:
    c = w.add(mk_claim(path, "2026-09-12T00:00:00Z", val, q, CHAMPION if path == "stakeholder_role" else None, speaker=CHAMPION))
    if path == "next_meeting":
        w.fields["next_meeting"] = scalar(c)
    elif path in ("objections", "decision_criteria"):
        w.list_item(path, c, val)
w.sig("customer_replied", "2026-09-12T00:00:00Z", CHAMPION)
w.sig("meeting_accepted", "2026-09-12T00:00:00Z", CHAMPION)
one("neg_routine_claims_only", "pitch section 1 + L1 eval 'Did we identify what materially changed?'",
    "NEGATIVE: only routine activity (a reply, the usual quarterly check-in, a minor objection, a decision criterion, the champion's "
    "unchanged role). Nothing material changed -> no transition.", UNK, w, NOW, gold(None, "unknown"))

w, rel = reorg_world(R_CONF, R_BG)
kit_owner(w, "2026-08-15T00:00:00Z")
kit_stakeholder(w, "2026-08-25T00:00:00Z", standing="third_party")
kit_need(w, "2026-08-25T00:00:00Z", speaker=None, standing="third_party")
kit_value(w, "2026-09-01T00:00:00Z", standing="third_party", speaker=None)
one("neg_exp_third_party_bundle", S2 + " (first-party evidence) + section 3",
    "NEGATIVE: in REORG, a new owner is identified first-hand and stable, but the expansion need (intent data), the new BU stakeholder "
    "(enrichment hire record) and the value statement (review site) are all third-party. Only one first-party piece -> no transition.",
    rel, w, NOW, gold(None, "REORG"),
    contested={"reading_a": "Third-party evidence never counts toward a transition; one first-party piece is not a candidate.",
               "reading_b": "Third-party evidence cannot CONFIRM but may count as 'meaningful signals', making this a CANDIDATE EXPANSION.",
               "your_label_follows": "a"})

w, rel = reorg_world("2026-04-01T00:00:00Z", "2026-03-29T00:00:00Z")
bundle(w, owner_at="2026-04-15T00:00:00Z", stake_at="2026-05-15T00:00:00Z", need_at="2026-05-15T00:00:00Z",
       value_at="2026-05-15T00:00:00Z", comm_at="2026-05-15T00:00:00Z")
one("neg_exp_stale_evidence_beyond_90d", S3 + " + configured expansion-evidence window (90 days)",
    "NEGATIVE: in REORG, the full expansion bundle was observed but the need, stakeholder, value and commercial step are all 123 days old "
    "(beyond the 90-day window). Only the (unwindowed) stable owner remains: one piece -> no transition.",
    rel, w, NOW, gold(None, "REORG"))

w, rel = reorg_world(R_CONF, "2026-06-24T00:00:00Z")
bundle(w, owner_at="2026-06-25T00:00:00Z", stake_at="2026-06-26T00:00:00Z", need_at="2026-06-26T00:00:00Z",
       value_at="2026-06-20T00:00:00Z", comm_at="2026-06-28T00:00:00Z")
one("neg_exp_evidence_reused_from_reorg", S3 + " ('remain in REORG until enough evidence...') + new-evidence rule",
    "NEGATIVE: REORG was confirmed on 2026-07-01 and every piece of expansion-looking evidence predates that confirmation (it was part of "
    "the picture that earned REORG). Nothing new since; evidence cannot be reused to earn the next state -> no transition.",
    rel, w, "2026-07-20T00:00:00Z", gold(None, "REORG"))

w1, rel = reorg_world(R_CONF, R_BG)
bundle(w1, owner_at="2026-08-15T00:00:00Z", stake_at="2026-08-25T00:00:00Z", need_at="2026-08-25T00:00:00Z",
       value_at="2026-09-01T00:00:00Z", comm_at="2026-09-10T00:00:00Z", tenure="interim")
w2 = w1.clone()
rl = w2.add(mk_claim("stakeholder_role", "2026-09-20T00:00:00Z", "Director of Operations",
                     "I've now been appointed permanently as Director of Operations.", OWNER2, speaker=OWNER2))
w2.member(OWNER2, ["champion"], "active", "2026-09-20T00:00:00Z", rl, "Director of Operations", "permanent")
scenario("neg_exp_interim_owner_then_permanent", S3 + " ('ownership stabilized') + configured owner-stability rule",
         "NEGATIVE (step 1): the full bundle, but the new owner is explicitly an acting/interim holder, so ownership is not stabilized -> "
         "CANDIDATE, not CONFIRMED. Step 2: the same person is appointed permanently and has held the permanent role 15 days (and the "
         "role overall 51 days); all expansion evidence is still within 90 days -> CONFIRMED.",
         rel, [("interim owner, full bundle", w1, NOW, gold("CANDIDATE", "REORG", "EXPANSION")),
               ("owner made permanent 15 days ago", w2, "2026-10-05T00:00:00Z", gold("CONFIRMED", "EXPANSION", "EXPANSION"))])

w, rel = reorg_world(R_CONF, R_BG)
ow = w.add(mk_claim("owner", "2026-09-05T00:00:00Z", SELLER_AE2, None, standing="crm_explicit"))
w.fields["owner"] = scalar(ow)
kit_need(w, "2026-09-08T00:00:00Z", speaker=OWNER2)
one("neg_exp_seller_side_owner_change", S3 + " ('new relationship owner/champion identified')",
    "NEGATIVE: in REORG, our own account executive was reassigned (seller-side owner field) and an existing user states an expansion need. "
    "The seller-side owner is not the customer's new owner/champion, so only one customer-side piece exists -> no transition.",
    rel, w, NOW, gold(None, "REORG"),
    contested={"reading_a": "'New relationship owner/champion' means the customer-side owner; a seller-side reassignment is not evidence, so no transition.",
               "reading_b": "'Relationship owner' could mean the seller-side account owner, making two pieces and a CANDIDATE EXPANSION.",
               "your_label_follows": "a"})

w = baseline()
ai = w.add(mk_claim("champion", "2026-09-10T00:00:00Z", OWNER2, "You can send the dashboard question to my colleague.", OWNER2, speaker=CHAMPION))
win = [c for c in w.claims if c["field_path"] == "champion"][0]
w.fields["champion"] = scalar(win, conflicts=[(ai, "Newer AI-extracted claim names a different champion")])
w.sig("field_contradicted", "2026-09-10T00:00:00Z", OWNER2, ai,
      details={"field": "champion", "winning_claim_id": win["id"], "winning_standing": win["standing"],
               "contradicting_claim_id": ai["id"], "contradicting_standing": "first_party_ai"})
one("neg_contradiction_without_suspected_transition", S2 + " (REJECTED needs a previously suspected transition)",
    "NEGATIVE: no transition is open; a lower-standing claim contradicts the CRM champion (winner stands). REJECTED only applies to a "
    "previously suspected transition, and one routine contradiction is not change evidence -> no transition.",
    UNK, w, NOW, gold(None, "unknown"))

# ================================================================ D. time boundaries on the expansion side
