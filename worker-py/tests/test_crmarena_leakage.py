"""HAR-114 gold v2, part C: no CRMArena case context contains a post-decision-time fact, and the generator that makes the
cases is correct (dating, excerpts, ids). The structural leakage tests run on the committed cases (CI); the record-level ones
rebuild from the git-ignored snapshot and are skipped where it is absent. The generator tests run on fixtures/crmarena_sample."""
from __future__ import annotations

import hashlib
import json
import re
from pathlib import Path

import pytest

from crmarena_gold_lib import CR_CASES, ROOT
from crmarena_gold import build, source
from crmarena_gold.knowledge import KNOWLEDGE
from crmarena_gold.source import Event, Snapshot, excerpt, iso, uid, vendor_address

MANIFEST = json.loads((ROOT / "fixtures" / "evals" / "crmarena" / "manifest.json").read_text(encoding="utf-8"))
SNAPSHOT = ROOT / "data" / "crmarena_b2b"
SAMPLE = ROOT / "fixtures" / "crmarena_sample"
needs_snapshot = pytest.mark.skipif(not (SNAPSHOT / "manifest.json").exists(), reason="data/crmarena_b2b is git-ignored; export it first")


def activities(case: dict) -> list[dict]:
    return [case["context"]["trigger"], *case["context"]["supporting_activities"]]


# ---------- structural leakage (committed cases only) ----------
@pytest.mark.parametrize("cid", list(CR_CASES))
def test_no_activity_is_dated_after_the_decision_time(cid: str) -> None:
    case = CR_CASES[cid]
    now, ctx = case["context"]["now"], case["context"]
    assert case["based_on"]["decision_at"] == now
    assert all(a["occurred_at"] <= now for a in activities(case)), "an activity is dated after now"
    trig = ctx["trigger"]
    assert trig["occurred_at"][:10] == now[:10] and trig["occurred_at"] <= now
    ids = [a["event_file"] for a in activities(case)]
    assert len(ids) == len(set(ids)) and case["based_on"]["events_before_decision"] >= len(ids)
    facts = ctx["timeline_facts"]
    assert all(v is None or v <= now for v in (facts["last_inbound_at"], facts["last_outbound_at"]))
    assert all(m["last_engaged_at"] is None or m["last_engaged_at"] <= now for m in ctx["state"]["buying_group"])
    assert all(k["created_at"] <= now for k in ctx["offered_knowledge"]), "knowledge is dated before the decision"


@pytest.mark.parametrize("cid", list(CR_CASES))
def test_candidate_cites_only_verbatim_evidence_that_existed_at_decision_time(cid: str) -> None:
    """Runs in CI: every quote is a substring of an activity text shown in the context, and that text matches its manifest hash."""
    case = CR_CASES[cid]
    now = case["context"]["now"]
    shown = {a["activity_id"]: a for a in activities(case)}
    for ref in case["candidate_action"]["evidence_refs"]:
        assert ref["occurred_at"] <= now and ref["activity_id"] in shown
        assert ref.get("quote", "") in shown[ref["activity_id"]]["text"], f"quote not verbatim in the excerpt: {ref['quote'][:60]!r}"
    evidence = MANIFEST["evidence"][cid]
    assert set(evidence) == {a["event_file"] for a in activities(case)}
    for a in activities(case):
        assert hashlib.sha256(a["text"].encode("utf-8")).hexdigest() == evidence[a["event_file"]]["excerpt_sha256"], "excerpt changed after the build"


@pytest.mark.parametrize("cid", list(CR_CASES))
def test_outcome_fields_the_loader_dates_at_the_deals_last_event_are_not_in_the_state(cid: str) -> None:
    case = CR_CASES[cid]
    fields, prov = case["context"]["state"]["fields"], case["context"]["state_provenance"]
    assert fields["stage"] == "unknown" or "stage" in prov, "a stage needs a pre-decision source"
    text = json.dumps(case["context"], ensure_ascii=False)
    assert not re.search(r"\b(?:Closed Won|Closed Lost|IsWon|CloseDate|StageName|Amount)\b", text)


