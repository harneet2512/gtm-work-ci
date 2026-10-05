"""Specs from deal 006Wt000007B62yIAC (PowerGrid Energy Optimization; rep Thato Mokoena).

Real record (2023-12-05/06): after a proposal call, Aditya Kumar asks for a brief meeting next week, case studies,
the implementation timeline and training; says he will discuss the proposal with the procurement team and revert
'by the end of this week'; the next morning he asks for a pricing plan with more room for customization. His role is
inconsistent in the record: the Contact says Research Analyst, one signature says Director of Energy Solutions, the
next says Procurement Manager.
"""
from __future__ import annotations

from .specs_common import items, spec

DEAL = "006Wt000007B62yIAC"
ADITYA, GABRIEL, FATIMA, YASIR = "003Wt00000JqxDxIAJ", "003Wt00000JqwUoIAJ", "003Wt00000JqrZJIAZ", "003Wt00000JqvfDIAR"
OUT_A, OUT_B, OUT_C = "02sWt00000202yLIAQ", "02sWt000001zpZtIAI", "02sWt000001zx5wIAA"
IN_A, IN_B, IN_C = "02sWt000001zpElIAI", "02sWt000001zu9iIAA", "02sWt000001zw1pIAA"
Q_PROC = "I’ll be discussing the proposal with our procurement team and should be able to get back to you with feedback or additional requests by the end of this week."
Q_MEET = "Could we schedule a brief meeting next week to address these points?"
Q_CASES = "if you have any case studies or references from similar projects, it would be helpful to review them as part of our decision-making process."
Q_ROOM = "We'll need a plan that allows more room for customizations as we have some specific project needs in mind."
Q_TIME = "Can you suggest a time next week to discuss this in more detail?"
Q_FLEX = "you raised an important concern regarding the flexibility of our pricing for custom projects"
SIGN = "Best regards,\nThato"
EXTRA = [(GABRIEL, ["unknown"]), (FATIMA, ["unknown"]), (YASIR, ["unknown"])]
PROV = {"buying_group roles": "inferred from each Contact's Title/Department; the record has no roles"}
CONFLICT = {"field": "champion role (Aditya Kumar's title)", "winner_value": "Research Analyst", "winner_standing": "crm_explicit",
            "winner_event_file": f"crmarena:Contact:{ADITYA}", "contradicting_value": "Procurement Manager / Director of Energy Solutions",
            "contradicting_standing": "first_party_record", "contradicting_event_file": f"crmarena:EmailMessage:{IN_C}",
            "contradicting_quote": "Procurement Manager"}

STATE_B = {
    "group_extra": EXTRA,
    "fields": {
        "decision_process": "Aditya will discuss the proposal with the procurement team and return feedback or additional requests by the end of this week (2023-12-08)",
        "objections": items("Wants the implementation timeline and training programs", "Wants case studies or references from similar projects"),
        "current_commitments": items(
            "Customer: feedback or additional requests by the end of this week (2023-12-08)",
            "Us: a brief meeting next week, the implementation timeline and training programs, and case studies (asked 2023-12-05)"),
        "next_milestone": "Customer feedback by 2023-12-08"},
    "provenance": {"decision_process": IN_B, "objections": IN_A, "current_commitments": f"{IN_A}, {IN_B}", **PROV},
}
STATE_C = {
    "group_extra": EXTRA,
    "fields": {
        **STATE_B["fields"],
        "objections": items("Wants the implementation timeline and training programs", "Wants case studies or references from similar projects",
                            "Wants a pricing plan with more room for customization"),
        "current_commitments": items(
            "Customer: feedback or additional requests by the end of this week (2023-12-08)",
            "Us: suggest a time next week to discuss flexible pricing (asked 2023-12-06)",
            "Us: implementation timeline, training programs and case studies (asked 2023-12-05)")},
    "conflicts": [CONFLICT],
    "provenance": {"decision_process": IN_B, "objections": f"{IN_A}, {IN_C}", "current_commitments": f"{IN_A}, {IN_B}, {IN_C}", **PROV},
}
RECENT_B = ("Aditya says he will take the proposal to the procurement team and revert by the end of the week; earlier today he asked "
            "for a meeting next week, case studies, the implementation timeline and training.",
            ["decision_process", "current_commitments", "objections"], ["customer_replied"])
RECENT_C = ("Aditya asks for a pricing plan with more room for customization and a time next week; his role differs across the "
            "Contact record and his two signatures.", ["objections", "current_commitments"], ["customer_replied", "pricing_interest", "field_contradicted"])

