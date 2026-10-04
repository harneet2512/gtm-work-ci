"""Deterministic guards on verified claims, applied after postprocessing.

`owner` is the seller-side account owner. The model sometimes reads a buyer's "on our side" from the seller's
perspective and files a buyer-side person as owner. When known_people marks that person internal=false (and no
seller-side person is also named) the claim is rejected, not relabelled: the quote may support champion,
economic_buyer or nothing at all, and guessing would invent a claim the model did not make.
"""
from __future__ import annotations

import re
import unicodedata
from collections.abc import Iterable

from ..models import ClaimCandidate, KnownPerson


def _fold(value: str) -> str:
    return unicodedata.normalize("NFKC", value).casefold().strip()


def _words(value: str) -> list[str]:
    return re.findall(r"[^\W_]+", value)


def _names(person: KnownPerson, folded_value: str) -> bool:
    """Does the (folded) claim value name this person, by raw identity or by first name (a full name
    contains it)? Whole words only, so "Inesa" does not name "Ines"."""
    identity = _fold(person.raw_identity)
    if identity and identity in folded_value:
        return True
    name_words = _words(_fold(person.display_name))
    return bool(name_words) and name_words[0] in _words(folded_value)


def _names_buyer_side(claim: ClaimCandidate, people: Iterable[KnownPerson]) -> bool:
    sided = [p for p in people if p.internal is not None]
    subject = _fold(claim.subject_identity or "")
    if subject and any(not p.internal and _fold(p.raw_identity) == subject for p in sided):
        return True
    if not isinstance(claim.value, str):
        return False
    value = _fold(claim.value)
    named = [p for p in sided if _names(p, value)]
    return bool(named) and all(not p.internal for p in named)


def reject_buyer_side_owners(claims: tuple[ClaimCandidate, ...],
                             people: tuple[KnownPerson, ...]) -> tuple[tuple[ClaimCandidate, ...], int]:
    """Drop `owner` claims that name only buyer-side known people; return the kept claims and the count."""
    kept = tuple(c for c in claims if not (c.field_path == "owner" and _names_buyer_side(c, people)))
    return kept, len(claims) - len(kept)