def test_every_case_is_a_current_deal_of_the_frozen_split() -> None:
    split = json.loads((ROOT / "bench" / "data" / "deal_split.json").read_text(encoding="utf-8"))
    current, previous = set(split["current_deal_ids"]), set(split["previous_deal_ids"])
    for case in CR_CASES.values():
        deal = case["based_on"]["deal_id"]
        assert deal in current and deal not in previous and case["based_on"]["cutoff"] == split["cutoff"]


# ---------- record-level leakage (needs the snapshot) ----------
@needs_snapshot
@pytest.mark.parametrize("cid", list(CR_CASES))
def test_context_matches_the_record_and_leaks_nothing_later(cid: str) -> None:
    case, snap = CR_CASES[cid], Snapshot()
    deal, now = case["based_on"]["deal_id"], case["context"]["now"]
    events = {e.sf_id: e for e in snap.deal_events(deal)}
    for a in activities(case):
        event = events[a["event_file"].rsplit(":", 1)[1]]
        assert event.occurred_at == a["occurred_at"] <= now and a["text"] in event.body, "not the record, or not verbatim"
        assert hashlib.sha256(event.body.encode("utf-8")).hexdigest() == MANIFEST["evidence"][cid][a["event_file"]]["body_sha256"]
    blob = json.dumps(case["context"], ensure_ascii=False)
    later = [e for e in events.values() if e.occurred_at > now]
    assert not [e.sf_id for e in later if e.sf_id in blob], "a later record id is in the context"
    for e in later:
        probe = e.body.strip()[:60]
        assert len(probe) < 20 or probe not in blob, f"text of a later {e.kind} leaks into the context"
    # The loader dates Amount (and StageName, CloseDate, quote Status) at the deal's LAST event, so none may be in a context.
    # CloseDate is not probed: a date the buyer wrote ('by Friday') can coincide with it without being a leak.
    amount = snap.opportunities[deal]["Amount"]
    assert str(amount) not in blob and f"{amount:.2f}" not in blob, "the deal amount leaks into the context"
    assert case["based_on"]["events_before_decision"] == len([e for e in events.values() if e.occurred_at <= now])


@needs_snapshot
def test_committed_cases_rebuild_from_the_snapshot_or_the_snapshot_differs() -> None:
    from crmarena_gold import cli

    files, _ = cli.cases_from_snapshot()
    manifest = json.loads((ROOT / "fixtures" / "evals" / "crmarena" / "manifest.json").read_text(encoding="utf-8"))
    if Snapshot().file_hashes["EmailMessage.json"] != manifest["snapshot_file_sha256"]["EmailMessage"]:
        pytest.skip("the local export differs from the snapshot the cases were cut from (the org is publicly writable)")
    assert {k: v for k, v in files.items()} == {k: (ROOT / "fixtures" / "evals" / "crmarena" / k).read_text(encoding="utf-8") for k in files}


@needs_snapshot
def test_knowledge_counts_match_the_previous_deals() -> None:
    from crmarena_gold import knowledge_stats

    got = knowledge_stats.stats(Snapshot())
    for k in KNOWLEDGE.values():
        assert (k["counts"]["decisions"], k["counts"]["outcomes_advanced"]) == (got[k["key"]]["decisions"], got[k["key"]]["outcomes_advanced"])


# ---------- generator (fixtures/crmarena_sample) ----------
class SampleSnapshot(Snapshot):
    manifest_sha256 = "sample"


