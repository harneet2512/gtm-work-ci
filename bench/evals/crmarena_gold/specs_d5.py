"""Specs from deal 006Wt000007BDAnIAO (EcoLite Advanced Lighting Collaboration; rep Luciana Alves).

Real record, 2023-11-29: the rep sends three near-identical contract-discussion emails at 09:30-09:35. Haruto Yamamoto
(Lead Automation Engineer, the only correspondent) asks about the implementation timeline and a call about the discounts
(10:00, 10:15); the rep proposes 'a call for Thursday at 2 PM' (11:00); at 14:00 Haruto writes that 'our legal team is
reviewing the document' and that he will 'reach out by Friday latest'.
"""
from __future__ import annotations

from .specs_common import items, spec

DEAL = "006Wt000007BDAnIAO"
HARUTO, ZARA, ELODIE = "003Wt00000JqwkvIAB", "003Wt00000JqghpIAB", "003Wt00000JqkiCIAR"
OUT_9, OUT_9B, OUT_9C, OUT_11 = "02sWt000001zqcNIAQ", "02sWt00000200zlIAA", "02sWt000001zpOVIAY", "02sWt00000200y9IAA"
IN_10, IN_1015, IN_14 = "02sWt000001ztVOIAY", "02sWt000001zqipIAA", "02sWt000002011NIAQ"
Q_LEGAL = "our legal team is reviewing the document, and I anticipate we can finalize all aspects shortly"
Q_FRIDAY = "If any adjustments are needed, I will reach out by Friday latest to ensure everything is ready by the anticipated signing date."
Q_TIMELINE = "I would just like a bit more clarification on the timelines involved in the implementation process, especially around the deployment of AI Cirku-Tech."
Q_SEC = "particularly regarding the implementation process and security compliance"
Q_CALLREQ = "Could we set up a brief call later this week?"
SIGN = "Warm regards,\nLuciana"
PROV = {"buying_group roles": "inferred from each Contact's Title/Department; the record has no roles"}
STATE = {
    "group_extra": [(ZARA, ["security"]), (ELODIE, ["unknown"])],
    "fields": {
        "decision_process": "Haruto's legal team is reviewing the draft contract; he will reach out by Friday (2023-12-01) at the latest if adjustments are needed; a signing date is anticipated",
        "objections": items("Wants clarification of the implementation timeline, especially deployment of AI Cirku-Tech",
                            "Wants to confirm how the discounts apply (asked for a brief call this week)"),
        "current_commitments": items(
            "Customer: legal review of the contract; adjustments by Friday 2023-12-01 at the latest",
            "Us: call on Thursday 2023-11-30 at 14:00 (proposed 11:00)",
            "Us: clarify the implementation timeline for AI Cirku-Tech"),
        "next_milestone": "Customer's legal review; feedback by 2023-12-01"},
    "provenance": {"decision_process": IN_14, "objections": f"{IN_10}, {IN_1015}", "current_commitments": f"{IN_14}, {OUT_11}, {IN_10}", **PROV},
}
RECENT = ("At 14:00 Haruto says his legal team is reviewing the contract and he will revert by Friday; earlier he asked for the "
          "implementation timeline and a call about the discounts.", ["decision_process", "current_commitments"], ["customer_replied"])
COMMITS = [("Legal review of the contract; adjustments by Friday 2023-12-01", "customer", "2023-12-01T00:00:00Z", "open"),
           ("Call Thursday 2023-11-30 at 14:00 (proposed 11:00)", "rep", "2023-11-30T14:00:00Z", "open"),
           ("Clarify the implementation timeline for AI Cirku-Tech", "rep", None, "open")]
SUPPORT = [(IN_10, None), (IN_1015, None), (OUT_11, None)]

