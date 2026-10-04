package demorun

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// DemoState is what `demo seed` and `demo play` remember for `demo play` and `demo verify`: identifiers only, never
// a secret.
type DemoState struct {
	ManifestID     string    `json:"manifest_id"`
	AccountID      string    `json:"account_id"`
	OpportunityID  string    `json:"opportunity_id"`
	CaseName       string    `json:"case_name"`
	HeldOutEventID string    `json:"held_out_event_id"`
	SeededAt       time.Time `json:"seeded_at"`
	// InvisibilityAtSeed is core's event-N-invisible status right after the seed ("withheld" when clean).
	InvisibilityAtSeed string `json:"invisibility_at_seed,omitempty"`

	PlayedAt        time.Time `json:"played_at,omitempty"`
	AccountChangeID string    `json:"account_change_id,omitempty"`
	BIUpdateID      string    `json:"bi_update_id,omitempty"`
	RunID           string    `json:"run_id,omitempty"`
	StrategySetID   string    `json:"strategy_set_id,omitempty"`
	EpisodeID       string    `json:"episode_id,omitempty"`
	SlackWasOn      bool      `json:"slack_was_on,omitempty"`
}

// LoadState reads the state file; a missing file is ok=false, not an error.
func LoadState(path string) (DemoState, bool, error) {
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return DemoState{}, false, nil
	}
	if err != nil {
		return DemoState{}, false, fmt.Errorf("demorun: read %s: %w", path, err)
	}
	var s DemoState
	if err := json.Unmarshal(b, &s); err != nil {
		return DemoState{}, false, fmt.Errorf("demorun: %s is not valid state (delete it or run `demo reset --yes`)", path)
	}
	return s, true, nil
}

// SaveState writes the state atomically.
func SaveState(path string, s DemoState) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("demorun: create %s: %w", filepath.Dir(path), err)
	}
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o600); err != nil {
		return fmt.Errorf("demorun: write state: %w", err)
	}
	return os.Rename(tmp, path)
}

// RequireSeeded fails with the next command to run when no case has been frozen.
func (s DemoState) RequireSeeded() error {
	if s.ManifestID == "" || s.AccountID == "" {
		return errors.New("demorun: nothing is seeded yet; run `demo seed` first")
	}
	return nil
}

// RequirePlayed fails with the next command to run when Event N has not been released.
func (s DemoState) RequirePlayed() error {
	if err := s.RequireSeeded(); err != nil {
		return err
	}
	if s.PlayedAt.IsZero() {
		return errors.New("demorun: Event N has not been released yet; run `demo play` first")
	}
	return nil
}
