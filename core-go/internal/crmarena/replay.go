package crmarena

import (
	"fmt"
	"sort"
)

// KnowledgeInputs are the events the knowledge layer may learn from at the cutoff: events of previous
// deals dated strictly before the cutoff, with every date-valued field at or after the cutoff removed.
// Current deals never are visible, and account-level records (accounts, contacts, cases, chats, orders)
// belong to no deal and are deliberately not deal knowledge.
func KnowledgeInputs(events []Event, s Split) ([]Event, error) {
	out, _, err := KnowledgeInputsWithReport(events, s)
	return out, err
}

// KnowledgeInputsWithReport is KnowledgeInputs plus a count of the redacted fields. The events passed
// in are not modified: a redacted event is a copy with a new payload.
func KnowledgeInputsWithReport(events []Event, s Split) ([]Event, Redactions, error) {
	red := Redactions{ByField: map[string]int{}}
	cutoff, err := s.CutoffTime()
	if err != nil {
		return nil, red, err
	}
	previous := s.PreviousSet()
	var out []Event
	for _, e := range events {
		if !previous[e.DealID] || !e.OccurredAt().Before(cutoff) {
			continue
		}
		payload, removed, err := redactLate([]byte(e.Source.Payload), cutoff)
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
	return out, red, nil
}

// ReplayedWith reports whether the event belongs to the replay of one of the deals: it is an event of
// the deal, or the creation of a contact whose first appearance is that deal's email (the contact
// arrives together with the first change that names them).
func (e Event) ReplayedWith(deals map[string]bool) bool {
	return deals[e.DealID] || (e.introducedBy != "" && deals[e.introducedBy])
}

// Replay returns one current deal's events in date order (ties by source identity): the change-by-change
// stream the account agent is replayed on.
func Replay(events []Event, s Split, dealID string) ([]Event, error) {
	if !s.CurrentSet()[dealID] {
		return nil, fmt.Errorf("crmarena: deal %s is not a current deal of the split", dealID)
	}
	var out []Event
	want := map[string]bool{dealID: true}
	for _, e := range events {
		if e.ReplayedWith(want) {
			out = append(out, e)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return ingestLess(out[i], out[j]) })
	return out, nil
}
