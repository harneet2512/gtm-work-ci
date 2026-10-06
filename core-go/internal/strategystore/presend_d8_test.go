package strategystore_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/harneet2512/gtm-work/core-go/internal/bucket2"
	"github.com/harneet2512/gtm-work/core-go/internal/strategystore"
)

// HAR-97 D8 is LIVE / REQUIRED immediately before Send: the exact final artifact is judged inside the send-time
// evaluation, before the outbox write. Only the eval catalog's blocking rules may refuse; any other D8 FAIL or WARN
// is stored as a warning. The judge here is a test double: no model is called.

type d8Judge struct {
	mu      sync.Mutex
	calls   []strategystore.FinalArtifactInput
	verdict bucket2.Verdict
	err     error
	// seenAtCall is the committed state when the judge was asked: the send had not been recorded yet.
	seenAtCall func()
}

func (j *d8Judge) JudgeFinalArtifact(_ context.Context, in strategystore.FinalArtifactInput) (bucket2.Result, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.calls = append(j.calls, in)
	if j.seenAtCall != nil {
		j.seenAtCall()
	}
	if j.err != nil {
		return bucket2.Result{}, j.err
	}
	return bucket2.Result{Gate: "D8", SubGate: "model", JudgedType: "FinalArtifact", JudgedID: in.CandidateID,
		SpanID: "recomputed_action:" + in.EpisodeID, Grader: bucket2.ModelGrader("test-double", "v0"), Verdict: j.verdict,
		EvidenceRefs: []string{"candidate:" + in.CandidateID}, Observed: "cta_timing_correct=" + string(j.verdict),
		Why: "double: the call to action is premature"}.Finalize(), nil
}

func (j *d8Judge) count() int {
	j.mu.Lock()
	defer j.mu.Unlock()
	return len(j.calls)
}

func d8Fixture(t *testing.T, j *d8Judge) *fixture {
	t.Helper()
	f := newFixture(t)
	f.svc.SetFinalJudge(j)
	if _, _, err := f.choose(f.seed.Candidates[1], nil); err != nil {
		t.Fatal(err)
	}
	return f
}

func d8Rows(t *testing.T, runID, sub string) string {
	t.Helper()
	return scalar(t, `SELECT COALESCE(string_agg(g.verdict, ','), '') FROM gate_results g JOIN decision_episodes de ON de.id = g.decision_episode_id
 WHERE de.agent_run_id = $1::uuid AND g.gate = 'D8' AND g.sub_gate = $2`, runID, sub)
}

func recordedEffects(t *testing.T, runID string) string {
	t.Helper()
	return scalar(t, `SELECT count(*)::text FROM agent_run_steps WHERE agent_run_id = $1::uuid AND step = 'execute' AND status = 'recorded'`, runID)
}

func TestD8RunsAndIsStoredBeforeTheOutboxWrite(t *testing.T) {
	j := &d8Judge{verdict: bucket2.Pass}
	f := newFixture(t)
	f.svc.SetFinalJudge(j)
	if _, _, err := f.choose(f.seed.Candidates[1], nil); err != nil {
		t.Fatal(err)
	}
	var effectsAtCall, decisionAtCall string
	j.seenAtCall = func() {
		effectsAtCall = recordedEffects(t, f.seed.RunID)
		decisionAtCall = scalar(t, `SELECT send_decision FROM human_strategy_decisions WHERE agent_run_id = $1::uuid`, f.seed.RunID)
	}
	if _, err := f.send("send"); err != nil {
		t.Fatal(err)
	}
	if j.count() != 1 {
		t.Fatalf("the D8 judge was asked %d times, want once, before the send", j.count())
	}
	if effectsAtCall != "0" || decisionAtCall != "pending" {
		t.Fatalf("D8 ran after the send was recorded: effects=%s decision=%s", effectsAtCall, decisionAtCall)
	}
	in := j.calls[0]
	if len(in.To) == 0 || in.Artifact.Body == "" || in.Artifact.Channel == "" || in.CandidateID != f.seed.Candidates[1] {
		t.Fatalf("the judge did not see the exact final artifact: %+v", in)
	}
	if got := d8Rows(t, f.seed.RunID, "model"); got != "pass" {
		t.Fatalf("the pre-send D8 model result is not stored: %q", got)
	}
	for _, sub := range []string{"recipients", "no_stale_content", "policy_gates"} {
		if d8Rows(t, f.seed.RunID, sub) == "" {
			t.Fatalf("the deterministic D8 sub-gate %s is not stored", sub)
		}
	}
	f.svc.WaitGates() // no gate runner here; the post-send pass must not ask the model again either way
	if j.count() != 1 {
		t.Fatalf("a send costs one D8 model call, got %d", j.count())
	}
}