SPECS = [
    spec(id="cr_d4_contract_with_friday_deadline_while_procurement_reviews", type="too_soon_cta", deal=DEAL, trigger=IN_B,
         title="Final contract with a Friday signature deadline, hours after the buyer said procurement is still reviewing",
         support=[(IN_A, None), (OUT_C, 600)], state=STATE_B, recent=RECENT_B,
         commitments=[("Customer returns feedback by the end of the week (2023-12-08)", "customer", "2023-12-08T00:00:00Z", "open"),
                      ("Meeting next week, timeline, training and case studies", "rep", None, "open")],
         stated_timing="Aditya: 'should be able to get back to you with feedback or additional requests by the end of this week.'",
         cand={"action": "send_email", "to": [ADITYA], "subject": "Final contract for PowerGrid Innovations", "attachments": ["PowerGrid_Final_Contract.pdf"],
               "body": "Hi Aditya,\n\nThanks for reviewing the proposal with your team. To keep the discount in our proposal in place, I have attached the final "
                       "contract. Could you sign and return it by Friday, December 8?\n\n" + SIGN,
               "next_step": "Signed contract returned", "due": "2023-12-08T17:00:00Z", "reason": "He is positive on scalability; move to the close.",
               "refs": [(IN_B, Q_PROC)]},
         best={"action": "send_email", "to": [ADITYA], "channel": "email",
               "why": "Answer his open asks (meeting next week, timeline, training, case studies) and let procurement finish its review."},
         evals=["buyer_readiness", "cta_calibration", "decision_process"],
         intent={"should_pass": False}, difficulty="easy", extra=["signature_push", "inbound_email"]),
    spec(id="cr_d4_finance_officer_pricing_direct_dropping_aditya", type="champion_bypass", deal=DEAL, trigger=IN_B,
         title="Pricing-flexibility email sent to the Finance Officer alone; Aditya, the only correspondent, is not on it",
         support=[(IN_A, None), (OUT_B, None)], state=STATE_B, recent=RECENT_B,
         commitments=[("Customer returns feedback by the end of the week (2023-12-08)", "customer", "2023-12-08T00:00:00Z", "open")],
         cand={"action": "send_email", "to": [GABRIEL], "subject": "PowerGrid proposal: pricing flexibility",
               "body": "Hi Gabriel,\n\nI have been working with Aditya on the PowerGrid proposal. Pricing flexibility for custom projects came up, so I would "
                       "value 20 minutes with you before the end of the week to align on the commercial terms.\n\n" + SIGN,
               "next_step": "20-minute call with the Finance Officer", "due": None, "reason": "Finance owns the budget; go to them while pricing is on the table.",
               "refs": [(OUT_B, Q_FLEX)]},
         best={"action": "send_email", "to": [ADITYA], "channel": "email",
               "why": "Keep Aditya as the thread owner; answer his open asks and offer meeting times."},
         evals=["champion_continuity", "stakeholder_selection", "buyer_readiness"],
         intent={"should_pass": False}, difficulty="easy", extra=["inferred_champion", "inbound_email"],
         notes="The champion is inferred (the only correspondent); CRMArena has no roles. See labels_needed."),
    spec(id="cr_d4_three_colleagues_copied_on_a_pricing_reply", type="over_threading", deal=DEAL, trigger=IN_C,
         title="Reply to Aditya copying the Finance Officer, the Supply Chain Coordinator and an Operations Assistant 'so everyone sees the options'",
         support=[(IN_B, None), (IN_A, None)], state=STATE_C, recent=RECENT_C,
         commitments=[("Suggest a time next week to discuss flexible pricing", "rep", None, "open")],
         cand={"action": "send_email", "to": [ADITYA], "cc": [GABRIEL, FATIMA, YASIR], "subject": "Re: Proposal Follow-Up",
               "body": "Hi Aditya,\n\nGlad flexible pricing is of interest. I have copied Gabriel, Fatima and Yasir so everyone sees the options. Could we meet next "
                       "week, for example Monday, December 11 at 10:00, to walk through a plan with room for customization?\n\n" + SIGN,
               "next_step": "Meeting Monday 2023-12-11 10:00", "due": "2023-12-11T10:00:00Z", "reason": "Widen the thread while pricing is discussed.",
               "refs": [(IN_C, Q_ROOM)]},
         best={"action": "send_email", "to": [ADITYA], "channel": "email", "why": "Reply to Aditya alone with meeting times; he is taking it to procurement himself."},
         evals=["stakeholder_coverage", "stakeholder_selection", "cta_calibration"],
         intent={"should_pass": False}, difficulty="medium", extra=["multi_cc", "inbound_email"]),
    spec(id="cr_d4_offers_two_slots_and_the_requested_material", type="correct_timing", deal=DEAL, trigger=IN_C,
         title="Same moment, good draft: two slots next week plus the case studies and timeline he asked for",
         support=[(IN_B, None), (IN_A, None)], state=STATE_C, recent=RECENT_C,
         commitments=[("Suggest a time next week to discuss flexible pricing", "rep", None, "open")],
         cand={"action": "send_email", "to": [ADITYA], "subject": "Re: Proposal Follow-Up", "attachments": ["Case_studies_and_references.pdf"],
               "body": "Hi Aditya,\n\nThank you. We can build a plan with more room for customization. Would Monday, December 11 at 10:00 or Tuesday, December 12 "
                       "at 14:00 work for a call? I have attached the case studies and references you asked for yesterday, and I will bring the "
                       "implementation timeline and training options.\n\n" + SIGN,
               "next_step": "Call on 2023-12-11 10:00 or 2023-12-12 14:00 (Aditya picks)", "due": None,
               "reason": "He asked for a time next week; answer the same morning and deliver what he asked for.", "refs": [(IN_C, Q_TIME), (IN_A, Q_CASES)]},
         best={"action": "send_email", "to": [ADITYA], "channel": "email", "why": "This: slots, materials, nothing the buyer has not asked for."},
         evals=["timing_cadence", "buyer_readiness", "cta_calibration", "grounding"],
         intent={"should_pass": True}, difficulty="easy", extra=["inbound_email", "good_draft"]),
    spec(id="cr_d4_treats_one_signature_title_as_fact", type="conflicting_account_state", deal=DEAL, trigger=IN_C,
         title="Calls Aditya 'Director of Energy Solutions' and plans the contract for his signature although the record also says Research Analyst and Procurement Manager",
         support=[(IN_B, None), (IN_A, None)], state=STATE_C, recent=RECENT_C,
         commitments=[("Suggest a time next week to discuss flexible pricing", "rep", None, "open")],
         cand={"action": "send_email", "to": [ADITYA], "subject": "Re: Proposal Follow-Up",
               "body": "Hi Aditya,\n\nThank you. As the director on this, you can take the plan to approval on your side, so I will have the contract ready alongside "
                       "the plan with more room for customization, in time for the meeting next week.\n\n" + SIGN,
               "next_step": "Plan and contract ready for Aditya's approval next week", "due": None, "reason": "He signs as a director; he can decide.",
               "refs": [(IN_B, "Director of Energy Solutions")]},
         best={"action": "send_email", "to": [ADITYA], "channel": "email",
               "why": "Do not assert his role or authority; ask who approves pricing and terms, and offer meeting times (the title conflict goes to the rep)."},
         evals=["grounding", "decision_process", "economic_buyer_coverage"],
         intent={"should_pass": False}, difficulty="hard", extra=["title_conflict", "inbound_email"]),
    spec(id="cr_d4_asks_who_approves_instead_of_assuming", type="conflicting_account_state", deal=DEAL, trigger=IN_C,
         title="Same moment, good draft: asks who approves pricing and terms rather than assuming from a signature",
         support=[(IN_B, None), (IN_A, None)], state=STATE_C, recent=RECENT_C,
         commitments=[("Suggest a time next week to discuss flexible pricing", "rep", None, "open")],
         cand={"action": "send_email", "to": [ADITYA], "subject": "Re: Proposal Follow-Up",
               "body": "Hi Aditya,\n\nThank you; we can build a plan with more room for customization. So that I involve the right people on your side, could you tell me "
                       "who needs to approve the pricing and the final terms? I am free next week, for example Monday, December 11 at 10:00, to walk "
                       "through the plan.\n\n" + SIGN,
               "next_step": "Aditya names the approver; call Monday 2023-12-11 10:00", "due": None,
               "reason": "His role differs across the record; ask instead of assuming.", "refs": [(IN_C, Q_ROOM)]},
         best={"action": "send_email", "to": [ADITYA], "channel": "email", "why": "This."},
         evals=["grounding", "decision_process", "economic_buyer_coverage"],
         intent={"should_pass": True}, difficulty="medium", extra=["title_conflict", "inbound_email", "good_draft"]),
]
