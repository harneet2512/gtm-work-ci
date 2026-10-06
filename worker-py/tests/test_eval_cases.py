"""WP16 (HAR-114) offline GTM eval benchmark (HAR-97 §20), per case: every case is schema-valid, grounded in the
fixture world (verbatim quotes, ingested-before-now evidence, gold-consistent state) and its gold judgments are
internally consistent with contracts/evals/eval_catalog.json (labels, evidence class, blocking rules). Catalog and
coverage checks live in test_eval_catalog.py; loaders and tables in eval_cases_lib.py."""
from __future__ import annotations

import json
import re
from collections import defaultdict
from pathlib import Path

import pytest

from eval_cases_lib import (ACTIVITY_FILE, CASE_FILES, CASE_TYPES, CASE_VALIDATOR, CASES, EVAL_TYPES, IDS,
                            KNOWLEDGE_VALIDATOR, MAX_LINES, OUTPUT_VALIDATOR, PEOPLE_BY_ID, SYN_PREFIX, SYNTHETIC_EVENTS,
                            activities, evidence_refs, person_ids, to_gold)
from test_contracts import EXAMPLES, load_json, validator
from test_fixtures import EVENTS, FIXTURES, body_text, ingested, normalize, ts

DOMAINS = {"acme": "acme.com", "beta": "beta.io", "northstar": "northstar.health"}
ACTING = {"send_email", "schedule_meeting", "share_document", "internal_note"}
ACTION_LABELS = {"NO_ACTION_NEEDED": {"no_action"}, "NO_ACTION": {"no_action"}, "WAIT": {"wait"}, "ACT_NOW": ACTING}


# ---------- per case ----------
@pytest.mark.parametrize("path", CASE_FILES, ids=lambda p: p.stem)
def test_case_validates_against_schema_and_contracts(path: Path) -> None:
    case = load_json(path)
    errors = list(CASE_VALIDATOR.iter_errors(case))
    assert not errors, [f"{list(e.absolute_path)}: {e.message}" for e in errors]
    errors = list(OUTPUT_VALIDATOR.iter_errors(case["candidate_action"]))
    assert not errors, [e.message for e in errors]
    for k in case["context"]["offered_knowledge"]:
        assert not list(KNOWLEDGE_VALIDATOR.iter_errors(k)), k["key"]
    assert case["case_type"] in CASE_TYPES, case["case_type"]
    assert path.stem == case["id"] and path.parent.name == case["case_type"]
    assert path.read_text(encoding="utf-8").count("\n") <= MAX_LINES


@pytest.mark.parametrize("cid", list(CASES))
def test_expected_judgments_are_consistent(cid: str) -> None:
    case = CASES[cid]
    exp = case["expected"]
    types = [e["eval_type"] for e in exp]
    assert len(types) == len(set(types)), "one judgment per eval type"
    assert all(e["verdict"] == "fail" for e in exp if e["blocking"]), "blocking => fail"
    has_fail = any(e["verdict"] == "fail" for e in exp)
    assert has_fail != case["should_pass"], "should_pass <=> no expected fail"
    for e in exp:  # the catalog fixes label set, evidence class and whether the type may block
        entry = EVAL_TYPES[e["eval_type"]]
        assert (e["label"] in entry["labels"]) if entry["labels"] else e["label"] is None, (e["eval_type"], e["label"])
        assert e["evidence_class"] == entry["evidence_class"], (e["eval_type"], e["evidence_class"])
        assert entry["can_block"] or not e["blocking"], f"{e['eval_type']} never blocks: {entry['blocking_rule']}"
    offered = {k["id"] for k in case["context"]["offered_knowledge"]}
    used = set(case["candidate_action"].get("knowledge_refs_used", []))
    assert used <= offered and all(set(e.get("knowledge_refs", [])) <= offered for e in exp)
    if "knowledge_applicability" in types:
        assert offered, "knowledge_applicability needs offered knowledge"
    if case["case_type"] in ("applicable_knowledge", "misleading_similar_knowledge"):
        assert "knowledge_applicability" in types
    if case["case_type"] == "explicit_exception":
        assert "exception_awareness" in types
    best, cand = case["expected_best_action"], case["candidate_action"]
    for e in exp:  # action-shaped labels must agree with the best action (e.g. a WAIT case is never NO_ACTION_NEEDED)
        contested = any(c.get("contested") and c["eval_type"] == e["eval_type"]
                        for rev in case.get("gold_revision", []) for c in rev["changes"])
        if e["label"] in ACTION_LABELS and not contested:  # a contested adjudicated flip keeps the original best action
            assert best["action"] in ACTION_LABELS[e["label"]], (e["eval_type"], e["label"], best["action"])
    if case["should_pass"]:
        assert best["action"] == cand["proposed_action_type"]
    now = ts(case["context"]["now"])
    for until in (cand.get("wait_until"), best.get("wait_until")):
        assert until is None or ts(until) > now
    slices = case["slice_tags"]
    assert slices["candidate"] == ("good" if case["should_pass"] else "flawed")
    assert slices["expected_action"] == best["action"]


