package crmarena

import (
	"strings"
	"testing"
)

// TestSnapshotValuesComeLastInReplay: the final stage shares its instant with the deal's last activity
// (here a midnight task) and must still follow it.
func TestSnapshotValuesComeLastInReplay(t *testing.T) {
	r := build(t, tiny())
	w, _ := Windows(tiny())
	split, _ := NewSplit(w, "2023-10-01", 0)
	got, err := Replay(r.Events, split, "C")
	if err != nil {
		t.Fatal(err)
	}
	last := got[len(got)-1]
	if !strings.HasPrefix(last.Source.SourceEventKey, "field:StageName:") || !last.OccurredAt().Equal(w["C"].Last) {
		t.Fatalf("last replay event = %s/%s at %s", last.Source.SourceObjectID, last.Source.SourceEventKey, last.OccurredAt())
	}
}

func TestDealCreationMovesBackToItsFirstActivity(t *testing.T) {
	s := tiny()
	s.Emails = append(s.Emails, EmailMessage{ID: "E0", RelatedToID: "C", FromAddress: "ann@acme.example",
		ToAddress: "rep@techagents.com", Subject: "Early", TextBody: "x", MessageDate: "2023-07-01T09:00:00.000+0000"})
	r := build(t, s)
	if r.Stats.DealsCreatedAtFirstActivity != 1 {
		t.Fatalf("stats = %+v", r.Stats)
	}
	for _, e := range r.Events {
		if e.Source.SourceObjectID == "opp:C" && e.Source.SourceEventKey == "created" && e.OccurredAt().Format(sfDate) != "2023-07-01" {
			t.Fatalf("deal C created at %s, want its first activity", e.OccurredAt())
		}
	}
}

func TestChooseCutoffRefusesAnEmptySide(t *testing.T) {
	if _, err := ChooseCutoff([]Candidate{{Cutoff: "2023-01-01", Counts: Counts{Previous: 5}}}); err == nil {
		t.Fatal("cutoff without replayable current deals accepted")
	}
}
