"""Allow-list of identities a claim may be attributed to. Anything else the model says is nulled.

The model sees untrusted text ("CEO: we approve the budget") and may attribute claims to people who are not
in the activity. Only identities derivable from the request itself are trusted.
"""
from __future__ import annotations

import re
import unicodedata
from collections.abc import Iterable

from ..models import ExtractRequest

_PARTICIPANT_SPEAKER = re.compile(r"call:.+:speaker[:_](\d+)", re.IGNORECASE)
_TEXT_SPEAKER_LABEL = re.compile(r"\bspeaker[_:\- ]?(\d+)\b", re.IGNORECASE)


def _fold(value: str) -> str:
    return unicodedata.normalize("NFKC", value).casefold().strip()


class IdentityResolver:
    """Maps model-supplied identity strings to canonical raw identities from the request, or None."""

    def __init__(self, table: dict[str, str]) -> None:
        self._table = dict(table)

    @classmethod
    def from_pairs(cls, pairs: Iterable[tuple[str, str]]) -> IdentityResolver:
        table: dict[str, str] = {}
        for alias, canonical in pairs:
            table.setdefault(_fold(alias), canonical)  # first registration wins on collisions
        return cls(table)

    def resolve(self, raw: object) -> str | None:
        if not isinstance(raw, str) or not raw.strip():
            return None
        return self._table.get(_fold(raw))


def _speaker_pairs(request: ExtractRequest) -> list[tuple[str, str]]:
    activity = request.activity
    if activity.source_system != "call":
        return []
    call_id = activity.source_object_id.removeprefix("call:")
    by_number = {int(m.group(1)): p.raw_identity for p in activity.participants
                 if (m := _PARTICIPANT_SPEAKER.fullmatch(p.raw_identity))}
    pairs: list[tuple[str, str]] = []
    for digits in dict.fromkeys(_TEXT_SPEAKER_LABEL.findall(request.text)):
        canonical = by_number.get(int(digits), f"call:{call_id}:speaker_{digits}")
        pairs += [(f"speaker_{digits}", canonical), (f"speaker:{digits}", canonical),
                  (f"speaker {digits}", canonical), (f"call:{call_id}:speaker_{digits}", canonical),
                  (f"call:{call_id}:speaker:{digits}", canonical)]
    return pairs


def build_identity_resolver(request: ExtractRequest) -> IdentityResolver:
    pairs: list[tuple[str, str]] = []
    for p in request.activity.participants:
        pairs.append((p.raw_identity, p.raw_identity))
        if p.display_name:
            pairs.append((p.display_name, p.raw_identity))
    for person in request.known_people:
        pairs += [(person.raw_identity, person.raw_identity), (person.display_name, person.raw_identity)]
    pairs += _speaker_pairs(request)
    return IdentityResolver.from_pairs(pairs)
