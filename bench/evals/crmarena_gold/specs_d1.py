"""Specs from deal 006Wt000007B2QgIAK (Innovative Robotics EDA Enhancement; rep Dariusz Wisniewski).

Real record: three same-day rep recaps (2024-03-22), then Chin Wei (Legal Compliance Coordinator, the only customer
correspondent) asks about additional costs and case studies, says the team is reviewing the proposal, and asks for an
AI Cirku-Tech demonstration. No rep reply follows in the record. Rep tasks (due dates) follow: case studies 03-24,
negotiation meeting 03-28, proposal follow-up 03-30.
"""
from __future__ import annotations

from .specs_common import items, merge, spec

DEAL = "006Wt000007B2QgIAK"
CHIN, CFO, SEC, DEV = "003Wt00000JqpGUIAZ", "003Wt00000JqnD1IAJ", "003Wt00000JqwmZIAR", "003Wt00000Jqx13IAB"
OUT1, OUT2, OUT3 = "02sWt000001zy6lIAA", "02sWt000001zoKMIAY", "02sWt00000201r1IAA"
IN_COST, IN_X, IN_Y = "02sWt000001zplAIAQ", "02sWt000001zpGXIAY", "02sWt000001zpGYIAY"
T_CASES, T_NEGO, T_FOLLOW = "00TWt000002yz2nMAA", "00TWt000002z3xcMAA", "00TWt000002z1XgMAI"
Q_COST = "I would like to understand more about any potential additional costs related to customization or future upgrades."
Q_REVIEW = "We're currently reviewing the proposal with the rest of the team and will provide feedback shortly."
Q_FINAL = "Looking forward to the final proposal version."
SIGN = "Best,\nDariusz"

BASE_STATE = {
    "group_extra": [(CFO, ["unknown"]), (SEC, ["security"]), (DEV, ["technical_evaluator"])],
    "fields": {
        "decision_process": "Chin Wei is reviewing the proposal with the rest of her team and will send feedback; no approver or timeline is named",
        "objections": items("Wants to understand additional costs related to customization or future upgrades"),
        "current_commitments": items(
            "Customer: review the proposal with the team and give feedback 'shortly'",
            "Us: answer the additional-cost question and send relevant case studies (asked 2024-03-22)",
            "Us: arrange another demonstration focused on AI Cirku-Tech (asked 2024-03-23)"),
        "next_milestone": "Customer feedback on the proposal"},
    "provenance": {"decision_process": f"{IN_X}, {IN_Y}", "objections": IN_COST, "current_commitments": f"{IN_COST}, {IN_Y}",
                   "buying_group roles": "inferred from each Contact's Title/Department; the record has no roles"},
}
DUP_STATE = {
    "group_extra": BASE_STATE["group_extra"],
    "fields": {
        "decision_process": "unknown: no approver, timeline or review step is stated yet",
        "objections": items("Wants to understand additional costs related to customization or future upgrades"),
        "current_commitments": items("Us: answer the additional-cost question and send relevant case studies (asked 2024-03-22)"),
        "next_milestone": "unknown"},
    "provenance": {"objections": IN_COST, "current_commitments": IN_COST,
                   "buying_group roles": "inferred from each Contact's Title/Department; the record has no roles"},
}
BASE_RECENT = ("Chin Wei replied twice on 2024-03-23: the team is reviewing the proposal, she wants another AI Cirku-Tech "
               "demonstration and the final proposal version; on 03-22 she asked about additional costs and case studies.",
               ["decision_process", "objections", "current_commitments"], ["customer_replied", "pricing_interest"])
SUPPORT_0323 = [(IN_X, None), (IN_COST, None), (OUT1, 700)]
REVIEW_NOW = "2024-03-23T09:30:00Z"

ANSWER_BODY = (
    "Hi Chin Wei,\n\nThanks for the questions. I have attached case studies from similar engineering teams. On additional "
    "costs: the prices in our proposal are the ones we quoted (for example PulseSim Pro at $4,499.91 for 10 units after the "
    "10% discount). For customization and future upgrades I would rather give you a written answer than a guess, so I will "
    "confirm what applies and send it by Friday, March 29. I will also look for dates for the AI Cirku-Tech demonstration "
    "you asked for; would early next week suit your team?\n\n" + SIGN)