@pytest.mark.parametrize("cid", list(CASES))
def test_case_is_grounded_in_the_fixture_world(cid: str) -> None:
    case = CASES[cid]
    based, ctx = case["based_on"], case["context"]
    now, acct = ts(ctx["now"]), based["account"]
    assert based["after_event_file"] in EVENTS
    seen = set(ingested(acct, based["after_event_file"]))
    if based["checkpoint"]:
        gold = load_json(FIXTURES / "gold" / acct / f"cp{based['checkpoint']}.json")
        if based["kind"] == "checkpoint":
            assert gold["after_event_file"] == based["after_event_file"]
    if based["kind"] != "synthetic_variant":
        assert not ctx["trigger"]["synthetic"] and ctx["trigger"]["event_file"] == based["after_event_file"]
    synthetic = {}
    for a in activities(case):
        assert ts(a["occurred_at"]) <= now
        if a["synthetic"]:
            assert a["activity_id"].startswith(SYN_PREFIX) and a["activity_id"] not in ACTIVITY_FILE
            src = SYNTHETIC_EVENTS[a["activity_id"]]  # its stored source message (fixtures/evals/synthetic)
            assert a["text"] in body_text(src), f"not verbatim in its synthetic source: {a['text']!r}"
            assert (a["activity_type"], ts(a["occurred_at"])) == (normalize(src)[1], ts(src["occurred_at"]))
            assert PEOPLE_BY_ID[a["actor_person_id"]]["email"] == src["payload"]["from"]["email"]
            synthetic[a["activity_id"]] = a
            continue
        ev = EVENTS[a["event_file"]]
        assert a["event_file"] in seen, f"{a['event_file']} not ingested by {based['after_event_file']}"
        assert a["activity_id"] == IDS["activities"][a["event_file"]]
        assert a["text"] in (body_text(ev) or ""), f"not verbatim: {a['text']!r}"
        assert (a["activity_type"], ts(a["occurred_at"])) == (normalize(ev)[1], ts(ev["occurred_at"]))
    for ref in evidence_refs(case):
        if ref["activity_id"] in synthetic:
            act = synthetic[ref["activity_id"]]
            assert ref.get("quote", "") in act["text"]
            assert ts(ref.get("occurred_at", act["occurred_at"])) == ts(act["occurred_at"])
            continue
        path = ACTIVITY_FILE.get(ref["activity_id"])
        assert path in seen, f"evidence {ref['activity_id']} ({path}) unknown or not yet ingested"
        assert ref.get("quote", "") in (body_text(EVENTS[path]) or ""), f"not verbatim in {path}: {ref.get('quote')!r}"
        assert ts(ref.get("occurred_at", EVENTS[path]["occurred_at"])) == ts(EVENTS[path]["occurred_at"])
        assert ts(EVENTS[path]["occurred_at"]) <= now


