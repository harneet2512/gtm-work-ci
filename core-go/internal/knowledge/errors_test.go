package knowledge

import (
	"errors"
	"testing"
)

func TestMalformedConditionsAreErrorsNotFalse(t *testing.T) {
	cases := []struct {
		name string
		c    Condition
	}{
		{"unknown state field", cond("mood", OpEq, "x")},
		{"unknown signal type", cond("diff.champion_left", OpExists)},
		{"unknown role", cond("buying_group.cfo", OpExists)},
		{"unknown transition attribute", cond("transition.target", OpEq, "EXPANSION")},
		{"eq on a signal", cond("diff.customer_replied", OpEq, "x")},
		{"eq on a list field", cond("blockers", OpEq, "x")},
		{"is_unknown on a role", cond("buying_group.champion", OpIsUnknown)},
		{"is_unknown on a transition", cond("transition.status", OpIsUnknown)},
		{"contains on a role", cond("buying_group.champion", OpContains, "x")},
		{"unknown op", cond("stage", "gt", 3)},
		{"eq without a value", cond("stage", OpEq)},
		{"eq with a list", cond("stage", OpEq, list("a", "b"))},
		{"exists with a value", cond("stage", OpExists, true)},
		{"in with one scalar", cond("stage", OpIn, "a")},
		{"in with an empty list", Condition{Field: "stage", Op: OpIn, Value: []any{}}},
		{"in with an object", cond("stage", OpIn, list(map[string]any{"a": 1}))},
		{"contains with blank text", cond("blockers", OpContains, " ")},
		{"contains with empty pattern", cond("blockers", OpContains, map[string]any{})},
		{"contains with unknown pattern key", cond("blockers", OpContains, map[string]any{"owner": "rep"})},
		{"contains with unknown status", cond("blockers", OpContains, map[string]any{"status": "closed"})},
		{"contains with empty status list", cond("blockers", OpContains, map[string]any{"status": []any{}})},
		{"contains with non-string status", cond("blockers", OpContains, map[string]any{"status": []any{1}})},
		{"contains with blank pattern text", cond("blockers", OpContains, map[string]any{"text": ""})},
		{"contains with a number", cond("blockers", OpContains, 3)},
		{"contains item status on a scalar", cond("stage", OpContains, map[string]any{"text": "x", "status": "open"})},
		{"contains pattern without text on a scalar", cond("stage", OpContains, map[string]any{"status": "open"})},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := newEnv(situation(map[string]Value{})).holds(tc.c)
			if !errors.Is(err, ErrInvalidCondition) {
				t.Fatalf("err = %v, want ErrInvalidCondition", err)
			}
			if _, err := Match(k([]Condition{tc.c}), situation(map[string]Value{})); !errors.Is(err, ErrInvalidCondition) {
				t.Fatalf("Match err = %v", err)
			}
		})
	}
}

func TestFieldsOfTheWrongShapeAreErrors(t *testing.T) {
	s := situation(map[string]Value{"stage": items(Item{Text: "x"}), "blockers": known("x")})
	for _, c := range []Condition{cond("stage", OpEq, "x"), cond("blockers", OpContains, "x")} {
		if _, _, err := newEnv(s).holds(c); !errors.Is(err, ErrInvalidSituation) {
			t.Fatalf("%s: err = %v", render(c), err)
		}
	}
}

func TestMalformedExceptionIsReportedEvenWhenTheSignatureFails(t *testing.T) {
	kn := k([]Condition{cond("motion", OpEq, "renewal")}, exc("typo", cond("champion_stauts", OpEq, "delegated")))
	if _, err := Match(kn, situation(map[string]Value{"motion": known("expansion")})); !errors.Is(err, ErrInvalidCondition) {
		t.Fatalf("err = %v", err)
	}
}

func TestInvalidKnowledgeObjects(t *testing.T) {
	cases := map[string]Knowledge{
		"no id":                        {Title: "t", SituationSignature: []Condition{cond("stage", OpExists)}},
		"empty signature":              k(nil),
		"exception without conditions": k([]Condition{cond("stage", OpExists)}, Exception{Description: "x"}),
	}
	for name, kn := range cases {
		if err := Validate(kn); !errors.Is(err, ErrInvalidKnowledge) {
			t.Fatalf("%s: err = %v", name, err)
		}
		if _, err := Match(kn, situation(map[string]Value{})); !errors.Is(err, ErrInvalidKnowledge) {
			t.Fatalf("%s: Match err = %v", name, err)
		}
	}
}

func TestRenderFormsConditions(t *testing.T) {
	if got := render(cond("diff.customer_replied", OpExists)); got != "diff.customer_replied exists" {
		t.Fatal(got)
	}
	if got := render(cond("stage", OpIn, list("a", "b"))); got != `stage in ["a","b"]` {
		t.Fatal(got)
	}
	if got := render(cond("stage", OpEq, func() {})); got == "" {
		t.Fatal("unmarshalable values still render")
	}
}
