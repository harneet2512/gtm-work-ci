package orchestrator

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/knowledge"
)

var guidanceAt = time.Date(2023, 11, 29, 10, 15, 0, 0, time.UTC)

// Recorded cassettes key on the prompt, and the prompt carries this document: with no lesson offered the guidance
// must stay byte-identical to what it was before closest-match retrieval (ADR-0013 amendment 2), so MedTech, which
// has no knowledge at its Event N, still replays.
func TestGuidanceWithNoLessonOfferedIsByteIdentical(t *testing.T) {
	const want = `{"id":"g","account_id":"a","agent_run_id":"r","state_version":3,"recommended_action":"send_email",` +
		`"not_recommended":[],"why_now":"No company knowledge applies to this situation; decide from the current state.",` +
		`"who_to_involve":[],"who_not_to_involve":[],"supporting_knowledge":[],"created_at":"2023-11-29T10:15:00Z"}`
	raw, err := json.Marshal(buildGuidance("g", "r", "a", 3, guidanceAt, nil, nil))
	if err != nil || string(raw) != want {
		t.Fatalf("guidance with no knowledge changed:\n got %s\nwant %s (%v)", raw, want, err)
	}
}

// What is and is not byte-identical (ADR-0013 amendment 2, item 8). With NO lesson in the knowledge base for the case the
// guidance is exactly what it was before closest-match retrieval (the test above, through the real retrieval path). A
// lesson that is present but NOT offered is rendered in the new format (feature renderings in matched_conditions and
// unmatched_conditions, no similarity field), which differs from the exact matcher's bytes; the agent is still told
// that no company knowledge applies. Both go through knowledge.RetrieveAll and buildGuidance, not a hand-built entry.
func realGuidance(t *testing.T, ks []knowledge.Knowledge) string {
	t.Helper()
	return realGuidanceFor(t, ks, knowledge.Situation{AccountID: "a", Now: guidanceAt, Conflicts: map[string][]string{},
		Fields: map[string]knowledge.Value{"stage": {Known: true, Scalar: "negotiation"}, "motion": {Known: true, Scalar: "expansion"}}})
}

