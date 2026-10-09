"""Conformance for the HAR-129 demo objects (ADR-0017): known-bad instances are rejected for the stated reason,
legal lifecycle states are accepted. Mirrors core-go/internal/contracts/demo_objects_test.go."""
from __future__ import annotations

import copy
from collections.abc import Callable, Iterator

import pytest
from jsonschema import ValidationError

from test_contracts import example, validator

Mutation = Callable[[dict], None]


def _walk(doc: object, path: list) -> object:
    for key in path:
        doc = doc[key]  # type: ignore[index]
    return doc


def set_(path: list, value: object) -> Mutation:
    def apply(doc: dict) -> None:
        _walk(doc, path[:-1])[path[-1]] = value  # type: ignore[index]
    return apply


def drop(*path: object) -> Mutation:
    def apply(doc: dict) -> None:
        del _walk(doc, list(path[:-1]))[path[-1]]  # type: ignore[arg-type,index]
    return apply


def all_(*fns: Mutation) -> Mutation:
    def apply(doc: dict) -> None:
        for fn in fns:
            fn(doc)
    return apply


def _leaves(errors: list[ValidationError]) -> Iterator[ValidationError]:
    for err in errors:
        if err.context:
            yield from _leaves(list(err.context))
        yield err


def _rejected_at(schema: str, doc: dict) -> list[list]:
    return [list(e.absolute_path) for e in _leaves(list(validator(schema).iter_errors(doc)))]


def _is_prefix_or_equal(expected: list, found: list) -> bool:
    return found[: len(expected)] == expected


def _event_not_relevant_with_result(doc: dict) -> None:
    doc["items"][0]["verdict"] = "not_relevant"


def _cut(key: str, n: int) -> Mutation:
    def apply(doc: dict) -> None:
        doc[key] = doc[key][:n]
    return apply


def _dup_first(doc: dict) -> None:
    doc["candidates"].append(doc["candidates"][0])


