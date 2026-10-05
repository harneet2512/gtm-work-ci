package recompute

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestEditKindsMapToAFieldAndAClass(t *testing.T) {
	cases := []struct {
		kind  string
		field Field
		class string
	}{
		{"recipient_added", Recipients, "stakeholder_change"},
		{"recipient_removed", Recipients, "stakeholder_change"},
		{"recipient_role_changed", Recipients, "stakeholder_change"},
		{"subject_changed", Subject, "content_change"},
		{"paragraph_added", Body, "content_change"},
		{"paragraph_removed", Body, "content_change"},
		{"paragraph_edited", Body, "content_change"},
		{"channel_changed", Channel, "channel_change"},
		{"attachment_added", Attachments, "attachment_change"},
		{"attachment_removed", Attachments, "attachment_change"},
	}
	for _, tc := range cases {
		field, class, err := classify(tc.kind)
		if err != nil || field != tc.field || class != tc.class {
			t.Errorf("%s: %s/%s/%v, want %s/%s", tc.kind, field, class, err, tc.field, tc.class)
		}
	}
	if _, _, err := classify("rewritten"); err == nil {
		t.Error("an edit kind outside the vocabulary was classified: an unknown change must not be given a field it may not have")
	}
}

func TestAnEvalTheTableDoesNotKnowDependsOnEveryField(t *testing.T) {
	for _, f := range allFields {
		if !dependsOn("some_future_eval", f) {
			t.Errorf("an unknown eval is declared independent of %s: preservation must be declared, never assumed", f)
		}
	}
}

func TestDeclaredDependencies(t *testing.T) {
	cases := []struct {
		evalType string
		field    Field
		want     bool
	}{
		{"recipient_correctness", Recipients, true},
		{"recipient_correctness", Body, false},
		{"recipient_correctness", Subject, false},
		{"champion_continuity", Recipients, true},
		{"champion_continuity", Body, true},
		{"champion_continuity", Subject, false},
		{"cta_calibration", Body, true},
		{"cta_calibration", Subject, true},
		{"cta_calibration", Recipients, false},
		{"pricing_integrity", Attachments, true},
		{"permission_policy", Channel, true},
		{"state_transition_support", Body, false},
		{"human_delta", Recipients, true}, // learned axes can read anything
		{"human_delta", Attachments, true},
	}
	for _, tc := range cases {
		if got := dependsOn(tc.evalType, tc.field); got != tc.want {
			t.Errorf("dependsOn(%s, %s) = %v, want %v", tc.evalType, tc.field, got, tc.want)
		}
	}
}

// Every eval type of the contract vocabulary is either declared in the table or deliberately left to the
// conservative default; the table never names a type the contract does not know.
func TestTheTableNamesOnlyContractEvalTypes(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(repoRoot(t), "contracts", "schemas", "eval_result.v1.json"))
	if err != nil {
		t.Fatal(err)
	}
	var schema struct {
		Defs struct {
			EvalType struct {
				Enum []string `json:"enum"`
			} `json:"evalType"`
		} `json:"$defs"`
	}
	if err := json.Unmarshal(raw, &schema); err != nil {
		t.Fatal(err)
	}
	known := map[string]bool{}
	for _, e := range schema.Defs.EvalType.Enum {
		known[e] = true
	}
	for name := range evalReads {
		if !known[name] {
			t.Errorf("the dependency table names %q, which is not an eval type of eval_result.v1.json", name)
		}
	}
}

func repoRoot(t *testing.T) string {
	t.Helper()
	dir, _ := os.Getwd()
	for {
		if _, err := os.Stat(filepath.Join(dir, "contracts", "schemas")); err == nil {
			return dir
		}
		if filepath.Dir(dir) == dir {
			t.Fatal("contracts/ not found")
		}
		dir = filepath.Dir(dir)
	}
}