func realGuidanceFor(t *testing.T, ks []knowledge.Knowledge, s knowledge.Situation) string {
	t.Helper()
	rules, err := knowledge.LoadSimilarity(similarityRulesPath(t))
	if err != nil {
		t.Fatal(err)
	}
	results, _, err := knowledge.RetrieveAll(ks, s, &rules)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(buildGuidance("g", "r", "a", 3, guidanceAt, ks, results))
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func TestGuidanceWithAnEmptyKnowledgeBaseIsByteIdenticalThroughTheRealPath(t *testing.T) {
	const want = `{"id":"g","account_id":"a","agent_run_id":"r","state_version":3,"recommended_action":"send_email",` +
		`"not_recommended":[],"why_now":"No company knowledge applies to this situation; decide from the current state.",` +
		`"who_to_involve":[],"who_not_to_involve":[],"supporting_knowledge":[],"created_at":"2023-11-29T10:15:00Z"}`
	if got := realGuidance(t, nil); got != want {
		t.Fatalf("empty knowledge base:\n got %s\nwant %s", got, want)
	}
}

func TestANotOfferedLessonIsRenderedInTheNewFormatAndSaysNothingOfSimilarity(t *testing.T) {
	ep := "00000000-0000-4000-8000-0000000000e1"
	far := knowledge.Knowledge{ID: "00000000-0000-4000-8000-0000000000f1", Title: "Far", Status: knowledge.StatusCandidate,
		SituationSignature:      []knowledge.Condition{{Field: "stage", Op: knowledge.OpEq, Value: "discovery"}},
		ApplicabilityConditions: []knowledge.Condition{{Field: "motion", Op: knowledge.OpEq, Value: "renewal"}, {Field: "health", Op: knowledge.OpEq, Value: "at_risk"}},
		Provenance:              knowledge.Provenance{CreatedFrom: "manual", SourceDecisionEpisodeID: &ep}}
	const want = `{"id":"g","account_id":"a","agent_run_id":"r","state_version":3,"recommended_action":"send_email",` +
		`"not_recommended":[],"why_now":"No company knowledge applies to this situation; decide from the current state.",` +
		`"who_to_involve":[],"who_not_to_involve":[],"supporting_knowledge":[WANT_ENTRY],"created_at":"2023-11-29T10:15:00Z"}`
	got := realGuidance(t, []knowledge.Knowledge{far})
	if got != strings.Replace(want, "WANT_ENTRY", `{"knowledge_id":"00000000-0000-4000-8000-0000000000f1","applies":false,`+
		`"matched_conditions":[],"unmatched_conditions":["stage: lesson discovery, case negotiation","motion: lesson renewal, case expansion"],`+
		`"exceptions_checked":[]}`, 1) {
		t.Fatalf("not-offered lesson:\n got %s", got)
	}
	if strings.Contains(got, "similarity") {
		t.Fatalf("a lesson that is not offered says nothing of similarity to the agent: %s", got)
	}
}

func TestAnOfferedLessonIsLabelledWithItsStatusAndSimilarity(t *testing.T) {
	k := knowledge.Knowledge{ID: "k1", Title: "Corrected inference: wait for the buyer",
		Guidance: knowledge.Guidance{Summary: "Do not push a date.", Do: []string{"wait"}, Dont: []string{}}}
	offered := knowledge.Result{Label: knowledge.LabelApplies, Entry: knowledge.Entry{KnowledgeID: "k1", Applies: true,
		MatchedConditions: []string{"stage: negotiation"}, UnmatchedConditions: []string{}, ExceptionsChecked: []knowledge.ExceptionCheck{},
		Similarity: &knowledge.EntrySimilarity{Status: knowledge.StatusCandidate, Score: 0.6667, Matched: []string{"stage: negotiation"}, Differs: []string{}}}}
	g := buildGuidance("g", "r", "a", 3, guidanceAt, []knowledge.Knowledge{k}, []knowledge.Result{offered})
	want := "Applicable company knowledge. candidate: Corrected inference: wait for the buyer (similarity 0.67): Do not push a date. Do: wait."
	if g.WhyNow != want {
		t.Fatalf("why_now = %q", g.WhyNow)
	}
	raw, _ := json.Marshal(g.SupportingKnowledge[0])
	if !strings.Contains(string(raw), `"similarity":{"status":"candidate","score":0.6667`) {
		t.Fatalf("entry = %s", raw)
	}
}

func similarityRulesPath(t *testing.T) string {
	t.Helper()
	dir, _ := os.Getwd()
	for {
		p := filepath.Join(dir, "contracts", "knowledge", "similarity.v1.json")
		if _, err := os.Stat(p); err == nil {
			return p
		}
		if filepath.Dir(dir) == dir {
			t.Fatal("contracts/knowledge/similarity.v1.json not found")
		}
		dir = filepath.Dir(dir)
	}
}

// MedTech has no knowledge at its Event N, so its guidance is the same bytes whatever topic the case has (unchanged by topic
// and by the owner decision of 2026-10-06). EcoLite's later case holds MedTech's lesson, which since that decision (two
// strong facts are enough) IS offered at similarity 0.83: EcoLite's guidance changes by design, and this pins the exact bytes.
func TestMedTechGuidanceIsUnchangedAndEcoLiteIsNowOfferedTheMedTechLesson(t *testing.T) {
	now := guidanceAt
	medtech := knowledge.Situation{AccountID: "a", Now: now, Conflicts: map[string][]string{}, Topics: []string{"blockers_risk", "buyer_intent", "next_step_commitment"}}
	const empty = `{"id":"g","account_id":"a","agent_run_id":"r","state_version":3,"recommended_action":"send_email",` +
		`"not_recommended":[],"why_now":"No company knowledge applies to this situation; decide from the current state.",` +
		`"who_to_involve":[],"who_not_to_involve":[],"supporting_knowledge":[],"created_at":"2023-11-29T10:15:00Z"}`
	if got := realGuidanceFor(t, nil, medtech); got != empty {
		t.Fatalf("MedTech (empty knowledge base, with a topic):\n got %s", got)
	}
	ep := "00000000-0000-4000-8000-0000000000e1"
	lesson := knowledge.Knowledge{ID: "00000000-0000-4000-8000-0000000000b1", Title: "Corrected inference: the contact is the champion",
		Status: knowledge.StatusCandidate, Provenance: knowledge.Provenance{CreatedFrom: "manual", SourceDecisionEpisodeID: &ep},
		SituationSignature: []knowledge.Condition{{Field: "diff.customer_replied", Op: knowledge.OpExists}},
		ApplicabilityConditions: []knowledge.Condition{{Field: "topic.blockers_risk", Op: knowledge.OpExists},
			{Field: "topic.buyer_intent", Op: knowledge.OpExists}, {Field: "topic.next_step_commitment", Op: knowledge.OpExists}}}
	ecolite := knowledge.Situation{AccountID: "a", Now: now, Conflicts: map[string][]string{}, Topics: []string{"blockers_risk", "buyer_intent"},
		Fields:  map[string]knowledge.Value{"stage": {Known: true, Scalar: "negotiation"}},
		Signals: []knowledge.Signal{{ID: "s1", Type: "customer_replied", CreatedAt: now.Add(-time.Hour)}}}
	const want = `{"id":"g","account_id":"a","agent_run_id":"r","state_version":3,"recommended_action":"send_email","not_recommended":[],` +
		`"why_now":"Applicable company knowledge. candidate: Corrected inference: the contact is the champion (similarity 0.83): ",` +
		`"who_to_involve":[],"who_not_to_involve":[],"supporting_knowledge":[{"knowledge_id":"00000000-0000-4000-8000-0000000000b1","applies":true,` +
		`"matched_conditions":["signals: customer_replied","topic: lesson blockers_risk, buyer_intent, next_step_commitment, case blockers_risk, buyer_intent"],` +
		`"unmatched_conditions":[],"exceptions_checked":[],"similarity":{"status":"candidate","score":0.8333,` +
		`"matched":["signals: customer_replied","topic: lesson blockers_risk, buyer_intent, next_step_commitment, case blockers_risk, buyer_intent"],"differs":[]}}],` +
		`"created_at":"2023-11-29T10:15:00Z"}`
	if got := realGuidanceFor(t, []knowledge.Knowledge{lesson}, ecolite); got != want {
		t.Fatalf("EcoLite (lesson offered):\n got %s\nwant %s", got, want)
	}
}
