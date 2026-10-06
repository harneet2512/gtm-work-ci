package slacksurface

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Cliff Message 3 (HAR-129 FINAL DEMO SURFACES), split from cliff_test.go: the interpretation, the plain-word labels, the
// verdict states and the Edit interpretation modal.

// ---- Message 3 --------------------------------------------------------------------------------------------------

func TestM3SaysWhatIChangedAndMyInterpretation(t *testing.T) {
	f := NewFixture()
	m := RenderJudgment(f.Inference, f.Strategies, FixtureRunID, testWeb)
	raw := mustJSON(m)

	if !strings.Contains(raw, "I noticed you changed the message to put less pressure on the buyer, the timing to follow the buyer's schedule and the ask to a smaller one.") {
		t.Errorf("the semantic thing is named from the labels: %s", raw)
	}
	if !strings.Contains(raw, "My interpretation: ") || !strings.Contains(raw, strings.ReplaceAll(f.Inference.InferredSemanticDelta.Statement, `"`, `\"`)) {
		t.Errorf("the scoped proposal follows \"My interpretation:\": %s", raw)
	}
	if strings.Contains(raw, "Needs correction") || strings.Contains(raw, "Add note") {
		t.Error("Needs correction and Add note are replaced")
	}
	if got := labelsOf(buttonsOf(m)); strings.Join(got, "|") != "Correct|Edit interpretation|Don't learn this" {
		t.Errorf("buttons = %v", got)
	}
	for action, id := range map[string]string{"Correct": ActionJudgmentConfirm, "Edit interpretation": ActionJudgmentCorrect, "Don't learn this": ActionJudgmentNoLearn} {
		var found bool
		for _, b := range buttonsOf(m) {
			found = found || (b.ActionID == id && b.Text.Text == action)
		}
		if !found {
			t.Errorf("%s must be action %s", action, id)
		}
	}
}

func TestM3NamesTheChangeFromTheOptionsWhenThereAreNoLabels(t *testing.T) {
	f := NewFixture()
	inf := f.Inference
	inf.InferredSemanticDelta.SemanticLabels = nil
	raw := mustJSON(RenderJudgment(inf, f.Strategies, FixtureRunID, ""))
	if !strings.Contains(raw, "I noticed you changed my recommendation, ") || !strings.Contains(raw, ", to ") {
		t.Errorf("without labels the change is named from the two options: %s", raw)
	}

	inf.Agreement, inf.HumanChoice = AgreementAgreed, inf.AgentPreference
	raw = mustJSON(RenderJudgment(inf, f.Strategies, FixtureRunID, ""))
	if !strings.Contains(raw, "I noticed you went with my recommendation, ") || !strings.Contains(raw, "My interpretation: ") {
		t.Errorf("agreeing with the recommendation is said plainly: %s", raw)
	}
}

func TestEverySemanticLabelOfTheContractHasPlainWords(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "contracts", "schemas", "human_delta.v1.json"))
	if err != nil {
		t.Fatal(err)
	}
	var schema struct {
		Properties struct {
			SemanticLabels struct {
				Items struct {
					Enum []string `json:"enum"`
				} `json:"items"`
			} `json:"semantic_labels"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(raw, &schema); err != nil {
		t.Fatal(err)
	}
	labels := schema.Properties.SemanticLabels.Items.Enum
	if len(labels) < 10 {
		t.Fatalf("read %d labels from human_delta.v1.json", len(labels))
	}
	for _, l := range labels {
		words, ok := labelWords[l]
		if !ok || strings.TrimSpace(words) == "" || strings.Contains(words, "_") {
			t.Errorf("label %q has no plain words", l)
		}
	}
	if got := changedThing([]string{"reduced_pressure", "delayed_cta", "style_only"}); got !=
		"the message to put less pressure on the buyer, the timing of the ask and the style only, not the substance" {
		t.Errorf("three labels join with commas and \"and\": %q", got)
	}
	if got := changedThing([]string{"invented_label"}); strings.Contains(got, "_") {
		t.Errorf("an unknown label never shows its raw code: %q", got)
	}
}

func TestM3VerdictStatesReplaceTheButtonsWithTheOutcome(t *testing.T) {
	f := NewFixture()
	for _, c := range []struct {
		name string
		inf  JudgmentInference
		want string
	}{
		{"confirmed", f.Confirmed, "Confirmed"},
		{"corrected", f.Corrected, "Corrected:"},
		{"no learning", f.NoLearning, "Not learning from this"},
	} {
		m := RenderJudgment(c.inf, f.Strategies, FixtureRunID, testWeb)
		raw := mustJSON(m)
		if !strings.Contains(raw, c.want) {
			t.Errorf("%s: want %q in %s", c.name, c.want, raw)
		}
		if got := labelsOf(buttonsOf(m)); len(got) != 0 {
			t.Errorf("%s: buttons = %v, want none once answered", c.name, got)
		}
		if strings.Contains(raw, "Should I learn this?") {
			t.Errorf("%s: the question is answered", c.name)
		}
		if err := ValidateMessage(m); err != nil {
			t.Errorf("%s: %v", c.name, err)
		}
	}
	if raw := mustJSON(RenderJudgment(f.NoLearning, f.Strategies, FixtureRunID, "")); strings.Contains(raw, "knowledge") && !strings.Contains(raw, "won't") {
		t.Errorf("the no-learning outcome must not claim anything became knowledge: %s", raw)
	}
}

func TestEditInterpretationModalIsPrefilledWithMyInterpretation(t *testing.T) {
	f := NewFixture()
	v := RenderCorrectionModal(f.Inference, Target{EpisodeID: FixtureEpisodeID, RunID: FixtureRunID})
	raw := mustJSON(v)

	if v.Title.Text != "Edit interpretation" || v.CallbackID != CallbackCorrectionModal {
		t.Errorf("modal = %q / %s", v.Title.Text, v.CallbackID)
	}
	if !strings.Contains(raw, strings.ReplaceAll(f.Inference.InferredSemanticDelta.Statement, `"`, `\"`)) || !strings.Contains(raw, `"initial_value"`) {
		t.Errorf("the input starts from Cliff's interpretation: %s", raw)
	}
	if !strings.Contains(raw, inputNote) {
		t.Error("the modal keeps the note field: the correction and its note are stored together")
	}
	if err := ValidateModal(v); err != nil {
		t.Fatal(err)
	}
}
