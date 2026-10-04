"""Specs from deal 006Wt000007BB0vIAG (AlphaTech Enhanced Media Solutions Partnership; rep Ayumi Shimizu).

Real record, 2023-11-07: Fernando Sosa (Supply Chain Optimization Analyst, the only correspondent) says he 'shared your
proposal with our finance team, and they are currently reviewing the pricing', asks for more detailed testimonials from
media companies; the rep promises them 'shortly'. Nothing further is recorded until the rep's tasks 'Prepare Contract
for Review' (11-15) and 'Conduct Final Negotiation Meeting' (11-20).
"""
from __future__ import annotations

from .specs_common import items, spec

DEAL = "006Wt000007BB0vIAG"
FERNANDO, ANITA = "003Wt00000Jqlr8IAB", "003Wt00000Jqp0KIAR"
IN_1, IN_2, OUT_T = "02sWt000001zsB4IAI", "02sWt000001zpGOIAY", "02sWt000001zx93IAA"
T_PREP, T_FINAL = "00TWt000002yxwvMAA", "00TWt000002yxyXMAQ"
Q_FIN = "I have shared your proposal with our finance team, and they are currently reviewing the pricing."
Q_TEST = "I'd be interested to see more detailed testimonials or use cases focusing on how other media companies have benefited from similar implementations."
Q_PROMISE = "I'll gather more detailed testimonials and success stories from media companies that have implemented our solutions and share these with you shortly."
SIGN = "Warm regards,\nAyumi"
PROV = {"buying_group roles": "inferred from each Contact's Title/Department; the record has no roles"}
STATE = {
    "group_extra": [(ANITA, ["unknown"])],
    "fields": {
        "decision_process": "Fernando shared the proposal with the finance team, who are reviewing the pricing; he said on 2023-11-07 he would revert soon",
        "current_commitments": items(
            "Customer: finance team's feedback on the pricing (open since 2023-11-07)",
            "Us: detailed testimonials from media companies, promised 'shortly' on 2023-11-07"),
        "next_milestone": "unknown: no date was agreed"},
    "provenance": {"decision_process": IN_1, "current_commitments": f"{IN_1}, {IN_2}, {OUT_T}", **PROV},
}
RECENT = ("No email since 2023-11-07; the rep's testimonials promise and the finance team's pricing review are both still open.",
          ["current_commitments"], ["customer_went_silent"])
SUPPORT = [(IN_1, None), (IN_2, None), (OUT_T, None)]
COMMITS = [("Finance team's feedback on the pricing", "customer", None, "open"),
           ("Share detailed media-company testimonials ('shortly', 2023-11-07)", "rep", None, "open")]

SPECS = [
    spec(id="cr_d7_contract_draft_to_fernando_alone_while_finance_reviews", type="under_threading", deal=DEAL, trigger=T_PREP, now="2023-11-15T09:00:00Z",
         title="Draft contract and testimonials sent to Fernando alone while his finance team's pricing review is open",
         support=SUPPORT, state=STATE, recent=RECENT, commitments=COMMITS,
         cand={"action": "send_email", "to": [FERNANDO], "subject": "AlphaTech: draft contract and media-company testimonials",
               "attachments": ["AlphaTech_Draft_Contract.pdf", "Media_company_testimonials.pdf"],
               "body": "Hi Fernando,\n\nAttached are the media-company testimonials I promised and the draft contract for your review. Send it back signed once your "
                       "team is comfortable.\n\n" + SIGN,
               "next_step": "Contract returned signed", "due": None, "reason": "The contract task is due; deliver the testimonials with it.",
               "refs": [(OUT_T, Q_PROMISE)]},
         best={"action": "send_email", "to": [FERNANDO], "channel": "email",
               "why": "Deliver the testimonials, ask where the finance review stands and offer to walk finance through the pricing before the contract goes out."},
         evals=["stakeholder_coverage", "decision_process", "commitment_consistency"],
         intent={"should_pass": False}, difficulty="hard", extra=["task_trigger", "finance_review_open"],
         notes="Debatable: whether not asking to engage finance is a coverage failure. See labels_needed."),
    spec(id="cr_d7_offers_to_walk_finance_through_the_pricing", type="under_threading", deal=DEAL, trigger=T_PREP, now="2023-11-15T09:00:00Z",
         title="Same moment, good draft: testimonials delivered and the finance review addressed by offering to include finance",
         support=SUPPORT, state=STATE, recent=RECENT, commitments=COMMITS,
         cand={"action": "send_email", "to": [FERNANDO], "subject": "AlphaTech: testimonials and next step on pricing",
               "attachments": ["Media_company_testimonials.pdf"],
               "body": "Hi Fernando,\n\nSorry for the wait; attached are the media-company testimonials I promised. I know your finance team is reviewing the pricing. If it "
                       "helps, I can walk them through it. Who should I include? I will hold the contract draft until you tell me where that review stands.\n\n" + SIGN,
               "next_step": "Fernando shares the finance review status and who to include", "due": None,
               "reason": "Deliver the overdue testimonials and ask about the open finance review before sending a contract.", "refs": [(IN_1, Q_FIN), (OUT_T, Q_PROMISE)]},
         best={"action": "send_email", "to": [FERNANDO], "channel": "email", "why": "This."},
         evals=["stakeholder_coverage", "decision_process", "commitment_consistency"],
         intent={"should_pass": True}, difficulty="medium", extra=["task_trigger", "finance_review_open", "good_draft"]),
    spec(id="cr_d7_promises_the_testimonials_again_thirteen_days_later", type="stale_commitment", deal=DEAL, trigger=T_FINAL, now="2023-11-20T09:00:00Z",
         title="'I'll gather testimonials and share them shortly', repeated 13 days after the same promise",
         support=SUPPORT, state=STATE, recent=RECENT, commitments=COMMITS,
         cand={"action": "send_email", "to": [FERNANDO], "subject": "AlphaTech: final negotiation meeting",
               "body": "Hi Fernando,\n\nI'll gather more detailed testimonials and success stories from media companies and share them with you shortly. Could we set a "
                       "final negotiation meeting this week to finalize the agreement?\n\n" + SIGN,
               "next_step": "Final negotiation meeting this week", "due": None, "reason": "The final-negotiation task is due; restate what is coming and ask for the meeting.",
               "refs": [(OUT_T, Q_PROMISE)]},
         best={"action": "send_email", "to": [FERNANDO], "channel": "email",
               "why": "Deliver the overdue testimonials with an apology, ask for the finance review status, then propose the meeting."},
         evals=["commitment_consistency", "timing_cadence", "next_action_quality"],
         intent={"should_pass": False}, difficulty="medium", extra=["task_trigger", "overdue_own_promise"]),
]
