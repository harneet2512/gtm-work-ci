package learning

import (
	"errors"
	"strings"
	"testing"

	"github.com/harneet2512/gtm-work/core-go/internal/evals/deterministic"
)

// draft is the smallest deterministic.Output an axis check reads.
func draft(to ...deterministic.Recipient) deterministic.Output {
	return deterministic.Output{Recipients: to,
		FinishedArtifact: deterministic.Artifact{Channel: "email", Body: "body"}}
}

var marco = deterministic.Recipient{PersonID: "0b0e0000-0000-4000-8000-0000000000a1", Role: "to"}
var priya = deterministic.Recipient{PersonID: "0b0e0000-0000-4000-8000-0000000000a2", Role: "cc"}

func TestRecipientChecks(t *testing.T) {
	person := func(id string) map[string]any { return map[string]any{"person_id": id, "role": "to"} }
	for name, tc := range map[string]struct {
		change   LiteralChange
		draft    deterministic.Output
		violated bool
	}{
		"added recipient missing": {LiteralChange{Kind: "recipient_added", After: person(marco.PersonID)}, draft(), true},
		"added recipient present": {LiteralChange{Kind: "recipient_added", After: person(marco.PersonID)}, draft(marco), false},
		"added recipient wrong role": {LiteralChange{Kind: "recipient_added", After: person(marco.PersonID)},
			draft(deterministic.Recipient{PersonID: marco.PersonID, Role: "cc"}), true},
		"removed recipient still there": {LiteralChange{Kind: "recipient_removed", Before: person(priya.PersonID)}, draft(marco, priya), true},
		"removed recipient gone":        {LiteralChange{Kind: "recipient_removed", Before: person(priya.PersonID)}, draft(marco), false},
		"role change applied": {LiteralChange{Kind: "recipient_role_changed",
			After: map[string]any{"person_id": priya.PersonID, "role": "cc"}}, draft(marco, priya), false},
		"role change missing": {LiteralChange{Kind: "recipient_role_changed",
			After: map[string]any{"person_id": priya.PersonID, "role": "cc"}},
			draft(marco, deterministic.Recipient{PersonID: priya.PersonID, Role: "to"}), true},
		"malformed change ignored": {LiteralChange{Kind: "recipient_added", After: "not-a-map"}, draft(), false},
	} {
		t.Run(name, func(t *testing.T) {
			violated, _ := Spec{LiteralChanges: []LiteralChange{tc.change}}.RunSpec(tc.draft)
			if violated != tc.violated {
				t.Fatalf("violated = %v, want %v", violated, tc.violated)
			}
		})
	}
}

func TestArtifactChecks(t *testing.T) {
	art := func(channel string, attachments ...string) deterministic.Output {
		return deterministic.Output{FinishedArtifact: deterministic.Artifact{
			Channel: channel, Body: "b", Attachments: attachments}}
	}
	for name, tc := range map[string]struct {
		change   LiteralChange
		draft    deterministic.Output
		violated bool
	}{
		"channel unchanged":  {LiteralChange{Kind: "channel_changed", After: "slack"}, art("slack"), false},
		"channel reverted":   {LiteralChange{Kind: "channel_changed", After: "slack"}, art("email"), true},
		"attachment missing": {LiteralChange{Kind: "attachment_added", After: "acme-soc2.pdf"}, art("email"), true},
		"attachment present, case-insensitive and whitespace-collapsed": {
			LiteralChange{Kind: "attachment_added", After: "acme soc2.pdf"},
			art("email", "Acme   SOC2.PDF"), false},
		"attachment removed, still carried": {LiteralChange{Kind: "attachment_removed", Before: "deck.pdf"},
			art("email", "deck.pdf"), true},
		"attachment removed, gone":      {LiteralChange{Kind: "attachment_removed", Before: "deck.pdf"}, art("email"), false},
		"empty after produces no check": {LiteralChange{Kind: "channel_changed", After: ""}, art("email"), false},
	} {
		t.Run(name, func(t *testing.T) {
			violated, _ := Spec{LiteralChanges: []LiteralChange{tc.change}}.RunSpec(tc.draft)
			if violated != tc.violated {
				t.Fatalf("violated = %v, want %v", violated, tc.violated)
			}
		})
	}
}

func TestSemanticKindsProduceNoCheck(t *testing.T) {
	for _, kind := range []string{"subject_changed", "cta_changed", "timing_changed", "paragraph_added",
		"paragraph_removed", "paragraph_edited", "action_type_changed", "crm_next_step_changed"} {
		s := Spec{LiteralChanges: []LiteralChange{{Kind: kind, After: "x"}}}
		if s.Executable() {
			t.Fatalf("%s produced an executable check; semantic edits never do", kind)
		}
		violated, _ := s.RunSpec(draft())
		if violated {
			t.Fatalf("%s violated a spec with no checks", kind)
		}
	}
}

