package strategystore_test

import (
	"testing"

	"github.com/harneet2512/gtm-work/core-go/internal/strategystore"
	"github.com/harneet2512/gtm-work/core-go/internal/strategystore/strategytest"
	"github.com/harneet2512/gtm-work/core-go/internal/workerclient"
)

// The HAR-119 write paths of the send and verdict flow: explained deltas queue generator_feedback,
// unexplained deltas and corrected verdicts seed candidate evaluator versions + candidate knowledge, and
// shadow axes run alongside the send-time evals without ever blocking.

func TestUnexplainedDeltaSeedsCandidateEvaluatorAndKnowledge(t *testing.T) {
	criterion := workerclient.DeltaCriterion{Statement: "The human rewrites the ask when the buyer slows the thread.",
		SuggestedEvalType: "cta_calibration"}
	labeler := &stubLabeler{resp: workerclient.LabelDeltaResponse{
		SemanticLabels:     []string{"reduced_pressure"},
		CandidateCriterion: &criterion, Model: "stub-labeler-v1"}}
	f := newFixtureWith(t, strategystore.WithLabeler(labeler))

	editAndSend(t, f)

	deltaID := scalar(t, `SELECT human_delta_id::text FROM decision_episodes WHERE id = $1::uuid`, f.seed.EpisodeID)
	var version, kind, status, knowledgeID, spec string
	if err := env.DB.QueryRow(`SELECT version::text, kind, status, knowledge_id::text, shadow_spec::text
 FROM evaluator_versions WHERE evaluator = 'cta_calibration' AND source_human_delta_id = $1::uuid`,
		deltaID).Scan(&version, &kind, &status, &knowledgeID, &spec); err != nil {
		t.Fatalf("the unexplained delta seeded no candidate evaluator: %v", err)
	}
	if kind != "semantic" || status != "candidate" {
		t.Fatalf("candidate evaluator = %s v%s (%s)", kind, version, status)
	}
	if m := decode(t, []byte(spec)); m["account_id"] != f.seed.AccountID {
		t.Fatalf("the shadow spec binds a different account: %v", m["account_id"])
	}
	if got := scalar(t, `SELECT status || ':' || (provenance ->> 'created_from') FROM knowledge WHERE id = $1::uuid`,
		knowledgeID); got != "candidate:human_delta" {
		t.Fatalf("seeded knowledge = %s, want a candidate created from the human delta", got)
	}
	if got := scalar(t, `SELECT candidate_criterion ->> 'knowledge_candidate_id' FROM human_deltas WHERE id = $1::uuid`,
		deltaID); got != knowledgeID {
		t.Fatalf("the delta's criterion links knowledge %s, want %s", got, knowledgeID)
	}
	// The candidate never runs as shadow and never feeds a generator_feedback row.
	if n := scalar(t, `SELECT count(*)::text FROM generator_feedback WHERE human_delta_id = $1::uuid`, deltaID); n != "0" {
		t.Fatalf("an unexplained delta queued %s feedback rows", n)
	}
}

// explainedEditFixture is an episode whose send repaired a blocking recipient edit: the delta is explained, so
// the send queued generator feedback.
func explainedEditFixture(t *testing.T) *fixture {
	t.Helper()
	labeler := &stubLabeler{resp: workerclient.LabelDeltaResponse{
		SemanticLabels: []string{"smaller_ask"}, Model: "stub-labeler-v1"}}
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
	return f
}