def sample_spec(snap: Snapshot) -> dict:
    """A minimal spec on the first current sample deal that has an inbound email after the cutoff."""
    for deal in snap.current_deals():
        if deal not in snap.opportunities or deal not in snap.events_by_deal:
            continue
        inbound = [e for e in snap.deal_events(deal) if e.kind == "email" and e.inbound and e.occurred_at[:10] >= "2023-11-01"]
        if inbound:
            return {"id": "cr_sample", "type": "no_action", "title": "t", "deal": deal, "trigger": inbound[-1].sf_id, "difficulty": "easy",
                    "state": {}, "recent": ("s", [], ["customer_replied"]), "evals": ["next_action_quality"], "intent": {"should_pass": True},
                    "cand": {"action": "wait", "next_step": "wait", "reason": "r", "wait_until": "2999-01-01T00:00:00Z"},
                    "best": {"action": "wait", "why": "w"}}
    pytest.skip("the sample has no current deal with a late inbound email")


def test_generator_builds_a_case_from_the_sample_with_nothing_after_now() -> None:
    snap = SampleSnapshot(SAMPLE)
    spec = sample_spec(snap)
    case = build.build_skeleton(snap, spec)
    now = case["context"]["now"]
    deal_events = snap.deal_events(spec["deal"])
    assert all(a["occurred_at"] <= now for a in activities(case))
    assert case["based_on"]["events_before_decision"] == len([e for e in deal_events if e.occurred_at <= now])
    assert case["context"]["trigger"]["event_file"].endswith(spec["trigger"])
    assert build.build_skeleton(snap, spec) == case, "deterministic"
    later = [e for e in deal_events if e.occurred_at > now]
    blob = json.dumps(case["context"], ensure_ascii=False)
    assert all(e.sf_id not in blob for e in later)


def test_generator_rejects_a_quote_that_is_not_verbatim_and_evidence_after_now() -> None:
    snap = SampleSnapshot(SAMPLE)
    spec = sample_spec(snap)
    bad = {**spec, "cand": {**spec["cand"], "refs": [(spec["trigger"], "this sentence is not in the email")]}}
    with pytest.raises(build.SpecError):
        build.build_skeleton(snap, bad)
    late = [e for e in snap.deal_events(spec["deal"]) if e.occurred_at > build.decision_time(snap.event(spec["deal"], spec["trigger"]), None)]
    if late:
        after = {**spec, "cand": {**spec["cand"], "refs": [(late[0].sf_id, "")]}}
        with pytest.raises(build.SpecError):
            build.build_skeleton(snap, after)


def test_source_helpers() -> None:
    assert iso("2024-03-22T10:00:00.000+0000") == "2024-03-22T10:00:00Z" and iso("2024-03-22") == "2024-03-22T00:00:00Z"
    assert uid("person", "003A") == uid("person", "003A") != uid("person", "003B") and len(uid("email", "02sX")) == 36
    assert vendor_address({"Email": "Jo.Smith@techagents.com"}) == "jo.smith@vendor.example"
    body = "First paragraph.\n\nSecond paragraph that is long enough to be cut somewhere in the middle of the text."
    assert excerpt(body, 1000) == body and excerpt(body, 40) == "First paragraph." and excerpt(body, 40) in body
    reply = Event("email", "02sX", "006", "2024-01-01T00:00:00Z", "Re: hi", "b", inbound=True)
    assert (reply.activity_type, Event("task", "00T", "006", "d", "s", "b").activity_type) == ("EmailReply", "CRMTaskLogged")
    assert Event("contract", "800", "006", "d", "s", "b").activity_type == "ContractSigned" and source.is_rep("x@techagents.com")


def test_sample_contract_is_dated_when_both_parties_signed() -> None:
    snap = SampleSnapshot(SAMPLE)
    contracts = {c["Id"]: c for c in snap.load("Contract")}
    for deal, events in snap.events_by_deal.items():
        for e in (e for e in events if e.kind == "contract"):
            signed = [d for d in (contracts[e.sf_id].get("CustomerSignedDate"), contracts[e.sf_id].get("CompanySignedDate")) if d]
            assert e.occurred_at == iso(max(signed)) if signed else True
