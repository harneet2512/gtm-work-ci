package ctxgraph

import (
	"testing"
	"time"
)

const unknownID = "99999999-9999-4999-8999-999999999999"

func hasHit(hits []Hit, kind, id string) bool {
	for _, h := range hits {
		if h.Kind == kind && h.ID == id {
			return true
		}
	}
	return false
}

func TestReferencingFindsWhatIsDerivedFromAnActivityOrItsSourceEvent(t *testing.T) {
	s, r := projected(t)

	byActivity, err := r.Referencing(bg, []string{s["ACT_MAIL"]})
	if err != nil {
		t.Fatal(err)
	}
	if !hasHit(byActivity, "node", s["ACT_MAIL"]) {
		t.Fatalf("the activity node itself must be found: %+v", byActivity)
	}

	byEvent, err := r.Referencing(bg, []string{s["SE_MAIL"]})
	if err != nil {
		t.Fatal(err)
	}
	if !hasHit(byEvent, "node", s["ACT_MAIL"]) {
		t.Fatalf("the activity derived from the source event must be found through source_event_ids: %+v", byEvent)
	}
}

func TestReferencingIsEmptyForAnIdTheGraphNeverSawAndForNoIds(t *testing.T) {
	_, r := projected(t)
	hits, err := r.Referencing(bg, []string{unknownID})
	if err != nil || len(hits) != 0 {
		t.Fatalf("an unknown id: %+v, %v", hits, err)
	}
	if hits, err := r.Referencing(bg, nil); err != nil || hits != nil {
		t.Fatalf("no ids: %+v, %v", hits, err)
	}
}

func TestReferencingFindsEdgesThatCiteTheActivity(t *testing.T) {
	s, r := projected(t)
	hits, err := r.Referencing(bg, []string{s["ACT_MAIL"]})
	if err != nil {
		t.Fatal(err)
	}
	edges := 0
	for _, h := range hits {
		if h.Kind == "edge" {
			edges++
		}
	}
	if edges == 0 {
		t.Fatalf("the activity's participation edges cite it as evidence: %+v", hits)
	}
}

func TestActivitiesFromFindsTheAccountsActivitiesAtOrAfterACutoff(t *testing.T) {
	s, r := projected(t)
	cutoff := time.Date(2026, 9, 3, 10, 0, 0, 0, time.UTC)
	hits, err := r.ActivitiesFrom(bg, s["A"], s["OPP"], cutoff)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"ACT_CRM", "ACT_DOC", "ACT_HID"} {
		if !hasHit(hits, "node", s[want]) {
			t.Errorf("%s (at or after the cutoff) is missing: %+v", want, hits)
		}
	}
	for _, not := range []string{"ACT_MAIL", "ACT_CALL", "ACT_B"} {
		if hasHit(hits, "node", s[not]) {
			t.Errorf("%s must not be found (before the cutoff, or another account): %+v", not, hits)
		}
	}
	none, err := r.ActivitiesFrom(bg, s["A"], s["OPP"], time.Date(2026, 9, 6, 0, 0, 0, 0, time.UTC))
	if err != nil || len(none) != 0 {
		t.Fatalf("a cutoff after the last activity: %+v, %v", none, err)
	}
}
