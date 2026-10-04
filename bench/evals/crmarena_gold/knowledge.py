"""Knowledge offered in the CRMArena gold cases (HAR-114 gold v2).

Four small lessons, written for the gold and seeded from PREVIOUS deals (before the 2023-11-01 cutoff): the counts are
measured by knowledge_stats.py (`--check` verifies them against the snapshot) and are weak: `outcomes_advanced` is
'the deal has a signed contract', and CRMArena records no reactions. They are test inputs for the applicability judges,
not claims that the practice works. Signatures are conditions over the state fields the cases author from the record.
"""
from __future__ import annotations

from typing import Any

from .source import uid

SEED_NOTE = ("Seeded from previous CRMArena deals (before 2023-11-01): decisions = previous deals with a matching customer "
             "email, outcomes_advanced = those with a signed contract; no reactions are recorded. Not evidence the practice works.")


def item(key: str, title: str, signature: list[dict], summary: str, do: list[str], dont: list[str],
         exceptions: list[dict], status: str, decisions: int, advanced: int) -> dict[str, Any]:
    return {
        "id": uid("knowledge", key), "key": key, "title": title, "situation_signature": signature,
        "guidance": {"summary": summary, "do": do, "dont": dont}, "status": status,
        "counts": {"decisions": decisions, "positive_reactions": 0, "negative_reactions": 0,
                   "outcomes_advanced": advanced, "counterexamples": 0},
        "supporting_decision_episode_ids": [], "counterexamples": [], "exceptions": exceptions,
        "evidence_classes": ["deal_data"], "used_by_evaluators": ["knowledge_applicability:v1"],
        "provenance": {"created_from": "seed_history", "source_decision_episode_id": None, "note": SEED_NOTE},
        "created_at": "2023-11-01T00:00:00Z", "last_validated_at": None}


def cond(field: str, op: str, value: Any) -> dict[str, Any]:
    return {"field": field, "op": op, "value": value}


KNOWLEDGE: dict[str, dict[str, Any]] = {
    "K101": item(
        "K101", "Answer a cost question in writing before proposing a call",
        [cond("objections", "contains", "cost")],
        "When the buyer asks what something will cost, answer in writing first; a call comes after the answer.",
        ["state what the quoted price covers and what it does not", "if you cannot give a figure, say what you will confirm and by when"],
        ["propose a negotiation meeting before the cost question is answered", "guess a figure"],
        [{"description": "The buyer asked explicitly for a call to discuss costs",
          "conditions": [cond("current_commitments", "contains", "call to discuss")]}],
        "provisional", 42, 11),
    "K102": item(
        "K102", "Do not ask for a signature while the buyer's legal review is open",
        [cond("decision_process", "contains", "legal")],
        "While the buyer's legal team is reviewing, leave signing to them; do not set a signature deadline.",
        ["answer open questions and keep calls that were agreed", "let the buyer say when legal is done"],
        ["send a signature version or set a signing deadline"],
        [{"description": "The buyer asks for the signature version",
          "conditions": [cond("current_commitments", "contains", "signature version")]}],
        "provisional", 8, 4),
    "K103": item(
        "K103", "Include the person the buyer asks to be included",
        [cond("decision_process", "contains", "asked that")],
        "When the buyer names someone to include in the next conversation, include them and ask for their address if you lack it.",
        ["confirm the person will be added", "ask the buyer for the contact details you lack"],
        ["confirm the meeting without the named person", "add people the buyer did not name"],
        [{"description": "The buyer withdraws the request",
          "conditions": [cond("decision_process", "contains", "no longer needed")]}],
        "candidate", 0, 0),
    "K104": item(
        "K104", "Check in the next business day after sending revised terms",
        [cond("next_milestone", "contains", "feedback on the revised terms")],
        "After revised terms go out, a short check-in the next business day keeps the thread warm.",
        ["send a short check-in asking whether the team has questions"],
        ["stay silent for more than two business days"],
        [{"description": "The buyer stated their own date for feedback and it has not passed",
          "conditions": [cond("decision_process", "contains", "said they will revert by")]}],
        "provisional", 78, 17),
}
