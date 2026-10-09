package orchestrator_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/knowledge"
)

type retrievalRecord struct {
	Candidates []struct {
		KnowledgeID string  `json:"knowledge_id"`
		Status      string  `json:"status"`
		Score       float64 `json:"score"`
		Decision    string  `json:"decision"`
	} `json:"candidates"`
	Message   string  `json:"message"`
	Threshold float64 `json:"threshold"`
}

func persistedRetrieval(t *testing.T, runID string) retrievalRecord {
	t.Helper()
	raw := scalar(t, `SELECT detail -> 'knowledge_retrieval' FROM agent_run_steps WHERE agent_run_id = $1::uuid AND step = 'build_context'`, runID)
	var r retrievalRecord
	if err := json.Unmarshal([]byte(raw), &r); err != nil {
		t.Fatalf("the run persisted no knowledge_retrieval record: %q (%v)", raw, err)
	}
	return r
}

// With no lesson learned the run still persists the closest-match record and it says so plainly.
func TestARunWithNoLessonPersistsThePlainNoSimilarKnowledgeStatement(t *testing.T) {
	sc := newScene(t)
	mustRun(t, service(t, newFake(sc)), sc.RunID)
	r := persistedRetrieval(t, sc.RunID)
	if len(r.Candidates) != 0 || !strings.HasPrefix(r.Message, "No similar knowledge in the knowledge base") || r.Threshold != 0.6 {
		t.Fatalf("record = %+v", r)
	}
}

// A corrected-verdict lesson that exists at the replay clock is ranked, scored and given a decision, and the record
// is persisted whether or not it is offered: the product says which, and why.
func TestARunPersistsTheClosestLessonItsScoreAndItsDecision(t *testing.T) {
	sc := newScene(t)
	id := newUUID()
	lesson := matchingKnowledge(id, sc.EventTime.Add(-72*time.Hour))
	ep := newUUID()
	lesson.Provenance = knowledge.Provenance{CreatedFrom: "manual", SourceDecisionEpisodeID: &ep}
	lesson.Title = "Corrected inference: wait for the buyer"
	lesson.SituationSignature = []knowledge.Condition{{Field: "stage", Op: "eq", Value: "negotiation"}}
	learnCandidate(t, lesson, sc.EventTime.Add(-48*time.Hour))
	mustRun(t, service(t, newFake(sc)), sc.RunID)
	r := persistedRetrieval(t, sc.RunID)
	if len(r.Candidates) != 1 || r.Candidates[0].KnowledgeID != id || r.Candidates[0].Status != knowledge.StatusCandidate || r.Candidates[0].Decision == "" {
		t.Fatalf("record = %+v", r)
	}
	if r.Candidates[0].Decision == knowledge.DecisionApplicable {
		t.Fatalf("one comparable feature can never be applicable: %+v", r)
	}
	if !strings.Contains(r.Message, "Corrected inference: wait for the buyer") {
		t.Fatalf("the message must name the closest lesson: %q", r.Message)
	}
}
