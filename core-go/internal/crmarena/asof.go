package crmarena

import (
	"fmt"
	"sort"
	"time"
)

// AsOf is the timeline as it stood at until: the events dated strictly before it, in ingest order,
// with every date-valued payload field at or after it removed (a quote's ExpirationDate or a
// contract's StartDate are future-dated values of records that happened earlier; an agent standing at
// until must not see them). Nothing dated at or after until is returned, so a current deal's final
// stage and quote status (dated at its last event) never are. The input events are not modified.
func AsOf(events []Event, until time.Time) ([]Event, Redactions, error) {
	red := Redactions{ByField: map[string]int{}}
	var out []Event
	for _, e := range events {
		if !e.OccurredAt().Before(until) {
			continue
		}
		payload, removed, err := redactLate([]byte(e.Source.Payload), until)
		if err != nil {
			return nil, red, fmt.Errorf("%s/%s: %w", e.Source.SourceObjectID, e.Source.SourceEventKey, err)
		}
		if len(removed) > 0 {
			e.Source.Payload = payload
			red.Events++
			red.Fields += len(removed)
			for _, name := range removed {
				red.ByField[name]++
			}
		}
		out = append(out, e)
	}
	sort.SliceStable(out, func(i, j int) bool { return ingestLess(out[i], out[j]) })
	return out, red, nil
}

// Between returns the events with from <= occurred_at < to in ingest order (date, phase, identity),
// the stream a replay feeds change by change. A zero to leaves the window open at the end. It is the
// complement of AsOf: Between(events, until, time.Time{}) holds exactly what AsOf withheld.
func Between(events []Event, from, to time.Time) []Event {
	var out []Event
	for _, e := range events {
		at := e.OccurredAt()
		if at.Before(from) || (!to.IsZero() && !at.Before(to)) {
			continue
		}
		out = append(out, e)
	}
	sort.SliceStable(out, func(i, j int) bool { return ingestLess(out[i], out[j]) })
	return out
}

// ReplayRemaining is Replay restricted to what happens at or after the split's cutoff: the changes an
// agent is fed one by one after being started on AsOf(cutoff).
func ReplayRemaining(events []Event, s Split, dealID string) ([]Event, error) {
	cutoff, err := s.CutoffTime()
	if err != nil {
		return nil, err
	}
	all, err := Replay(events, s, dealID)
	if err != nil {
		return nil, err
	}
	return Between(all, cutoff, time.Time{}), nil
}
