"""PullLog: an immutable record of this run's pulls, and the grounding rules built on it."""
from __future__ import annotations

import dataclasses

import pytest

from ghost_worker.draft.core_client import ContextPacket
from ghost_worker.draft.grounding import PullLog
from ghost_worker.errors import UngroundedProposalError
from ghost_worker.models.draft import EvidenceRef

ACT = "0ac70000-0000-4000-8000-000000000101"
ACT2 = "0ac70000-0000-4000-8000-000000000102"
CLAIM = "0c1a0000-0000-4000-8000-000000000201"
MARCO = "0b0e0000-0000-4000-8000-000000000018"
PRIYA = "0b0e0000-0000-4000-8000-000000000017"
OTHER = "0b0e0000-0000-4000-8000-000000000099"


def pkt(tool: str, items: list, access_id: int = 1) -> ContextPacket:
    return ContextPacket.model_validate(
        {"access_id": access_id, "tool": tool, "items": items, "truncated": False, "bytes": 10})


def log_with(*packets: ContextPacket) -> PullLog:
    log = PullLog()
    for packet in packets:
        log = log.record(packet)
    return log


def test_record_returns_a_new_log_and_leaves_the_old_one_untouched() -> None:
    empty = PullLog()
    one = empty.record(pkt("people", [{"person_id": MARCO}], 7))
    assert empty.refs == () and empty.person_ids == frozenset()
    assert one is not empty and one.access_ids == (7,) and one.person_ids == {MARCO}
    assert one.record(pkt("evidence", [{"claim_id": CLAIM}], 8)).access_ids == (7, 8)
    assert one.access_ids == (7,)


def test_the_log_exposes_only_immutable_collections() -> None:
    log = log_with(pkt("state", [{"value": PRIYA, "evidence_refs": [{"activity_id": ACT, "claim_id": CLAIM}]}]))
    assert isinstance(log.refs, tuple) and isinstance(log.packets, tuple) and isinstance(log.access_ids, tuple)
    for ids in (log.activity_ids, log.claim_ids, log.person_ids):
        assert isinstance(ids, frozenset)
    with pytest.raises(dataclasses.FrozenInstanceError):
        log.refs = ()  # type: ignore[misc]
    with pytest.raises(dataclasses.FrozenInstanceError):
        log.refs[0].activity_ids = frozenset()  # type: ignore[misc]


def test_ref_records_tool_access_id_bytes_and_ids() -> None:
    (ref,) = log_with(pkt("evidence", [{"claim_id": CLAIM, "activity_id": ACT, "speaker_person_id": MARCO}], 102)).refs
    assert (ref.tool, ref.access_id, ref.bytes) == ("evidence", 102, 10)
    assert ref.activity_ids == {ACT} and ref.claim_ids == {CLAIM}
    assert ref.person_ids == frozenset()  # an evidence pull cannot name recipients


def test_ids_are_found_in_nested_structures_and_state_values() -> None:
    state = pkt("state", [{"field_path": "champion", "value": PRIYA,
                           "evidence_refs": [{"activity_id": ACT, "claim_id": CLAIM}]}])
    log = log_with(state)
    assert log.person_ids == {PRIYA} and log.activity_ids == {ACT} and log.claim_ids == {CLAIM}


def test_person_ids_come_only_from_people_and_state_tools() -> None:
    log = log_with(pkt("evidence", [{"speaker_person_id": MARCO}]),
                   pkt("activities", [{"activity_id": ACT, "participants": [{"person_id": OTHER}]}]))
    assert log.person_ids == frozenset()
    assert log_with(pkt("people", [{"person_id": MARCO}, {"id": PRIYA}])).person_ids == {MARCO, PRIYA}


def test_activity_rows_may_identify_themselves_by_id() -> None:
    log = log_with(pkt("activities", [{"id": ACT}]), pkt("people", [{"id": MARCO}]))
    assert log.activity_ids == {ACT} and log.person_ids == {MARCO}


def test_non_uuid_strings_are_ignored_and_deep_nesting_is_safe() -> None:
    deep: object = {"activity_id": ACT}
    for _ in range(5000):
        deep = {"x": [deep]}
    log = log_with(pkt("activities", [{"activity_id": "not-a-uuid"}, deep]))
    assert log.activity_ids == {ACT}


def test_uuids_under_unrelated_keys_are_not_ids() -> None:
    log = log_with(pkt("people", [{"note": ACT, "person_id": MARCO}]))
    assert log.activity_ids == frozenset() and log.person_ids == {MARCO}


def test_evidence_filter_keeps_only_pulled_ids_and_reports_the_pulls_that_cited_them() -> None:
    log = log_with(pkt("state", [{"value": "x"}], 1),
                   pkt("evidence", [{"claim_id": CLAIM, "activity_id": ACT}], 2),
                   pkt("activities", [{"activity_id": ACT2}], 3))
    refs = (EvidenceRef(activity_id=ACT, claim_id=CLAIM), EvidenceRef(activity_id=ACT2),
            EvidenceRef(activity_id=ACT, claim_id="0c1a0000-0000-4000-8000-000000000999"),
            EvidenceRef(activity_id="0ac70000-0000-4000-8000-000000000999"))
    grounded = log.ground_evidence(refs)
    assert grounded.kept == (refs[0], refs[1]) and grounded.dropped == 2
    assert grounded.access_ids == (2, 3)


def test_evidence_filter_on_an_empty_log_drops_everything() -> None:
    grounded = PullLog().ground_evidence((EvidenceRef(activity_id=ACT),))
    assert grounded.kept == () and grounded.dropped == 1 and grounded.access_ids == ()


def test_named_people_must_come_from_people_or_state() -> None:
    log = log_with(pkt("people", [{"person_id": MARCO}, {"person_id": PRIYA}]))
    log.require_known_people([MARCO, PRIYA], "recipient")
    log.require_known_people([], "decision")
    with pytest.raises(UngroundedProposalError, match="recipient.*" + OTHER):
        log.require_known_people([MARCO, OTHER], "recipient")
