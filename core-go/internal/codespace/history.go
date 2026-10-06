package codespace

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// CaseHistory is what the sealed baseline holds for one case, read from its frozen manifest: history events 1..N-1 went
// through the real pipeline once (the freeze), Event N is held out. The default Start and Reset restore exactly this and
// never process those events again.
type CaseHistory struct {
	Slot string `json:"slot"`
	// HistoryEvents is how many events were replayed; ThroughPosition is the position of the last one (N-1) and EventNPosition
	// the position of the held-out event (N). The positions of the history are exactly 1..N-1.
	HistoryEvents   int    `json:"history_events"`
	ThroughPosition int    `json:"through_position"`
	EventNPosition  int    `json:"event_n_position"`
	HeldOutEventID  string `json:"held_out_event_id"`
}

type frozenManifest struct {
	Events []struct {
		Event struct {
			ReplayPosition int `json:"replay_position"`
		} `json:"event"`
	} `json:"events"`
	HeldOut struct {
		EventID        string `json:"event_id"`
		ReplayPosition int    `json:"replay_position"`
	} `json:"held_out_event"`
}

// ReadCaseHistory reads a case's history facts from its frozen manifest and checks they are consistent: positions 1..N-1 with no
// gap, the held-out event at N, and nothing at or after N among the history events.
func ReadCaseHistory(manifestPath, slot string) (CaseHistory, error) {
	b, err := os.ReadFile(manifestPath)
	if err != nil {
		return CaseHistory{}, fmt.Errorf("codespace: read the frozen manifest of %s: %w", slot, err)
	}
	var m frozenManifest
	if err := json.Unmarshal(b, &m); err != nil {
		return CaseHistory{}, fmt.Errorf("codespace: the frozen manifest of %s is not valid: %w", slot, err)
	}
	h := CaseHistory{Slot: slot, HistoryEvents: len(m.Events), EventNPosition: m.HeldOut.ReplayPosition, HeldOutEventID: m.HeldOut.EventID}
	for i, e := range m.Events {
		if e.Event.ReplayPosition != i+1 {
			return h, fmt.Errorf("codespace: %s history event %d sits at replay position %d, want %d", slot, i+1, e.Event.ReplayPosition, i+1)
		}
		h.ThroughPosition = e.Event.ReplayPosition
	}
	switch {
	case h.HistoryEvents == 0:
		return h, fmt.Errorf("codespace: %s has no history events", slot)
	case h.EventNPosition != h.ThroughPosition+1:
		return h, fmt.Errorf("codespace: %s holds out Event N at position %d, but its history ends at %d", slot, h.EventNPosition, h.ThroughPosition)
	case h.HeldOutEventID == "":
		return h, fmt.Errorf("codespace: %s has no held-out Event N", slot)
	}
	return h, nil
}

// savedState is the part of a case's seed state that says whether Event N was released.
type savedState struct {
	PlayedAt string `json:"played_at"`
	Held     string `json:"held_out_event_id"`
}

// checkSealedCase checks one case under a cases directory of a baseline: the history facts equal what the manifest recorded, and the seed
// state says Event N was never released (so the baseline stops at N-1).
func checkSealedCase(casesDir string, want CaseHistory) error {
	dir := filepath.Join(casesDir, want.Slot)
	got, err := ReadCaseHistory(filepath.Join(dir, "manifest.json"), want.Slot)
	if err != nil {
		return err
	}
	if got != want {
		return fmt.Errorf("codespace: the sealed history of %s is %+v, the baseline manifest recorded %+v", want.Slot, got, want)
	}
	b, err := os.ReadFile(filepath.Join(dir, "state.json"))
	if err != nil {
		return fmt.Errorf("codespace: read the sealed state of %s: %w", want.Slot, err)
	}
	var st savedState
	if err := json.Unmarshal(b, &st); err != nil {
		return fmt.Errorf("codespace: the sealed state of %s is not valid: %w", want.Slot, err)
	}
	if st.PlayedAt != "" && st.PlayedAt != "0001-01-01T00:00:00Z" {
		return fmt.Errorf("codespace: the sealed state of %s already released Event N (%s)", want.Slot, st.PlayedAt)
	}
	if st.Held != want.HeldOutEventID {
		return fmt.Errorf("codespace: the sealed state of %s holds out a different event than its manifest", want.Slot)
	}
	return nil
}
