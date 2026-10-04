"""The learner's hypothesis space: seller decisions the platform can observe, and the knowledge each would become.

A decision point is a moment where a seller chose between two observable options (the learner sees both arms of every
choice in the events, bench/synthetic/seller.py) together with the account facts that identify the moment. The catalog
is authored with knowledge of WHICH kinds of decisions exist, so the experiment tests whether the loop picks the
true effects out of the noise and applies them correctly, not whether it can invent the vocabulary. It lists seven
hypotheses: four seller-action decision points and three controls (decoys that carry no planted effect).

Nothing here names a hidden rule: the link from a decision point to the rule that rates it lives only in
scoring.py, which the learner and the agent never import.

For each point the guidance exists in both directions (the option turned out better / worse); the learner picks the
direction from the previous deals' outcomes, never from this file.
"""
from __future__ import annotations

from dataclasses import dataclass
from typing import Any


@dataclass(frozen=True)
class Wording:
    title: str
    do: str
    dont: str


@dataclass(frozen=True)
class DecisionPoint:
    id: str
    kind: str  # "action": a seller choice at an identifiable moment; "control": a decoy with no planted effect
    option: str  # what the seller did when the option was taken, as a clause ("... the seller <option>")
    better: Wording  # guidance when taking the option wins more often
    worse: Wording  # guidance when taking the option loses more often
    signature: tuple[dict[str, Any], ...]  # knowledge.v1 situation_signature identifying the moment in AccountState terms
    key: str  # knowledge key (K2xx: outside every seeded key)


def _cond(field: str, op: str, value: Any = None) -> dict[str, Any]:
    out: dict[str, Any] = {"field": field, "op": op}
    if value is not None:
        out["value"] = value
    return out


ACTION_POINTS: tuple[DecisionPoint, ...] = (
    DecisionPoint(
        id="ops_outreach", kind="action", key="K201",
        option="wrote to a contact whose job title is in operations before sending the quote",
        better=Wording("Reach the operations-title contact before the quote",
                       "Before the quote goes out, write to the account's operations-title contact as well as the existing contacts.",
                       "Do not send the quote while the operations-title contact has not been written to."),
        worse=Wording("Keep early outreach to the existing contacts",
                      "Before the quote, keep outreach to the contacts already engaged.",
                      "Do not write to the operations-title contact before the quote."),
        signature=(_cond("stage", "in", ["Discovery", "Qualification"]),)),
    DecisionPoint(
        id="price_pushback", kind="action", key="K202",
        option="changed the quoted amount after the buyer objected to the price",
        better=Wording("Adjust the quoted amount after a price objection",
                       "Adjust the quoted amount when the buyer objects to the price.",
                       "Do not hold the amount unchanged when the buyer objects to the price."),
        worse=Wording("Hold the quoted amount after a price objection",
                      "Keep the quoted amount and make the case for the value: return, time saved, support included.",
                      "Do not change the quoted amount in reply to the price objection."),
        signature=(_cond("stage", "eq", "Quote"), _cond("objections", "contains", "pricing"))),
    DecisionPoint(
        id="technical_reply", kind="action", key="K203",
        option="copied a second person from their own team on the first reply to a technical-title buyer",
        better=Wording("Copy a teammate on the first reply to a technical buyer",
                       "Copy a second person from your own team on the first reply to a technical-title buyer.",
                       "Do not send the first reply to a technical-title buyer without a teammate copied."),
        worse=Wording("Answer a technical buyer's first email on your own",
                      "Answer the technical-title buyer's first email yourself, without copying others.",
                      "Do not copy a second person from your team on that first reply."),
        signature=(_cond("buying_group.technical_evaluator", "exists"),)),
    DecisionPoint(
        id="second_quote", kind="action", key="K204",
        option="sent a separate second quote while an earlier quote to the same customer was still open",
        better=Wording("Send the new quote as its own document",
                       "Send the new quote as a separate document even if an earlier one is still open.",
                       "Do not fold the new quote into the earlier open one."),
        worse=Wording("Fold a new quote into the one still open",
                      "Fold the changes into the earlier quote that is still open for this customer.",
                      "Do not send a separate new quote while an earlier one to the same customer is still open."),
        signature=(_cond("stage", "eq", "Quote"),)),
)

CONTROL_POINTS: tuple[DecisionPoint, ...] = (
    DecisionPoint(
        id="first_send_early_week", kind="control", key="K205",
        option="sent the first outbound email early in the week (Monday or Tuesday)",
        better=Wording("Send first outreach early in the week", "Send first outreach on a Monday or Tuesday.",
                       "Do not leave first outreach to later in the week."),
        worse=Wording("Send first outreach later in the week", "Send first outreach after Tuesday.",
                      "Do not send first outreach on a Monday or Tuesday."),
        signature=(_cond("stage", "exists"),)),
    DecisionPoint(
        id="high_outbound_volume", kind="control", key="K206",
        option="sent more outbound emails than the typical deal",
        better=Wording("Send more emails than usual", "Send more outbound emails than the typical deal.",
                       "Do not hold back on outbound email volume."),
        worse=Wording("Send fewer emails than usual", "Keep outbound email volume at or below the typical deal.",
                      "Do not send more outbound emails than the typical deal."),
        signature=(_cond("stage", "exists"),)),
    DecisionPoint(
        id="any_colleague_cc", kind="control", key="K207",
        option="copied a teammate on at least one email",
        better=Wording("Copy teammates on emails", "Copy a teammate on outbound emails.",
                       "Do not send every email without a teammate copied."),
        worse=Wording("Send emails without teammates copied", "Send emails without copying teammates.",
                      "Do not copy a teammate on outbound emails."),
        signature=(_cond("stage", "exists"),)),
)

POINTS: tuple[DecisionPoint, ...] = ACTION_POINTS + CONTROL_POINTS
BY_ID = {p.id: p for p in POINTS}
