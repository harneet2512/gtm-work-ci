"""Specs from deal 006Wt000007B1klIAC (InspireTech Collaboration Expansion; rep Mei Chen).

A thin real record: the rep's recap (2023-11-20 14:00) and Hiroshi Saito's reply (15:30), who asks for another meeting
'including perhaps a demonstration' of SecureFlow Suite's integration capabilities. Then nothing but rep tasks:
'Create tailored proposal' (12-05), 'Organize a product demo' (12-10), case studies (12-12), negotiation (12-15).
"""
from __future__ import annotations

from .specs_common import items, spec

DEAL = "006Wt000007B1klIAC"
HIROSHI, ANISHA = "003Wt00000Jqe4vIAB", "003Wt00000JqwWRIAZ"
OUT_1, IN_1 = "02sWt000001zurFIAQ", "02sWt00000204nJIAQ"
T_PROP, T_DEMO = "00TWt000002z8SfMAI", "00TWt000002z7jOMAQ"
Q_DEMO = "Could we schedule another meeting to discuss this further, including perhaps a demonstration of these features in action?"
Q_INTEG = "I’d be keen to delve deeper into the integration capabilities of SecureFlow Suite."
SIGN = "Best regards,\nMei"
PROV = {"buying_group roles": "inferred from each Contact's Title/Department; the record has no roles"}
STATE = {
    "group_extra": [(ANISHA, ["unknown"])],
    "fields": {
        "decision_process": "unknown: no approver, budget, timeline or review step is stated anywhere in the record",
        "current_commitments": items("Us: another meeting including a demonstration of SecureFlow Suite's integration capabilities (asked 2023-11-20)"),
        "next_milestone": "unknown"},
    "provenance": {"current_commitments": IN_1, "decision_process": "absence of any statement in the two emails", **PROV},
}
RECENT = ("No email since Hiroshi's request for another meeting and a demonstration on 2023-11-20; the rep's tasks have come due.",
          ["current_commitments"], ["customer_went_silent"])
COMMITS = [("Another meeting including a SecureFlow Suite integration demonstration (asked 2023-11-20)", "rep", None, "open")]
SUPPORT = [(IN_1, None), (OUT_1, 800)]

SPECS = [
    spec(id="cr_d6_proposal_attached_as_if_the_demo_request_were_fresh", type="too_late_follow_up", deal=DEAL, trigger=T_PROP, now="2023-12-05T09:00:00Z",
         title="Proposal sent after fifteen days of silence with no mention of the unanswered meeting and demo request",
         support=SUPPORT, state=STATE, recent=RECENT, commitments=COMMITS,
         cand={"action": "send_email", "to": [HIROSHI], "subject": "InspireTech: tailored proposal", "attachments": ["InspireTech_Tailored_Proposal.pdf"],
               "body": "Hi Hiroshi,\n\nAttached is our tailored proposal for InspireTech. Let me know if you have any questions and we can discuss on a call whenever works.\n\n" + SIGN,
               "next_step": "Await questions", "due": None, "reason": "The proposal task is due; send it.", "refs": [(IN_1, Q_INTEG)]},
         best={"action": "send_email", "to": [HIROSHI], "channel": "email",
               "why": "Acknowledge the 15-day gap, offer dates for the meeting and demo he asked for, and send the proposal ahead of it."},
         evals=["timing_cadence", "commitment_consistency", "momentum", "next_action_quality"],
         intent={"should_pass": False}, difficulty="medium", extra=["task_trigger", "two_weeks_of_silence"]),
    spec(id="cr_d6_apologises_for_the_gap_and_offers_demo_dates", type="too_late_follow_up", deal=DEAL, trigger=T_PROP, now="2023-12-05T09:00:00Z",
         title="Same moment, good draft: owns the gap, offers two dates for the requested meeting and demo",
         support=SUPPORT, state=STATE, recent=RECENT, commitments=COMMITS,
         cand={"action": "send_email", "to": [HIROSHI], "subject": "InspireTech: your request for a meeting and demo", "attachments": ["InspireTech_Tailored_Proposal.pdf"],
               "body": "Hi Hiroshi,\n\nApologies for the long silence since our conversation on 20 November. You asked for another meeting and a demonstration of the SecureFlow "
                       "Suite integration capabilities. Could I offer Tuesday, December 12 at 11:00 or Thursday, December 14 at 15:00? I have attached our tailored proposal so "
                       "you can read it beforehand.\n\n" + SIGN,
               "next_step": "Hiroshi picks 2023-12-12 11:00 or 2023-12-14 15:00", "due": None,
               "reason": "The request is 15 days old; say so and give concrete dates.", "refs": [(IN_1, Q_DEMO)]},
         best={"action": "send_email", "to": [HIROSHI], "channel": "email", "why": "This."},
         evals=["timing_cadence", "commitment_consistency", "momentum", "next_action_quality"],
         intent={"should_pass": True}, difficulty="medium", extra=["task_trigger", "two_weeks_of_silence", "good_draft"]),
    spec(id="cr_d6_invents_budget_go_live_and_a_ciso", type="insufficient_evidence", deal=DEAL, trigger=T_DEMO, now="2023-12-10T09:00:00Z",
         title="Demo invitation planned around a January go-live the record never mentions",
         support=SUPPORT, state=STATE, recent=RECENT, commitments=COMMITS,
         cand={"action": "schedule_meeting", "to": [HIROSHI, ANISHA], "subject": "SecureFlow Suite demo for InspireTech", "channel": "email",
               "body": "Hi Hiroshi,\n\nThanks for your patience. With your January go-live in mind, I have booked the SecureFlow Suite demonstration for Friday, "
                       "December 15 at 10:00 and copied Anisha Gupta from your project team.\n\n" + SIGN,
               "next_step": "Demo on 2023-12-15 10:00", "due": "2023-12-15T10:00:00Z", "reason": "Demo task due; plan the date around the go-live.",
               "refs": [(IN_1, Q_INTEG)]},
         best={"action": "send_email", "to": [HIROSHI], "channel": "email",
               "why": "Ask for what the record lacks (who attends, timeline, constraints) and offer demo dates; assert nothing about budget, go-live or titles."},
         evals=["grounding", "evidence_sufficiency", "next_action_quality"],
         intent={"should_pass": False}, difficulty="easy", extra=["task_trigger", "invented_facts"]),
    spec(id="cr_d6_asks_before_planning_the_demo", type="insufficient_evidence", deal=DEAL, trigger=T_DEMO, now="2023-12-10T09:00:00Z",
         title="Same moment, good draft: asks who should attend and what matters before planning the demo",
         support=SUPPORT, state=STATE, recent=RECENT, commitments=COMMITS,
         cand={"action": "send_email", "to": [HIROSHI], "subject": "Planning the SecureFlow Suite demo", "channel": "email",
               "body": "Hi Hiroshi,\n\nI would like to plan the SecureFlow Suite demonstration you asked for. So that it covers the right ground, could you tell me who from your team "
                       "should attend, which integrations matter most to you, and what timeline you are working to? I can offer Wednesday, December 13 or Friday, December 15 once I know.\n\n" + SIGN,
               "next_step": "Hiroshi shares attendees, priority integrations and timeline", "due": None,
               "reason": "The record states no attendees, budget or timeline; ask before assuming.", "refs": [(IN_1, Q_DEMO)]},
         best={"action": "send_email", "to": [HIROSHI], "channel": "email", "why": "This."},
         evals=["grounding", "evidence_sufficiency", "next_action_quality"],
         intent={"should_pass": True}, difficulty="medium", extra=["task_trigger", "good_draft"]),
]