func TestExplainedDeltaQueuesGeneratorFeedback(t *testing.T) {
	f := explainedEditFixture(t)

	// The explained eval of the delta queued one feedback row: the same eval_run_id the explanation cites.
	var evalRunID, evalType, instruction, episodeID, consumed string
	err := env.DB.QueryRow(`SELECT g.eval_run_id::text, g.eval_type::text, g.instruction, g.decision_episode_id::text,
  COALESCE(g.consumed_by_run_id::text, '')
 FROM generator_feedback g JOIN human_deltas d ON d.id = g.human_delta_id
 WHERE d.decision_episode_id = $1::uuid`, f.seed.EpisodeID).
		Scan(&evalRunID, &evalType, &instruction, &episodeID, &consumed)
	if err != nil {
		t.Fatalf("the explained delta queued no generator feedback: %v", err)
	}
	if episodeID != f.seed.EpisodeID || instruction == "" || consumed != "" {
		t.Fatalf("feedback = eval %s type %s episode %s instruction %q consumed %q",
			evalRunID, evalType, episodeID, instruction, consumed)
	}
	if got := scalar(t, `SELECT count(*)::text FROM human_delta_explanations x
 JOIN human_deltas d ON d.id = x.human_delta_id WHERE x.eval_run_id = $1::uuid AND d.decision_episode_id = $2::uuid`,
		evalRunID, f.seed.EpisodeID); got != "1" {
		t.Fatalf("the queued eval %s is not the explaining eval", evalRunID)
	}
}

func TestCorrectedVerdictSeedsTheHumanDeltaCriterion(t *testing.T) {
	f := sentFixture(t)
	if _, err := f.verdict(func(r *strategystore.VerdictRequest) {
		r.Verdict, r.CorrectedStatement = "corrected", "The buyer asked for a slower cadence, not a smaller ask."
	}); err != nil {
		t.Fatal(err)
	}
	var status, kind, createdFrom, knowledgeID string
	if err := env.DB.QueryRow(`SELECT status, kind, created_from, COALESCE(knowledge_id::text, '')
 FROM evaluator_versions WHERE evaluator = 'human_delta' ORDER BY version DESC LIMIT 1`).
		Scan(&status, &kind, &createdFrom, &knowledgeID); err != nil {
		t.Fatalf("the corrected verdict seeded no candidate: %v", err)
	}
	if status != "candidate" || kind != "human_delta" || createdFrom != "manual" || knowledgeID == "" {
		t.Fatalf("human_delta candidate = %s %s %s knowledge %s", status, kind, createdFrom, knowledgeID)
	}
	if got := scalar(t, `SELECT status FROM knowledge WHERE id = $1::uuid`, knowledgeID); got != "candidate" {
		t.Fatalf("verdict knowledge = %s, want candidate", got)
	}
}

func TestShadowAxisRunsAlongTheSendWithoutBlocking(t *testing.T) {
	f := newFixture(t)
	// A learned axis already earned shadow on this account: it checks the corrected channel.
	spec := `{"account_id":"` + f.seed.AccountID + `","literal_changes":[{"kind":"channel_changed","after":"slack"}]}`
	if _, err := env.DB.Exec(`INSERT INTO evaluator_versions
 (evaluator, version, status, kind, rubric, created_from, shadow_spec)
 VALUES ('channel_appropriateness', 42, 'shadow', 'deterministic',
 'The human moves this thread to Slack when the buyer lives there.', 'human_delta', $1::jsonb)`, spec); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, _ = env.DB.Exec(`UPDATE evaluator_versions SET status = 'retired' WHERE evaluator = 'channel_appropriateness' AND version = 42`)
	}()

	if _, _, err := f.choose(f.seed.Candidates[1], nil); err != nil {
		t.Fatal(err)
	}
	if _, err := f.send("send"); err != nil {
		t.Fatalf("a shadow axis must never block a send: %v", err)
	}
	// The send's final artifact is email while the correction chose Slack: the axis reports the violation
	// alongside the evals, unblocking.
	var verdict, blocking, version string
	if err := env.DB.QueryRow(`SELECT verdict, blocking::text, evaluator_version FROM eval_runs
 WHERE agent_run_id = $1::uuid AND evaluator = 'channel_appropriateness'
   AND evaluator_version = 'channel_appropriateness:v42'`, f.seed.RunID).
		Scan(&verdict, &blocking, &version); err != nil {
		t.Fatalf("no shadow result persisted: %v", err)
	}
	if verdict != "fail" || blocking != "false" || version != "channel_appropriateness:v42" {
		t.Fatalf("shadow result = %s blocking %s version %s", verdict, blocking, version)
	}
}

