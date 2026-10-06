"""Pull log and grounding guard: a proposal may only cite what a logged context pull returned in this run."""
from __future__ import annotations

import re
from collections.abc import Iterable, Iterator, Mapping
from dataclasses import dataclass
from typing import Any

from ..errors import UngroundedProposalError
from ..models.draft import UUID_PATTERN, EvidenceRef
from .core_client import ContextPacket, ToolName

_UUID = re.compile(UUID_PATTERN)
_PERSON_TOOLS = frozenset({"people", "state"})
_PERSON_KEYS = frozenset({"person_id", "speaker_person_id"})


def _is_uuid(value: object) -> bool:
    return isinstance(value, str) and _UUID.fullmatch(value) is not None


def _walk(items: Iterable[Any]) -> Iterator[tuple[str, Any]]:
    """Every (key, value) pair in the packet, iteratively (hostile nesting cannot hit the recursion limit)."""
    stack: list[Any] = list(items)
    while stack:
        node = stack.pop()
        if isinstance(node, Mapping):
            for key, value in node.items():
                yield key, value
                stack.append(value)
        elif isinstance(node, list):
            stack.extend(node)


@dataclass(frozen=True)
class ContextRef:
    """What one logged pull returned: the packet plus the ids found in it, by kind."""

    tool: ToolName
    access_id: int
    bytes: int
    packet: ContextPacket
    activity_ids: frozenset[str]
    claim_ids: frozenset[str]
    person_ids: frozenset[str]  # only people/state pulls may name people

    @classmethod
    def from_packet(cls, packet: ContextPacket) -> ContextRef:
        activities: set[str] = set()
        claims: set[str] = set()
        people: set[str] = set()
        for key, value in _walk(packet.items):
            if not _is_uuid(value):
                continue
            if key == "activity_id" or (key == "id" and packet.tool == "activities"):
                activities.add(value)
            elif key == "claim_id":
                claims.add(value)
            elif key in _PERSON_KEYS or (key == "id" and packet.tool == "people") or (
                    key == "value" and packet.tool == "state"):
                people.add(value)
        return cls(tool=packet.tool, access_id=packet.access_id, bytes=packet.bytes, packet=packet,
                   activity_ids=frozenset(activities), claim_ids=frozenset(claims),
                   person_ids=frozenset(people) if packet.tool in _PERSON_TOOLS else frozenset())


@dataclass(frozen=True)
class GroundedEvidence:
    kept: tuple[EvidenceRef, ...]
    dropped: int
    access_ids: tuple[int, ...]  # pulls that returned the kept references' ids


@dataclass(frozen=True)
class PullLog:
    """Immutable record of this run's context pulls; `record` returns a new log."""

    refs: tuple[ContextRef, ...] = ()

    def record(self, packet: ContextPacket) -> PullLog:
        return PullLog((*self.refs, ContextRef.from_packet(packet)))

    @property
    def packets(self) -> tuple[ContextPacket, ...]:
        return tuple(r.packet for r in self.refs)

    @property
    def access_ids(self) -> tuple[int, ...]:
        return tuple(r.access_id for r in self.refs)

    @property
    def activity_ids(self) -> frozenset[str]:
        return frozenset().union(*(r.activity_ids for r in self.refs))

    @property
    def claim_ids(self) -> frozenset[str]:
        return frozenset().union(*(r.claim_ids for r in self.refs))

    @property
    def person_ids(self) -> frozenset[str]:
        return frozenset().union(*(r.person_ids for r in self.refs))

    def ground_evidence(self, refs: tuple[EvidenceRef, ...]) -> GroundedEvidence:
        """Keep references whose activity (and claim, when given) this run pulled."""
        kept = tuple(r for r in refs if r.activity_id in self.activity_ids
                     and (r.claim_id is None or r.claim_id in self.claim_ids))
        cited = {pull.access_id for pull in self.refs for ref in kept
                 if ref.activity_id in pull.activity_ids or ref.claim_id in pull.claim_ids}
        return GroundedEvidence(kept=kept, dropped=len(refs) - len(kept), access_ids=tuple(sorted(cited)))

    def unknown_people(self, person_ids: Iterable[str]) -> tuple[str, ...]:
        """The named people no people/state pull in this run returned (sorted, unique)."""
        return tuple(sorted(set(person_ids) - self.person_ids))

    def require_known_people(self, person_ids: Iterable[str], role: str) -> None:
        """Every named person must have been returned by a people/state pull (`role` says who named them)."""
        unknown = self.unknown_people(person_ids)
        if unknown:
            raise UngroundedProposalError(
                f"{role} person(s) not returned by the people/state tools: {', '.join(unknown)}")