SPECS = [
    spec(id="cr_d5_signature_by_thursday_while_legal_reviews", type="too_soon_cta", deal=DEAL, trigger=IN_14,
         title="Signature version with a Thursday deadline, sent as the buyer says his legal team is still reviewing",
         support=SUPPORT, state=STATE, recent=RECENT, commitments=COMMITS, stated_timing="Haruto: 'I will reach out by Friday latest'",
         cand={"action": "send_email", "to": [HARUTO], "subject": "Signature version of the EcoLite contract", "attachments": ["EcoLite_Contract_Signature_Version.pdf"],
               "body": "Hi Haruto,\n\nGreat that legal is reviewing. To save time I have attached the signature version. Could you sign it by Thursday, November 30 "
                       "so we can lock in the discounts?\n\n" + SIGN,
               "next_step": "Signed contract returned", "due": "2023-11-30T17:00:00Z", "reason": "Everything is agreed except legal; get the signature in early.",
               "refs": [(IN_14, Q_LEGAL)]},
         best={"action": "send_email", "to": [HARUTO], "channel": "email",
               "why": "Let legal finish; answer the implementation-timeline question and keep the Thursday call for the discount details."},
         evals=["buyer_readiness", "cta_calibration", "decision_process"],
         intent={"should_pass": False}, difficulty="easy", extra=["signature_push", "inbound_email", "legal_review_open"]),
    spec(id="cr_d5_marketing_director_copied_on_contract_thread", type="over_threading", deal=DEAL, trigger=IN_14,
         title="Reply to Haruto copying the Marketing Director 'to align on rollout messaging' while legal review is running",
         support=SUPPORT, state=STATE, recent=RECENT, commitments=COMMITS,
         cand={"action": "send_email", "to": [HARUTO], "cc": [ELODIE], "subject": "Re: Final Contract Discussions and Preparation",
               "body": "Hi Haruto,\n\nThanks for the update. I have copied Elodie Rousseau so marketing can align on rollout messaging while legal finishes. "
                       "I will send the deployment timeline for AI Cirku-Tech tomorrow and we can use Thursday's call for the discount details.\n\n" + SIGN,
               "next_step": "Deployment timeline sent 2023-11-30; call Thursday 14:00", "due": "2023-11-30T14:00:00Z",
               "reason": "Bring marketing in early so the rollout is ready at signing.", "refs": [(IN_14, Q_FRIDAY)]},
         best={"action": "send_email", "to": [HARUTO], "channel": "email", "why": "Haruto alone: timeline answer and the Thursday call; no new stakeholders while legal reviews."},
         evals=["stakeholder_coverage", "stakeholder_selection", "cta_calibration"],
         intent={"should_pass": False}, difficulty="medium", extra=["executive_cc", "inbound_email"]),
    spec(id="cr_d5_introduces_security_with_haruto_copied", type="champion_bypass", deal=DEAL, trigger=IN_14,
         title="New thread to the Cyber Risk Analyst about security compliance, with Haruto copied",
         support=SUPPORT, state=STATE, recent=RECENT, commitments=COMMITS,
         cand={"action": "send_email", "to": [ZARA], "cc": [HARUTO], "subject": "EcoLite: security compliance walkthrough",
               "body": "Hi Zara,\n\nHaruto and I have been working through the contract, and security compliance has been important to EcoLite throughout. I would "
                       "like to offer a 30-minute walkthrough of our compliance controls at a time that suits you. I have copied Haruto so he stays in the loop.\n\n" + SIGN,
               "next_step": "Zara picks a time for a security walkthrough", "due": None,
               "reason": "Security compliance is a stated priority; offer the walkthrough to the security contact with the working contact included.",
               "refs": [(IN_14, Q_SEC)]},
         best={"action": "send_email", "to": [HARUTO], "channel": "email",
               "why": "Acceptable alternative: ask Haruto whether he wants a security walkthrough; he stays the owner."},
         evals=["champion_continuity", "stakeholder_selection", "stakeholder_coverage"],
         intent={"should_pass": True}, difficulty="hard", extra=["inferred_champion", "inbound_email", "new_contact_with_champion_cc"],
         notes="Hard: an unrequested new contact, but the role matches the customer's stated security priority and the correspondent is copied."),
    spec(id="cr_d5_repeats_the_thursday_call_proposal", type="duplicate_action", deal=DEAL, trigger=IN_14,
         title="Proposes 'a call on Thursday at 2 PM' again three hours after the same proposal went out unanswered",
         support=SUPPORT, state=STATE, recent=RECENT, commitments=COMMITS,
         cand={"action": "send_email", "to": [HARUTO], "subject": "Re: Final Contract Discussions and Preparation",
               "body": "Hello Haruto,\n\nThanks for the update. I am happy to go over the discount terms and any specifics within the contract. Could we schedule a "
                       "call for Thursday at 2 PM? Let me know if this works or propose another time.\n\n" + SIGN,
               "next_step": "Call Thursday 2023-11-30 14:00", "due": "2023-11-30T14:00:00Z", "reason": "Offer a call to settle the discount questions.",
               "refs": [(IN_1015, Q_CALLREQ)]},
         best={"action": "send_email", "to": [HARUTO], "channel": "email",
               "why": "The call was already proposed at 11:00; send the implementation-timeline clarification instead."},
         evals=["duplicate_action", "next_action_quality", "commitment_consistency"],
         intent={"should_pass": False}, difficulty="easy", extra=["inbound_email", "repeats_own_proposal"]),
    spec(id="cr_d5_answers_the_open_timeline_question", type="duplicate_action", deal=DEAL, trigger=IN_14,
         title="Same moment, good draft: nothing is repeated; the open timeline question gets a dated commitment",
         support=SUPPORT, state=STATE, recent=RECENT, commitments=COMMITS,
         cand={"action": "send_email", "to": [HARUTO], "subject": "Re: Final Contract Discussions and Preparation",
               "body": "Hello Haruto,\n\nThank you; we will leave the contract with your legal team. On your question about the implementation timeline, especially deployment of "
                       "AI Cirku-Tech, I will send a written timeline by tomorrow, Thursday, in time for our 2 PM call that day.\n\n" + SIGN,
               "next_step": "Written deployment timeline sent 2023-11-30 before the 14:00 call", "due": "2023-11-30T12:00:00Z",
               "reason": "The call proposal is already out; the timeline question is the open item.", "refs": [(IN_10, Q_TIMELINE)]},
         best={"action": "send_email", "to": [HARUTO], "channel": "email", "why": "This."},
         evals=["duplicate_action", "next_action_quality", "commitment_consistency"],
         intent={"should_pass": True}, difficulty="medium", extra=["inbound_email", "good_draft"]),
]
