package crmarena

import (
	"fmt"
	"time"
)

// ReplayDeal is the opportunity whose replay the event belongs to: its own deal, or for a contact's creation
// the deal whose first email names the contact. "" for account-level records that belong to no deal.
func (e Event) ReplayDeal() string {
	if e.DealID != "" {
		return e.DealID
	}
	return e.introducedBy
}

// AsKnown returns the event as the world knew it when it happened: every date-valued payload field dated
// after the event itself is removed (a quote's ExpirationDate, a deal's CloseDate are future-dated values of
// a record that happened earlier). Replaying each event this way makes the state after event k a function
// of events 1..k alone, whatever the cut, so a case mined over the full timeline freezes to the same states.
// The count is the number of fields removed. The input is not modified.
func (e Event) AsKnown() (Event, int, error) {
	// A field dated at the event's own instant is known; the cutoff is therefore one nanosecond later.
	cutoff := e.OccurredAt().Add(time.Nanosecond)
	payload, removed, err := redactLate([]byte(e.Source.Payload), cutoff)
	if err != nil {
		return e, 0, fmt.Errorf("%s/%s: %w", e.Source.SourceObjectID, e.Source.SourceEventKey, err)
	}
	if len(removed) > 0 {
		e.Source.Payload = payload
	}
	return e, len(removed), nil
}