@pytest.mark.parametrize("cid", list(CASES))
def test_people_and_accounts_resolve(cid: str) -> None:
    case = CASES[cid]
    acct = case["based_on"]["account"]
    state = case["context"]["state"]
    assert (state["account_id"], state["opportunity_id"]) == (IDS["accounts"][acct]["account_id"], IDS["accounts"][acct]["opportunity_id"])
    for pid in person_ids(case):
        assert pid in PEOPLE_BY_ID, pid
        assert PEOPLE_BY_ID[pid]["account"] in ("org", acct), f"{PEOPLE_BY_ID[pid]['key']} is not in {acct}"
    for m in state["buying_group"]:
        assert m["display_name"] == PEOPLE_BY_ID[m["person_id"]]["display_name"]
        assert PEOPLE_BY_ID[m["person_id"]]["account"] == acct, "buying group holds customer people only"
    synthetic_people = {pid for pid in person_ids(case) if PEOPLE_BY_ID[pid]["synthetic"]}
    assert not synthetic_people or case["based_on"]["kind"] == "synthetic_variant"


@pytest.mark.parametrize("cid", list(CASES))
def test_state_matches_gold_except_declared_changes(cid: str) -> None:
    case = CASES[cid]
    based = case["based_on"]
    if not based["checkpoint"]:
        return
    exp = load_json(FIXTURES / "gold" / based["account"] / f"cp{based['checkpoint']}.json")["expected"]
    state, changed = case["context"]["state"], set(based["changed_fields"])
    for name, value in state["fields"].items():
        if name not in changed:
            assert to_gold(value) == exp["state_fields"].get(name, "unknown"), name
    if "buying_group" not in changed:
        got = sorted((PEOPLE_BY_ID[m["person_id"]]["key"], tuple(m["roles"]), m["status"]) for m in state["buying_group"])
        assert got == sorted((m["person_key"], tuple(m["roles"]), m["status"]) for m in exp["buying_group"])
    if "coverage_gaps" not in changed:
        assert state["coverage_gaps"] == exp["coverage_gaps"]
    if "conflicts" not in changed:  # ADR-0008: surface the same contradictions as gold (unchanged fields only)
        expected_conflicts = [c for c in exp.get("conflicts", []) if c["field"] not in changed]
        assert [c for c in state.get("conflicts", []) if c["field"] not in changed] == expected_conflicts
    if based["kind"] == "checkpoint":
        rc = case["context"]["recent_changes"]
        assert (rc["material_diff_fields"], rc["signals"]) == (exp["material_diff_fields"], exp["signals"])


@pytest.mark.parametrize("cid", list(CASES))
def test_slice_tags_and_timeline_match_state(cid: str) -> None:
    case = CASES[cid]
    fields, slices, based = case["context"]["state"]["fields"], case["slice_tags"], case["based_on"]
    assert slices["account"] == based["account"] and slices["origin"] == based["kind"]
    assert slices["motion"] == fields["motion"] and slices["champion_status"] == fields["champion_status"]
    assert slices["stage"] == fields["stage"].lower().replace(" ", "_") and slices["risk"] == fields["relationship_risk"]
    assert slices["economic_buyer"] == ("unknown" if fields["economic_buyer"] == "unknown" else "known")
    if based["kind"] == "synthetic_variant":
        return
    inbound, outbound, sender = None, None, None
    for path in ingested(based["account"], based["after_event_file"]):
        ev = EVENTS[path]
        if ev["source_system"] != "email":
            continue
        if ev["payload"]["direction"] == "outbound":
            outbound = ev["occurred_at"]
        else:
            inbound, sender = ev["occurred_at"], ev["payload"]["from"]["email"]
    tl = case["context"]["timeline_facts"]
    assert (tl["last_inbound_at"], tl["last_outbound_at"]) == (inbound, outbound)
    assert PEOPLE_BY_ID[tl["last_inbound_from_person_id"]]["email"] == sender


