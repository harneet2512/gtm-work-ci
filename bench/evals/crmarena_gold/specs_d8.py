"""Specs from deal 006Wt000007BBDrIAO (TerraForm's Sustainable EDA Expansion; rep Jaemin Park).

Real record, 2023-11-29: the rep emails a proposal draft (14:00); Samuel Okafor (Security Engineer, the only correspondent)
asks about AI Cirku-Tech update frequency and 'whether they come at any additional costs' (16:30), wants security details and
testimonials (16:00) and asks to 'arrange a time next week'. The rep's 17:30 reply does not say whether updates cost extra and
promises client testimonials. The next record is the rep's 'Organize product demo' task (2023-12-05).
"""
from __future__ import annotations

from .specs_common import items, spec

DEAL = "006Wt000007BBDrIAO"
SAMUEL = "003Wt00000JqmDmIAJ"
OUT_DRAFT, IN_16, IN_1630, OUT_1730 = "02sWt000001zwrbIAA", "02sWt000001zqAuIAI", "02sWt00000200VCIAY", "02sWt00000200QUIAY"
T_DEMO = "00TWt000002z44CMAQ"
Q_COST = "it's crucial for us to understand how frequently these updates occur and whether they come at any additional costs."
Q_TIME = "Let’s arrange a time next week to delve deeper and discuss any adjustments necessary."
Q_PROMISE = "I will send over some client testimonials that I believe will resonate with your needs and give more perspective on user satisfaction."
SIGN = "Kind regards,\nJaemin"
NOW = "2023-12-05T09:00:00Z"
PROV = {"buying_group roles": "inferred from each Contact's Title/Department; the record has no roles"}
STATE = {
    "group_extra": [],
    "fields": {
        "decision_process": "unknown: no approver, timeline or review step is stated",
        "objections": items("Wants to know how frequently AI Cirku-Tech updates occur and whether they come at any additional cost"),
        "current_commitments": items(
            "Us: answer the update-frequency and additional-cost question (asked 2023-11-29)",
            "Us: send client testimonials (promised 2023-11-29 17:30)",
            "Customer asked to arrange a time 'next week' (2023-11-29)"),
        "next_milestone": "A meeting 'next week', asked for by the customer on 2023-11-29"},
    "provenance": {"objections": IN_1630, "current_commitments": f"{IN_1630}, {OUT_1730}", **PROV},
}
RECENT = ("Six days after Samuel's questions the rep's demo task is due.", ["objections", "current_commitments"], [])
COMMITS = [("Answer the update-frequency and additional-cost question", "rep", None, "open"),
           ("Send client testimonials (promised 2023-11-29)", "rep", None, "open")]
SUPPORT = [(IN_1630, None), (IN_16, 700), (OUT_1730, None)]
SLOTS = "Tuesday, December 12 at 10:00 or Thursday, December 14 at 14:00"

