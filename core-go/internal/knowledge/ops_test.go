package knowledge

import "testing"

func opsSituation() Situation {
	s := situation(map[string]Value{
		"stage":            known("Technical  Evaluation"),
		"economic_buyer":   unknown(),
		"next_meeting":     {Known: true, Scalar: nil},
		"summary":          known("  "),
		"health":           known(3.0),
		"decision_process": known("Security lead owns the evaluation; champion joins for sign-off"),
		"blockers": items(Item{Text: "Budget approval in Q3 planning", Status: "resolved"},
			Item{Text: "Security sign-off", Status: "open"}),
		"objections":    {Known: true, List: true},
		"coverage_gaps": items(Item{Text: "economic_buyer"}),
	})
	s.BuyingGroup = []Member{
		{PersonID: "p1", Roles: []string{"champion"}, Status: "active"},
		{PersonID: "p2", Roles: []string{"economic_buyer"}, Status: "departed"},
		{PersonID: "p3", Roles: []string{"security", "technical_evaluator"}, Status: "new"},
	}
	return s
}

func TestConditionOps(t *testing.T) {
	cases := []struct {
		name string
		c    Condition
		want bool
	}{
		{"eq ignores case and spacing", cond("stage", OpEq, "technical evaluation"), true},
		{"eq on an unknown field is false", cond("economic_buyer", OpEq, "unknown"), false},
		{"neq on an unknown field is false", cond("economic_buyer", OpNeq, "someone"), false},
		{"neq on a known different value", cond("stage", OpNeq, "Discovery"), true},
		{"neq on a known null is false", cond("next_meeting", OpNeq, "x"), false},
		{"eq on numbers", cond("health", OpEq, 3), true},
		{"eq number against string is false", cond("health", OpEq, "3"), false},
		{"in matches one value", cond("stage", OpIn, list("Discovery", "Technical evaluation")), true},
		{"in without a match", cond("stage", OpIn, list("Discovery")), false},
		{"contains on a scalar string", cond("decision_process", OpContains, "SECURITY LEAD"), true},
		{"contains on a scalar without the text", cond("decision_process", OpContains, "procurement"), false},
		{"contains item text", cond("blockers", OpContains, "security"), true},
		{"contains pattern needs the same item", cond("blockers", OpContains, map[string]any{"text": "budget", "status": "open"}), false},
		{"contains pattern text and status", cond("blockers", OpContains, map[string]any{"text": "budget", "status": "resolved"}), true},
		{"contains pattern status list", cond("blockers", OpContains, map[string]any{"status": []any{"open", "overdue"}}), true},
		{"contains on an empty list", cond("objections", OpContains, "x"), false},
		{"contains on a missing list", cond("current_commitments", OpContains, "x"), false},
		{"contains coverage gap", cond("coverage_gaps", OpContains, "economic_buyer"), true},
		{"exists on a known value", cond("stage", OpExists), true},
		{"exists on unknown is false", cond("economic_buyer", OpExists), false},
		{"exists on known null is false", cond("next_meeting", OpExists), false},
		{"exists on blank string is false", cond("summary", OpExists), false},
		{"exists on a non-empty list", cond("blockers", OpExists), true},
		{"exists on an empty list is false", cond("objections", OpExists), false},
		{"not_exists on known null", cond("next_meeting", OpNotExists), true},
		{"not_exists on unknown", cond("economic_buyer", OpNotExists), true},
		{"not_exists on a value is false", cond("stage", OpNotExists), false},
		{"is_unknown on unknown", cond("economic_buyer", OpIsUnknown), true},
		{"is_unknown on a missing field", cond("product_use_case", OpIsUnknown), true},
		{"is_unknown on known null is false", cond("next_meeting", OpIsUnknown), false},
		{"is_unknown on a known list is false", cond("objections", OpIsUnknown), false},
		{"role exists for an engaged member", cond("buying_group.champion", OpExists), true},
		{"role held only by a departed member does not exist", cond("buying_group.economic_buyer", OpExists), false},
		{"role not_exists for a departed member", cond("buying_group.economic_buyer", OpNotExists), true},
		{"role exists for a new member", cond("buying_group.technical_evaluator", OpExists), true},
		{"role absent", cond("buying_group.legal", OpExists), false},
		{"role eq status", cond("buying_group.economic_buyer", OpEq, "departed"), true},
		{"role in statuses", cond("buying_group.security", OpIn, list("active", "new")), true},
		{"role eq other status", cond("buying_group.champion", OpEq, "departed"), false},
	}
	s := opsSituation()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := holdsIn(t, s, tc.c); got != tc.want {
				t.Fatalf("%s = %v, want %v", render(tc.c), got, tc.want)
			}
		})
	}
}

func TestRelationshipStateAndTransitionConditions(t *testing.T) {
	none := situation(map[string]Value{})
	open := with(none, func(s *Situation) {
		s.RelationshipState = "REORG"
		s.Transition = &Transition{ID: "t1", FromState: "REORG", Status: "UNRESOLVED"}
	})
	cases := []struct {
		name string
		s    Situation
		c    Condition
		want bool
	}{
		{"relationship_state unknown", none, cond("relationship_state", OpIsUnknown), true},
		{"relationship_state literal unknown", with(none, func(s *Situation) { s.RelationshipState = "unknown" }), cond("relationship_state", OpExists), false},
		{"relationship_state eq", open, cond("relationship_state", OpEq, "reorg"), true},
		{"relationship_state neq when unknown", none, cond("relationship_state", OpNeq, "REORG"), false},
		{"no transition: eq fails", none, cond("transition.status", OpEq, "CANDIDATE"), false},
		{"no transition: not_exists holds", none, cond("transition.status", OpNotExists), true},
		{"no transition: neq fails", none, cond("transition.from_state", OpNeq, "REORG"), false},
		{"unresolved has no target", open, cond("transition.to_state", OpExists), false},
		{"unresolved status", open, cond("transition.status", OpIn, list("CANDIDATE", "UNRESOLVED")), true},
		{"from_state eq", open, cond("transition.from_state", OpEq, "REORG"), true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := holdsIn(t, tc.s, tc.c); got != tc.want {
				t.Fatalf("%s = %v, want %v", render(tc.c), got, tc.want)
			}
		})
	}
}
