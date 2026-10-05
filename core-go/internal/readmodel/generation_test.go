package readmodel_test

import (
	"context"
	"errors"
	"testing"

	"github.com/harneet2512/gtm-work/core-go/internal/ctxfixture"
	"github.com/harneet2512/gtm-work/core-go/internal/readmodel"
)

// runWithDraftStep is a fresh run of account A with the five typed steps, the draft step set as given.
func runWithDraftStep(t *testing.T, runStatus, stepStatus, detail string) string {
	t.Helper()
	w := ctxfixture.Get(t, env.DB)
	run := ctxfixture.FreshRun(t, env.DB, w.AccountA, runStatus)
	exec(t, `INSERT INTO agent_run_steps (agent_run_id, seq, step, run_mode, status)
 SELECT $1::uuid, ord, step, 'dry_run', 'pending' FROM unnest(ARRAY['build_context','draft','crm_intent','await_human','execute']) WITH ORDINALITY AS u(step, ord)`, run)
	exec(t, `UPDATE agent_run_steps SET status = $2, detail = $3::jsonb WHERE agent_run_id = $1::uuid AND step = 'draft'`, run, stepStatus, detail)
	return run
}

func TestRunReportsTheGenerationPhaseFromTheDraftStep(t *testing.T) {
	cases := []struct {
		name, runStatus, stepStatus, detail string
		phase                               string
		attempt                             int
		reason                              string // "" = null
	}{
		{"queued", "pending", "pending", `{}`, readmodel.PhaseQueued, 0, ""},
		{"generating", "context_built", "running", `{"attempt":1}`, readmodel.PhaseGenerating, 1, ""},
		{"evaluating", "context_built", "running", `{"attempt":1,"generated":{"candidates":[]}}`, readmodel.PhaseEvaluating, 1, ""},
		{"paused with the recorded reason", "context_built", "failed", `{"attempt":2,"error":{"phase":"generate","kind":"transient","reason":"provider circuit breaker is open"}}`,
			readmodel.PhasePaused, 2, "provider circuit breaker is open"},
		{"not orchestrated", "awaiting_human", "succeeded", `{}`, readmodel.PhaseNotOrchestrated, 0, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			run := runWithDraftStep(t, c.runStatus, c.stepStatus, c.detail)
			got, err := reader(t).Run(context.Background(), run)
			if err != nil {
				t.Fatal(err)
			}
			g := got.Generation
			if g == nil || g.Phase != c.phase || g.Attempt != c.attempt {
				t.Fatalf("generation = %+v, want phase %s attempt %d", g, c.phase, c.attempt)
			}
			if (c.reason == "") != (g.Reason == nil) || (g.Reason != nil && *g.Reason != c.reason) {
				t.Fatalf("reason = %v, want %q", g.Reason, c.reason)
			}
			if g.StrategySetID != nil || g.DecisionEpisodeID != nil {
				t.Fatalf("no set exists yet: %+v", g)
			}
			if got.ID != run || len(got.Steps) != 5 {
				t.Fatalf("run %s with %d steps", got.ID, len(got.Steps))
			}
		})
	}
}

func TestAPermanentlyFailedRunCarriesItsReason(t *testing.T) {
	run := runWithDraftStep(t, "context_built", "failed", `{"attempt":2,"error":{"reason":"step"}}`)
	exec(t, `UPDATE agent_runs SET status = 'failed', error = 'invalid output after one retry' WHERE id = $1::uuid`, run)
	got, err := reader(t).Run(context.Background(), run)
	if err != nil {
		t.Fatal(err)
	}
	if g := got.Generation; g.Phase != readmodel.PhaseFailed || g.Reason == nil || *g.Reason != "invalid output after one retry" {
		t.Fatalf("generation = %+v", g)
	}
}

func TestRunOfAnUnknownOrMalformedIdIsNotFound(t *testing.T) {
	for _, id := range []string{missing, "not-a-uuid", ""} {
		if _, err := reader(t).Run(context.Background(), id); !errors.Is(err, readmodel.ErrNotFound) {
			t.Fatalf("run %q: %v", id, err)
		}
	}
}

func TestTheTraceCarriesTheSameGenerationAsTheRun(t *testing.T) {
	run := runWithDraftStep(t, "context_built", "running", `{"attempt":1}`)
	tr, err := reader(t).Trace(context.Background(), run)
	if err != nil {
		t.Fatal(err)
	}
	if tr.Run.Generation == nil || tr.Run.Generation.Phase != readmodel.PhaseGenerating {
		t.Fatalf("trace run generation = %+v", tr.Run.Generation)
	}
}
