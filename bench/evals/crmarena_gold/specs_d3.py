"""Specs from deal 006Wt000007BCRfIAO (TrueNorth Technologies, Tech Infrastructure Optimization; rep Carlos Fernandez).

Real record: three rep emails (2023-11-24/25) prepare a negotiation meeting 'November 30th at 10:30 AM'; Klara Novakova
(Legal Affairs Manager, the only customer correspondent) answers three times on 11-25, asks for industry case studies
and ROI, and writes 'Please include our CTO, Martin Hrubý, in the conversation'. Martin Hrubý is not a Contact in the
export. The rep's 'Negotiation Meeting' task is dated 2023-12-01; nothing is recorded after 11-25.
"""
from __future__ import annotations

from .specs_common import items, spec

DEAL = "006Wt000007BCRfIAO"
KLARA, EMILY = "003Wt00000JqwWPIAZ", "003Wt00000JqwWQIAZ"
OUT1, OUT2, OUT3 = "02sWt000001zx12IAA", "02sWt000001zwMnIAI", "02sWt000001zuO7IAI"
IN1, IN2, IN3 = "02sWt000001zrJrIAI", "02sWt000001zpl7IAA", "02sWt000002006wIAA"
T_NEGO = "00TWt000002z4lWMAQ"
Q_CTO = "Please include our CTO, Martin Hrubý, in the conversation as he will bring additional insights into our technical requirements."
Q_MEET = "negotiation meeting scheduled for November 30th at 10:30 AM"
Q_CASES = "I have a few additional questions about specific case studies related to our industry and the potential ROI we might expect."
Q_REVIEW = "I will review the specifics of your offer, particularly around DevVision IDE, and get back to you if we have further questions or require clarifications before the meeting."
SIGN = "Best regards,\nCarlos"
GROUP = [(EMILY, ["legal"])]
PROV = {"buying_group roles": "inferred from each Contact's Title/Department; the record has no roles"}

STATE_CTO = {
    "group_extra": GROUP,
    "fields": {
        "decision_process": "Negotiation meeting 2023-11-30 10:30 to finalize terms and pricing; Klara asked that the CTO, Martin Hrubý (not a Contact in the record), be included",
        "objections": items("Wants industry case studies and expected ROI before the meeting"),
        "current_commitments": items(
            "Us: include Martin Hrubý (CTO) in the 2023-11-30 conversation, as the customer asked",
            "Us: provide industry case studies and ROI expectations (asked 2023-11-25)",
            "Customer: review the DevVision IDE offer and send questions before the meeting"),
        "next_milestone": "Negotiation meeting on 2023-11-30 at 10:30"},
    "gaps": ["economic_buyer", "technical_evaluator"],
    "provenance": {"decision_process": f"{OUT2}, {IN3}", "objections": IN2, "current_commitments": f"{IN1}, {IN2}, {IN3}", **PROV},
}
STATE_DUP = {
    "group_extra": GROUP,
    "fields": {
        "decision_process": "Negotiation meeting set for 2023-11-30 10:30 in the rep's emails; Klara is reviewing the DevVision IDE offer",
        "current_commitments": items("Customer: review the specifics of the DevVision IDE offer and revert before the meeting"),
        "next_milestone": "Negotiation meeting on 2023-11-30 at 10:30"},
    "provenance": {"decision_process": f"{OUT2}, {IN1}", "current_commitments": IN1, **PROV},
}
STATE_STALE = {
    "group_extra": GROUP,
    "fields": {
        "decision_process": "Negotiation meeting was planned for 2023-11-30 10:30; the record does not show whether it took place",
        "objections": items("Wants industry case studies and expected ROI"),
        "current_commitments": items(
            "Us: include Martin Hrubý (CTO) in the conversation, as the customer asked",
            "Us: provide industry case studies and ROI expectations (asked 2023-11-25)"),
        "next_milestone": "unknown: the planned 2023-11-30 meeting date has passed"},
    "gaps": ["economic_buyer", "technical_evaluator"],
    "provenance": {"next_milestone": f"{OUT2} (date) and the absence of any later email", "current_commitments": f"{IN2}, {IN3}", **PROV},
}
RECENT_CTO = ("Klara replied three times on 2023-11-25: she will review the DevVision IDE offer, wants industry case studies and "
              "ROI, and asks that the CTO, Martin Hrubý, be included.", ["decision_process", "objections", "current_commitments"],
              ["customer_replied", "new_stakeholder_entered"])
