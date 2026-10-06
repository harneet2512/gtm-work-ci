package orchestrator_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"github.com/harneet2512/gtm-work/core-go/internal/bucket1run"
	"github.com/harneet2512/gtm-work/core-go/internal/workerclient"
)

// replayJudge answers the way a recorded cassette does: one verdict per requested dimension, citing the first
// evidence ids it was allowed. It counts its calls per kind.
type replayJudge struct {
	mu    sync.Mutex
	calls map[string]int
}

var judgeDimension = map[string]string{
	"b1_inference_boundary": "inference_boundary", "b3_prior_context": "supporting_and_conflicting_links",
	"b5_precedent_relevance": "relevance_and_misses", "b8_synthesis": "confidence_and_omissions",
}

func (j *replayJudge) DecisionJudge(_ context.Context, req workerclient.DecisionJudgeRequest) (workerclient.DecisionJudgeResponse, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.calls == nil {
		j.calls = map[string]int{}
	}
	j.calls[req.Kind]++
	refs := req.EvidenceIDs
	if len(refs) > 2 {
		refs = refs[:2]
	}
	return workerclient.DecisionJudgeResponse{Kind: req.Kind, SubjectID: req.SubjectID, Model: "qwen/qwen3.8-flash", PromptVersion: "decision_judge:v1",
		Dimensions: []workerclient.DimensionVerdict{{Dimension: judgeDimension[req.Kind], Verdict: "pass", Why: "recorded judgment", EvidenceRefs: refs}}}, nil
}

type storedGate struct {
	gate, verdict, observed, why, grader string
	refs                                 []string
}

func storedBucket1(t *testing.T, episodeID string) map[string]storedGate {
	t.Helper()
	rows, err := env.DB.Query(`SELECT gate, verdict, observed, why, evidence_refs::text, grader::text FROM gate_results
 WHERE decision_episode_id = $1::uuid AND gate LIKE 'B%'`, episodeID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := map[string]storedGate{}
	for rows.Next() {
		var g storedGate
		var refs string
		if err := rows.Scan(&g.gate, &g.verdict, &g.observed, &g.why, &refs, &g.grader); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal([]byte(refs), &g.refs); err != nil {
			t.Fatal(err)
		}
		out[g.gate] = g
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func refResolves(t *testing.T, db *sql.DB, ref string) bool {
	t.Helper()
	kind, id, _ := strings.Cut(ref, ":")
	table := map[string]string{"activity": "activities", "claim": "claims", "agent_run_step": "agent_run_steps"}[kind]
	if table == "" {
		return false
	}
	var ok bool
	if err := db.QueryRow(`SELECT EXISTS (SELECT 1 FROM `+table+` WHERE id = $1::uuid)`, id).Scan(&ok); err != nil {
		t.Fatal(err)
	}
	return ok
}

// A published fixture episode produces B1 to B9, persisted. Each is a real verdict whose evidence resolves in the
// database, or unknown with a stated reason; no model gate is judged twice.
func TestPublishedEpisodePersistsBucket1WithResolvableEvidenceOrAStatedReason(t *testing.T) {
	sc := newScene(t)
	judge := &replayJudge{}
	runner, err := bucket1run.New(env.DB, judge, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	svc := service(t, newFake(sc))
	var hookErr error
	svc.SetAfterPublish(func(ctx context.Context, runID string) { _, hookErr = runner.RunPublish(ctx, runID) })
	out := mustRun(t, svc, sc.RunID)
	if hookErr != nil {
		t.Fatalf("bucket 1 after publish: %v", hookErr)
	}
	got := storedBucket1(t, out.EpisodeID)
	for _, g := range []string{"B1", "B2", "B3", "B4", "B5", "B6", "B7", "B8", "B9"} {
		r, ok := got[g]
		if !ok {
			t.Fatalf("%s was not persisted", g)
		}
		t.Logf("%s %s: %s | refs %v", g, r.verdict, r.why, r.refs)
		if r.why == "" || r.observed == "" {
			t.Errorf("%s has no observed or why: %+v", g, r)
		}
		if r.verdict == "unknown" {
			continue // a stated reason (r.why) is all unknown needs
		}
		if len(r.refs) == 0 {
			t.Errorf("%s says %s with no evidence", g, r.verdict)
		}
		for _, ref := range r.refs {
			if !refResolves(t, env.DB, ref) {
				t.Errorf("%s cites %s, which does not resolve in the database", g, ref)
			}
		}
	}
	if r := got["B5"]; r.verdict != "unknown" || !strings.Contains(r.why, "no precedent") {
		t.Errorf("B5 without precedents = %s (%s), want unknown: no precedent", r.verdict, r.why)
	}
	// One call per semantic gate, never more, and none for a gate with nothing to read.
	for kind, n := range judge.calls {
		if n != 1 {
			t.Errorf("%s was judged %d times", kind, n)
		}
	}
	if judge.calls["b5_precedent_relevance"] != 0 {
		t.Error("B5 made a model call with no precedents to read")
	}
	before := len(judge.calls)
	if _, err := runner.RunPublish(bg, sc.RunID); err != nil {
		t.Fatalf("second run: %v", err)
	}
	for kind, n := range judge.calls {
		if n != 1 {
			t.Errorf("a re-run judged %s again (%d calls)", kind, n)
		}
	}
	if len(judge.calls) != before {
		t.Error("a re-run called another model gate")
	}
}

func TestWithoutAJudgeTheSemanticAssertionsAreNotMeasuredAndTheDeterministicOnesStillRun(t *testing.T) {
	sc := newScene(t)
	runner, err := bucket1run.New(env.DB, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	out := mustRun(t, service(t, newFake(sc)), sc.RunID)
	if _, err := runner.RunPublish(bg, sc.RunID); err != nil {
		t.Fatal(err)
	}
	got := storedBucket1(t, out.EpisodeID)
	if len(got) != 9 {
		t.Fatalf("%d gates persisted", len(got))
	}
	if r := got["B1"]; r.verdict != "unknown" {
		t.Errorf("B1 without a recorded judgment = %s, never a pass", r.verdict)
	}
}
