package crmarena

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

// isSnapshotEvent recognizes the values the source holds only as of the snapshot (final stage, quote
// status) by their event key, independently of the loader's own ordering phase.
func isSnapshotEvent(e Event) bool {
	key, obj := e.Source.SourceEventKey, e.Source.SourceObjectID
	return strings.HasPrefix(key, "field:StageName:") && strings.HasPrefix(obj, "opp:") ||
		strings.HasPrefix(key, "field:Status:") && strings.HasPrefix(obj, "quote:")
}

// assertSnapshotValuesComeLast: no deal's final stage or quote status precedes, in time or in ingest
// order, any other event of that deal (HAR-130 review: a contract signed after the last email must not
// be followed by a stage that already says "Closed").
func assertSnapshotValuesComeLast(t *testing.T, events []Event) {
	t.Helper()
	lastIndex := map[string]int{}
	lastTime := map[string]time.Time{}
	for i, e := range events {
		if e.DealID == "" || isSnapshotEvent(e) {
			continue
		}
		lastIndex[e.DealID] = i
		lastTime[e.DealID] = later(lastTime[e.DealID], e.OccurredAt())
	}
	checked := 0
	for i, e := range events {
		if e.DealID == "" || !isSnapshotEvent(e) {
			continue
		}
		checked++
		if e.OccurredAt().Before(lastTime[e.DealID]) || i < lastIndex[e.DealID] {
			t.Errorf("deal %s: %s/%s (%s, position %d) precedes another event of the deal (%s, position %d)", e.DealID,
				e.Source.SourceObjectID, e.Source.SourceEventKey, e.OccurredAt(), i, lastTime[e.DealID], lastIndex[e.DealID])
		}
	}
	if checked == 0 {
		t.Fatal("no snapshot-only event to check")
	}
}

// walkDates calls visit for every date-like string anywhere in a decoded JSON value.
func walkDates(v any, path string, visit func(path string, at time.Time)) {
	switch x := v.(type) {
	case string:
		if at, ok := parseAnyDate(x); ok { // the redactor's own parser: one list of formats, not two
			visit(path, at)
		}
	case map[string]any:
		for k, child := range x {
			walkDates(child, path+"."+k, visit)
		}
	case []any:
		for i, child := range x {
			walkDates(child, fmt.Sprintf("%s[%d]", path, i), visit)
		}
	}
}

// assertNoDateAtOrAfter walks every payload field of every event and fails on any date-like value at or
// after the cutoff. It does not re-apply KnowledgeInputs' filter: it only looks at what comes out.
// Dates written inside free text (email bodies, descriptions) are not parsed.
func assertNoDateAtOrAfter(t *testing.T, events []Event, cutoff time.Time) {
	t.Helper()
	for _, e := range events {
		var payload any
		if err := json.Unmarshal(e.Source.Payload, &payload); err != nil {
			t.Fatal(err)
		}
		walkDates(payload, e.Source.SourceObjectID+"/"+e.Source.SourceEventKey, func(path string, at time.Time) {
			if !at.Before(cutoff) {
				t.Errorf("knowledge input carries %s = %s, at or after the cutoff %s", path, at.Format(time.RFC3339), cutoff.Format(sfDate))
			}
		})
		if !e.OccurredAt().Before(cutoff) {
			t.Errorf("%s/%s occurs at %s", e.Source.SourceObjectID, e.Source.SourceEventKey, e.OccurredAt())
		}
	}
}

func TestSnapshotValuesComeAfterEveryOtherEventOfTheDeal(t *testing.T) {
	assertSnapshotValuesComeLast(t, build(t, tiny()).Events)
	assertSnapshotValuesComeLast(t, build(t, sample(t)).Events)
}

// TestFinalStageFollowsALaterContractSignature is the reviewed case: the contract is signed 12 days
// after the last email, and the final stage must be dated at the signature, not at the email.
func TestFinalStageFollowsALaterContractSignature(t *testing.T) {
	want := map[string]string{"P": "2023-03-15", "L": "2023-11-12"} // contract signature, after the last email
	for _, e := range build(t, tiny()).Events {
		if day, ok := want[e.DealID]; ok && strings.HasPrefix(e.Source.SourceEventKey, "field:StageName:") &&
			strings.HasPrefix(e.Source.SourceObjectID, "opp:") {
			if got := e.OccurredAt().Format(sfDate); got != day {
				t.Errorf("deal %s final stage dated %s, want %s (its last event)", e.DealID, got, day)
			}
			delete(want, e.DealID)
		}
	}
	if len(want) != 0 {
		t.Fatalf("stage events missing for %v", want)
	}
}

func TestPreviousDealKnowledgeCarriesNoDateAtOrAfterTheCutoff(t *testing.T) {
	r := build(t, tiny())
	w, _ := Windows(tiny())
	split, _ := NewSplit(w, "2023-10-01", 0)
	got, red, err := KnowledgeInputsWithReport(r.Events, split)
	if err != nil {
		t.Fatal(err)
	}
	at, _ := split.CutoffTime()
	assertNoDateAtOrAfter(t, got, at)
	for field, n := range map[string]int{"Opportunity.CloseDate": 1, "Quote.ExpirationDate": 1, "Contract.EndDate": 1} {
		if red.ByField[field] != n {
			t.Errorf("redacted %s = %d, want %d (all: %v)", field, red.ByField[field], n, red.ByField)
		}
	}
	if red.Fields != 3 || red.Events != 3 {
		t.Errorf("redactions = %+v, want 3 fields in 3 events", red)
	}
	kept := false
	for _, e := range got {
		if f, ok := payloadOf(t, e)["fields"].(map[string]any); ok && e.Source.SourceObjectID == "opp:P" {
			_, hasStage := f["StageName"]
			_, hasClose := f["CloseDate"]
			kept = kept || hasStage
			if hasClose {
				t.Error("CloseDate survived redaction")
			}
		}
	}
	if !kept {
		t.Error("redaction removed the stage with the date")
	}
	for _, e := range r.Events { // the input events are never mutated
		if e.Source.SourceObjectID == "opp:P" && strings.HasPrefix(e.Source.SourceEventKey, "field:StageName:") &&
			!strings.Contains(string(e.Source.Payload), "CloseDate") {
			t.Error("redaction mutated the caller's event")
		}
	}
}

func TestSampleKnowledgeLeakageAndOrderingRunInCI(t *testing.T) {
	frozen := frozenSplit(t)
	s := sample(t)
	r := build(t, s)
	w, err := Windows(s)
	if err != nil {
		t.Fatal(err)
	}
	split, err := NewSplit(w, frozen.Cutoff, frozen.Seed)
	if err != nil {
		t.Fatal(err)
	}
	if split.Counts.Previous == 0 {
		t.Fatal("the sample has no previous deal at the frozen cutoff")
	}
	got, red, err := KnowledgeInputsWithReport(r.Events, split)
	if err != nil || len(got) == 0 {
		t.Fatalf("knowledge inputs: %d events, %v", len(got), err)
	}
	if red.Fields == 0 {
		t.Error("the sample should exercise the redaction (stage CloseDate, quote expiry, contract end)")
	}
	at, _ := split.CutoffTime()
	assertNoDateAtOrAfter(t, got, at)
	assertSnapshotValuesComeLast(t, r.Events)
}
