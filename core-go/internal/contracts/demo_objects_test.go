package contracts

import (
	"testing"
)

// Conformance for the HAR-129 demo objects (ADR-0017): known-bad instances are rejected and the legal
// states the lifecycle passes through are accepted. The same cases run in Python (test_demo_contracts.py).

// at walks keys (string) and indexes (int) into a decoded example.
func at(m any, path ...any) any {
	cur := m
	for _, p := range path {
		switch k := p.(type) {
		case string:
			cur = cur.(map[string]any)[k]
		case int:
			cur = cur.([]any)[k]
		}
	}
	return cur
}

// put sets the value at path (the last element is the key being set).
func put(m map[string]any, path []any, v any) {
	parent := at(m, path[:len(path)-1]...)
	switch k := path[len(path)-1].(type) {
	case string:
		parent.(map[string]any)[k] = v
	case int:
		parent.([]any)[k] = v
	}
}

func set(path []any, v any) func(map[string]any) { return func(m map[string]any) { put(m, path, v) } }

func drop(path ...any) func(map[string]any) {
	return func(m map[string]any) {
		parent := at(m, path[:len(path)-1]...)
		delete(parent.(map[string]any), path[len(path)-1].(string))
	}
}

func all(fns ...func(map[string]any)) func(map[string]any) {
	return func(m map[string]any) {
		for _, f := range fns {
			f(m)
		}
	}
}

type demoCase struct {
	name, schema string
	fn           func(map[string]any)
}

func p(parts ...any) []any { return parts }

