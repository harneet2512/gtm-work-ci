"""Deterministic guard: an `owner` claim (seller side by definition) naming a buyer-side known person is rejected."""
from __future__ import annotations

from typing import Any

import pytest

from ghost_worker.extract.guards import reject_buyer_side_owners
from ghost_worker.models import ClaimCandidate, KnownPerson

BUYER = KnownPerson(raw_identity="ines@buyerco.example", display_name="Ines Duarte", internal=False)
SELLER = KnownPerson(raw_identity="kofi@seller.example", display_name="Kofi Mensah", internal=True)
UNKNOWN = KnownPerson(raw_identity="yuki@other.example", display_name="Yuki Tanaka")
PEOPLE = (BUYER, SELLER, UNKNOWN)


def claim(value: Any = "Ines Duarte", field_path: str = "owner", subject: str | None = None) -> ClaimCandidate:
    return ClaimCandidate(field_path=field_path, value=value, confidence=0.8, evidence_quote="q",
                          subject_identity=subject)


@pytest.mark.parametrize("value", ["Ines Duarte", "ines duarte", "Ines", "Ines Duarte (rollout lead)",
                                   "ines@buyerco.example", "Ｉｎｅｓ Duarte"])
def test_owner_naming_a_buyer_side_person_is_rejected(value: str) -> None:
    kept, rejected = reject_buyer_side_owners((claim(value),), PEOPLE)
    assert (kept, rejected) == ((), 1)


def test_owner_whose_subject_is_a_buyer_side_person_is_rejected() -> None:
    kept, rejected = reject_buyer_side_owners((claim("the rollout lead", subject=BUYER.raw_identity),), PEOPLE)
    assert (kept, rejected) == ((), 1)


@pytest.mark.parametrize("value", ["Kofi Mensah", "Yuki Tanaka", "Inesa Novak", "our account team",
                                   "Kofi Mensah with Ines Duarte"])
def test_owner_naming_a_seller_unknown_or_ambiguous_person_is_kept(value: str) -> None:
    original = (claim(value),)
    assert reject_buyer_side_owners(original, PEOPLE) == (original, 0)


def test_only_owner_claims_are_checked_and_order_is_kept() -> None:
    claims = (claim(field_path="champion"), claim("Kofi Mensah"), claim(), claim(field_path="delegation"))
    kept, rejected = reject_buyer_side_owners(claims, PEOPLE)
    assert kept == (claims[0], claims[1], claims[3]) and rejected == 1


def test_non_string_values_are_checked_by_subject_only() -> None:
    structured = claim({"name": "Ines Duarte"})
    assert reject_buyer_side_owners((structured,), PEOPLE) == ((structured,), 0)


def test_shared_first_name_with_a_seller_side_person_is_not_enough() -> None:
    namesake = KnownPerson(raw_identity="ines@seller.example", display_name="Ines Almeida", internal=True)
    original = (claim("Ines"),)
    assert reject_buyer_side_owners(original, (*PEOPLE, namesake)) == (original, 0)


def test_without_side_information_nothing_is_rejected() -> None:
    original = (claim(),)
    unmarked = KnownPerson(raw_identity=BUYER.raw_identity, display_name=BUYER.display_name)
    assert reject_buyer_side_owners(original, (unmarked,)) == (original, 0)
