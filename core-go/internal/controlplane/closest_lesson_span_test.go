package controlplane

import (
	"strings"
	"testing"
)

// The retrieved span says what was read and, when the run recorded one, the closest past lesson in core's own words.
// It never says the lesson influenced anything.
func TestTheRetrievedSpanCarriesTheClosestLessonStatement(t *testing.T) {
	const msg = "No similar knowledge in the knowledge base (closest: Corrected inference: wait, similarity 0.33 — below 0.60)."
	in := &traceInput{attr: &attribution{Retrieved: []string{"k1"}, ClosestLesson: msg}}
	s := in.knowledgeSpan(kindRetrieved)
	if !strings.Contains(s.Summary, "1 knowledge object retrieved at the replay clock. "+msg) {
		t.Fatalf("summary = %q", s.Summary)
	}
	if strings.Contains(strings.ToLower(s.Summary), "influenc") {
		t.Fatalf("retrieval is not influence: %q", s.Summary)
	}
	plain := (&traceInput{attr: &attribution{Retrieved: []string{"k1"}}}).knowledgeSpan(kindRetrieved)
	if strings.Contains(plain.Summary, "similar") {
		t.Fatalf("a run without the record says nothing of similarity: %q", plain.Summary)
	}
}