def test_knowledge_is_identical_wherever_offered() -> None:
    by_id: dict[str, dict] = {}
    for case in CASES.values():
        for k in case["context"]["offered_knowledge"]:
            assert IDS["knowledge"][k["key"]] == k["id"]
            assert by_id.setdefault(k["id"], k) == k, f"{k['key']} drifts between cases"
    # K17 v2 = the contract example (v1) with a reachable signature and new version metadata (ADR-0011).
    v1, v2 = load_json(EXAMPLES / "knowledge.example.json"), by_id[IDS["knowledge"]["K17"]]
    assert v1["id"] == IDS["knowledge"]["K17@v1"] != v2["id"]
    versioned = {"id", "situation_signature", "provenance", "created_at", "last_validated_at"}
    assert {k: v for k, v in v1.items() if k not in versioned} == {k: v for k, v in v2.items() if k not in versioned}
    assert v1["id"] in v2["provenance"]["note"], "v2 names the version it supersedes"


@pytest.mark.parametrize("cid", list(CASES))
def test_knowledge_case_types_are_distinguishable(cid: str) -> None:
    """Signature first (ADR-0011): misleadingly similar knowledge fails its signature; an explicit exception holds it
    and is overridden by an exception the draft must notice."""
    case = CASES[cid]
    labels = {e["eval_type"]: e["label"] for e in case["expected"]}
    if case["case_type"] == "misleading_similar_knowledge":
        assert labels["knowledge_applicability"] == "DOES_NOT_APPLY"
    if case["case_type"] == "explicit_exception":
        assert "exception_awareness" in labels
        assert labels.get("knowledge_applicability", "EXCEPTION_TRIGGERED") == "EXCEPTION_TRIGGERED"


def test_same_candidate_in_same_situation_gets_same_verdicts() -> None:
    """Judgment consistency: identical candidate + trigger + time => overlapping evals agree (knowledge-specific evals excepted)."""
    groups: dict[str, list[dict]] = defaultdict(list)
    for case in CASES.values():
        key = json.dumps([case["candidate_action"], case["context"]["trigger"]["activity_id"], case["context"]["now"]], sort_keys=True)
        groups[key].append(case)
    for cases in groups.values():
        verdicts: dict[str, set] = defaultdict(set)
        for case in cases:
            for e in case["expected"]:
                if e["eval_type"] not in ("knowledge_applicability", "exception_awareness"):
                    verdicts[e["eval_type"]].add((e["verdict"], e["label"]))
        assert all(len(v) == 1 for v in verdicts.values()), ([c["id"] for c in cases], dict(verdicts))



@pytest.mark.parametrize("aid", list(SYNTHETIC_EVENTS))
def test_synthetic_source_messages_are_contract_valid_and_used(aid: str) -> None:
    ev = SYNTHETIC_EVENTS[aid]
    for name, doc in (("source_event", ev), ("source_payloads", ev["payload"])):
        assert not list(validator(name).iter_errors(doc)), name
    assert ev["source_object_id"] == normalize(ev)[0]
    used_by = [c for c in CASES.values() if any(a["activity_id"] == aid for a in activities(c))]
    assert used_by and all(c["based_on"]["kind"] == "synthetic_variant" for c in used_by), "synthetic text only in marked variants"
    accounts = {c["based_on"]["account"] for c in used_by}
    assert len(accounts) == 1, accounts
    domain = DOMAINS[accounts.pop()]
    domains = {e.split("@")[1].rstrip(">") for e in re.findall(r"[\w.+-]+@[\w.>-]+", json.dumps(ev["payload"]))}
    assert domains <= {domain, f"mail.{domain}", "ghostvendor.com"}, domains
