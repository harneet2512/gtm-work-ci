"""Contrastive field taxonomy for the extract prompt: what each field_path is, what it is NOT (and where
that goes instead), and one synthetic example. Part of the versioned prompt: any wording change here
changes cassette keys, so bump EXTRACTOR_VERSION in prompt.py.

Examples are invented. Never copy benchmark fixture sentences here: tests/test_extract_taxonomy.py fails
on any six-word run shared with fixtures/ or on a fixture person or account name.
"""
from __future__ import annotations

from dataclasses import dataclass


@dataclass(frozen=True)
class FieldDefinition:
    path: str
    means: str
    is_not: str
    example: str


def _d(path: str, means: str, is_not: str, example: str) -> FieldDefinition:
    return FieldDefinition(path=path, means=means, is_not=is_not, example=example)


FIELD_DEFINITIONS: tuple[FieldDefinition, ...] = (
    _d("stage",
       "the opportunity's sales stage, only when the text names it explicitly (a CRM stage change, a stated phase).",
       "a stage you infer from tone (omit it); the next step in the deal (use next_milestone).",
       '"Moved the opportunity to Negotiation today." -> value "Negotiation"'),
    _d("health",
       "an explicit overall verdict on the deal's health (on track, at risk, stalled) stated by someone.",
       "one specific risk signal (use relationship_risk); one concrete obstacle (use blockers).",
       '"Honestly, I think this one has stalled for the quarter." -> value "stalled"'),
    _d("owner",
       "a person at the seller company (side seller in the participant or known people lists) who owns the account "
       "or opportunity, including a handover between seller reps; never a buyer-side person.",
       "a buyer-side person driving, running or owning the rollout, even when the buyer writes 'on our side' or "
       "'my team' (use champion, or buying_group.member plus stakeholder_role); a buyer-side person taking over "
       "the evaluation (use delegation).",
       '"Account owner changed to Kofi Mensah in the CRM today." -> value "Kofi Mensah"'),
    _d("motion",
       "the kind of commercial motion when stated: new business, expansion or renewal.",
       "what the product will be used for or at what scope (use product_use_case).",
       '"This would be a renewal of the current contract for another year." -> value "renewal"'),
    _d("champion",
       "the buyer-side person who actively advocates for us internally (sponsors the project, drives the rollout, "
       "pushes it forward), including continuity: a buyer saying someone is still or remains the main contact or "
       "the driver; value = that person's name, subject_identity = that person.",
       "the budget holder who only signs (use economic_buyer); a change in how engaged the champion is "
       "(use champion_status); a hand-off to someone else (use delegation); a seller-side person (use owner).",
       '"Lina Varga remains the person championing this with our leadership." -> value "Lina Varga"'),
    _d("champion_status",
       "the existing champion's own engagement changing: stepping back, handing off the daily work, leaving, going "
       "quiet, or re-engaging; value = the new state in a word or two, subject_identity = the champion. Report it "
       "even when the same message also names who takes over.",
       "who the champion is (use champion); who we should now work with instead (use delegation; when one message "
       "does both, emit both, each with its own quote).",
       '"With the board audit on my plate I will only check in on this project once a month." -> '
       'value "stepping back"'),
    _d("economic_buyer",
       "the person who controls the budget or gives final financial sign-off; value = their name, or 'unknown' "
       "when the text says nobody is identified or a committee decides.",
       "a reviewer whose approval is one step of the process (use decision_process or buying_group.member); "
       "an approval that has just happened (use decision_process).",
       '"Our COO, Nikhil Rao, has the final say on any spend at this level." -> value "Nikhil Rao"'),
    _d("buying_group.member",
       "a named buyer-side person who takes part in this decision, typically when introduced or first involved "
       "(introduces themselves with their function, must sign off, will evaluate, will own the evaluation); "
       "value = name, subject_identity = that person, role = their role.",
       "the role of someone already in the group (use stakeholder_role; when a newcomer's role in the decision "
       "is stated, emit both); the order of approvals (use decision_process).",
       '"Adding Yuki Tanaka, who runs purchasing and has to approve every new supplier." -> '
       'value "Yuki Tanaka", role procurement'),
    _d("stakeholder_role",
       "the part a specific person plays in this decision (technical approver, security reviewer, end user, "
       "purchasing); value = that role in a few words, subject_identity = the person, role = the closest role.",
       "a mention that only says a person is involved, with no role stated (use buying_group.member; when a "
       "newcomer is introduced with their role, emit both); the order of steps or approvals (use decision_process).",
       '"Amara Osei will be the one testing the API integration." -> '
       'value "technical evaluator", role technical_evaluator'),
    _d("blockers",
       "a concrete obstacle that stops or gates progress now: an approval, document, review or budget decision "
       "that must happen before the deal can move; also a blocker that is now cleared (value starts 'resolved: ').",
       "a standard or goal the buyer will judge the outcome by (use decision_criteria); price, billing or contract "
       "terms (use commercial_issue); a soft concern (use objections); a routine approval step that is not "
       "holding anything up (use decision_process).",
       '"We cannot move forward until our lawyers have reviewed your data processing agreement." -> value '
       '"legal review of the DPA"'),
    _d("objections",
       "a concern or pushback the buyer raises about our product, price, fit or timing that is not a hard gate.",
       "a requirement that must be met before progress (use blockers); risk to the relationship itself "
       "(use relationship_risk).",
       '"I worry the mobile app will feel sluggish for our field technicians." -> value "mobile app performance"'),
    _d("decision_criteria",
       "the standard, metric, capability or deadline the buyer will judge the purchase or its success by "
       "(what a yes depends on), including an external deadline the solution must be live by.",
       "a specific approval or document that is still outstanding (use blockers); who approves and in what order "
       "(use decision_process); the planned date of the next deal step (use next_milestone).",
       '"We will choose whichever tool cuts new-hire ramp time in half." -> value "halve new-hire ramp time"'),
    _d("decision_process",
       "how the buyer decides: the approval steps, which teams must review and in what order, and approval events "
       "as they happen (a business case approved, a review passed).",
       "one person's job or role (use stakeholder_role); naming the budget holder (use economic_buyer); an "
       "obstacle still holding up progress (use blockers).",
       '"Once IT has signed, the purchase goes to the finance committee for a vote." -> value '
       '"IT approval, then finance committee vote"'),
    _d("commitment",
       "a promise by a named party, us or them, to do a specific thing (I will send, we can share once, let me "
       "introduce you), or an explicit request that we deliver a document or material (the obligation is then "
       "ours); value = who does what, due_at only if a date is given.",
       "the next stage of the deal that nobody personally promised (use next_milestone); a meeting being booked "
       "or a request to send an invite (use next_meeting).",
       '"I will get the completed security questionnaire back to you by Thursday." -> value '
       '"return the completed security questionnaire by Thursday"'),
    _d("next_milestone",
       "the next step of the deal itself (an evaluation, review, approval, pilot, signature, go-live) or its date; "
       "value 'unknown' when the text says the date cannot be set yet.",
       "one person's promise to do something (use commitment); meeting logistics such as a time slot or an "
       "invite (use next_meeting; when the meeting is itself the next deal step, emit both).",
       '"Next up is a two-week pilot with the Lyon support desk in May." -> value "two-week pilot in Lyon in May"'),
    _d("next_meeting",
       "a meeting that is scheduled, proposed or requested (call, demo, review session, a request to send a "
       "calendar invite), with its time if given; value 'unknown' when a meeting is wanted but no time is fixed.",
       "a deal step with no meeting attached (use next_milestone; when the meeting is itself the next deal "
       "step, emit both); someone handing over ownership (use delegation); asking for an invite is never delegation.",
       '"Could we do a demo for the wider team on Tuesday at 3pm?" -> value "team demo Tuesday 3pm (proposed)"'),
    _d("relationship_risk",
       "a signal that the relationship itself is at risk: going silent, frustration, escalation, a competitor in "
       "play.",
       "a gating step in the buying process (use blockers); a product concern (use objections); a change in "
       "the champion's own engagement (use champion_status).",
       '"Just so you know, we have also started a trial with another vendor." -> value "evaluating a competitor"'),
    _d("product_use_case",
       "what the buyer will use the product for and at what scope: teams, sites, workflows, user or seat counts, "
       "volumes.",
       "price, discount or contract terms for that scope (use commercial_issue); the kind of deal (use motion).",
       '"We would start with the three claims-handling teams, about 90 people in total." -> value '
       '"claims-handling teams, about 90 users"'),
    _d("commercial_issue",
       "pricing, discounts, billing, invoicing, payment terms, the contracting entity or other contract terms.",
       "a budget approval that is still pending (use blockers); seat counts or scope (use product_use_case).",
       '"Can you bill us annually instead of monthly?" -> value "wants annual billing"'),
    _d("delegation",
       "who we should now work with instead: a buyer-side person tells us to work with someone else from now on, "
       "or hands ownership of the evaluation or decision to another person; value = who takes over (and the scope "
       "if stated), subject_identity = the person who delegates.",
       "the delegating person's own change of engagement (use champion_status; when one message does both, emit "
       "both, each with its own quote); a request to send an invite or book a meeting (use next_meeting); a "
       "promise to make an introduction without handing over ownership (use commitment); a statement that "
       "someone is still or remains the contact or driver (use champion: continuity, not a hand-off); a handover "
       "between seller-side reps (use owner).",
       '"From now on, please go through Mei Chen in IT for anything technical." -> value '
       '"Mei Chen (IT) for technical questions"'),
    _d("summary",
       "a one-line factual summary of the interaction, only when no finer field applies.",
       "anything that fits a specific field above (use that field, e.g. commitment or blockers).",
       '"Thanks for the catch-up earlier, nothing new on our side." -> value "check-in, no new facts"'),
)


def render_field_guide() -> str:
    """IS / NOT / EXAMPLE block per field_path, in contract enum order."""
    return "\n".join(
        f"[{d.path}]\n  IS: {d.means}\n  NOT: {d.is_not}\n  EXAMPLE: {d.example}" for d in FIELD_DEFINITIONS)