SUPPORT_CTO = [(OUT2, 800), (IN1, 700), (IN2, None)]

SPECS = [
    spec(id="cr_d3_cto_requested_but_not_included", type="under_threading", deal=DEAL, trigger=IN3, title="Meeting confirmation to Klara alone although she asked for the CTO to be included",
         support=SUPPORT_CTO, state=STATE_CTO, recent=RECENT_CTO,
         commitments=[("Include Martin Hrubý (CTO) in the 2023-11-30 conversation", "rep", "2023-11-30T10:30:00Z", "open"),
                      ("Provide industry case studies and ROI expectations", "rep", "2023-11-30T10:30:00Z", "open")],
         stated_timing="Meeting 2023-11-30 10:30 AM",
         cand={"action": "send_email", "to": [KLARA], "subject": "Confirming Thursday's negotiation meeting",
               "body": "Hi Klára,\n\nThank you for the replies. We are confirmed for the negotiation meeting on November 30 at 10:30 AM. I will send "
                       "an agenda the day before and bring the DevVision IDE pricing details so we can finalize terms.\n\n" + SIGN,
               "next_step": "Negotiation meeting 2023-11-30 10:30", "due": "2023-11-30T10:30:00Z", "reason": "Confirm the meeting and keep momentum toward terms.",
               "refs": [(OUT2, Q_MEET)]},
         best={"action": "send_email", "to": [KLARA], "channel": "email",
               "why": "Confirm the meeting, ask for Martin Hrubý's address to add him, and commit to the case studies and ROI figures."},
         evals=["stakeholder_selection", "stakeholder_coverage", "next_step_quality"],
         intent={"should_pass": False}, difficulty="easy", extra=["named_stakeholder_requested", "inbound_email"]),
    spec(id="cr_d3_asks_for_ctos_address_and_adds_him", type="under_threading", deal=DEAL, trigger=IN3, title="Same moment, good draft: Klara's request is acted on, the CTO's address is asked for, the case studies are promised",
         support=SUPPORT_CTO, state=STATE_CTO, recent=RECENT_CTO,
         commitments=[("Include Martin Hrubý (CTO) in the 2023-11-30 conversation", "rep", "2023-11-30T10:30:00Z", "open"),
                      ("Provide industry case studies and ROI expectations", "rep", "2023-11-30T10:30:00Z", "open")],
         cand={"action": "send_email", "to": [KLARA], "subject": "Adding Martin Hrubý to Thursday's meeting",
               "body": "Hi Klára,\n\nHappy to include Martin Hrubý. Could you send me his email address so I can add him to the invite for November 30 at "
                       "10:30 AM? I will also bring industry case studies and the ROI expectations you asked about.\n\n" + SIGN,
               "next_step": "Klára sends Martin Hrubý's address; invite updated", "due": None,
               "reason": "She asked for the CTO to be included and for case studies and ROI; he is not a Contact yet, so ask for his address.",
               "refs": [(IN3, Q_CTO), (IN2, Q_CASES)]},
         best={"action": "send_email", "to": [KLARA], "channel": "email", "why": "This: include the CTO as asked and answer the case-study request."},
         evals=["stakeholder_selection", "stakeholder_coverage", "next_step_quality", "grounding"],
         intent={"should_pass": True}, difficulty="medium", extra=["named_stakeholder_requested", "inbound_email", "good_draft"]),
    spec(id="cr_d3_fourth_negotiation_prep_email", type="duplicate_action", deal=DEAL, trigger=IN1, title="A fourth 'negotiation meeting preparation' email after three on the same theme in 16 hours",
         support=[(OUT1, 600), (OUT2, 600), (OUT3, 600)], state=STATE_DUP, recent=(
             "Klara answered the preparation emails: she will review the DevVision IDE offer and revert before the meeting.",
             ["decision_process", "current_commitments"], ["customer_replied"]),
         commitments=[("Customer reviews the DevVision IDE offer and reverts before the meeting", "customer", None, "open")],
         cand={"action": "send_email", "to": [KLARA], "subject": "Negotiation Meeting Preparation", "body": "Hi Klára,\n\nA quick note ahead of our negotiation "
               "meeting on November 30 at 10:30 AM: AI Cirku-Tech scales with TrueNorth's growing data demands, our integration process is "
               "fast and seamless, and DevVision IDE comes with a 5% discount. Let me know what else to prepare.\n\n" + SIGN,
               "next_step": "Negotiation meeting 2023-11-30 10:30", "due": "2023-11-30T10:30:00Z", "reason": "Keep the meeting preparation on her radar.",
               "refs": [(OUT2, Q_MEET)]},
         best={"action": "wait", "wait_until": "2023-11-27T09:00:00Z", "channel": "none",
               "why": "Three preparation emails already went out; Klara is reviewing the offer and will revert. Wait for her questions."},
         evals=["duplicate_action", "next_action_quality", "relationship_pressure"],
         intent={"should_pass": False}, difficulty="easy", extra=["inbound_email", "same_day_duplicates"]),
    spec(id="cr_d3_looking_forward_to_a_meeting_that_has_passed", type="stale_commitment", deal=DEAL, trigger=T_NEGO, now="2023-12-01T09:00:00Z",
         title="'Looking forward to our meeting on November 30' sent on December 1",
         support=[(IN3, None), (OUT2, 700), (IN2, None)], state=STATE_STALE, recent=(
             "The rep's 'Negotiation Meeting' task is due 2023-12-01; the planned meeting date (11-30) has passed; no email since 11-25.",
             ["next_milestone"], ["customer_went_silent", "commitment_overdue"]),
         commitments=[("Negotiation meeting planned for 2023-11-30 10:30", "rep", "2023-11-30T10:30:00Z", "overdue"),
                      ("Include Martin Hrubý (CTO) and provide case studies and ROI", "rep", None, "open")],
         cand={"action": "send_email", "to": [KLARA], "subject": "Our negotiation meeting",
               "body": "Hi Klára,\n\nLooking forward to our negotiation meeting on November 30 at 10:30 AM. I will send the agenda tomorrow so we can "
                       "finalize terms and pricing.\n\n" + SIGN,
               "next_step": "Negotiation meeting 2023-11-30 10:30", "due": "2023-11-30T10:30:00Z", "reason": "Task due: confirm the meeting.",
               "refs": [(OUT2, Q_MEET)]},
         best={"action": "send_email", "to": [KLARA], "channel": "email",
               "why": "Close the loop: ask whether the 11-30 meeting took place, offer new times, and deliver the CTO invitation and case studies."},
         evals=["commitment_consistency", "timing_cadence", "next_action_quality"],
         intent={"should_pass": False}, difficulty="easy", extra=["task_trigger", "past_slot"]),
    spec(id="cr_d3_asks_whether_the_planned_meeting_took_place", type="stale_commitment", deal=DEAL, trigger=T_NEGO, now="2023-12-01T09:00:00Z",
         title="Same moment, good draft: closes the loop on the passed meeting date and re-offers the CTO invitation",
         support=[(IN3, None), (OUT2, 700), (IN2, None)], state=STATE_STALE, recent=(
             "The rep's 'Negotiation Meeting' task is due 2023-12-01; the planned meeting date (11-30) has passed; no email since 11-25.",
             ["next_milestone"], ["customer_went_silent", "commitment_overdue"]),
         commitments=[("Negotiation meeting planned for 2023-11-30 10:30", "rep", "2023-11-30T10:30:00Z", "overdue"),
                      ("Include Martin Hrubý (CTO) and provide case studies and ROI", "rep", None, "open")],
         cand={"action": "send_email", "to": [KLARA], "subject": "Closing the loop on our negotiation meeting",
               "body": "Hi Klára,\n\nI want to close the loop on the negotiation meeting we had planned for November 30 at 10:30 AM. Did it go ahead as "
                       "planned? If not, I can offer new times this week, and I will make sure Martin Hrubý is invited as you asked. I can also send "
                       "the industry case studies and ROI figures ahead of it.\n\n" + SIGN,
               "next_step": "Klára confirms status or picks a new time", "due": None,
               "reason": "The planned date has passed with no record of the meeting; ask rather than assume.", "refs": [(OUT2, Q_MEET), (IN3, Q_CTO)]},
         best={"action": "send_email", "to": [KLARA], "channel": "email", "why": "This: ask, offer new times, keep the CTO and case-study commitments."},
         evals=["commitment_consistency", "timing_cadence", "next_action_quality"],
         intent={"should_pass": True}, difficulty="medium", extra=["task_trigger", "past_slot", "good_draft"]),
]