var demoBad = []demoCase{
	{"a strategy set has at least 3 candidates", "strategy_set", func(m map[string]any) {
		m["candidates"] = m["candidates"].([]any)[:2]
	}},
	{"a strategy set has at most 3 candidates", "strategy_set", func(m map[string]any) {
		c := m["candidates"].([]any)
		m["candidates"] = append(c, c[0])
	}},
	{"rankings 1, 2 and 3 each appear once", "strategy_set", set(p("candidates", 2, "ranking"), 2.0)},
	{"exactly one candidate is preferred", "strategy_set", set(p("candidates", 1, "preferred_by_agent"), true)},
	{"nobody preferred", "strategy_set", set(p("candidates", 0, "preferred_by_agent"), false)},
	{"a set's candidate carries its eval bundle", "strategy_set", drop("candidates", 0, "eval_bundle_ref")},
	{"a set's candidate carries its draft index", "strategy_set", drop("candidates", 1, "draft_index")},
	{"a set belongs to an episode", "strategy_set", drop("decision_episode_id")},
	{"ghost prefers only its first-ranked candidate", "strategy_candidate", set(p("preferred_by_agent"), true)},
	{"an email candidate addresses someone", "strategy_candidate", set(p("to"), []any{})},
	{"an email candidate uses the email channel", "strategy_candidate", set(p("full_action_artifact", "channel"), "slack")},
	{"a candidate cites evidence", "strategy_candidate", set(p("evidence_refs"), []any{})},
	{"to recipients have the to role", "strategy_candidate", set(p("to", 0, "role"), "cc")},
	{"strategy type is lower snake case", "strategy_candidate", set(p("strategy_type"), "Stronger CTA")},
	{"unknown candidate fields are rejected", "strategy_candidate", set(p("score"), 0.9)},
	{"every BI claim carries evidence", "business_intelligence_update", set(p("claims", 0, "evidence_refs"), []any{})},
	{"a BI claim has no confidence score", "business_intelligence_update", set(p("claims", 0, "confidence"), 0.9)},
	{"a BI update has no confidence score", "business_intelligence_update", set(p("confidence"), 0.9)},
	{"a BI update reports at least one change", "business_intelligence_update", set(p("claims"), []any{})},
	{"a BI update points at the map", "business_intelligence_update", drop("account_map_ref")},
	{"event N pins its payload", "demo_manifest", drop("held_out_event", "payload_sha256")},
	{"the payload pin is lowercase sha256 hex", "demo_manifest", set(p("held_out_event", "payload_sha256"), "ABC")},
	{"a BI update says which transition it saw, or none", "business_intelligence_update", drop("transition")},
	{"a BI transition says whether the event touched it", "business_intelligence_update", drop("transition", "touched_by_event")},
	{"a not-relevant eval has no result", "eval_bundle", func(m map[string]any) {
		put(m, p("items", 0, "verdict"), "not_relevant")
	}},
	{"an item verdict equals its result's", "eval_bundle", set(p("items", 0, "verdict"), "fail")},
	{"a ran eval has a result", "eval_bundle", set(p("items", 2, "verdict"), "pass")},
	{"a selected eval states its relevance", "eval_bundle", drop("items", 0, "relevance_reason")},
	{"send needs the final artifact", "human_strategy_decision", drop("final_artifact")},
	{"send is recorded as a human decision", "human_strategy_decision", set(p("human_decision_id"), nil)},
	{"send vocabulary", "human_strategy_decision", set(p("send_decision"), "sent")},
	{"a pending choice has no human decision yet", "human_strategy_decision", set(p("send_decision"), "pending")},
	{"edits need the final artifact", "human_strategy_decision", all(
		set(p("send_decision"), "pending"), set(p("send_decided_at"), nil), set(p("human_decision_id"), nil),
		set(p("final_artifact"), nil))},
	{"a discard is recorded as a human decision", "human_strategy_decision", all(
		set(p("send_decision"), "discard"), set(p("human_decision_id"), nil))},
	{"a correction carries the corrected statement", "judgment_inference", set(p("corrected_statement"), nil)},
	{"an unanswered inference has no verdict time", "judgment_inference", set(p("human_verdict"), "pending")},
	{"a confirmed inference has nothing to correct", "judgment_inference", set(p("human_verdict"), "confirmed")},
	{"a no-learning inference has nothing to correct", "judgment_inference", set(p("human_verdict"), "no_learning")},
	{"a no-learning inference is dated", "judgment_inference", all(
		set(p("human_verdict"), "no_learning"), set(p("corrected_statement"), nil), set(p("verdict_at"), nil))},
	{"an inference needs account-state evidence", "judgment_inference", set(p("evidence", "evidence_refs"), []any{})},
	{"agreement vocabulary", "judgment_inference", set(p("agreement"), "same")},
	{"verdict vocabulary", "judgment_inference", set(p("human_verdict"), "approved")},
	{"an episode has a status", "decision_episode", drop("status")},
	{"status vocabulary", "decision_episode", set(p("status"), "sent")},
	{"a decided episode has its decision", "decision_episode", all(set(p("status"), "decided"), drop("human_decision_id"))},
	{"an undecided episode has no human action", "decision_episode", set(p("status"), "chosen")},
	{"a replay episode points at its change", "decision_episode", drop("account_change_id")},
	{"learning scope vocabulary", "decision_episode", set(p("learning_scope"), "global")},
	{"a material change is traceable", "account_change", set(p("evidence_refs"), []any{})},
	{"a change names its graph diff", "account_change", drop("graph_diff_ref")},
	{"a manifest has history", "demo_manifest", set(p("events"), []any{})},
	{"a manifest names the held-out event", "demo_manifest", drop("held_out_event")},
	{"event N carries no snapshot", "demo_manifest", set(p("held_out_event", "state_after"), map[string]any{"account_id": "x", "version": 1.0})},
	{"event N carries no material flag", "demo_manifest", set(p("held_out_event", "is_material"), true)},
	{"a change's snapshots are account-level", "account_change", set(p("previous_state_ref", "opportunity_id"), "0c0f0000-0000-4000-8000-000000000004")},
	{"an opportunity state starts at version 1", "strategy_set", set(p("state_ref", "version"), 0.0)},
	{"a strategy draft is not a revision", "agent_run_draft", all(set(p("source"), "strategy_generator"), set(p("draft_index"), 3.0))},
	{"a revision draft is draft 2 or later", "agent_run_draft", set(p("draft_index"), 1.0)},
	{"a material event points at its diff", "demo_manifest", set(p("events", 0, "state_diff_id"), nil)},
	{"a material event names what changed", "demo_manifest", set(p("events", 0, "material_dimensions"), []any{})},
	{"change dimension vocabulary", "demo_manifest", set(p("events", 0, "material_dimensions"), []any{"vibes"})},
	{"a manifest says why it was selected", "demo_manifest", set(p("why_selected"), "")},
	{"the content hash is sha256 hex", "demo_manifest", set(p("content_sha256"), "abc")},
	{"origin vocabulary of a replay event", "held_out_event", set(p("provenance", "origin"), "base")},
	{"a synthetic event carries a synthetic provenance", "held_out_event", all(set(p("provenance", "origin"), "synthetic"), set(p("provenance", "provenance"), "crmarena-pro:b2b"))},
	{"a synthetic provenance only on a synthetic event", "held_out_event", set(p("provenance", "provenance"), "synthetic:v1")},
	{"replay position is 1-based", "held_out_event", set(p("replay_position"), 0.0)},
	{"a decision class is carried out by its own action", "strategy_candidate", set(p("action_class"), "WAIT")},
	{"an unknown decision class", "strategy_candidate", set(p("action_class"), "PUSH")},
	{"the five questions are required", "strategy_candidate", drop("five_questions")},
	{"every one of the five questions is answered", "strategy_candidate", set(p("five_questions", "why_next_action"), "")},
	{"exactly five questions", "strategy_candidate", set(p("five_questions", "extra"), "x")},
	{"a restriction says why", "eval_bundle", set(p("candidate_policy"), map[string]any{"transition_status": "CANDIDATE", "status": "restricted", "reasons": []any{}})},
	{"an allowed candidate has no reasons", "eval_bundle", set(p("candidate_policy"), map[string]any{"transition_status": "CONFIRMED", "status": "allowed", "reasons": []any{"pricing_push"}})},
	{"a world has current state", "replay_world", set(p("current_state_refs"), []any{})},
	{"a world has an account", "replay_world", set(p("entities", "account_ids"), []any{})},
	{"the provenance manifest is pinned by hash", "replay_world", set(p("provenance_manifest_ref", "content_sha256"), "short")},
}

