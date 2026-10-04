// Package abcrun is the Go half of the blind A/B/C knowledge-uplift experiment (HAR-128 WP30, HAR-129 'Required
// knowledge-uplift experiment'): it learns the experiment's knowledge store through the real lifecycle, builds each
// test situation's world through the real ingest and recompute path, and runs every arm through the real
// orchestrator and the real worker. Scoring, statistics and the report live in Python (bench/uplift).
//
// A Pack is the frozen, committed input: the events of each situation up to its trigger, the vendor's employees,
// and the planted facts a ground-truth extractor reports for the rendered customer emails.
package abcrun

import (
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/claims"
	"github.com/harneet2512/gtm-work/core-go/internal/graph"
	"github.com/harneet2512/gtm-work/core-go/internal/normalize"
)

// PackVersion is written into every pack and refused when it differs.
const PackVersion = "abc_pack.v1"

// Trigger is the customer email that opens the agent run (reason eligible_customer_replied).
type Trigger struct {
	SourceObjectID string    `json:"source_object_id"`
	SourceEventKey string    `json:"source_event_key"`
	OccurredAt     time.Time `json:"occurred_at"`
}

// Person is one account contact as the agent will see it (display and scoring only).
type Person struct {
	Email string `json:"email"`
	Name  string `json:"name"`
	Title string `json:"title"`
	Side  string `json:"side"` // buyer | seller
}

// Situation is one test situation of the experiment.
type Situation struct {
	ID              string  `json:"id"`
	Kind            string  `json:"kind"` // discriminating | exception
	DecisionPoint   string  `json:"decision_point"`
	DealID          string  `json:"deal_id"`
	AccountID       string  `json:"account_id"`
	SelectionReason string  `json:"selection_reason"`
	Trigger         Trigger `json:"trigger"`
	// Events are every event of the situation up to and including the trigger, in ingest order.
	Events []normalize.SourceEvent `json:"events"`
	// Truth maps the source_object_id of a customer email to the planted facts a perfect extractor reports.
	Truth  map[string][]claims.Candidate `json:"truth_claims"`
	People []Person                      `json:"people"`
}

// Pack is the whole frozen input.
type Pack struct {
	Version    string        `json:"version"`
	Company    graph.Company `json:"company"`
	Situations []Situation   `json:"situations"`
}

// LoadPack reads and checks a pack.
func LoadPack(path string) (Pack, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Pack{}, fmt.Errorf("abcrun: read pack: %w", err)
	}
	var p Pack
	if err := json.Unmarshal(raw, &p); err != nil {
		return Pack{}, fmt.Errorf("abcrun: decode pack %s: %w", path, err)
	}
	return p, p.Check()
}

// Check refuses a pack that cannot be run: wrong version, no situations, a situation without its trigger event.
func (p Pack) Check() error {
	if p.Version != PackVersion {
		return fmt.Errorf("abcrun: pack version %q, want %q", p.Version, PackVersion)
	}
	if len(p.Situations) == 0 {
		return fmt.Errorf("abcrun: the pack has no situations")
	}
	seen := map[string]bool{}
	for _, s := range p.Situations {
		if seen[s.ID] {
			return fmt.Errorf("abcrun: duplicate situation id %s", s.ID)
		}
		seen[s.ID] = true
		if s.Kind != "discriminating" && s.Kind != "exception" && s.Kind != "control" {
			return fmt.Errorf("abcrun: situation %s has kind %q", s.ID, s.Kind)
		}
		if !s.hasTrigger() {
			return fmt.Errorf("abcrun: situation %s does not contain its trigger event %s/%s", s.ID, s.Trigger.SourceObjectID, s.Trigger.SourceEventKey)
		}
		for _, e := range s.Events {
			if e.OccurredAt != nil && e.OccurredAt.After(s.Trigger.OccurredAt) {
				return fmt.Errorf("abcrun: situation %s holds an event dated after its trigger (future leakage): %s", s.ID, e.SourceObjectID)
			}
		}
	}
	return nil
}

func (s Situation) hasTrigger() bool {
	for _, e := range s.Events {
		if e.SourceObjectID == s.Trigger.SourceObjectID && e.SourceEventKey == s.Trigger.SourceEventKey {
			return true
		}
	}
	return false
}
