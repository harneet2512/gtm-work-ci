"""Specs from deal 006Wt000007BAT4IAO (Quantum Innovation Partnership; rep Terek Al-Farsi).

Real record, 2023-11-10: three rep emails (11:00-12:00) recap the call and send revised terms. Isabelle Leclerc then writes that
she 'will review the terms with my team and get back to you by early next week' (13:00), asks about further future upgrades and
says she will 'provide further feedback shortly' (14:00), and says they 'aim to have feedback by the end of the week' (15:00).
The loaded knowledge K104 (check in the next business day after revised terms) has the exception 'the buyer stated their own
date for feedback and it has not passed'; her written dates are the explicit exception.
"""
from __future__ import annotations

from .specs_common import items, spec

DEAL = "006Wt000007BAT4IAO"
ISABELLE = "003Wt00000JqvorIAB"
OUT_A, OUT_B, OUT_C = "02sWt000001zxygIAA", "02sWt000001zwD1IAI", "02sWt000001zsqyIAA"
IN_13, IN_14, IN_15 = "02sWt000001zqE3IAI", "02sWt000001zq7bIAA", "02sWt000001zqh6IAA"
Q_EARLY = "I’ll review the terms with my team and get back to you by early next week."
Q_SHORTLY = "I will confer with our team and provide further feedback shortly."
Q_UPGRADES = "Could we perhaps look into the possibilities for further future upgrades?"
Q_WEEK = "We will review the terms discussed and aim to have feedback by the end of the week."
SIGN = "Warm regards,\nTerek"
PROV = {"buying_group roles": "inferred from each Contact's Title/Department; the record has no roles"}
SUPPORT = [(OUT_B, 1100), (OUT_A, 500)]
STATE_13 = {
    "group_extra": [],
    "fields": {
        "decision_process": "Isabelle said they will revert by early next week; she is reviewing the revised terms with her team",
        "current_commitments": items("Customer: feedback on the revised terms by early next week (said 2023-11-10 13:00)"),
        "next_milestone": "Customer's feedback on the revised terms (sent 2023-11-10)"},
    "provenance": {"decision_process": IN_13, "current_commitments": IN_13, **PROV},
}
STATE_15 = {
    "group_extra": [],
    "fields": {
        "decision_process": "Isabelle said they will revert by early next week and, in a later email, by the end of the week; she is reviewing the terms with her team",
        "objections": items("Asks whether further future upgrades are possible"),
        "current_commitments": items("Customer: feedback on the revised terms by early next week / the end of the week",
                                     "Us: answer whether further future upgrades are possible (asked 2023-11-10 14:00)"),
        "next_milestone": "Customer's feedback on the revised terms (sent 2023-11-10)"},
    "provenance": {"decision_process": f"{IN_13}, {IN_15}", "objections": IN_14, "current_commitments": f"{IN_13}, {IN_14}, {IN_15}", **PROV},
}
RECENT_13 = ("Isabelle replied an hour after the revised terms: she will review them with her team and revert by early next week.",
             ["decision_process", "current_commitments"], ["customer_replied"])
RECENT_15 = ("Isabelle wrote three times after the revised terms: feedback by early next week / the end of the week, and a question on "
             "further future upgrades.", ["decision_process", "objections", "current_commitments"], ["customer_replied", "expansion_interest"])

