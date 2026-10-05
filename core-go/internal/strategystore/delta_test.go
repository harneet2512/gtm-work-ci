package strategystore_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/harneet2512/gtm-work/core-go/internal/strategystore"
	"github.com/harneet2512/gtm-work/core-go/internal/strategystore/strategytest"
	"github.com/harneet2512/gtm-work/core-go/internal/workerclient"
)

// stubLabeler is a scripted DeltaLabeler: it answers every call with resp or fails with err, and it
// remembers the last request so a test can check the contract fields core sent.
type stubLabeler struct {
	resp workerclient.LabelDeltaResponse
	err  error
	last workerclient.LabelDeltaRequest
}

func (s *stubLabeler) LabelDelta(_ context.Context, req workerclient.LabelDeltaRequest) (workerclient.LabelDeltaResponse, error) {
	s.last = req
	if s.err != nil {
		return workerclient.LabelDeltaResponse{}, s.err
	}
	return s.resp, nil
}

// editAndSend chooses candidate 2, edits its subject and body and sends.
func editAndSend(t *testing.T, f *fixture) []byte {
	t.Helper()
	if _, _, err := f.choose(f.seed.Candidates[1], nil); err != nil {
		t.Fatal(err)
	}
	subject := "Changed at send time"
	edited := strategystore.Artifact{Channel: "email", Subject: &subject, Body: "Hi Marco,\n\nLet us know what timing suits.\n\nBest,\nDana"}
	if _, _, err := f.choose(f.seed.Candidates[1], func(r *strategystore.DecisionRequest) { r.FinalArtifact = &edited }); err != nil {
		t.Fatal(err)
	}
	doc, err := f.send("send")
	if err != nil {
		t.Fatal(err)
	}
	return doc
}

// deltaDoc is the episode's human_deltas row as a human_delta.v1.json document.
func deltaDoc(t *testing.T, episodeID string) []byte {
	t.Helper()
	var doc string
	err := env.DB.QueryRow(`SELECT jsonb_build_object(
 'id', d.id, 'decision_episode_id', d.decision_episode_id, 'literal_changes', d.literal_changes,
 'semantic_labels', to_jsonb(d.semantic_labels),
 'explained_by_eval_result_ids', COALESCE((SELECT jsonb_agg(x.eval_run_id) FROM human_delta_explanations x
   WHERE x.human_delta_id = d.id), '[]'::jsonb),
 'unexplained', d.unexplained, 'candidate_criterion', d.candidate_criterion, 'model', d.model,
 'created_at', d.created_at)::text FROM human_deltas d WHERE d.decision_episode_id = $1::uuid`, episodeID).Scan(&doc)
	if err != nil {
		t.Fatalf("delta doc of %s: %v", episodeID, err)
	}
	return []byte(doc)
}

func TestUneditedSendWritesNoDelta(t *testing.T) {
	f := newFixture(t)
	if _, _, err := f.choose(f.seed.Candidates[0], nil); err != nil {
		t.Fatal(err)
	}
	if _, err := f.send("send"); err != nil {
		t.Fatal(err)
	}
	if n := scalar(t, `SELECT count(*)::text FROM human_deltas WHERE decision_episode_id = $1::uuid`, f.seed.EpisodeID); n != "0" {
		t.Fatalf("an unedited send wrote %s deltas, want none", n)
	}
	if got := scalar(t, `SELECT human_action || ':' || (human_delta_id IS NULL)::text FROM decision_episodes WHERE id = $1::uuid`, f.seed.EpisodeID); got != "APPROVE_UNCHANGED:true" {
		t.Fatalf("episode = %s, want APPROVE_UNCHANGED with no delta link", got)
	}
}

