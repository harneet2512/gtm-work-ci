package demorun

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestStateRoundTripAndMissingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "state.json")
	if _, ok, err := LoadState(path); err != nil || ok {
		t.Fatalf("a missing state is not an error: ok=%v err=%v", ok, err)
	}
	want := DemoState{ManifestID: "m-1", AccountID: "a-1", OpportunityID: "006X", CaseName: "MedTech Advances",
		HeldOutEventID: "e-1", SeededAt: time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC), RunID: "r-1"}
	if err := SaveState(path, want); err != nil {
		t.Fatal(err)
	}
	got, ok, err := LoadState(path)
	if err != nil || !ok || got.ManifestID != "m-1" || got.CaseName != "MedTech Advances" || got.RunID != "r-1" || !got.SeededAt.Equal(want.SeededAt) {
		t.Fatalf("round trip: %+v ok=%v err=%v", got, ok, err)
	}
}

func TestLoadStateRejectsGarbageWithoutEchoingIt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	if err := os.WriteFile(path, []byte(`{"manifest_id": "super-secret-looking-text`), 0o600); err != nil {
		t.Fatal(err)
	}
	_, _, err := LoadState(path)
	if err == nil || strings.Contains(err.Error(), "super-secret") {
		t.Fatalf("want a parse error that does not echo content, got %v", err)
	}
}

func TestStateRequireSeededAndPlayed(t *testing.T) {
	if err := (DemoState{}).RequireSeeded(); err == nil || !strings.Contains(err.Error(), "demo seed") {
		t.Fatalf("an unseeded state must point at `demo seed`: %v", err)
	}
	if err := (DemoState{ManifestID: "m", AccountID: "a"}).RequireSeeded(); err != nil {
		t.Fatal(err)
	}
	if err := (DemoState{ManifestID: "m", AccountID: "a"}).RequirePlayed(); err == nil || !strings.Contains(err.Error(), "demo play") {
		t.Fatalf("an unplayed state must point at `demo play`: %v", err)
	}
	if err := (DemoState{ManifestID: "m", AccountID: "a", PlayedAt: time.Now()}).RequirePlayed(); err != nil {
		t.Fatal(err)
	}
}