func TestSpecKinds(t *testing.T) {
	exec := Spec{LiteralChanges: []LiteralChange{{Kind: "channel_changed", After: "slack"}}}
	if !exec.Executable() || !exec.Agnostic() {
		t.Fatal("a channel change is executable and world-agnostic")
	}
	bound := Spec{LiteralChanges: []LiteralChange{
		{Kind: "recipient_added", After: map[string]any{"person_id": marco.PersonID, "role": "to"}}}}
	if !bound.Executable() || bound.Agnostic() {
		t.Fatal("a recipient change is executable but bound to its account's people")
	}
	if (Spec{}).Executable() || (Spec{}).Agnostic() {
		t.Fatal("an empty spec is neither executable nor agnostic")
	}
}

func TestRunSpecDetailNamesTheViolations(t *testing.T) {
	s := Spec{LiteralChanges: []LiteralChange{
		{Kind: "channel_changed", After: "slack"},
		{Kind: "recipient_added", After: map[string]any{"person_id": marco.PersonID, "role": "to"}},
	}}
	violated, detail := s.RunSpec(draft(marco))
	if !violated || !strings.Contains(detail, "channel") || strings.Contains(detail, "omits recipient") {
		t.Fatalf("violated=%v detail=%q — only the channel predicate should have failed", violated, detail)
	}
	violated, detail = s.RunSpec(deterministic.Output{Recipients: []deterministic.Recipient{marco},
		FinishedArtifact: deterministic.Artifact{Channel: "slack"}})
	if violated || !strings.Contains(detail, "slack") {
		t.Fatalf("violated=%v detail=%q — a corrected draft passes with the satisfied predicates named", violated, detail)
	}
}

func TestParseSpec(t *testing.T) {
	if s, err := parseSpec(nil); err != nil || s.Executable() {
		t.Fatalf("empty spec = %+v %v", s, err)
	}
	if s, err := parseSpec([]byte("null")); err != nil || s.Executable() {
		t.Fatalf("null spec = %+v %v", s, err)
	}
	if _, err := parseSpec([]byte("{")); !errors.Is(err, ErrInvalidSpec) {
		t.Fatalf("bad spec error = %v, want ErrInvalidSpec", err)
	}
	s, err := parseSpec([]byte(`{"account_id":"a1","literal_changes":[{"kind":"channel_changed","after":"slack"}]}`))
	if err != nil || s.AccountID != "a1" || !s.Executable() {
		t.Fatalf("spec = %+v %v", s, err)
	}
}

func TestSeedUUIDIsDeterministicAndShaped(t *testing.T) {
	a, b := seedUUID("knowledge", "d1"), seedUUID("knowledge", "d1")
	if a != b {
		t.Fatal("same input must derive the same id")
	}
	if c := seedUUID("knowledge", "d2"); c == a {
		t.Fatal("different input derived the same id")
	}
	if len(a) != 36 || a[8] != '-' || a[13] != '-' || a[18] != '-' || a[23] != '-' || a[14] != '5' {
		t.Fatalf("id %q is not a version-5-shaped UUID", a)
	}
}

func TestSituationSignatures(t *testing.T) {
	sig, app := Situation{TransitionStatus: "CANDIDATE", TransitionTo: "EXPANSION", TransitionFrom: "NEW_LOGO"}.Signature()
	if len(sig) != 1 || sig[0].Field != "transition.status" || sig[0].Value != "CANDIDATE" {
		t.Fatalf("transition signature = %+v", sig)
	}
	if len(app) != 2 || app[0].Field != "transition.to_state" || app[1].Field != "transition.from_state" {
		t.Fatalf("transition applicability = %+v", app)
	}
	sig, app = Situation{Relationship: "EXPANSION"}.Signature()
	if len(sig) != 1 || sig[0].Field != "relationship_state" || sig[0].Op != "eq" || len(app) != 0 {
		t.Fatalf("relationship signature = %+v %+v", sig, app)
	}
	sig, _ = Situation{}.Signature()
	if len(sig) != 1 || sig[0].Op != "is_unknown" {
		t.Fatalf("an unknown situation still produces a signature: %+v", sig)
	}
}

func TestAxisVocabulary(t *testing.T) {
	for _, axis := range []string{"recipient_correctness", "cta_calibration", "human_delta", "trajectory"} {
		if !ValidAxis(axis) {
			t.Fatalf("%s is a catalogued eval axis", axis)
		}
	}
	if ValidAxis("made_up_axis") || ValidAxis("") {
		t.Fatal("an invented axis is not valid")
	}
	if KindOf("recipient_correctness") != "deterministic" || KindOf("cta_calibration") != "semantic" ||
		KindOf("trajectory") != "trace" || KindOf("human_delta") != "human_delta" {
		t.Fatal("KindOf mislabels an axis kind")
	}
}

func TestClipIsRuneSafe(t *testing.T) {
	if got := clip("short", 10); got != "short" {
		t.Fatalf("clip = %q", got)
	}
	long := strings.Repeat("é", 100)
	if got := clip(long, 10); len([]rune(got)) != 10 || !strings.HasSuffix(got, "…") {
		t.Fatalf("clip(%d runes) = %q", len([]rune(long)), got)
	}
}

func TestGateReason(t *testing.T) {
	if err := gateReason(true, "never"); err != nil {
		t.Fatal(err)
	}
	if err := gateReason(false, "nope %d", 7); !errors.Is(err, ErrGate) || !strings.Contains(err.Error(), "nope 7") {
		t.Fatalf("err = %v", err)
	}
}
