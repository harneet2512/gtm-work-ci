"""Cross-deal specs: knowledge cases, the wrong-channel and no-action extras, and the handoff analogues (HAR-114 gold v2).

The valid_champion_handoff cases are ANALOGUES: CRMArena records no delegation and no departure (docs/data/crmarena-b2b.md),
so each case uses the nearest real fact, a buyer who asks us in writing to bring one more person (a CTO, a tech team, a lead
engineer) into the conversation while the correspondent stays on the thread. They carry an evidence_gap.
"""
from __future__ import annotations

from . import specs_d1 as d1
from . import specs_d2 as d2
from . import specs_d3 as d3
from . import specs_d4 as d4
from . import specs_d5 as d5
from .specs_common import items, spec

GAP = ("CRMArena has no delegation or departure, so a true VALID_HANDOFF cannot be built from the record; this case uses the nearest "
       "real fact (the buyer asks in writing to include another person while the correspondent stays on the thread). Replace or "
       "extend it with the WP32 synthetic layer.")
SOPHIA, OMEGA2 = "003Wt00000JqnswIAB", "006Wt000007BEq1IAG"
OM2_IN1, OM2_IN2 = "02sWt000001zxh3IAA", "02sWt000001zr3kIAA"
Q_NEXTWEEK = "Would it be possible to schedule a call early next week, perhaps on Monday or Tuesday?"
Q_FWD = "I have forwarded the details to our finance and technical teams for further review."
OMEGA, ZARA_A = "006Wt000007BCl0IAG", "003Wt00000JqxHBIAZ"
OMEGA_OUT, OMEGA_IN, OMEGA_T = "02sWt000002034nIAA", "02sWt000001zpeeIAA", "00TWt000002z60zMAA"
Q_TECH = "Could we perhaps set up a session where our tech team and yours can delve into these specifics next week?"
Q_MID = "I will get back to you by mid-week with a comprehensive response, hopefully paving the way for a finalized agreement."
PROV = {"buying_group roles": "inferred from each Contact's Title/Department; the record has no roles"}

