package changedim_test

import (
	"strings"
	"testing"

	"github.com/harneet2512/gtm-work/core-go/internal/biwriter"
	"github.com/harneet2512/gtm-work/core-go/internal/changedim"
)

// The topics of the two demo cases, from the material_diff_fields recorded in their judge cassettes (read-only): MedTech
// Advances at Event N and EcoLite Innovations at Event N.
func TestTopicsOfTheDemoCases(t *testing.T) {
	medtech := []string{"commercial_issue", "current_commitments", "decision_criteria", "objections", "product_use_case", "relationship_risk"}
	ecolite := []string{"blockers", "commercial_issue", "objections", "stage"}
	for name, c := range map[string]struct {
		fields []string
		want   string
	}{
		"MedTech":  {medtech, "blockers_risk,buyer_intent,next_step_commitment"},
		"EcoLite":  {ecolite, "blockers_risk,buyer_intent"},
		"none":     {nil, ""},
		"unmapped": {[]string{"not_a_field"}, ""},
	} {
		if got := strings.Join(changedim.TopicsOf(c.fields), ","); got != c.want {
			t.Errorf("%s: topics = %q, want %q", name, got, c.want)
		}
	}
	if changedim.TopicsOf(nil) != nil {
		t.Fatal("no known topic is nil, never an empty known topic")
	}
}

func TestEveryDimensionTheWriterEmitsIsAKnownChangeDimension(t *testing.T) {
	known := map[string]bool{"stakeholder_structure": true, "relationship_ownership": true, "buyer_intent": true,
		"blockers_risk": true, "next_step_commitment": true, "recommended_action": true}
	for _, f := range biwriter.KnownFields() {
		if !known[changedim.Of(f)] {
			t.Errorf("field %s has dimension %q, not in common.v1.json#changeDimension", f, changedim.Of(f))
		}
	}
}
