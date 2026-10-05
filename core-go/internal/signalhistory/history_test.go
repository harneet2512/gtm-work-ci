package signalhistory_test

import (
	"testing"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/signalhistory"
)

var t0 = time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC)

func end(d time.Duration) *time.Time { e := t0.Add(d); return &e }

func ids(records []signalhistory.Record, opp string, at time.Time) []string {
	out := []string{}
	for _, s := range signalhistory.OpenAsOf(records, opp, at) {
		out = append(out, s.ID)
	}
	return out
}

func TestWindowAndOccurrenceBoundTheQuery(t *testing.T) {
	recs := []signalhistory.Record{
		{ID: "event", Type: "new_stakeholder_entered", OccurredAt: t0, ExpiresAt: end(14 * 24 * time.Hour)},
		{ID: "standing", Type: "security_blocker_appeared", OccurredAt: t0},
	}
	cases := []struct {
		at   time.Time
		want int
	}{
		{t0.Add(-time.Second), 0},        // not yet occurred
		{t0, 2},                          // the moment it occurred
		{t0.Add(14 * 24 * time.Hour), 2}, // last instant of the window
		{t0.Add(14*24*time.Hour + 1), 1}, // the event expired; the standing signal is left to the state
		{t0.Add(365 * 24 * time.Hour), 1},
	}
	for _, c := range cases {
		if got := ids(recs, "", c.at); len(got) != c.want {
			t.Errorf("at %s: %v", c.at, got)
		}
	}
}

func TestAnEarlierDiffsSignalIsStillOpenLater(t *testing.T) {
	recs := []signalhistory.Record{{ID: "k17", Type: "new_stakeholder_entered", OccurredAt: t0, ExpiresAt: end(14 * 24 * time.Hour)}}
	if got := ids(recs, "", t0.Add(5*24*time.Hour)); len(got) != 1 {
		t.Fatalf("a signal from a diff five days ago must be visible: %v", got)
	}
}

func TestOpportunityScoping(t *testing.T) {
	recs := []signalhistory.Record{
		{ID: "acct-level", Type: "customer_replied", OccurredAt: t0, ExpiresAt: end(time.Hour)},
		{ID: "opp-a", Type: "customer_replied", OpportunityID: "a", OccurredAt: t0, ExpiresAt: end(time.Hour)},
		{ID: "opp-b", Type: "customer_replied", OpportunityID: "b", OccurredAt: t0, ExpiresAt: end(time.Hour)},
	}
	if got := ids(recs, "a", t0); len(got) != 2 || got[0] != "acct-level" || got[1] != "opp-a" {
		t.Fatalf("got %v", got)
	}
	if got := ids(recs, "", t0); len(got) != 1 || got[0] != "acct-level" {
		t.Fatalf("no opportunity sees only account-level signals: %v", got)
	}
}

func TestResultIsOldestFirstAndCarriesTheSubject(t *testing.T) {
	recs := []signalhistory.Record{
		{ID: "late", Type: "security_blocker_appeared", OccurredAt: t0.Add(time.Hour), SubjectClaimID: "c1", SubjectItemKey: "soc2"},
		{ID: "early", Type: "security_blocker_appeared", OccurredAt: t0},
	}
	got := signalhistory.OpenAsOf(recs, "", t0.Add(2*time.Hour))
	if got[0].ID != "early" || got[1].ID != "late" || got[1].SubjectClaimID == nil || *got[1].SubjectClaimID != "c1" || got[1].SubjectItemKey != "soc2" {
		t.Fatalf("got %+v", got)
	}
	if !got[1].CreatedAt.Equal(t0.Add(time.Hour)) {
		t.Fatal("matcher time is occurred_at")
	}
}