SPECS = [
    spec(id="cr_x_call_offered_instead_of_the_written_cost_answer", type="wrong_channel", deal=d1.DEAL, trigger=d1.IN_COST,
         title="A call is offered when the buyer asked for a written cost answer and case studies",
         support=[(d1.OUT1, 600), (d1.OUT2, 600), (d1.OUT3, 600)], state=d1.DUP_STATE, recent=(
             "Chin Wei replied to the recaps with a cost question and a case-study request.", ["objections", "current_commitments"], ["customer_replied", "pricing_interest"]),
         commitments=[("Answer the additional-cost question and send case studies", "rep", None, "open")],
         cand={"action": "schedule_meeting", "to": [d1.CHIN], "subject": "Call on Monday to go through costs and case studies", "channel": "email",
               "body": "Hi Chin Wei,\n\nRather than going back and forth by email, let's talk through the costs and the case studies on a call. I have sent an invite for Monday, "
                       "March 25 at 10:00.\n\n" + d1.SIGN,
               "next_step": "Call Monday 2024-03-25 10:00", "due": "2024-03-25T10:00:00Z", "reason": "A call is faster than email for cost questions.",
               "refs": [(d1.IN_COST, d1.Q_COST)]},
         best=d1.ANSWER_BEST, evals=["channel_appropriateness", "next_action_quality", "commitment_consistency"],
         intent={"should_pass": False}, difficulty="medium", extra=["inbound_email", "written_answer_requested"]),
    spec(id="cr_x_k101_ignored_cost_answer_deferred_to_a_call", type="applicable_knowledge", deal=d1.DEAL, trigger=d1.T_CASES, now="2024-03-24T09:00:00Z", knowledge=["K101"],
         title="K101 applies (an open cost question); the draft sends case studies but defers the cost answer to a call",
         support=[(d1.IN_COST, None), (d1.IN_X, None), (d1.IN_Y, None)], state=d1.BASE_STATE, recent=(
             "The rep task 'Send targeted case studies' is due; Chin Wei's additional-cost question and demo request are her latest messages.", ["current_commitments"], []),
         commitments=[("Answer the additional-cost question and send case studies", "rep", "2024-03-24T00:00:00Z", "open")],
         cand={"action": "send_email", "to": [d1.CHIN], "subject": "Case studies; costs on a call", "attachments": ["Case_studies_engineering_teams.pdf"],
               "body": "Hi Chin Wei,\n\nAttached are case studies from similar engineering teams. On additional costs for customization and upgrades, let's go through them on a call on "
                       "Monday, March 25 at 10:00.\n\n" + d1.SIGN,
               "next_step": "Call Monday 2024-03-25 10:00 on costs", "due": "2024-03-25T10:00:00Z", "reason": "Send the case studies now; costs are easier to discuss live.",
               "refs": [(d1.IN_COST, d1.Q_COST)]},
         best=d1.ANSWER_BEST, evals=["knowledge_applicability", "commitment_consistency", "next_action_quality"],
         intent={"should_pass": False}, difficulty="medium", extra=["task_trigger", "knowledge_ignored"]),
    spec(id="cr_x_k101_misapplied_cost_breakdown_instead_of_the_call", type="misleading_similar_knowledge", deal=d2.DEAL, trigger=d2.IN_CALL, knowledge=["K101"],
         title="K101 is about cost questions; Keiko only praised the pricing and asked for a call, yet the draft sends a cost breakdown",
         support=d2.SUPPORT, state=d2.STATE, recent=d2.RECENT, commitments=d2.COMMITS,
         cand={"action": "send_email", "to": [d2.KEIKO], "subject": "Re: Tailored proposal for EDA expansion", "attachments": ["Written_cost_breakdown.pdf"], "kn": ["K101"],
               "body": "Hi Keiko,\n\nBefore we speak, I have attached a written breakdown of the costs in the proposal, so you have it first. Let me know what you think.\n\n" + d2.SIGN,
               "next_step": "Await her view on the cost breakdown", "due": None, "reason": "K101: put cost information in writing before a call.", "refs": [(d2.IN_CALL, d2.Q_PRICE)]},
         best={"action": "schedule_meeting", "to": [d2.KEIKO], "channel": "email", "why": "She asked for a Thursday-afternoon call and raised no cost question: book it."},
         evals=["knowledge_applicability", "next_step_quality", "next_action_quality"],
         intent={"should_pass": False}, difficulty="hard", extra=["inbound_email", "knowledge_misapplied", "pricing_praised_not_questioned"]),
    spec(id="cr_x_k102_misapplied_hold_off_on_a_requested_meeting", type="misleading_similar_knowledge", deal=d4.DEAL, trigger=d4.IN_B, knowledge=["K102"],
         title="K102 is about legal review; the buyer's review is procurement, yet the draft leaves the requested meeting until the review is done",
         support=[(d4.IN_A, None), (d4.OUT_C, 600)], state=d4.STATE_B, recent=d4.RECENT_B,
         commitments=[("Customer returns feedback by the end of the week (2023-12-08)", "customer", "2023-12-08T00:00:00Z", "open"),
                      ("Meeting next week, timeline, training and case studies", "rep", None, "open")],
         cand={"action": "send_email", "to": [d4.ADITYA], "subject": "Re: Proposal Follow-Up", "kn": ["K102"],
               "attachments": ["Case_studies_and_references.pdf"],
               "body": "Hi Aditya,\n\nAttached are the case studies you asked for. Since your team is working through the paperwork on its side, I will leave the meeting for next week until you tell me the review is complete.\n\n" + d4.SIGN,
               "next_step": "Aditya says the review is complete; then a meeting is booked", "due": None, "reason": "K102: do not push while the buyer's review is open.",
               "refs": [(d4.IN_B, d4.Q_PROC)]},
         best={"action": "send_email", "to": [d4.ADITYA], "channel": "email", "why": "Answer the open asks (meeting next week, timeline, training, case studies); the review is procurement's, not legal's."},
         evals=["knowledge_applicability", "grounding", "next_action_quality"],
         intent={"should_pass": False}, difficulty="hard", extra=["inbound_email", "knowledge_misapplied"]),
    spec(id="cr_x_k102_followed_no_signature_chase", type="applicable_knowledge", deal=d5.DEAL, trigger=d5.IN_14, knowledge=["K102"],
         title="K102 applies (legal is reviewing) and the draft follows it: no signature chase, the agreed call and the timeline kept",
         support=d5.SUPPORT, state=d5.STATE, recent=d5.RECENT, commitments=d5.COMMITS,
         cand={"action": "send_email", "to": [d5.HARUTO], "subject": "Re: Final Contract Discussions and Preparation", "kn": ["K102"],
               "body": "Hello Haruto,\n\nThank you. We will leave the contract with your legal team and I will not chase a signature; tell me when they are through. Our call on Thursday "
                       "at 2 PM stays for the discount details, and I will send the implementation timeline before it.\n\n" + d5.SIGN,
               "next_step": "Call Thursday 2023-11-30 14:00; written timeline beforehand", "due": "2023-11-30T14:00:00Z",
               "reason": "K102: legal review is open, so no signature request; keep the agreed items.", "refs": [(d5.IN_14, d5.Q_LEGAL)]},
         best={"action": "send_email", "to": [d5.HARUTO], "channel": "email", "why": "This."},
         evals=["knowledge_applicability", "buyer_readiness", "cta_calibration", "decision_process"],
         intent={"should_pass": True}, difficulty="medium", extra=["inbound_email", "knowledge_followed"]),
    spec(id="cr_x_k103_not_triggered_by_a_legal_team_mention", type="misleading_similar_knowledge", deal=d5.DEAL, trigger=d5.IN_14, knowledge=["K103"],
         title="K103 is about a person the buyer asks us to include; Haruto only says his legal team is reviewing, and the draft correctly stays with him",
         support=d5.SUPPORT, state=d5.STATE, recent=d5.RECENT, commitments=d5.COMMITS,
         cand={"action": "send_email", "to": [d5.HARUTO], "subject": "Re: Final Contract Discussions and Preparation",
               "body": "Hello Haruto,\n\nThank you for the update. I will wait for your legal team's review and for your note by Friday. Our call on Thursday at 2 PM stays for the discount "
                       "details, and I will send the implementation timeline before it.\n\n" + d5.SIGN,
               "next_step": "Call Thursday 2023-11-30 14:00; written timeline beforehand", "due": "2023-11-30T14:00:00Z",
               "reason": "He asked for no one to be added; K103 does not apply, so the thread stays as it is.", "refs": [(d5.IN_14, d5.Q_FRIDAY)]},
         best={"action": "send_email", "to": [d5.HARUTO], "channel": "email", "why": "This."},
         evals=["knowledge_applicability", "stakeholder_selection", "stakeholder_coverage"],
         intent={"should_pass": True}, difficulty="hard", extra=["inbound_email", "knowledge_correctly_not_applied"]),
    spec(id="cr_x_waits_while_the_buyer_reviews_the_offer", type="no_action", deal=d3.DEAL, trigger=d3.IN1,
         title="Three preparation emails have gone out and Klara is reviewing the offer: wait for her questions",
         support=[(d3.OUT1, 600), (d3.OUT2, 600), (d3.OUT3, 600)], state=d3.STATE_DUP, recent=(
             "Klara answered the preparation emails: she will review the DevVision IDE offer and revert before the meeting.", ["decision_process", "current_commitments"], ["customer_replied"]),
         commitments=[("Customer reviews the DevVision IDE offer and reverts before the meeting", "customer", None, "open")],
         cand={"action": "wait", "recipients": [], "channel": "none", "subject": None, "body": "", "wait_until": "2023-11-27T09:00:00Z",
               "next_step": "Wait for Klára's questions; the meeting is on 2023-11-30", "due": "2023-11-27T09:00:00Z",
               "reason": "Three preparation emails already went out; she is reviewing and will revert.", "refs": [(d3.IN1, d3.Q_REVIEW)]},
         best={"action": "wait", "wait_until": "2023-11-27T09:00:00Z", "channel": "none", "why": "This."},
         evals=["next_action_quality", "buyer_readiness", "relationship_pressure"],
         intent={"should_pass": True}, difficulty="easy", extra=["inbound_email", "buyer_reviewing", "good_draft"]),
    spec(id="cr_x_schedule_with_the_cto_to_be_added", type="valid_champion_handoff", deal=d3.DEAL, trigger=d3.IN3, gap=GAP,
         title="Invite for the 30 November meeting to Klara, with the CTO to be added once she sends his address (analogue)",
         support=d3.SUPPORT_CTO, state=d3.STATE_CTO, recent=d3.RECENT_CTO,
         commitments=[("Include Martin Hrubý (CTO) in the 2023-11-30 conversation", "rep", "2023-11-30T10:30:00Z", "open")],
         cand={"action": "schedule_meeting", "to": [d3.KLARA], "subject": "Negotiation meeting, November 30 at 10:30 AM", "channel": "email",
               "body": "Hi Klára,\n\nHere is the invite for November 30 at 10:30 AM. I will add Martin Hrubý as soon as you send me his email address, as you asked.\n\n" + d3.SIGN,
               "next_step": "Klára sends Martin Hrubý's address; he is added to the invite", "due": None,
               "reason": "She asked for the CTO to join; the correspondent stays on the invite and I need his address.", "refs": [(d3.IN3, d3.Q_CTO)]},
         best={"action": "schedule_meeting", "to": [d3.KLARA], "channel": "email", "why": "This."},
         evals=["champion_continuity", "stakeholder_selection", "stakeholder_coverage"],
         intent={"should_pass": True}, difficulty="medium", extra=["inbound_email", "handoff_analogue", "named_stakeholder_requested", "good_draft"]),
    spec(id="cr_x_tech_session_asks_who_from_the_tech_team", type="valid_champion_handoff", deal=OMEGA, trigger=OMEGA_IN, gap=GAP,
         title="Zara asks for a session with 'our tech team and yours'; the draft asks who should join and offers two slots (analogue)",
         support=[(OMEGA_OUT, 900), (OMEGA_T, None)], recent=(
             "Zara thanks the rep for addressing customization, asks for a session where the two tech teams go through the specifics next week, and will answer on "
             "pricing by mid-week.", ["decision_process", "current_commitments"], ["customer_replied", "pricing_interest"]),
         state={"group_extra": [], "fields": {
             "decision_process": "Zara will answer on pricing and discounts by mid-week after checking with her team; she asked for a session where the customer's tech team and ours go through OptiPower Manager and SecureFlow Suite customization next week",
             "current_commitments": items("Us: set up the tech session next week (asked 2023-12-02)", "Customer: a comprehensive response on pricing by mid-week"),
             "next_milestone": "Technical session next week"}, "provenance": {"decision_process": OMEGA_IN, **PROV}},
         commitments=[("Set up the tech session next week", "rep", None, "open"), ("Zara's pricing response by mid-week", "customer", None, "open")],
         cand={"action": "send_email", "to": [ZARA_A], "subject": "Re: Negotiation meeting to finalize terms",
               "body": "Hi Zara,\n\nHappy to set up the session with your tech team. Which colleagues should join? I can offer Tuesday, December 5 at 11:00 or Wednesday, December 6 at 15:00.\n\nBest regards,\nJaemin",
               "next_step": "Zara names the attendees and picks a slot", "due": None, "reason": "She asked for a session with her tech team; ask who, so the right people are added while she stays the owner.",
               "refs": [(OMEGA_IN, Q_TECH)]},
         best={"action": "send_email", "to": [ZARA_A], "channel": "email", "why": "This."},
         evals=["champion_continuity", "stakeholder_selection", "stakeholder_coverage"],
         intent={"should_pass": True}, difficulty="medium", extra=["inbound_email", "handoff_analogue", "tech_team_requested", "good_draft"]),
    spec(id="cr_x_finance_and_tech_review_call_asks_who_joins", type="valid_champion_handoff", deal=OMEGA2, trigger=OM2_IN2, gap=GAP,
         title="Sophia has sent the quote to her finance and technical teams and wants a call; the draft offers two slots and asks who should join (analogue)",
         support=[(OM2_IN1, None)], recent=("Sophia forwarded the quote to her finance and technical teams, wants a follow-up call on integration points early next week, "
                                           "and had earlier proposed Thursday afternoon.", ["decision_process", "current_commitments"], ["customer_replied"]),
         state={"group_extra": [], "fields": {
             "decision_process": "Sophia forwarded the quote to her finance and technical teams for review and wants a follow-up call on integration points",
             "current_commitments": items("Us: a call early next week, Monday or Tuesday (asked 2023-11-15 14:45)", "Us: a call on Thursday afternoon (asked 2023-11-15 13:45)"),
             "next_milestone": "Follow-up call early next week"}, "provenance": {"decision_process": OM2_IN2, **PROV}},
         commitments=[("Follow-up call early next week", "rep", None, "open")],
         cand={"action": "send_email", "to": [SOPHIA], "subject": "Re: Quote Preparation for OmegaDesign Labs",
               "body": "Hi Sophia,\n\nMonday, November 20 at 10:00 or Tuesday, November 21 at 14:00 both work for me. Should someone from your finance and technical teams join so we can cover the integration points? If so, send me their names and I will add them.\n\nBest regards,\nPriya",
               "next_step": "Sophia picks a slot and names who joins", "due": None, "reason": "She asked for a call early next week and has the quote with finance and technical teams; ask who joins while she stays the owner.",
               "refs": [(OM2_IN2, Q_NEXTWEEK), (OM2_IN2, Q_FWD)]},
         best={"action": "send_email", "to": [SOPHIA], "channel": "email", "why": "This."},
         evals=["champion_continuity", "stakeholder_selection", "stakeholder_coverage"],
         intent={"should_pass": True}, difficulty="medium", extra=["inbound_email", "handoff_analogue", "teams_named_not_required", "good_draft"]),
    spec(id="cr_x_legal_specialist_added_instead_of_the_cto", type="valid_champion_handoff", deal=d3.DEAL, trigger=d3.IN3, gap=GAP,
         title="Invite for the 30 November meeting adds a legal colleague of Klara's; the CTO she asked for is not mentioned (analogue)",
         support=d3.SUPPORT_CTO, state=d3.STATE_CTO, recent=d3.RECENT_CTO,
         commitments=[("Include Martin Hrubý (CTO) in the 2023-11-30 conversation", "rep", "2023-11-30T10:30:00Z", "open")],
         cand={"action": "schedule_meeting", "to": [d3.KLARA], "cc": [d3.EMILY], "subject": "Negotiation meeting, November 30 at 10:30 AM", "channel": "email",
               "body": "Hi Klára,\n\nHere is the invite for November 30 at 10:30 AM. I have copied Emily Johnson from your legal team so the contract points are covered.\n\n" + d3.SIGN,
               "next_step": "Negotiation meeting 2023-11-30 10:30", "due": "2023-11-30T10:30:00Z",
               "reason": "Cover the legal side of the terms discussion.", "refs": [(d3.OUT2, d3.Q_MEET)]},
         best={"action": "schedule_meeting", "to": [d3.KLARA], "channel": "email", "why": "Send the invite and ask for Martin Hrubý's address so he is added, as she asked."},
         evals=["champion_continuity", "stakeholder_selection", "stakeholder_coverage"],
         intent={"should_pass": False}, difficulty="medium", extra=["inbound_email", "handoff_analogue", "named_stakeholder_requested"]),
]