ANSWER = {"action": "send_email", "to": [CHIN], "subject": "Case studies and your cost question",
          "body": ANSWER_BODY, "attachments": ["Case_studies_engineering_teams.pdf"],
          "next_step": "Send written answer on customization and upgrade costs; propose AI Cirku-Tech demo dates",
          "due": "2024-03-29T17:00:00Z", "reason": "Answers the two open customer questions and the demo request; no ask the buyer has not signalled.",
          "refs": [(IN_COST, Q_COST), (IN_Y, "It would be helpful to have another demonstration focused on the AI Cirku-Tech")]}
ANSWER_BEST = {"action": "send_email", "to": [CHIN], "channel": "email",
               "why": "Answer the cost question, send the case studies and offer demo dates; leave the team's review to finish."}

SPECS = [
    spec(id="cr_d1_signature_push_after_review_notice", type="too_soon_cta", deal=DEAL, trigger=IN_Y, now=REVIEW_NOW,
         title="Order form with a Friday signature deadline right after the buyer says her team is still reviewing",
         support=SUPPORT_0323, state=BASE_STATE, recent=BASE_RECENT,
         commitments=[("Customer reviews the proposal with her team", "customer", None, "open"),
                      ("Answer the additional-cost question and send case studies", "rep", None, "open")],
         stated_timing="Chin Wei: 'We're currently reviewing the proposal with the rest of the team and will provide feedback shortly.'",
         cand={"action": "send_email", "to": [CHIN], "subject": "Next steps: order form for Innovative Robotics",
               "body": "Hi Chin Wei,\n\nGreat to hear the team is positive. To keep us on schedule I have attached the order form "
                       "for the SecureFlow Suite and CircuitMaster Analyzer bundle. Could you have it signed and returned by "
                       "Friday, March 29 so we can hold the 10% discount?\n\n" + SIGN,
               "attachments": ["Innovative_Robotics_Order_Form.pdf"], "next_step": "Signed order form returned",
               "due": "2024-03-29T17:00:00Z", "reason": "She is positive on price and scalability; ask for the close now.",
               "refs": [(IN_Y, Q_REVIEW)]},
         best=ANSWER_BEST, evals=["buyer_readiness", "cta_calibration", "next_action_quality"],
         intent={"should_pass": False}, difficulty="easy", extra=["signature_push", "inbound_email"]),
    spec(id="cr_d1_cfo_pulled_in_while_team_reviews", type="over_threading", deal=DEAL, trigger=IN_Y, now=REVIEW_NOW,
         title="Reply to Chin Wei that copies the CFO and the lead developer, with pricing, while the working-level review runs",
         support=SUPPORT_0323, state=BASE_STATE, recent=BASE_RECENT,
         commitments=[("Customer reviews the proposal with her team", "customer", None, "open")],
         cand=merge(ANSWER, to=[CHIN], cc=[CFO, DEV], subject="Innovative Robotics: proposal, costs and case studies",
                    reason="Bring the finance and engineering leads in now so everyone is aligned on the 10% discount."),
         best=ANSWER_BEST, evals=["stakeholder_coverage", "stakeholder_selection", "champion_continuity"],
         intent={"should_pass": False}, difficulty="medium", extra=["executive_cc", "inbound_email"]),
    spec(id="cr_d1_cfo_direct_pricing_drops_chin_wei", type="champion_bypass", deal=DEAL, trigger=IN_Y, now=REVIEW_NOW,
         title="Pricing email sent to the CFO alone; the only correspondent, who is mid-review, is not on it",
         support=SUPPORT_0323, state=BASE_STATE, recent=BASE_RECENT,
         commitments=[("Customer reviews the proposal with her team", "customer", None, "open")],
         cand={"action": "send_email", "to": [CFO], "subject": "Innovative Robotics: proposal and pricing",
               "body": "Hi Liam,\n\nI have been working with your team on the EDA proposal for Innovative Robotics. PulseSim Pro comes to "
                       "$4,499.91 for 10 units after a 10% discount, with similar discounts across the other products. I would "
                       "welcome 15 minutes with you next week to walk through the commercials.\n\n" + SIGN,
               "next_step": "15-minute commercial walkthrough with the CFO", "due": None,
               "reason": "The CFO controls budget; go to the decision maker directly.",
               "refs": [(IN_Y, "Regarding pricing, the alignment with expected ROI is clear, and we appreciate the transparent approach.")]},
         best=ANSWER_BEST, evals=["champion_continuity", "stakeholder_selection", "buyer_readiness"],
         intent={"should_pass": False}, difficulty="easy", extra=["inferred_champion", "inbound_email"],
         notes="The champion is inferred (the only correspondent); CRMArena has no roles. See labels_needed."),
    spec(id="cr_d1_case_studies_sent_cost_question_ignored", type="unresolved_blocker", deal=DEAL, trigger=T_CASES,
         now="2024-03-24T09:00:00Z", title="Case studies plus a negotiation-meeting push that skips the open additional-cost question",
         support=[(IN_COST, None), (IN_X, None), (IN_Y, None)], state=BASE_STATE, recent=(
             "The rep task 'Send targeted case studies' is due; Chin Wei's additional-cost question (03-22) and demo request "
             "(03-23) are her latest messages.", ["current_commitments"], []),
         commitments=[("Answer the additional-cost question and send case studies", "rep", "2024-03-24T00:00:00Z", "open")],
         cand={"action": "send_email", "to": [CHIN], "subject": "Case studies and next step", "attachments": ["Case_studies_engineering_teams.pdf"],
               "body": "Hi Chin Wei,\n\nAttached are case studies from similar engineering teams. Since your team is reviewing, can we lock in a "
                       "negotiation meeting on Thursday, March 28 at 10:00 to finalize terms and pricing?\n\n" + SIGN,
               "next_step": "Negotiation meeting Thursday 2024-03-28 10:00", "due": "2024-03-28T10:00:00Z",
               "reason": "Task is due; combine the case studies with the next step.", "refs": [(IN_COST, "I’m also interested in any case studies or examples you can share")]},
         best=ANSWER_BEST, evals=["commitment_consistency", "next_step_quality", "next_action_quality"],
         intent={"should_pass": False}, difficulty="medium", extra=["task_trigger", "open_cost_question"]),
    spec(id="cr_d1_answers_cost_question_and_sends_case_studies", type="correct_timing", deal=DEAL, trigger=T_CASES,
         now="2024-03-24T09:00:00Z", title="Same moment, good draft: case studies, a written answer on cost, demo dates offered",
         support=[(IN_COST, None), (IN_X, None), (IN_Y, None)], state=BASE_STATE, recent=(
             "The rep task 'Send targeted case studies' is due; Chin Wei's additional-cost question and demo request are her latest messages.",
             ["current_commitments"], []),
         commitments=[("Answer the additional-cost question and send case studies", "rep", "2024-03-24T00:00:00Z", "open")],
         cand=ANSWER, best=ANSWER_BEST, evals=["timing_cadence", "buyer_readiness", "cta_calibration", "grounding"],
         intent={"should_pass": True}, difficulty="easy", extra=["task_trigger", "good_draft"]),
    spec(id="cr_d1_just_checking_in_after_a_week_of_silence", type="too_late_follow_up", deal=DEAL, trigger=T_FOLLOW,
         now="2024-03-30T09:00:00Z", title="A casual 'just checking in' eight days after the buyer's unanswered questions",
         support=[(IN_Y, None), (IN_COST, None), (T_NEGO, None)], state=BASE_STATE, recent=(
             "The last outbound email was on 2024-03-22 and the last customer email on 2024-03-23; the negotiation task (03-28) is past.", ["current_commitments"], ["customer_went_silent"]),
         commitments=[("Answer the additional-cost question, send case studies, arrange demo", "rep", None, "open")],
         cand={"action": "send_email", "to": [CHIN], "subject": "Checking in on the proposal",
               "body": "Hi Chin Wei,\n\nJust a quick check-in on the proposal. Any questions or feedback from the team? Happy to jump on a "
                       "call whenever suits.\n\n" + SIGN, "next_step": "Await feedback", "due": None,
               "reason": "The follow-up task is due; keep it light.", "refs": [(IN_Y, Q_REVIEW)]},
         best={"action": "send_email", "to": [CHIN], "channel": "email",
               "why": "Acknowledge the delay, send the case studies and the written cost answer, propose demo dates."},
         evals=["timing_cadence", "commitment_consistency", "momentum", "next_action_quality"],
         intent={"should_pass": False}, difficulty="medium", extra=["task_trigger", "week_of_silence"]),
    spec(id="cr_d1_negotiation_invite_while_review_open", type="conflicting_account_state", deal=DEAL, trigger=T_NEGO,
         now="2024-03-28T09:00:00Z", title="CRM plan says negotiate now; the buyer's last words say review pending and 'final proposal version' awaited",
         support=[(IN_X, None), (IN_Y, None), (IN_COST, None)], state=merge(BASE_STATE, fields={
             "stage": "Negotiation (per the rep's own CRM tasks; the customer has not said so)"}, provenance={
             "stage": "CRM task 'Hold negotiation meeting' due 2024-03-28 (a plan); contradicted by IN_X/IN_Y"}), recent=(
             "The CRM task to hold a negotiation meeting is due today; the customer's last message (03-23) says the team is still reviewing "
             "and awaits the final proposal version.", ["stage", "next_milestone"], ["field_contradicted"]),
         commitments=[("Hold negotiation meeting to finalize terms (CRM task)", "rep", "2024-03-28T00:00:00Z", "open"),
                      ("Customer reviews the proposal with her team", "customer", None, "open")],
         cand={"action": "schedule_meeting", "to": [CHIN], "subject": "Negotiation meeting: terms and pricing",
               "body": "Hi Chin Wei,\n\nAs planned, I have set up our negotiation meeting for Friday, March 29 at 10:00 to finalize terms and "
                       "pricing. Calendar invite attached.\n\n" + SIGN, "channel": "email", "next_step": "Negotiation meeting 2024-03-29 10:00",
               "due": "2024-03-29T10:00:00Z", "reason": "The CRM task says it is time to negotiate.",
               "refs": [(IN_X, "I will review the proposal with my team and get back to you with any feedback or questions.")]},
         best={"action": "send_email", "to": [CHIN], "channel": "email",
               "why": "Follow the customer's own statement: ask where the review stands, send the final proposal version, hold the negotiation meeting until she says the team is done; note the CRM-vs-customer conflict."},
         evals=["commitment_consistency", "buyer_readiness", "grounding"],
         intent={"should_pass": False}, difficulty="hard", extra=["task_trigger", "crm_vs_customer"]),
    spec(id="cr_d1_fourth_recap_after_three_identical", type="duplicate_action", deal=DEAL, trigger=IN_COST,
         title="A fourth meeting recap sent after three near-identical recaps went out in the last 90 minutes",
         support=[(OUT1, 600), (OUT2, 600), (OUT3, 600)], state=DUP_STATE, recent=(
             "Chin Wei replied to the three recaps with a new cost question and a case-study request.",
             ["objections", "current_commitments"], ["customer_replied", "pricing_interest"]),
         commitments=[("Answer the additional-cost question and send case studies", "rep", None, "open")],
         cand={"action": "send_email", "to": [CHIN], "subject": "In-depth Needs Analysis Discussion",
               "body": "Hi Chin Wei,\n\nThank you again for today's meeting. To recap: SecureFlow Suite and CircuitMaster Analyzer can raise "
                       "the efficiency and scalability of your operations, and our pricing includes a 10% discount on 10 units of PulseSim Pro. "
                       "Let me know if you have any questions.\n\n" + SIGN, "next_step": "Await questions", "due": None,
               "reason": "Make sure the key points of the meeting are on record.", "refs": [(OUT1, "Thank you for your time and active participation in today’s meeting.")]},
         best=ANSWER_BEST, evals=["duplicate_action", "state_change_relevance", "next_action_quality"],
         intent={"should_pass": False}, difficulty="easy", extra=["inbound_email", "same_day_duplicates"]),
]