// TestSendTimeLearnedFailureNeverExplainsTheDeltaItFlags is the repairedEvals self-support regression:
// a learned axis on a canonical eval name writes a send-time FAIL (kind human_delta) while the canonical
// eval PASSes — under a bare axis-name match that fail row counted as a "prior failure repaired",
// explaining the delta with the very result written beside it. Priors are now the rows that predate the
// send-time batch, matched on (evaluator, version, kind).
func TestSendTimeLearnedFailureNeverExplainsTheDeltaItFlags(t *testing.T) {
	labeler := &stubLabeler{resp: workerclient.LabelDeltaResponse{
		SemanticLabels: []string{"reduced_pressure"}, Model: "stub-labeler-v1"}}
	f := newFixtureWith(t, strategystore.WithLabeler(labeler))

	// A learned axis sits on the canonical deterministic name: its proxy (the human moves this thread
	// to Slack) fails on the email artifact about to be sent.
	spec := `{"account_id":"` + f.seed.AccountID + `","literal_changes":[{"kind":"channel_changed","after":"slack"}]}`
	if _, err := env.DB.Exec(`INSERT INTO evaluator_versions
 (evaluator, version, status, kind, rubric, created_from, shadow_spec)
 VALUES ('recipient_correctness', 2, 'shadow', 'deterministic',
 'The human moves this thread to Slack.', 'human_delta', $1::jsonb)`, spec); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, _ = env.DB.Exec(`UPDATE evaluator_versions SET status = 'retired' WHERE evaluator = 'recipient_correctness' AND version = 2`)
	}()

	// Attempt 1 refuses on a recipient with no email: persistRefused writes the genuine prior fail.
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

	// Attempt 2 repairs the recipient but stays on email — canonical recipient_correctness passes,
	// the learned axis still fails (kind human_delta, version v2).
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

	learnedFail := scalar(t, `SELECT id::text FROM eval_runs
 WHERE agent_run_id = $1::uuid AND kind = 'human_delta' AND verdict = 'fail'`, f.seed.RunID)
	if learnedFail == "" {
		t.Fatal("the learned axis produced no fail row — the fixture no longer exercises the bug")
	}
	deltaID := scalar(t, `SELECT human_delta_id::text FROM decision_episodes WHERE id = $1::uuid`, f.seed.EpisodeID)
	if deltaID == "" {
		t.Fatal("the edited send wrote no delta")
	}
	if n := scalar(t, `SELECT count(*)::text FROM human_delta_explanations x JOIN eval_runs e ON e.id = x.eval_run_id
 WHERE x.human_delta_id = $1::uuid AND e.kind = 'human_delta'`, deltaID); n != "0" {
		t.Fatalf("%s learned results explain the delta — send-time self-support", n)
	}
	if n := scalar(t, `SELECT count(*)::text FROM human_delta_explanations
 WHERE human_delta_id = $1::uuid AND eval_run_id = $2::uuid`, deltaID, learnedFail); n != "0" {
		t.Fatal("the delta cites the send-time learned fail it was written alongside")
	}
	// The delta IS explained — by the attempt-1 canonical failure, same axis name, v1, deterministic.
	if got := scalar(t, `SELECT e.evaluator || '|' || e.evaluator_version || '|' || e.kind FROM human_delta_explanations x
 JOIN eval_runs e ON e.id = x.eval_run_id WHERE x.human_delta_id = $1::uuid AND e.evaluator = 'recipient_correctness'`,
		deltaID); got != "recipient_correctness|recipient_correctness:v1|deterministic" {
		t.Fatalf("explaining eval = %q, want the attempt-1 canonical recipient_correctness:v1 fail", got)
	}
	if got := scalar(t, `SELECT unexplained::text FROM human_deltas WHERE id = $1::uuid`, deltaID); got != "false" {
		t.Fatalf("delta unexplained = %s — the genuine canonical repair was lost with the bogus one", got)
	}
}