// demoGood are legal states of the lifecycle that must stay accepted.
var demoGood = []demoCase{
	{"a worker candidate has no draft or bundle yet", "strategy_candidate", all(drop("draft_index"), drop("eval_bundle_ref"))},
	{"an expansion motion can be a meeting", "strategy_candidate", all(set(p("action_class"), "EXPANSION_MOTION"), set(p("action_type"), "schedule_meeting"))},
	{"a restricted candidate says why and needs review", "eval_bundle", set(p("candidate_policy"), map[string]any{"transition_status": "CANDIDATE", "status": "restricted", "reasons": []any{"expansion_motion"}, "requires_human_review": true})},
	{"no transition, no suite and no policy", "eval_bundle", all(set(p("candidate_policy"), nil), set(p("selected_eval_suite"), nil))},
	{"a wait strategy needs no recipient", "strategy_candidate", all(
		set(p("action_type"), "wait"), set(p("action_class"), "WAIT"), set(p("to"), []any{}), set(p("full_action_artifact", "channel"), "none"),
		set(p("subject"), nil), set(p("full_action_artifact", "subject"), nil))},
	{"a choice not yet sent", "human_strategy_decision", all(
		set(p("send_decision"), "pending"), set(p("send_decided_at"), nil), set(p("human_decision_id"), nil),
		drop("final_to"), drop("final_cc"), drop("final_artifact"), set(p("edits"), []any{}))},
	{"an unedited send", "human_strategy_decision", set(p("edits"), []any{})},
	{"a discard", "human_strategy_decision", all(set(p("send_decision"), "discard"), set(p("edits"), []any{}),
		drop("final_to"), drop("final_artifact"))},
	{"an inference awaiting the human", "judgment_inference", all(
		set(p("human_verdict"), "pending"), set(p("corrected_statement"), nil), set(p("verdict_at"), nil),
		set(p("verdict_surface"), nil), set(p("human_note"), nil))},
	{"a confirmed inference where the human agreed with ghost", "judgment_inference", all(
		set(p("human_verdict"), "confirmed"), set(p("corrected_statement"), nil), set(p("agreement"), "agreed"))},
	{"an inference where the human declined learning", "judgment_inference", all(
		set(p("human_verdict"), "no_learning"), set(p("corrected_statement"), nil))},
	{"an episode that is waiting for the choice", "decision_episode", all(
		set(p("status"), "awaiting_choice"), drop("final_draft_index"), drop("human_decision_id"),
		drop("human_action"), drop("human_final_artifact"), drop("human_delta_id"))},
	{"a live episode without a replay event", "decision_episode", all(
		drop("held_out_event_id"), drop("account_change_id"), drop("business_intelligence_update_id"))},
	{"a legacy episode recorded before strategy sets", "decision_episode", set(p("status"), "decided")},
	{"a bundle that abstained", "eval_bundle", all(
		set(p("items", 0, "verdict"), "abstain"), set(p("items", 0, "result", "verdict"), "abstain"))},
	{"a world after Play", "replay_world", all(
		set(p("event_cursor", "replay_position"), 2.0), set(p("event_cursor", "held_out_event_id"), nil))},
	{"a BI update that applies no knowledge", "business_intelligence_update", set(p("knowledge_refs"), []any{})},
	{"a BI update with no transition", "business_intelligence_update", set(p("transition"), nil)},
	{"a BI update restating an open transition the event left unchanged", "business_intelligence_update", set(p("transition", "touched_by_event"), false)},
	{"a strategy candidate draft at any index", "agent_run_draft", all(set(p("source"), "strategy_generator"), set(p("draft_index"), 3.0), set(p("revision_feedback"), []any{}))},
	{"a quiet event in the manifest", "demo_manifest", all(
		set(p("events", 0, "is_material"), false), set(p("events", 0, "state_diff_id"), nil),
		set(p("events", 0, "material_dimensions"), []any{}))},
	{"a change with no material effect", "account_change", all(set(p("material_change"), false), set(p("evidence_refs"), []any{}))},
}

func TestDemoObjectsBadInstancesAreRejected(t *testing.T) {
	root := contractsDir(t)
	c := compiler(t, root)
	for _, tc := range demoBad {
		t.Run(tc.name, func(t *testing.T) {
			if err := schemaFor(t, c, tc.schema).Validate(mutate(t, root, tc.schema, tc.fn)); err == nil {
				t.Fatalf("expected %s to be rejected", tc.schema)
			}
		})
	}
}

func TestDemoObjectsLegalStatesAreAccepted(t *testing.T) {
	root := contractsDir(t)
	c := compiler(t, root)
	for _, tc := range demoGood {
		t.Run(tc.name, func(t *testing.T) {
			if err := schemaFor(t, c, tc.schema).Validate(mutate(t, root, tc.schema, tc.fn)); err != nil {
				t.Fatalf("expected %s to be accepted: %v", tc.schema, err)
			}
		})
	}
}
