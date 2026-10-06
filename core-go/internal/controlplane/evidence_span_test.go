package controlplane

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// The column is CHECK-constrained to an array, so an unreadable list cannot come from the database today; the span
// still must not turn a decode failure into a confident "0 evidence references".
func TestEvidenceSpanSaysSoWhenItsReferencesCannotBeRead(t *testing.T) {
	in := &traceInput{change: &changeRow{id: "c1", evidenceRefs: json.RawMessage(`{"not": "a list"}`), createdAt: time.Unix(0, 0)}}
	s := in.evidenceSpan()
	if s.Status != statusNotRecorded || s.Attributes["reason"] != "unreadable" || strings.Contains(s.Summary, "0 evidence") {
		t.Fatalf("an unreadable list must not read as an empty one: %+v", s)
	}
	ok := (&traceInput{change: &changeRow{id: "c1", evidenceRefs: json.RawMessage(`[{"a":1},{"b":2}]`), createdAt: time.Unix(0, 0)}}).evidenceSpan()
	if ok.Status != statusRecorded || !strings.Contains(ok.Summary, "2 evidence references") {
		t.Fatalf("a readable list is counted: %+v", ok)
	}
}
