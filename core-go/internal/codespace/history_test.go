package codespace

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeCase(t *testing.T, dir, manifest, state string) {
	t.Helper()
	writeTree(t, dir, map[string]string{"manifest.json": manifest, "state.json": state})
}

func TestReadCaseHistoryReadsEventsOneToNMinusOneAndTheHeldOutEventN(t *testing.T) {
	dir := t.TempDir()
	writeCase(t, dir, manifestJSON(4, "held"), `{}`)
	h, err := ReadCaseHistory(filepath.Join(dir, "manifest.json"), "case1")
	if err != nil {
		t.Fatal(err)
	}
	if h.HistoryEvents != 4 || h.ThroughPosition != 4 || h.EventNPosition != 5 || h.HeldOutEventID != "held" || h.Slot != "case1" {
		t.Fatalf("history = %+v", h)
	}
}

func TestReadCaseHistoryRejectsAnInconsistentFreeze(t *testing.T) {
	cases := map[string]string{
		"gap in the history":     `{"events":[{"event":{"replay_position":1}},{"event":{"replay_position":3}}],"held_out_event":{"event_id":"h","replay_position":4}}`,
		"Event N inside history": `{"events":[{"event":{"replay_position":1}},{"event":{"replay_position":2}}],"held_out_event":{"event_id":"h","replay_position":2}}`,
		"Event N far after":      `{"events":[{"event":{"replay_position":1}}],"held_out_event":{"event_id":"h","replay_position":5}}`,
		"no history":             `{"events":[],"held_out_event":{"event_id":"h","replay_position":1}}`,
		"no held-out event id":   `{"events":[{"event":{"replay_position":1}}],"held_out_event":{"replay_position":2}}`,
		"not json":               `{nope`,
	}
	for name, body := range cases {
		dir := t.TempDir()
		writeCase(t, dir, body, `{}`)
		if _, err := ReadCaseHistory(filepath.Join(dir, "manifest.json"), "case1"); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if _, err := ReadCaseHistory(filepath.Join(t.TempDir(), "absent.json"), "case1"); err == nil {
		t.Error("a missing manifest must be an error")
	}
}

func TestCheckSealedCaseRequiresEventNToBeUnreleasedAndTheSameAsTheManifest(t *testing.T) {
	casesDir := t.TempDir()
	dir := filepath.Join(casesDir, "case1")
	good := manifestJSON(2, "held")
	want, err := func() (CaseHistory, error) {
		writeCase(t, dir, good, `{"held_out_event_id":"held","played_at":"0001-01-01T00:00:00Z"}`)
		return ReadCaseHistory(filepath.Join(dir, "manifest.json"), "case1")
	}()
	if err != nil {
		t.Fatal(err)
	}
	if err := checkSealedCase(casesDir, want); err != nil {
		t.Fatalf("a baseline at N-1 must pass: %v", err)
	}
	for name, state := range map[string]string{
		"released":       `{"held_out_event_id":"held","played_at":"2026-10-05T10:00:00Z"}`,
		"other held-out": `{"held_out_event_id":"other"}`,
		"not json":       `{nope`,
	} {
		writeCase(t, dir, good, state)
		if err := checkSealedCase(casesDir, want); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	_ = os.Remove(filepath.Join(dir, "state.json"))
	if err := checkSealedCase(casesDir, want); err == nil || !strings.Contains(err.Error(), "sealed state") {
		t.Errorf("a missing state must be reported: %v", err)
	}
	writeCase(t, dir, manifestJSON(3, "held"), `{"held_out_event_id":"held"}`) // history changed after the manifest recorded it
	if err := checkSealedCase(casesDir, want); err == nil || !strings.Contains(err.Error(), "recorded") {
		t.Errorf("a changed history must be reported: %v", err)
	}
	_ = os.Remove(filepath.Join(dir, "manifest.json"))
	if err := checkSealedCase(casesDir, want); err == nil {
		t.Error("a missing manifest must be reported")
	}
}
