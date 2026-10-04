package strategystore

import (
	"encoding/json"
	"reflect"
	"testing"
)

func str(s string) *string { return &s }

func baseDraft() Draft {
	return Draft{
		To:       []Recipient{{PersonID: "marco", Role: "to"}},
		CC:       []Recipient{{PersonID: "priya", Role: "cc"}},
		Artifact: Artifact{Channel: "email", Subject: str("Re: EU"), Body: "Hi Marco,\n\nHere are the documents.\n\nBest,\nDana", Attachments: []string{"soc2.pdf"}},
	}
}

func kinds(edits []Edit) []string {
	out := []string{}
	for _, e := range edits {
		out = append(out, e.Kind)
	}
	return out
}

func TestComputeEditsIsEmptyForAnUnchangedDraft(t *testing.T) {
	// Arrange
	d := baseDraft()
	// Act
	got := ComputeEdits(d, d)
	// Assert
	if got == nil || len(got) != 0 {
		t.Fatalf("edits = %#v, want an empty non-nil slice", got)
	}
	if b, _ := json.Marshal(got); string(b) != "[]" {
		t.Fatalf("encoded = %s, want []", b)
	}
}

func TestComputeEditsNamesEveryKindOfChange(t *testing.T) {
	cases := []struct {
		name   string
		change func(d *Draft)
		want   []string
	}{
		{"recipient added", func(d *Draft) { d.CC = append(d.CC, Recipient{PersonID: "sam", Role: "cc"}) }, []string{"recipient_added"}},
		{"recipient removed", func(d *Draft) { d.CC = nil }, []string{"recipient_removed"}},
		{"recipient moved from cc to to", func(d *Draft) {
			d.To = append(d.To, Recipient{PersonID: "priya", Role: "to"})
			d.CC = nil
		}, []string{"recipient_role_changed"}},
		{"subject", func(d *Draft) { d.Artifact.Subject = str("Re: EU rollout") }, []string{"subject_changed"}},
		{"subject removed", func(d *Draft) { d.Artifact.Subject = nil }, []string{"subject_changed"}},
		{"channel", func(d *Draft) { d.Artifact.Channel = "slack" }, []string{"channel_changed"}},
		{"paragraph edited", func(d *Draft) { d.Artifact.Body = "Hi Marco,\n\nHere is the package.\n\nBest,\nDana" }, []string{"paragraph_edited"}},
		{"paragraph added", func(d *Draft) { d.Artifact.Body += "\n\nP.S. call anytime" }, []string{"paragraph_added"}},
		{"paragraph removed", func(d *Draft) { d.Artifact.Body = "Hi Marco,\n\nHere are the documents." }, []string{"paragraph_removed"}},
		{"attachment added", func(d *Draft) { d.Artifact.Attachments = []string{"soc2.pdf", "pentest.pdf"} }, []string{"attachment_added"}},
		{"attachment removed", func(d *Draft) { d.Artifact.Attachments = nil }, []string{"attachment_removed"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange
			final := baseDraft()
			tc.change(&final)
			// Act
			got := kinds(ComputeEdits(baseDraft(), final))
			// Assert
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("kinds = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestComputeEditsKeepsBeforeAndAfter(t *testing.T) {
	final := baseDraft()
	final.Artifact.Subject = str("New")
	got := ComputeEdits(baseDraft(), final)
	if len(got) != 1 || got[0].Before != "Re: EU" || got[0].After != "New" {
		t.Fatalf("edits = %#v", got)
	}
}

func TestOverlayDoesNotMutateItsBase(t *testing.T) {
	base := baseDraft()
	to := []Recipient{{PersonID: "sam", Role: "to"}}
	out := overlay(base, DecisionRequest{FinalTo: &to})
	out.To[0].PersonID = "changed"
	if base.To[0].PersonID != "marco" || to[0].PersonID != "sam" {
		t.Fatalf("overlay mutated its inputs: base=%v to=%v", base.To, to)
	}
}

func TestRequestValidation(t *testing.T) {
	ok := DecisionRequest{SelectedCandidateID: "0ca00000-0000-4000-8000-0000000000a1", Surface: "slack", ActorLabel: "alex"}
	if err := ok.Validate(); err != nil {
		t.Fatalf("valid request refused: %v", err)
	}
	empty := []Recipient{}
	bad := map[string]DecisionRequest{
		"candidate id": {SelectedCandidateID: "x", Surface: "slack", ActorLabel: "a"},
		"surface":      {SelectedCandidateID: ok.SelectedCandidateID, Surface: "fax", ActorLabel: "a"},
		"label":        {SelectedCandidateID: ok.SelectedCandidateID, Surface: "slack"},
		"empty to":     {SelectedCandidateID: ok.SelectedCandidateID, Surface: "slack", ActorLabel: "a", FinalTo: &empty},
	}
	for name, r := range bad {
		if r.Validate() == nil {
			t.Errorf("%s: invalid request accepted", name)
		}
	}
	for name, v := range map[string]VerdictRequest{
		"nothing":            {Surface: "slack", ActorLabel: "a"},
		"correction no text": {Verdict: "corrected", Surface: "slack", ActorLabel: "a"},
		"confirmed + text":   {Verdict: "confirmed", CorrectedStatement: "x", Surface: "slack", ActorLabel: "a"},
		"bad verdict":        {Verdict: "maybe", Surface: "slack", ActorLabel: "a"},
	} {
		if v.Validate() == nil {
			t.Errorf("%s: invalid verdict accepted", name)
		}
	}
	if (SendRequest{Decision: "later", Surface: "slack", ActorLabel: "a"}).Validate() == nil {
		t.Error("an unknown send decision was accepted")
	}
}