SPECS = [
    spec(id="cr_d9_k104_waits_for_the_date_the_buyer_gave", type="explicit_exception", deal=DEAL, trigger=IN_13, knowledge=["K104"],
         title="K104 would say check in the next business day; the buyer wrote 'early next week', and the draft waits",
         support=SUPPORT, state=STATE_13, recent=RECENT_13,
         commitments=[("Customer: feedback on the revised terms by early next week", "customer", "2023-11-14T00:00:00Z", "open")],
         stated_timing="Isabelle: 'I’ll review the terms with my team and get back to you by early next week.'",
         cand={"action": "wait", "recipients": [], "channel": "none", "subject": None, "body": "", "wait_until": "2023-11-14T09:00:00Z", "kn": ["K104"],
               "next_step": "Wait for Isabelle's feedback; check in on Tuesday 2023-11-14 if nothing has arrived", "due": "2023-11-14T09:00:00Z",
               "reason": "K104 suggests a next-day check-in, but Isabelle gave her own date ('early next week'); honour it.", "refs": [(IN_13, Q_EARLY)]},
         best={"action": "wait", "wait_until": "2023-11-14T09:00:00Z", "channel": "none", "why": "This: her written date overrides the default check-in."},
         evals=["exception_awareness", "knowledge_applicability", "next_action_quality", "timing_cadence"],
         intent={"should_pass": True}, difficulty="medium", extra=["inbound_email", "customer_stated_date", "exception_honoured"]),
    spec(id="cr_d9_k104_check_in_sent_despite_the_buyers_date", type="explicit_exception", deal=DEAL, trigger=IN_13, knowledge=["K104"],
         title="Same moment: the K104 check-in goes out an hour after the buyer wrote she would revert by early next week",
         support=SUPPORT, state=STATE_13, recent=RECENT_13,
         commitments=[("Customer: feedback on the revised terms by early next week", "customer", "2023-11-14T00:00:00Z", "open")],
         cand={"action": "send_email", "to": [ISABELLE], "subject": "Re: Negotiation of terms and finalizing pricing", "kn": ["K104"],
               "body": "Hi Isabelle,\n\nA quick check-in on the revised terms. Does your team have any questions? It would help to have your feedback by Monday.\n\n" + SIGN,
               "next_step": "Isabelle's feedback by Monday 2023-11-13", "due": "2023-11-13T17:00:00Z",
               "reason": "K104: check in the next business day after revised terms.", "refs": [(IN_13, Q_EARLY)]},
         best={"action": "wait", "wait_until": "2023-11-14T09:00:00Z", "channel": "none", "why": "She stated her own date; do not press before it."},
         evals=["exception_awareness", "knowledge_applicability", "next_action_quality", "timing_cadence"],
         intent={"should_pass": False}, difficulty="medium", extra=["inbound_email", "customer_stated_date", "exception_missed"]),
    spec(id="cr_d9_k104_answers_the_upgrade_question_without_a_deadline", type="explicit_exception", deal=DEAL, trigger=IN_15, knowledge=["K104"],
         title="K104 would nudge; the draft answers the upgrade question and leaves the buyer's own feedback date alone",
         support=[(IN_14, None), (IN_13, 600), (OUT_B, 700)], state=STATE_15, recent=RECENT_15,
         commitments=[("Customer: feedback on the revised terms by early next week / the end of the week", "customer", None, "open"),
                      ("Answer whether further future upgrades are possible", "rep", None, "open")],
         cand={"action": "send_email", "to": [ISABELLE], "subject": "Re: Negotiation of terms and finalizing pricing", "kn": ["K104"],
               "body": "Hi Isabelle,\n\nOn your question about further future upgrades: I am checking with our product team what we can include and will reply in writing by Tuesday, "
                       "November 14. Take the time you need with your team; I will wait for your feedback.\n\n" + SIGN,
               "next_step": "Written answer on future upgrades by 2023-11-14", "due": "2023-11-14T17:00:00Z",
               "reason": "She asked about upgrades; answer that, and respect her stated feedback date instead of the default check-in.",
               "refs": [(IN_14, Q_UPGRADES), (IN_15, Q_WEEK)]},
         best={"action": "send_email", "to": [ISABELLE], "channel": "email", "why": "This."},
         evals=["exception_awareness", "knowledge_applicability", "next_action_quality"],
         intent={"should_pass": True}, difficulty="hard", extra=["inbound_email", "customer_stated_date", "open_question", "exception_honoured"]),
]
