package graph_test

import (
	"testing"
	"time"
)

// Closing the existing holder at a time not after its own valid_from must still satisfy
// relationships_interval (valid_to > valid_from).
func TestExclusiveUpsertAtAnEarlierTimeThanTheHolderDoesNotViolateTheInterval(t *testing.T) {
	f := newEdgeFixture(t)
	first := spec(f, f.priya, "champion_for", "first_party_ai")
	first.ValidFrom = t0.Add(48 * time.Hour)
	if _, err := upsert(t, first); err != nil {
		t.Fatal(err)
	}
	earlier := spec(f, f.tom, "champion_for", "first_party_ai")
	earlier.Exclusive = true
	earlier.ValidFrom = t0 // older evidence arriving later

	if _, err := upsert(t, earlier); err != nil {
		t.Fatalf("exclusive upsert at an earlier time failed: %v", err)
	}

	if got := openCount(t, "champion_for"); got != 1 {
		t.Errorf("%d open champion_for edges, want one", got)
	}
	if got := num(t, `SELECT count(*) FROM relationships WHERE valid_to IS NOT NULL AND valid_to > valid_from`); got != 1 {
		t.Error("the closed holder must have valid_to strictly after valid_from")
	}
}
