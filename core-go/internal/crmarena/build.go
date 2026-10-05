package crmarena

import (
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/normalize"
)

// Connector identity stamped on every event.
const (
	Connector        = "crmarena-loader"
	ConnectorVersion = "wp31-v1"
	// IntegrationActor makes changes no rep made: records owned by the org's data-loading admin and
	// values that only exist as of the snapshot.
	IntegrationActor = "integration:crmarena"
)

// Ingest phases: at equal times an account precedes its contacts, which precede opportunity creation.
const (
	phaseAccount = iota
	phaseContact
	phaseOpportunity
	phaseActivity
	// phaseSnapshot: values known only as of the snapshot (final stage, quote status) are dated at the
	// deal's last activity and must sort after it, never before it.
	phaseSnapshot
)

// Event is one SourceEvent plus the Salesforce records it belongs to.
type Event struct {
	Source    normalize.SourceEvent
	AccountID string // Salesforce account id
	DealID    string // Salesforce opportunity id; "" for account-level records
	// introducedBy is, for a contact's creation event, the deal whose first email names the contact: the
	// contact arrives with that deal's replayed event. "" otherwise.
	introducedBy string
	phase        int
}

// OccurredAt is when the event happened in the source's world (UTC).
func (e Event) OccurredAt() time.Time { return *e.Source.OccurredAt }

// Stats counts the loader's derived links and anomalies (reported by ghostctl and the data card).
type Stats struct {
	DerivedDomains        int // accounts whose domain comes from their contacts' addresses
	AccountsWithoutDomain int // contacts disagree or are missing: no domain hint
	RepliesLinked         int // "Re:" emails linked to an earlier email of the same deal and subject
	RepliesUnlinked       int // "Re:" emails with no earlier email to answer
	// DealsCreatedAtFirstActivity: deals whose email or task predates CreatedDate; creation is moved
	// back to that first activity so the activity can attach to the deal.
	DealsCreatedAtFirstActivity int
}

// Result is the loader output: events in ingest order, the reps, and stats.
type Result struct {
	Events []Event
	Reps   Reps
	Stats  Stats
}

// SourceEvents returns the events' SourceEvents in ingest order.
func (r Result) SourceEvents() []normalize.SourceEvent {
	out := make([]normalize.SourceEvent, len(r.Events))
	for i, e := range r.Events {
		out[i] = e.Source
	}
	return out
}

// Epoch is the time of the first event; seeded identities start then.
func (r Result) Epoch() time.Time {
	if len(r.Events) == 0 {
		return time.Time{}
	}
	return r.Events[0].OccurredAt()
}

// Build maps a snapshot onto SourceEvents in ingest order. Every event is checked by the normalizer,
// so a mapping that breaks the contract fails here rather than at ingest.
func Build(s Snapshot) (Result, error) {
	if err := checkContracts(s); err != nil {
		return Result{}, err
	}
	reps, err := buildReps(s)
	if err != nil {
		return Result{}, err
	}
	windows, err := Windows(s)
	if err != nil {
		return Result{}, err
	}
	b := &builder{snap: s, reps: reps, windows: windows, idx: newIndex(s, reps)}
	for _, step := range []func() error{b.opportunities, b.emails, b.tasks, b.quotes, b.contracts, b.orders, b.cases, b.chats} {
		if err := step(); err != nil {
			return Result{}, err
		}
	}
	if err := b.masterData(); err != nil {
		return Result{}, err
	}
	sortEvents(b.events)
	for _, e := range b.events {
		if _, err := normalize.Normalize(e.Source); err != nil {
			return Result{}, fmt.Errorf("crmarena: %s/%s/%s does not normalize: %w",
				e.Source.SourceSystem, e.Source.SourceObjectID, e.Source.SourceEventKey, err)
		}
	}
	return Result{Events: b.events, Reps: reps, Stats: b.stats}, nil
}

type builder struct {
	snap    Snapshot
	reps    Reps
	windows map[string]Window
	idx     index
	events  []Event
	stats   Stats
}

// add marshals the payload and appends one event.
func (b *builder) add(system, objectID, key string, at time.Time, payload any, acct, deal string, phase int) error {
	raw, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("crmarena: marshal %s/%s: %w", system, objectID, err)
	}
	at = at.UTC()
	b.events = append(b.events, Event{
		Source: normalize.SourceEvent{SourceSystem: system, SourceObjectID: objectID, SourceEventKey: key, OccurredAt: &at,
			Connector: Connector, ConnectorVersion: ConnectorVersion, Payload: raw},
		AccountID: acct, DealID: deal, phase: phase,
	})
	return nil
}

// actor is the rep's tenant address, or the integration actor for records no rep owns.
func (b *builder) actor(userID string) string {
	if a, ok := b.reps.ByUser(userID); ok {
		return a
	}
	return IntegrationActor
}

// sortEvents orders by time, then phase, then identity: a total, deterministic ingest order.
func sortEvents(events []Event) {
	sort.SliceStable(events, func(i, j int) bool { return ingestLess(events[i], events[j]) })
}

// ingestLess is the ingest order: time, then phase, then source identity.
func ingestLess(a, c Event) bool {
	if !a.OccurredAt().Equal(c.OccurredAt()) {
		return a.OccurredAt().Before(c.OccurredAt())
	}
	if a.phase != c.phase {
		return a.phase < c.phase
	}
	if a.Source.SourceObjectID != c.Source.SourceObjectID {
		return a.Source.SourceObjectID < c.Source.SourceObjectID
	}
	return a.Source.SourceEventKey < c.Source.SourceEventKey
}

// strPtr returns nil for "" so optional payload fields serialize as null.
func strPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
