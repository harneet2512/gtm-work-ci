"""Specs from deal 006Wt000007B9YfIAK (TerraSync Green Tech Integration; rep Monique Dubois).

Real record: emails in August 2023 (Zara Aminu asks the rep to 'send over the final documentation so we can proceed'), then the
rep's tasks and a contract signed on 2023-12-06 (customer 12-05, company 12-06). The task 'Hold internal review meeting'
(2023-12-10) is internal: 'Discuss key learnings and outcomes from the closed deal.'
"""
from __future__ import annotations

from .specs_common import items, spec

DEAL = "006Wt000007B9YfIAK"
ZARA = "003Wt00000Jqx7WIAR"
CONTRACT, IN_AUG, T_KICK, T_REVIEW = "800Wt00000DE0s0IAD", "02sWt000001zvH2IAI", "00TWt000002z5o9MAA", "00TWt000002z5zRMAQ"
Q_DOCS = "Please send over the final documentation so we can proceed accordingly."
SIGN = "Warm regards,\nMonique"
NOW = "2023-12-10T09:00:00Z"
STATE = {
    "group_extra": [],
    "fields": {
        "decision_process": "A contract was signed on 2023-12-06",
        "current_commitments": items("Us: organize the customer kick-off meeting (rep task due 2023-12-07; no email recorded)"),
        "next_milestone": "Customer kick-off meeting"},
    "provenance": {"decision_process": f"ContractSigned {CONTRACT}", "current_commitments": T_KICK,
                   "buying_group roles": "inferred from each Contact's Title/Department; the record has no roles"},
}
RECENT = ("A contract was signed on 2023-12-06. The rep's internal review meeting task is due today.",
          ["stage"], ["stage_advanced"])
SUPPORT = [(CONTRACT, None), (T_KICK, None), (IN_AUG, None)]

SPECS = [
    spec(id="cr_d11_internal_review_task_needs_no_customer_contact", type="no_action", deal=DEAL, trigger=T_REVIEW, now=NOW,
         title="Internal 'review key learnings' task after a signed contract: nothing to send to the customer",
         support=SUPPORT, state=STATE, recent=RECENT, commitments=[("Organize the customer kick-off meeting (rep task)", "rep", "2023-12-07T00:00:00Z", "open")],
         cand={"action": "no_action", "recipients": [], "channel": "none", "subject": None, "body": "",
               "next_step": "None; the internal review meeting is the rep's own", "due": None,
               "reason": "The trigger is an internal meeting task; no customer request is open and the contract is signed.", "refs": []},
         best={"action": "no_action", "channel": "none", "why": "This: an internal meeting is no reason to contact the customer."},
         evals=["next_action_quality", "state_change_relevance", "buyer_readiness"],
         intent={"should_pass": True}, difficulty="easy", extra=["task_trigger", "internal_task", "contract_signed"]),
    spec(id="cr_d11_upsell_and_case_study_ask_on_an_internal_task", type="no_action", deal=DEAL, trigger=T_REVIEW, now=NOW,
         title="An expansion call and a case-study request sent to the customer because an internal review meeting is due",
         support=SUPPORT, state=STATE, recent=RECENT, commitments=[("Organize the customer kick-off meeting (rep task)", "rep", "2023-12-07T00:00:00Z", "open")],
         cand={"action": "send_email", "to": [ZARA], "subject": "TerraSync: next phase and a short case study",
               "body": "Hi Zara,\n\nCongratulations on getting started. As part of our internal review I would love 30 minutes this week to talk about expanding to more TechPulse "
                       "products, and to ask whether we may write up TerraSync as a case study.\n\n" + SIGN,
               "next_step": "30-minute expansion call this week", "due": None, "reason": "The internal review is due; use it to open an expansion conversation.",
               "refs": [(IN_AUG, Q_DOCS)]},
         best={"action": "no_action", "channel": "none", "why": "An internal review meeting does not call for customer contact; the kick-off is its own task."},
         evals=["next_action_quality", "state_change_relevance", "relationship_pressure"],
         intent={"should_pass": False}, difficulty="easy", extra=["task_trigger", "internal_task", "contract_signed"]),
]