# (schema, mutation, instance path the rejection must point at (prefix), why)
BAD = [
    ("strategy_set", _cut("candidates", 2), ["candidates"], "a demo set has at least 3 candidates"),
    ("strategy_set", _dup_first, ["candidates"], "a demo set has at most 3 candidates"),
    ("strategy_set", set_(["candidates", 2, "ranking"], 2), ["candidates"], "rankings 1, 2 and 3 each appear once"),
    ("strategy_set", set_(["candidates", 1, "preferred_by_agent"], True), ["candidates"], "exactly one candidate is preferred"),
    ("strategy_set", set_(["candidates", 0, "preferred_by_agent"], False), ["candidates"], "somebody is preferred"),
    ("strategy_set", drop("candidates", 0, "eval_bundle_ref"), ["candidates", 0], "a set's candidate carries its bundle"),
    ("strategy_set", drop("candidates", 1, "draft_index"), ["candidates", 1], "a set's candidate carries its draft"),
    ("strategy_set", drop("decision_episode_id"), [], "a set belongs to an episode"),
    ("strategy_candidate", set_(["preferred_by_agent"], True), ["preferred_by_agent"], "ghost prefers only rank 1"),
    ("strategy_candidate", set_(["to"], []), ["to"], "an email candidate addresses someone"),
    ("strategy_candidate", set_(["full_action_artifact", "channel"], "slack"), ["full_action_artifact", "channel"], "email channel"),
    ("strategy_candidate", set_(["evidence_refs"], []), ["evidence_refs"], "a candidate cites evidence"),
    ("strategy_candidate", set_(["to", 0, "role"], "cc"), ["to", 0, "role"], "to recipients have the to role"),
    ("strategy_candidate", set_(["strategy_type"], "Stronger CTA"), ["strategy_type"], "lower snake case"),
    ("strategy_candidate", set_(["score"], 0.9), [], "no extra fields"),
    ("business_intelligence_update", set_(["claims", 0, "evidence_refs"], []), ["claims", 0, "evidence_refs"], "every claim has a trace"),
    ("business_intelligence_update", set_(["claims", 0, "confidence"], 0.9), ["claims", 0], "no bare confidence score on a claim"),
    ("business_intelligence_update", set_(["confidence"], 0.9), [], "no bare confidence score"),
    ("business_intelligence_update", set_(["claims"], []), ["claims"], "at least one change"),
    ("business_intelligence_update", drop("account_map_ref"), [], "the map is reachable"),
    ("demo_manifest", drop("held_out_event", "payload_sha256"), ["held_out_event"], "event N pins its payload"),
    ("demo_manifest", set_(["held_out_event", "payload_sha256"], "ABC"), ["held_out_event", "payload_sha256"], "the pin is lowercase sha256 hex"),
    ("business_intelligence_update", drop("transition"), [], "the update says which transition it saw, or none"),
    ("business_intelligence_update", drop("transition", "touched_by_event"), ["transition"], "the transition says whether the event touched it"),
    ("eval_bundle", _event_not_relevant_with_result, ["items", 0, "result"], "not_relevant has no result"),
    ("eval_bundle", set_(["items", 0, "verdict"], "fail"), ["items", 0, "result", "verdict"], "verdict equals the result's"),
    ("eval_bundle", set_(["items", 2, "verdict"], "pass"), ["items", 2, "result"], "a ran eval has a result"),
    ("eval_bundle", drop("items", 0, "relevance_reason"), ["items", 0], "relevance is stated"),
    ("human_strategy_decision", drop("final_artifact"), [], "send needs the final artifact"),
    ("human_strategy_decision", set_(["human_decision_id"], None), ["human_decision_id"], "send is recorded"),
    ("human_strategy_decision", set_(["send_decision"], "sent"), ["send_decision"], "send vocabulary"),
    ("human_strategy_decision", set_(["send_decision"], "pending"), ["send_decided_at"], "pending has no send time"),
    ("human_strategy_decision", all_(set_(["send_decision"], "pending"), set_(["send_decided_at"], None),
                                    set_(["human_decision_id"], None), set_(["final_artifact"], None)),
     ["final_artifact"], "edits need the final artifact"),
    ("human_strategy_decision", all_(set_(["send_decision"], "discard"), set_(["human_decision_id"], None)),
     ["human_decision_id"], "a discard is recorded"),
    ("judgment_inference", set_(["corrected_statement"], None), ["corrected_statement"], "a correction has its statement"),
    ("judgment_inference", set_(["human_verdict"], "pending"), ["corrected_statement"], "unanswered inference has no correction"),
    ("judgment_inference", set_(["human_verdict"], "confirmed"), ["corrected_statement"], "confirmed has nothing to correct"),
    ("judgment_inference", set_(["evidence", "evidence_refs"], []), ["evidence", "evidence_refs"], "evidence is required"),
    ("judgment_inference", set_(["agreement"], "same"), ["agreement"], "agreement vocabulary"),
    ("judgment_inference", set_(["human_verdict"], "approved"), ["human_verdict"], "verdict vocabulary"),
    ("decision_episode", drop("status"), [], "an episode has a status"),
    ("decision_episode", set_(["status"], "sent"), ["status"], "status vocabulary"),
    ("decision_episode", all_(set_(["status"], "decided"), drop("human_decision_id")), [], "decided has its decision"),
    ("decision_episode", set_(["status"], "chosen"), [], "undecided episodes have no human action"),
    ("decision_episode", drop("account_change_id"), [], "a replay episode points at its change"),
    ("decision_episode", set_(["learning_scope"], "global"), ["learning_scope"], "learning scope vocabulary"),
    ("account_change", set_(["evidence_refs"], []), ["evidence_refs"], "a material change is traceable"),
    ("strategy_set", set_(["state_ref", "version"], 0), ["state_ref", "version"], "an opportunity state starts at version 1"),
    ("account_change", drop("graph_diff_ref"), [], "a change names its graph diff"),
    ("account_change", set_(["previous_state_ref", "opportunity_id"], "0c0f0000-0000-4000-8000-000000000004"),
     ["previous_state_ref", "opportunity_id"], "a change's snapshots are account-level"),
    ("demo_manifest", set_(["events"], []), ["events"], "a manifest has history"),
    ("demo_manifest", drop("held_out_event"), [], "a manifest names the held-out event"),
    ("demo_manifest", set_(["held_out_event", "state_after"], {"account_id": "x", "version": 1}), ["held_out_event"], "event N carries no snapshot"),
    ("demo_manifest", set_(["held_out_event", "is_material"], True), ["held_out_event"], "event N carries no material flag"),
    ("demo_manifest", set_(["events", 0, "state_diff_id"], None), ["events", 0, "state_diff_id"], "a material event has its diff"),
    ("demo_manifest", set_(["events", 0, "material_dimensions"], []), ["events", 0, "material_dimensions"], "names what changed"),
    ("demo_manifest", set_(["events", 0, "material_dimensions"], ["vibes"]), ["events", 0, "material_dimensions", 0], "dimension vocabulary"),
    ("demo_manifest", set_(["why_selected"], ""), ["why_selected"], "why is stated"),
    ("demo_manifest", set_(["content_sha256"], "abc"), ["content_sha256"], "sha256 hex"),
    ("held_out_event", set_(["provenance", "origin"], "base"), ["provenance", "origin"], "origin vocabulary"),
    ("held_out_event", all_(set_(["provenance", "origin"], "synthetic"), set_(["provenance", "provenance"], "crmarena-pro:b2b")),
     ["provenance", "provenance"], "a synthetic event carries synthetic:vN"),
    ("held_out_event", set_(["provenance", "provenance"], "synthetic:v1"), ["provenance", "origin"], "synthetic provenance only on synthetic"),
    ("held_out_event", set_(["replay_position"], 0), ["replay_position"], "replay position is 1-based"),
    ("agent_run_draft", all_(set_(["source"], "strategy_generator"), set_(["revision_feedback"], [{"eval_result_id": "0e1a0000-0000-4000-8000-000000000901", "instruction": "x"}])),
     ["revision_feedback"], "a strategy draft is not a revision"),
    ("agent_run_draft", set_(["draft_index"], 1), ["draft_index"], "a revision draft is draft 2 or later"),
    ("strategy_candidate", set_(["action_class"], "WAIT"), ["action_type"], "WAIT is carried out as wait"),
    ("strategy_candidate", set_(["action_class"], "PUSH"), ["action_class"], "decision class vocabulary"),
    ("strategy_candidate", drop("five_questions"), [], "the five questions are required"),
    ("strategy_candidate", set_(["five_questions", "why_next_action"], ""), ["five_questions", "why_next_action"], "every question is answered"),
    ("strategy_candidate", set_(["five_questions", "extra"], "x"), ["five_questions"], "exactly five questions"),
    ("eval_bundle", set_(["candidate_policy"], {"transition_status": "CANDIDATE", "status": "restricted", "reasons": []}),
     ["candidate_policy"], "a restriction says why"),
    ("eval_bundle", set_(["candidate_policy"], {"transition_status": "CONFIRMED", "status": "allowed", "reasons": ["pricing_push"]}),
     ["candidate_policy"], "an allowed candidate has no reasons"),
    ("replay_world", set_(["current_state_refs"], []), ["current_state_refs"], "a world has current state"),
    ("replay_world", set_(["entities", "account_ids"], []), ["entities", "account_ids"], "a world has an account"),
    ("replay_world", set_(["provenance_manifest_ref", "content_sha256"], "short"), ["provenance_manifest_ref", "content_sha256"], "pinned by hash"),
]

