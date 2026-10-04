package crmarena

import (
	"strings"
	"testing"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/normalize"
)

func TestAsOfWithholdsEverythingFromTheCutoffOn(t *testing.T) {
	r := build(t, tiny())
	cut := cutoff(t, "2023-10-01")
	got, red, err := AsOf(r.Events, cut)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) == 0 || len(got) >= len(r.Events) {
		t.Fatalf("as-of kept %d of %d events", len(got), len(r.Events))
	}
	for _, e := range got {
		if !e.OccurredAt().Before(cut) {
			t.Errorf("%s/%s at %s is not before the cutoff", e.Source.SourceObjectID, e.Source.SourceEventKey, e.OccurredAt())
		}
		if (e.DealID == "C" || e.DealID == "L") && isSnapshotEvent(e) {
			t.Errorf("current deal %s has its snapshot-only value %s in the as-of timeline", e.DealID, e.Source.SourceEventKey)
		}
		if _, err := normalize.Normalize(e.Source); err != nil { // a redacted event must still be ingestible
			t.Errorf("%s/%s no longer normalizes: %v", e.Source.SourceObjectID, e.Source.SourceEventKey, err)
		}
	}
	assertNoDateAtOrAfter(t, got, cut)
	if red.ByField["Quote.ExpirationDate"] != 1 {
		t.Errorf("redactions = %+v, want the future quote expiry removed", red)
	}
	for i := 1; i < len(got); i++ {
		if ingestLess(got[i], got[i-1]) {
			t.Fatalf("as-of timeline out of ingest order at %d", i)
		}
	}
}

func TestAsOfAndBetweenPartitionTheTimeline(t *testing.T) {
	r := build(t, tiny())
	cut := cutoff(t, "2023-10-01")
	before, _, err := AsOf(r.Events, cut)
	if err != nil {
		t.Fatal(err)
	}
	after := Between(r.Events, cut, time.Time{})
	if len(before)+len(after) != len(r.Events) {
		t.Fatalf("%d + %d events != %d", len(before), len(after), len(r.Events))
	}
	if len(after) == 0 || after[0].OccurredAt().Before(cut) {
		t.Fatalf("remaining events start at %v", after[0].OccurredAt())
	}
	window := Between(r.Events, cut, cutoff(t, "2023-11-05"))
	for _, e := range window {
		if !e.OccurredAt().Before(cutoff(t, "2023-11-05")) || e.OccurredAt().Before(cut) {
			t.Errorf("event at %s outside [cutoff, 2023-11-05)", e.OccurredAt())
		}
	}
	if len(window) >= len(after) {
		t.Errorf("a closed window kept all %d remaining events", len(after))
	}
}

func TestReplayRemainingIsTheCurrentDealsPostCutoffStream(t *testing.T) {
	r := build(t, tiny())
	w, _ := Windows(tiny())
	split, _ := NewSplit(w, "2023-10-01", 0)
	got, err := ReplayRemaining(r.Events, split, "C")
	if err != nil {
		t.Fatal(err)
	}
	cut := cutoff(t, "2023-10-01")
	if len(got) < 3 { // e4, t2, final stage
		t.Fatalf("remaining stream has %d events", len(got))
	}
	for _, e := range got {
		if e.DealID != "C" || e.OccurredAt().Before(cut) {
			t.Errorf("%s of deal %s at %s", e.Source.SourceEventKey, e.DealID, e.OccurredAt())
		}
	}
	if last := got[len(got)-1]; !strings.HasPrefix(last.Source.SourceEventKey, "field:StageName:") {
		t.Errorf("the final stage must come last, got %s", last.Source.SourceEventKey)
	}
	if _, err := ReplayRemaining(r.Events, split, "P"); err == nil {
		t.Error("replay of a previous deal accepted")
	}
}