SPECS = [
    spec(id="cr_d8_demo_slots_while_the_cost_of_updates_stays_unanswered", type="unresolved_blocker", deal=DEAL, trigger=T_DEMO, now=NOW,
         title="Demo slots offered; the buyer's open question on whether updates cost extra and the promised testimonials are skipped",
         support=SUPPORT, state=STATE, recent=RECENT, commitments=COMMITS,
         cand={"action": "send_email", "to": [SAMUEL], "subject": "Demo of EcoPCB Creator and OptiPower Manager",
               "body": "Hi Samuel,\n\nI would like to set up the demonstration of EcoPCB Creator and OptiPower Manager. Would " + SLOTS + " suit you?\n\n" + SIGN,
               "next_step": "Demo on 2023-12-12 10:00 or 2023-12-14 14:00", "due": None, "reason": "The demo task is due; offer dates next week as he asked.",
               "refs": [(IN_1630, Q_TIME)]},
         best={"action": "send_email", "to": [SAMUEL], "channel": "email", "why": "Deliver the testimonials, say how the update-cost question will be answered and by when, and offer the demo slots."},
         evals=["commitment_consistency", "next_step_quality", "next_action_quality"],
         intent={"should_pass": False}, difficulty="medium", extra=["task_trigger", "open_cost_question"]),
    spec(id="cr_d8_testimonials_cost_answer_date_and_demo_slots", type="unresolved_blocker", deal=DEAL, trigger=T_DEMO, now=NOW,
         title="Same moment, good draft: testimonials attached, a dated promise on the update-cost question, demo slots",
         support=SUPPORT, state=STATE, recent=RECENT, commitments=COMMITS,
         cand={"action": "send_email", "to": [SAMUEL], "subject": "Testimonials, update costs and demo dates", "attachments": ["Client_testimonials.pdf"],
               "body": "Hi Samuel,\n\nAttached are the client testimonials I promised; sorry for the delay. On whether AI Cirku-Tech updates cost extra, I am confirming with our product "
                       "team which updates are included in the licence and will reply in writing by Friday, December 8. For the demonstration, would " + SLOTS + " suit you?\n\n" + SIGN,
               "next_step": "Written answer on update costs by 2023-12-08; demo slot chosen", "due": "2023-12-08T17:00:00Z",
               "reason": "Close the two overdue items and give a date for the one I cannot answer yet.", "refs": [(IN_1630, Q_COST), (OUT_1730, Q_PROMISE)]},
         best={"action": "send_email", "to": [SAMUEL], "channel": "email", "why": "This."},
         evals=["commitment_consistency", "next_step_quality", "next_action_quality"],
         intent={"should_pass": True}, difficulty="medium", extra=["task_trigger", "open_cost_question", "good_draft"]),
    spec(id="cr_d8_invents_agreement_on_the_update_cadence", type="insufficient_evidence", deal=DEAL, trigger=T_DEMO, now=NOW,
         title="'Since you confirmed quarterly updates work for TerraForm' when Samuel only asked how often updates come",
         support=SUPPORT, state=STATE, recent=RECENT, commitments=COMMITS,
         cand={"action": "send_email", "to": [SAMUEL], "subject": "Proposal update: quarterly updates",
               "body": "Hi Samuel,\n\nSince you confirmed that quarterly updates work for TerraForm, I have added them at no extra cost to the proposal. Could we finalize next week?\n\n" + SIGN,
               "next_step": "Finalize the proposal next week", "due": None, "reason": "Close the open update question and move to finalizing.", "refs": [(IN_1630, Q_COST)]},
         best={"action": "send_email", "to": [SAMUEL], "channel": "email",
               "why": "Answer only what the record supports: no confirmed cadence and no price statement; deliver the testimonials and date the cost answer."},
         evals=["grounding", "evidence_sufficiency", "next_action_quality"],
         intent={"should_pass": False}, difficulty="easy", extra=["task_trigger", "invented_facts"]),
    spec(id="cr_d8_k101_cost_answered_in_writing_with_slots", type="applicable_knowledge", deal=DEAL, trigger=T_DEMO, now=NOW, knowledge=["K101"],
         title="K101 applies (an open cost question) and the draft follows it: written answer first, call afterwards",
         support=SUPPORT, state=STATE, recent=RECENT, commitments=COMMITS,
         cand={"action": "send_email", "to": [SAMUEL], "subject": "Your question on update costs", "attachments": ["Client_testimonials.pdf"], "kn": ["K101"],
               "body": "Hi Samuel,\n\nYou asked how often AI Cirku-Tech updates come and whether they cost extra. I do not want to guess, so I am confirming with our product team what the "
                       "licence includes and will send it to you in writing by Friday, December 8. The client testimonials I promised are attached. After you have the written answer, "
                       "we can use " + SLOTS + " for the demonstration.\n\n" + SIGN,
               "next_step": "Written cost answer by 2023-12-08, then a demo slot", "due": "2023-12-08T17:00:00Z",
               "reason": "K101: answer the cost question in writing before proposing the call.", "refs": [(IN_1630, Q_COST)]},
         best={"action": "send_email", "to": [SAMUEL], "channel": "email", "why": "This: the written answer comes before the demo."},
         evals=["knowledge_applicability", "commitment_consistency", "next_action_quality"],
         intent={"should_pass": True}, difficulty="medium", extra=["task_trigger", "open_cost_question", "knowledge_followed"]),
]
