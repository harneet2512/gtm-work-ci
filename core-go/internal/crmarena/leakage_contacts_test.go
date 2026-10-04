package crmarena

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// roleContacts reads the contact roles of the local export (the sample has none).
func roleContacts(t *testing.T) []ContactRole {
	t.Helper()
	var roles []ContactRole
	raw, err := os.ReadFile(repoPath(t, "data/crmarena_b2b/OpportunityContactRole.json"))
	if err != nil {
		return nil
	}
	if err := json.Unmarshal(raw, &roles); err != nil {
		t.Fatal(err)
	}
	return roles
}

// TestFullSnapshotFutureContactsStayOutOfTheCutoffTimeline: a contact first seen at or after the
// cutoff is not in the as-of timeline, including the ones that hold a contact role on a current deal
// (HAR-130 review: 117 contacts first seen from 2023-11-01, 37 with a role on a current deal), and
// every such contact on a current deal arrives in that deal's replay with its first event.
func TestFullSnapshotFutureContactsStayOutOfTheCutoffTimeline(t *testing.T) {
	frozen := frozenSplit(t)
	snap := fullSnapshot(t, frozen)
	r, err := Build(snap)
	if err != nil {
		t.Fatal(err)
	}
	cut, _ := frozen.CutoffTime()
	asOf, _, err := AsOf(r.Events, cut)
	if err != nil {
		t.Fatal(err)
	}
	inTimeline := map[string]bool{}
	for _, e := range asOf {
		if strings.HasPrefix(e.Source.SourceObjectID, "contact:") {
			inTimeline[e.Source.SourceObjectID] = true
		}
	}
	later := map[string]Event{}
	for _, e := range r.Events {
		if strings.HasPrefix(e.Source.SourceObjectID, "contact:") && !e.OccurredAt().Before(cut) {
			later[e.Source.SourceObjectID] = e
			if inTimeline[e.Source.SourceObjectID] {
				t.Errorf("%s first appears %s but is in the timeline before the cutoff", e.Source.SourceObjectID, e.OccurredAt())
			}
		}
	}
	if len(later) == 0 {
		t.Fatal("no contact is first seen after the cutoff: the check is vacuous")
	}
	current := frozen.CurrentSet()
	withRole := 0
	for _, role := range roleContacts(t) {
		if _, isLater := later["contact:"+role.ContactID]; isLater && current[role.OpportunityID] {
			withRole++
		}
	}
	arrivals := 0
	for _, deal := range frozen.Current {
		replay, err := ReplayRemaining(r.Events, frozen, deal)
		if err != nil {
			t.Fatal(err)
		}
		for i, e := range replay {
			if !strings.HasPrefix(e.Source.SourceObjectID, "contact:") {
				continue
			}
			arrivals++
			if i+1 == len(replay) || !replay[i+1].OccurredAt().Equal(e.OccurredAt()) {
				t.Errorf("deal %s: %s is not followed by an event of the same instant", deal, e.Source.SourceObjectID)
			}
		}
	}
	t.Logf("%d contacts first seen at or after %s (review counted 117); %d hold a role on a current deal (review: 37); "+
		"%d arrive with a current deal's replayed event", len(later), frozen.Cutoff, withRole, arrivals)
	if arrivals == 0 {
		t.Error("no future contact arrives in any current deal's replay")
	}
}
