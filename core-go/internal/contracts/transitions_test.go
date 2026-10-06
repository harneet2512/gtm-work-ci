package contracts

import (
	"path/filepath"
	"testing"
)

// TestTransitionInstancesValidate checks the rule set and the routing table (contracts/transitions,
// HAR-126 / ADR-0012) against their schemas.
func TestTransitionInstancesValidate(t *testing.T) {
	root := contractsDir(t)
	c := compiler(t, root)
	for file, schema := range map[string]string{"rules.v1.json": "transition_rules", "routing.v1.json": "transition_routing"} {
		if err := schemaFor(t, c, schema).Validate(readJSON(t, filepath.Join(root, "transitions", file))); err != nil {
			t.Errorf("%s invalid: %v", file, err)
		}
	}
	files, err := filepath.Glob(filepath.Join(root, "transitions", "*.json"))
	if err != nil || len(files) != 2 {
		t.Fatalf("contracts/transitions must hold exactly rules.v1.json and routing.v1.json, found %v (%v)", files, err)
	}
}

func list(m map[string]any, key string) []any { return m[key].([]any) }

func obj(v any) map[string]any { return v.(map[string]any) }

// requiredOnly keeps the missing facts whose "required" flag equals want.
func requiredOnly(m map[string]any, want bool) []any {
	kept := []any{}
	for _, f := range list(m, "missing_facts") {
		if obj(f)["required"] == want {
			kept = append(kept, f)
		}
	}
	return kept
}

func contradiction(rejects bool) map[string]any {
	return map[string]any{
		"key": "support_risk_high", "description": "d", "required": false, "satisfied": true, "rejects": rejects,
		"evidence_refs": []any{map[string]any{"activity_id": "0ac70000-0000-4000-8000-000000000101"}},
	}
}

func firstCondition(m map[string]any) map[string]any {
	fact := obj(list(obj(list(m, "transitions")[0]), "facts")[0])
	return obj(list(obj(list(fact, "any_of")[0]), "all_of")[0])
}

type badCase struct {
	name, schema string
	fn           func(m map[string]any)
}

var transitionBadCases = []badCase{
	{"transition status vocabulary", "state_transition", func(m map[string]any) { m["status"] = "PROBABLE" }},
	{"relationship states are upper case", "state_transition", func(m map[string]any) { m["from_state"] = "reorg" }},
	{"unknown is never a target", "state_transition", func(m map[string]any) { m["to_state_candidate"] = "unknown" }},
	{"a transition changes state", "state_transition", func(m map[string]any) { m["to_state_candidate"] = "REORG" }},
	{"only UNRESOLVED may lack a target", "state_transition", func(m map[string]any) { m["to_state_candidate"] = nil }},
	{"UNRESOLVED has no target", "state_transition", func(m map[string]any) { m["status"] = "UNRESOLVED" }},
	{"confidence has three decimals", "state_transition", func(m map[string]any) { m["confidence"] = 0.4444 }},
	{"a transition is triggered by activity", "state_transition", func(m map[string]any) { m["trigger_activity_ids"] = []any{} }},
	{"a satisfied fact rests on evidence", "state_transition", func(m map[string]any) {
		obj(list(m, "supporting_facts")[0])["evidence_refs"] = []any{}
	}},
	{"only contradictions carry rejects", "state_transition", func(m map[string]any) {
		obj(list(m, "supporting_facts")[0])["rejects"] = false
	}},
	{"a candidate misses a required fact", "state_transition", func(m map[string]any) { m["missing_facts"] = requiredOnly(m, false) }},
	{"a decisively contradicted candidate is rejected", "state_transition", func(m map[string]any) {
		m["contradicting_facts"] = []any{contradiction(true)}
	}},
	{"a contradiction is never a requirement", "state_transition", func(m map[string]any) {
		c := contradiction(false)
		c["required"] = true
		m["contradicting_facts"] = []any{c}
	}},
	{"a confirmed transition has no unmet required fact", "state_transition", func(m map[string]any) {
		m["status"], m["confirmed_at"] = "CONFIRMED", "2026-10-20T09:00:00Z"
	}},
	{"a rejected transition needs a decisive contradiction", "state_transition", func(m map[string]any) {
		m["status"], m["rejected_at"], m["contradicting_facts"] = "REJECTED", "2026-10-20T09:00:00Z", []any{contradiction(false)}
	}},
	{"only UNRESOLVED is closed", "state_transition", func(m map[string]any) {
		m["closed_at"], m["close_reason"] = "2026-10-20T09:00:00Z", "stale"
	}},
	{"relationship_state is the earned vocabulary, not CRM motion", "account_state", func(m map[string]any) {
		obj(m["relationship_state"])["value"] = "expansion"
	}},
	{"open_transition is non-terminal", "account_state", func(m map[string]any) { obj(m["open_transition"])["status"] = "CONFIRMED" }},
	{"champion_since is a timestamp", "account_state", func(m map[string]any) {
		obj(obj(m["fields"])["champion_since"])["value"] = "two weeks ago"
	}},
	{"rule paths are state./claim./buying_group./diff.", "transition_rules", func(m map[string]any) {
		firstCondition(m)["path"] = "vibes.mood"
	}},
	{"no static headcount op", "transition_rules", func(m map[string]any) {
		c := firstCondition(m)
		c["path"], c["op"] = "buying_group.champion", "count_gte"
		delete(c, "value")
	}},
	{"a contradiction says whether it is decisive", "transition_rules", func(m map[string]any) {
		delete(obj(list(obj(list(m, "transitions")[0]), "contradictions")[0]), "rejects")
	}},
	{"an org_change names its kind and summary", "claim", func(m map[string]any) {
		m["field_path"], m["value"] = "org_change", "they reorganised"
	}},
	{"claim evidence counts only first-party standings", "transition_rules", func(m map[string]any) { delete(m, "claim_standings") }},
	{"a key the op ignores is rejected", "transition_rules", func(m map[string]any) {
		firstCondition(m)["threshold"] = "owner_stable_min_days"
	}},
	{"a state fallback names its default suite", "transition_routing", func(m map[string]any) {
		routes := list(m, "routes")
		delete(obj(routes[len(routes)-1]), "fallback_suite")
	}},
	{"an action route keys on a decision class", "transition_routing", func(m map[string]any) {
		obj(list(m, "action_routes")[0])["action_class"] = "PUSH"
	}},
	{"an action route names the statuses it refines", "transition_routing", func(m map[string]any) {
		obj(list(m, "action_routes")[0])["statuses"] = []any{}
	}},
	{"routes key on transition statuses", "transition_routing", func(m map[string]any) {
		obj(list(m, "routes")[0])["status"] = "PROBABLE"
	}},
	{"routed evals are catalog eval types", "transition_routing", func(m map[string]any) {
		suite := obj(obj(m["suites"])["reorg_disruption"])
		obj(list(suite, "evals")[0])["eval_types"] = []any{"vibes"}
	}},
}

func TestKnownBadTransitionsAreRejected(t *testing.T) {
	root := contractsDir(t)
	c := compiler(t, root)
	for _, tc := range transitionBadCases {
		t.Run(tc.name, func(t *testing.T) {
			if err := schemaFor(t, c, tc.schema).Validate(mutate(t, root, tc.schema, tc.fn)); err == nil {
				t.Fatalf("expected %s to be rejected", tc.schema)
			}
		})
	}
}