GOOD = [
    ("strategy_candidate", all_(set_(["action_class"], "EXPANSION_MOTION"), set_(["action_type"], "schedule_meeting")),
     "an expansion motion can be a meeting"),
    ("strategy_candidate", all_(set_(["action_class"], "ASK_RESEARCH"), set_(["action_type"], "internal_note"), set_(["to"], []),
                               set_(["full_action_artifact", "channel"], "slack")), "ask the internal owner"),
    ("eval_bundle", set_(["candidate_policy"], {"transition_status": "CANDIDATE", "status": "restricted",
                                                  "reasons": ["expansion_motion"], "requires_human_review": True}),
     "a restricted candidate"),
    ("eval_bundle", all_(set_(["candidate_policy"], None), set_(["selected_eval_suite"], None)), "no transition, no suite"),
    ("agent_run_draft", all_(set_(["source"], "strategy_generator"), set_(["draft_index"], 3), set_(["revision_feedback"], [])),
     "a strategy candidate draft at any index"),
    ("strategy_candidate", all_(drop("draft_index"), drop("eval_bundle_ref")), "a worker candidate has no draft or bundle yet"),
    ("strategy_candidate", all_(set_(["action_type"], "wait"), set_(["action_class"], "WAIT"), set_(["to"], []), set_(["full_action_artifact", "channel"], "none"),
                               set_(["subject"], None), set_(["full_action_artifact", "subject"], None)), "a wait strategy"),
    ("human_strategy_decision", all_(set_(["send_decision"], "pending"), set_(["send_decided_at"], None), set_(["human_decision_id"], None),
                                    drop("final_to"), drop("final_cc"), drop("final_artifact"), set_(["edits"], [])), "chosen, not sent"),
    ("human_strategy_decision", set_(["edits"], []), "an unedited send"),
    ("human_strategy_decision", all_(set_(["send_decision"], "discard"), set_(["edits"], []), drop("final_to"), drop("final_artifact")), "a discard"),
    ("judgment_inference", all_(set_(["human_verdict"], "pending"), set_(["corrected_statement"], None), set_(["verdict_at"], None),
                               set_(["verdict_surface"], None), set_(["human_note"], None)), "awaiting the human"),
    ("judgment_inference", all_(set_(["human_verdict"], "confirmed"), set_(["corrected_statement"], None), set_(["agreement"], "agreed")),
     "confirmed, human agreed with ghost"),
    ("decision_episode", all_(set_(["status"], "awaiting_choice"), drop("final_draft_index"), drop("human_decision_id"), drop("human_action"),
                             drop("human_final_artifact"), drop("human_delta_id")), "waiting for the choice"),
    ("decision_episode", all_(drop("held_out_event_id"), drop("account_change_id"), drop("business_intelligence_update_id")), "a live episode"),
    ("decision_episode", set_(["status"], "decided"), "a legacy decided episode"),
    ("eval_bundle", all_(set_(["items", 0, "verdict"], "abstain"), set_(["items", 0, "result", "verdict"], "abstain")), "an abstaining eval"),
    ("replay_world", all_(set_(["event_cursor", "replay_position"], 2), set_(["event_cursor", "held_out_event_id"], None)), "after Play"),
    ("business_intelligence_update", set_(["knowledge_refs"], []), "no applicable knowledge"),
    ("business_intelligence_update", set_(["transition"], None), "no transition"),
    ("business_intelligence_update", set_(["transition", "touched_by_event"], False), "an open transition the event left unchanged"),
    ("demo_manifest", all_(set_(["events", 0, "is_material"], False), set_(["events", 0, "state_diff_id"], None),
                          set_(["events", 0, "material_dimensions"], [])), "a quiet event"),
    ("account_change", all_(set_(["material_change"], False), set_(["evidence_refs"], [])), "an immaterial change"),
]