func TestDiscardWritesNoDeltaEvenWithEdits(t *testing.T) {
	f := newFixture(t)
	if _, _, err := f.choose(f.seed.Candidates[1], nil); err != nil {
		t.Fatal(err)
	}
	subject := "Discarded edit"
	if _, _, err := f.choose(f.seed.Candidates[1], func(r *strategystore.DecisionRequest) {
		r.FinalArtifact = &strategystore.Artifact{Channel: "email", Subject: &subject, Body: "body"}
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.send("discard"); err != nil {
		t.Fatal(err)
	}
	if n := scalar(t, `SELECT count(*)::text FROM human_deltas WHERE decision_episode_id = $1::uuid`, f.seed.EpisodeID); n != "0" {
		t.Fatalf("a discarded edit wrote %s deltas, want none (the delta supervises a sent action)", n)
	}
	if got := scalar(t, `SELECT human_action FROM decision_episodes WHERE id = $1::uuid`, f.seed.EpisodeID); got != "REJECT" {
		t.Fatalf("episode action = %s, want REJECT", got)
	}
}

func TestEditedSendWritesTheWorkerLabelledDelta(t *testing.T) {
	criterion := workerclient.DeltaCriterion{Statement: "The human drops the CTA when the buyer asks for time.",
		SuggestedEvalType: "cta_calibration"}
	labeler := &stubLabeler{resp: workerclient.LabelDeltaResponse{
		SemanticLabels:     []string{"reduced_pressure", "deferred_to_buyer_timing"},
		CandidateCriterion: &criterion, Model: "stub-labeler-v1"}}
	f := newFixtureWith(t, strategystore.WithLabeler(labeler))

	editAndSend(t, f)

	// The labeler saw the contract shape: the episode and run ids, the literal diff, the unexplained flag.
	if labeler.last.DecisionEpisodeID != f.seed.EpisodeID || labeler.last.RunID != f.seed.RunID || labeler.last.AccountID != f.seed.AccountID {
		t.Fatalf("the labeler request ids = %+v", labeler.last)
	}
	if !labeler.last.Unexplained || len(labeler.last.ExplainingEvals) != 0 {
		t.Fatal("the seeded world has no prior failing evals: the delta must go to the worker as unexplained")
	}
	var changes []struct {
		Kind string `json:"kind"`
	}
	if err := json.Unmarshal(labeler.last.LiteralChanges, &changes); err != nil || len(changes) == 0 {
		t.Fatalf("literal_changes of the request = %v %s", err, labeler.last.LiteralChanges)
	}
	if len(labeler.last.SelectedCandidate) == 0 {
		t.Fatal("the selected candidate document was not passed to the labeler")
	}

	doc := deltaDoc(t, f.seed.EpisodeID)
	valid(t, "human_delta", doc)
	m := decode(t, doc)
	labels, _ := m["semantic_labels"].([]any)
	if len(labels) != 2 || labels[0] != "reduced_pressure" {
		t.Fatalf("semantic_labels = %v, want the worker's answer", labels)
	}
	if m["model"] != "stub-labeler-v1" {
		t.Fatalf("model = %v", m["model"])
	}
	if m["unexplained"] != true {
		t.Fatal("unexplained = false, want true")
	}
	crit, _ := m["candidate_criterion"].(map[string]any)
	if crit["suggested_eval_type"] != "cta_calibration" {
		t.Fatalf("candidate_criterion = %v", crit)
	}
	var kinds []string
	for _, e := range m["literal_changes"].([]any) {
		kinds = append(kinds, e.(map[string]any)["kind"].(string))
	}
	if !contains(kinds, "subject_changed") || !contains(kinds, "paragraph_edited") {
		t.Fatalf("literal_changes = %v, want subject_changed and paragraph_edited", kinds)
	}
	if got := scalar(t, `SELECT human_delta_id IS NOT NULL FROM decision_episodes WHERE id = $1::uuid`, f.seed.EpisodeID); got != "true" {
		t.Fatal("the episode does not link its human delta")
	}
	if got := scalar(t, `SELECT human_action FROM decision_episodes WHERE id = $1::uuid`, f.seed.EpisodeID); got != "APPROVE_WITH_EDIT" {
		t.Fatalf("episode action = %s, want APPROVE_WITH_EDIT", got)
	}
}

func TestExplainedDeltaDropsTheWorkersCriterion(t *testing.T) {
	// The worker offers a criterion anyway; an explained delta stores none — its links explain it.
	criterion := workerclient.DeltaCriterion{Statement: "not stored", SuggestedEvalType: "cta_calibration"}
	labeler := &stubLabeler{resp: workerclient.LabelDeltaResponse{
		SemanticLabels: []string{"smaller_ask"}, CandidateCriterion: &criterion, Model: "stub-labeler-v1"}}
	f := newFixtureWith(t, strategystore.WithLabeler(labeler))
	noEmail := strategytest.NewID()
	if _, err := env.DB.Exec(`INSERT INTO people (id, kind, display_name, account_id) VALUES ($1::uuid, 'contact', 'No Email (seed)', $2::uuid)`,
		noEmail, f.seed.AccountID); err != nil {
		t.Fatal(err)
	}

	if _, _, err := f.choose(f.seed.Candidates[1], nil); err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.choose(f.seed.Candidates[1], func(r *strategystore.DecisionRequest) {
		r.FinalTo = &[]strategystore.Recipient{{PersonID: noEmail, Role: "to"}}
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.send("send"); err == nil {
		t.Fatal("the no-email recipient must refuse the send")
	}
	subject := "Repaired"
	if _, _, err := f.choose(f.seed.Candidates[1], func(r *strategystore.DecisionRequest) {
		r.FinalTo = &[]strategystore.Recipient{{PersonID: f.seed.Marco, Role: "to"}}
		r.FinalArtifact = &strategystore.Artifact{Channel: "email", Subject: &subject, Body: "Hi Marco,\n\nLet us know what timing suits.\n\nBest,\nDana"}
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.send("send"); err != nil {
		t.Fatal(err)
	}

	doc := deltaDoc(t, f.seed.EpisodeID)
	valid(t, "human_delta", doc)
	m := decode(t, doc)
	if m["unexplained"] != false || m["candidate_criterion"] != nil {
		t.Fatalf("an explained delta stored %v", m)
	}
	if ids, _ := m["explained_by_eval_result_ids"].([]any); len(ids) == 0 {
		t.Fatal("the explained delta names no eval")
	}
}

func TestDeltaFallsBackWhenTheLabelerIsUnusable(t *testing.T) {
	for name, labeler := range map[string]*stubLabeler{
		"call fails":              {err: errors.New("worker down")},
		"label outside the vocab": {resp: workerclient.LabelDeltaResponse{SemanticLabels: []string{"made_up"}, Model: "bad"}},
		"unexplained, no criterion": {resp: workerclient.LabelDeltaResponse{
			SemanticLabels: []string{"smaller_ask"}, Model: "no-criterion"}},
	} {
		t.Run(name, func(t *testing.T) {
			f := newFixtureWith(t, strategystore.WithLabeler(labeler))
			editAndSend(t, f)

			doc := deltaDoc(t, f.seed.EpisodeID)
			valid(t, "human_delta", doc)
			m := decode(t, doc)
			if m["model"] != nil {
				t.Fatalf("fallback model = %v, want null", m["model"])
			}
			if labels, _ := m["semantic_labels"].([]any); len(labels) != 0 {
				t.Fatalf("fallback labels = %v, want none", labels)
			}
			crit, _ := m["candidate_criterion"].(map[string]any)
			if crit["statement"] == nil || crit["suggested_eval_type"] == nil {
				t.Fatalf("an unexplained fallback delta needs the core-written criterion: %v", crit)
			}
		})
	}
}

func TestDeltaWithoutALabelerFallsBackTheSameWay(t *testing.T) {
	f := newFixture(t) // no labeler configured
	editAndSend(t, f)
	doc := deltaDoc(t, f.seed.EpisodeID)
	valid(t, "human_delta", doc)
	m := decode(t, doc)
	if m["model"] != nil || m["unexplained"] != true {
		t.Fatalf("the unconfigured delta = %v", m)
	}
}

func contains(xs []string, x string) bool {
	for _, e := range xs {
		if e == x {
			return true
		}
	}
	return false
}