func TestACatalogBlockingD8FailureRefusesTheSend(t *testing.T) {
	j := &d8Judge{verdict: bucket2.Pass}
	f := d8Fixture(t, j)
	// A passage that exists only in a candidate that was not selected is stale content from another candidate.
	other := scalar(t, `SELECT full_action_artifact->>'body' FROM strategy_candidates WHERE id = $1::uuid`, f.seed.Candidates[0])
	own := scalar(t, `SELECT full_action_artifact->>'body' FROM strategy_candidates WHERE id = $1::uuid`, f.seed.Candidates[1])
	var stale string
	for _, s := range strings.FieldsFunc(other, func(r rune) bool { return r == '.' || r == '\n' }) {
		if s = strings.TrimSpace(s); len(s) >= 60 && !strings.Contains(strings.ToLower(own), strings.ToLower(s)) {
			stale = s
			break
		}
	}
	if stale == "" {
		t.Skip("the seeded candidates share every long sentence")
	}
	subject := "Follow up"
	if _, _, err := f.choose(f.seed.Candidates[1], func(r *strategystore.DecisionRequest) {
		r.FinalArtifact = &strategystore.Artifact{Channel: "email", Subject: &subject, Body: "Hi Marco,\n\n" + stale + ".\n\nBest,\nDana"}
	}); err != nil {
		t.Fatal(err)
	}
	_, err := f.send("send")
	asRefused(t, err, "blocking_eval")
	if !strings.Contains(err.Error(), "grounding") {
		t.Fatalf("the refusal does not name the catalog eval that blocked: %v", err)
	}
	if got := d8Rows(t, f.seed.RunID, "no_stale_content"); got != "fail" {
		t.Fatalf("the blocking D8 result is not stored for audit: %q", got)
	}
	if recordedEffects(t, f.seed.RunID) != "0" ||
		scalar(t, `SELECT count(*)::text FROM human_decisions WHERE agent_run_id = $1::uuid`, f.seed.RunID) != "0" {
		t.Fatal("a refused send was delivered or recorded as decided")
	}
}

func TestANonBlockingD8FailLetsTheSendThroughAndIsStoredAsAWarning(t *testing.T) {
	j := &d8Judge{verdict: bucket2.Fail}
	f := d8Fixture(t, j)
	if _, err := f.send("send"); err != nil {
		t.Fatalf("a D8 model FAIL must not block: %v", err)
	}
	if recordedEffects(t, f.seed.RunID) != "1" {
		t.Fatal("the send was not delivered")
	}
	if got := d8Rows(t, f.seed.RunID, "model"); got != "fail" {
		t.Fatalf("the D8 warning is not stored: %q", got)
	}
	why := scalar(t, `SELECT g.why FROM gate_results g JOIN decision_episodes de ON de.id = g.decision_episode_id
 WHERE de.agent_run_id = $1::uuid AND g.gate = 'D8' AND g.sub_gate = 'model'`, f.seed.RunID)
	if !strings.Contains(why, "premature") {
		t.Fatalf("the stored warning lost the judge's wording: %q", why)
	}
}

func TestAD8JudgeErrorIsUnknownAndDoesNotBlock(t *testing.T) {
	j := &d8Judge{err: errors.New("worker unreachable")}
	f := d8Fixture(t, j)
	if _, err := f.send("send"); err != nil {
		t.Fatalf("an unreachable D8 judge must not block: %v", err)
	}
	if got := d8Rows(t, f.seed.RunID, "model"); got != "unknown" {
		t.Fatalf("D8 must read unknown, never pass, when the judge failed: %q", got)
	}
}

func TestADiscardDoesNotRunD8(t *testing.T) {
	j := &d8Judge{verdict: bucket2.Pass}
	f := d8Fixture(t, j)
	if _, err := f.send("discard"); err != nil {
		t.Fatal(err)
	}
	if j.count() != 0 {
		t.Fatal("D8 judged an artifact that was never going to be sent")
	}
}