@pytest.mark.parametrize(("schema", "mutate", "path", "why"), BAD, ids=[b[3] for b in BAD])
def test_bad_demo_instance_is_rejected_for_the_right_reason(schema: str, mutate: Mutation, path: list, why: str) -> None:
    doc = copy.deepcopy(example(schema))
    mutate(doc)
    paths = _rejected_at(schema, doc)
    assert paths, f"{schema} accepted an instance that should fail: {why}"
    assert any(_is_prefix_or_equal(path, found) or _is_prefix_or_equal(found, path) for found in paths), (why, path, paths)


@pytest.mark.parametrize(("schema", "mutate", "why"), GOOD, ids=[g[2] for g in GOOD])
def test_legal_demo_state_is_accepted(schema: str, mutate: Mutation, why: str) -> None:
    doc = copy.deepcopy(example(schema))
    mutate(doc)
    errors = _rejected_at(schema, doc)
    assert not errors, (why, errors)


def test_demo_example_ids_agree_across_the_objects() -> None:
    """The examples describe one episode: the references between the objects resolve."""
    episode, strategy_set = example("decision_episode"), example("strategy_set")
    decision, inference = example("human_strategy_decision"), example("judgment_inference")
    change, bi = example("account_change"), example("business_intelligence_update")
    candidates = {c["candidate_id"]: c for c in strategy_set["candidates"]}
    bundle = example("eval_bundle")
    assert strategy_set["decision_episode_id"] == decision["decision_episode_id"] == inference["decision_episode_id"] == episode["id"]
    assert decision["strategy_set_id"] == strategy_set["id"]
    assert decision["selected_candidate_id"] in candidates
    preferred = [c["candidate_id"] for c in strategy_set["candidates"] if c["preferred_by_agent"]]
    assert preferred == [decision["original_agent_preference"]] == [inference["agent_preference"]]
    assert inference["human_choice"] == decision["selected_candidate_id"]
    assert inference["human_strategy_decision_id"] == decision["id"]
    assert (inference["agreement"] == "agreed") == (inference["agent_preference"] == inference["human_choice"])
    assert episode["account_change_id"] == change["id"] == bi["account_change_id"]
    assert episode["business_intelligence_update_id"] == bi["id"]
    assert episode["state_diff_id"] == change["state_diff_id"] == strategy_set["state_diff_id"]
    assert bundle["strategy_candidate_id"] in candidates
    assert candidates[bundle["strategy_candidate_id"]]["eval_bundle_ref"] == bundle["id"]
    assert candidates[bundle["strategy_candidate_id"]]["draft_index"] == bundle["draft_index"]
    assert {c["draft_index"] for c in candidates.values()} == {1, 2, 3}


def test_candidate_subject_equals_the_artifact_subject_in_every_example() -> None:
    for c in example("strategy_set")["candidates"] + [example("strategy_candidate")]:
        assert c["subject"] == c["full_action_artifact"].get("subject")


def test_manifest_is_chronological_and_ends_with_the_held_out_event() -> None:
    manifest = example("demo_manifest")
    events = [e["event"] for e in manifest["events"]] + [manifest["held_out_event"]]
    assert [e["replay_position"] for e in events] == list(range(1, len(events) + 1))
    assert [e["occurred_at"] for e in events] == sorted(e["occurred_at"] for e in events)
    assert manifest["held_out_event"] == example("held_out_event")
    assert example("decision_episode")["held_out_event_id"] == manifest["held_out_event"]["event_id"]


def test_every_bundle_result_belongs_to_the_bundles_draft() -> None:
    bundle = example("eval_bundle")
    for item in bundle["items"]:
        if item["result"] is not None:
            assert item["result"]["agent_run_id"] == bundle["agent_run_id"]
            assert item["result"]["draft_index"] == bundle["draft_index"]
