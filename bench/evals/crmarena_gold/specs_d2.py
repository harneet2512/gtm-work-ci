"""Specs from deal 006Wt000007BAeOIAW (SkyLink Communications EDA Expansion; rep Mohamed Ahmed).

Real record: the rep emails a tailored proposal on 2023-11-14 16:00 and asks for 'your availability this week for a brief
call'; Keiko Matsuda answers an hour later and offers Thursday afternoon (2023-11-16).
"""
from __future__ import annotations

from .specs_common import items, spec

DEAL = "006Wt000007BAeOIAW"
KEIKO, ZAINAB = "003Wt00000Jqy6nIAB", "003Wt00000JquciIAB"
OUT_PROPOSAL, IN_CALL = "02sWt000002008ZIAQ", "02sWt00000200N4IAI"
Q_CALL = "I'd be interested in arranging a time to discuss this further. Could we schedule a call later this week? I have some availability on Thursday afternoon."
Q_PRICE = "your pricing transparency is certainly appreciated"

STATE = {
    "group_extra": [(ZAINAB, ["legal"])],
    "fields": {
        "decision_process": "unknown: no approver, timeline or review step is stated",
        "current_commitments": items("Us: set up a call with Keiko; she offered Thursday afternoon (2023-11-16)"),
        "next_milestone": "Call with Keiko on Thursday afternoon, 2023-11-16"},
    "provenance": {"current_commitments": IN_CALL, "next_milestone": IN_CALL,
                   "buying_group roles": "inferred from each Contact's Title/Department; the record has no roles"},
}
RECENT = ("Keiko replied within an hour of the tailored proposal: promising, pricing transparency appreciated, and asks "
          "for a call, offering Thursday afternoon.", ["current_commitments", "next_milestone"], ["customer_replied", "pricing_interest"])
COMMITS = [("Set up a call with Keiko (she offered Thursday afternoon, 2023-11-16)", "rep", "2023-11-16T00:00:00Z", "open")]
SUPPORT = [(OUT_PROPOSAL, 1100)]
SIGN = "Best regards,\nMohamed"

SPECS = [
    spec(id="cr_d2_long_email_instead_of_the_requested_call", type="wrong_channel", deal=DEAL, trigger=IN_CALL,
         title="A long written overview sent when the buyer asked for a call on Thursday afternoon",
         support=SUPPORT, state=STATE, recent=RECENT, commitments=COMMITS,
         stated_timing="Keiko: 'Could we schedule a call later this week? I have some availability on Thursday afternoon.'",
         cand={"action": "send_email", "to": [KEIKO], "subject": "Re: Tailored proposal for EDA expansion",
               "body": "Hi Keiko,\n\nThanks for the quick reply. To save you time, here is the proposal in brief: PulseSim Pro for simulation accuracy, "
                       "SecureAnalytics Pro for data protection and real-time analysis, guided setup and transparent pricing with long-term value "
                       "from customizable EDA solutions. Everything you need is in the proposal; let me know if any questions come up.\n\n" + SIGN,
               "next_step": "Await questions", "due": None, "reason": "A written summary answers her note without taking her time.",
               "refs": [(IN_CALL, Q_PRICE)]},
         best={"action": "schedule_meeting", "to": [KEIKO], "channel": "email",
               "why": "She asked for a call and offered Thursday afternoon: book Thursday 2023-11-16 and confirm by email."},
         evals=["channel_appropriateness", "next_step_quality", "next_action_quality"],
         intent={"should_pass": False}, difficulty="easy", extra=["call_requested", "inbound_email"]),
    spec(id="cr_d2_crm_note_only_no_reply_to_customer", type="wrong_channel", deal=DEAL, trigger=IN_CALL,
         title="Right content, wrong place: the call request is logged as a CRM note and nothing goes to the buyer",
         support=SUPPORT, state=STATE, recent=RECENT, commitments=COMMITS,
         cand={"action": "internal_note", "recipients": [], "channel": "crm_note", "subject": None,
               "body": "Keiko Matsuda (SkyLink) replied to the tailored proposal and wants a call on Thursday afternoon (2023-11-16). Schedule it.",
               "next_step": "Rep to schedule the call", "due": None,
               "reason": "Capture the request in the CRM so the rep can act on it.", "refs": [(IN_CALL, Q_CALL)]},
         best={"action": "schedule_meeting", "to": [KEIKO], "channel": "email",
               "why": "Book the Thursday-afternoon call she offered and confirm it to her by email."},
         evals=["channel_appropriateness", "next_step_quality", "next_action_quality"],
         intent={"should_pass": False}, difficulty="medium", extra=["call_requested", "inbound_email", "internal_only"]),
    spec(id="cr_d2_books_the_thursday_call", type="correct_timing", deal=DEAL, trigger=IN_CALL,
         title="Same moment, good draft: the Thursday-afternoon call is booked and confirmed the same hour",
         support=SUPPORT, state=STATE, recent=RECENT, commitments=COMMITS,
         cand={"action": "schedule_meeting", "to": [KEIKO], "subject": "Call on Thursday, November 16 at 2:00 PM",
               "body": "Hi Keiko,\n\nThursday afternoon works well. I have sent an invite for Thursday, November 16 at 2:00 PM; if another time that "
                       "afternoon suits you better, tell me and I will move it. I will walk you through the proposal and answer questions on setup "
                       "guidance and pricing.\n\n" + SIGN,
               "channel": "email", "next_step": "Call on Thursday 2023-11-16 at 14:00", "due": "2023-11-16T14:00:00Z",
               "reason": "She asked for a call and offered Thursday afternoon; confirm quickly.", "refs": [(IN_CALL, Q_CALL)]},
         best={"action": "schedule_meeting", "to": [KEIKO], "channel": "email",
               "why": "This: book Thursday 2023-11-16 afternoon and confirm by email."},
         evals=["timing_cadence", "buyer_readiness", "cta_calibration", "next_step_quality"],
         intent={"should_pass": True}, difficulty="easy", extra=["call_requested", "inbound_email", "good_draft"]),
    spec(id="cr_d2_books_thursday_and_attaches_the_order_form", type="correct_timing", deal=DEAL, trigger=IN_CALL,
         title="Right timing, premature commercial step: the Thursday call is booked the same hour and an order form is attached for reference",
         support=SUPPORT, state=STATE, recent=RECENT, commitments=COMMITS,
         cand={"action": "schedule_meeting", "to": [KEIKO], "subject": "Call on Thursday, November 16 at 2:00 PM", "attachments": ["SkyLink_Order_Form.pdf"],
               "body": "Hi Keiko,\n\nThursday afternoon works well. I have sent an invite for Thursday, November 16 at 2:00 PM. To make the call efficient I have attached our standard "
                       "order form for reference, so we can go through the commercial steps together.\n\n" + SIGN,
               "channel": "email", "next_step": "Call on 2023-11-16 14:00; order form read beforehand", "due": "2023-11-16T14:00:00Z",
               "reason": "She is positive; use the call to cover the commercial steps.", "refs": [(IN_CALL, Q_CALL)]},
         best={"action": "schedule_meeting", "to": [KEIKO], "channel": "email", "why": "Book the Thursday call and leave out the order form: she has not yet discussed the proposal."},
         evals=["timing_cadence", "buyer_readiness", "cta_calibration", "next_step_quality"],
         intent={"should_pass": False}, difficulty="medium", extra=["call_requested", "inbound_email", "signature_push"]),
]
