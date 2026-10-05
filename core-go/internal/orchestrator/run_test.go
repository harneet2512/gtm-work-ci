package orchestrator_test

import (
	"encoding/json"
	"testing"

	"github.com/harneet2512/gtm-work/core-go/internal/orchestrator"
	"github.com/harneet2512/gtm-work/core-go/internal/runs"
	"github.com/harneet2512/gtm-work/core-go/internal/schemacheck"
	"github.com/harneet2512/gtm-work/core-go/internal/strategystore"
)

func validDoc(t testing.TB, name string, doc []byte) {
	t.Helper()
	v, err := schemacheck.New()
	if err != nil {
		t.Fatal(err)
	}
	if err := v.Validate(name, doc); err != nil {
		t.Fatalf("%s violates its contract: %v\n%s", name, err, doc)
	}
}

func decodeMap(t testing.TB, doc []byte) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(doc, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func mustRun(t testing.TB, svc *orchestrator.Service, runID string) orchestrator.Outcome {
	t.Helper()
	out, err := svc.Run(bg, runID)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	return out
}

func TestHappyPathPublishesOneCompleteSetThatTheReadEndpointServes(t *testing.T) {
	sc := newScene(t)
	fw := newFake(sc)
	var statusDuringGeneration, guidanceBefore string
	fw.onGen = func() {
		statusDuringGeneration = scalar(t, `SELECT status FROM agent_runs WHERE id = $1::uuid`, sc.RunID)
		guidanceBefore = scalar(t, `SELECT decision_guidance_id::text FROM agent_runs WHERE id = $1::uuid`, sc.RunID)
		// R1: while the run is generating it is context_built, which can never be recorded as a dry-run send.
		ex, _ := runs.NewExecutor(runs.DryRun, nil)
		if _, err := runs.ExecuteStep(bg, env.DB, ex, sc.RunID, runs.Effect{Kind: "send_email", Target: sc.Contact1, Body: []byte(`{}`)}); err == nil {
			t.Error("ExecuteStep must be refused while the run is context_built")
		}
	}
	svc := service(t, fw)
	out := mustRun(t, svc, sc.RunID)
	if !out.Published || out.SetID == "" || out.EpisodeID == "" {
		t.Fatalf("outcome %+v", out)
	}
	if statusDuringGeneration != "context_built" || guidanceBefore == "" {
		t.Fatalf("during generation: status %q, guidance %q: guidance must be persisted and the run context_built first", statusDuringGeneration, guidanceBefore)
	}
	if got := scalar(t, `SELECT status FROM agent_runs WHERE id = $1::uuid`, sc.RunID); got != "awaiting_human" {
		t.Fatalf("run status = %s", got)
	}
	if got := scalar(t, `SELECT status FROM decision_episodes WHERE id = $1::uuid`, out.EpisodeID); got != "awaiting_choice" {
		t.Fatalf("episode status = %s", got)
	}

	svcRead, err := strategystore.New(env.DB, nil)
	if err != nil {
		t.Fatal(err)
	}
	doc, err := svcRead.Strategies(bg, sc.RunID)
	if err != nil {
		t.Fatalf("the strategy-set read endpoint: %v", err)
	}
	var served struct {
		Set     json.RawMessage   `json:"strategy_set"`
		Bundles []json.RawMessage `json:"eval_bundles"`
	}
	if err := json.Unmarshal(doc, &served); err != nil {
		t.Fatal(err)
	}
	validDoc(t, "strategy_set", served.Set)
	if len(served.Bundles) != 3 {
		t.Fatalf("%d bundles", len(served.Bundles))
	}
	for _, b := range served.Bundles {
		validDoc(t, "eval_bundle", b)
		items := decodeMap(t, b)["items"].([]any)
		var det, sem int
		for _, it := range items {
			if it.(map[string]any)["eval_type"] == "buyer_readiness" {
				sem++
			}
			if it.(map[string]any)["eval_type"] == "recipient_correctness" {
				det++
			}
		}
		if det != 1 || sem != 1 {
			t.Fatalf("a bundle needs its deterministic and semantic evals attached: %s", b)
		}
	}
	cands := decodeMap(t, served.Set)["candidates"].([]any)
	if len(cands) != 3 {
		t.Fatalf("%d candidates", len(cands))
	}
	first := cands[0].(map[string]any)
	if first["preferred_by_agent"] != true || first["candidate_id"] != out.PreferredID {
		t.Fatalf("rank 1 = %v, outcome preferred %s", first, out.PreferredID)
	}
	if blocked := count(t, `SELECT count(*) FROM strategy_candidates c JOIN eval_bundles b ON b.id = c.eval_bundle_id
 WHERE c.id = $1::uuid AND jsonb_path_exists(b.items, '$[*] ? (@.result.blocking == true)')`, out.PreferredID); blocked != 0 {
		t.Fatal("the preferred candidate has a blocking failure")
	}
	if n := count(t, `SELECT count(*) FROM eval_runs WHERE agent_run_id = $1::uuid`, sc.RunID); n < 3*8 {
		t.Fatalf("only %d eval_runs rows: deterministic and semantic results must be saved per draft", n)
	}
	// E7: retrieved -> applicable -> used is on the build_context step; the run carries what was used.
	if scalar(t, `SELECT detail -> 'knowledge_attribution' ->> 'as_of' FROM agent_run_steps WHERE agent_run_id = $1::uuid AND step = 'build_context'`, sc.RunID) == "" {
		t.Fatal("the knowledge attribution was not recorded")
	}
	for step, want := range map[string]string{"build_context": "succeeded", "draft": "succeeded", "crm_intent": "skipped", "await_human": "running", "execute": "pending"} {
		if got := scalar(t, `SELECT status FROM agent_run_steps WHERE agent_run_id = $1::uuid AND step = $2`, sc.RunID, step); got != want {
			t.Fatalf("step %s = %s, want %s", step, got, want)
		}
	}
}

func TestARunThatAlreadyHasItsSetIsReturnedNeverRegenerated(t *testing.T) {
	sc := newScene(t)
	fw := newFake(sc)
	svc := service(t, fw)
	first := mustRun(t, svc, sc.RunID)
	again := mustRun(t, svc, sc.RunID)
	if again.Published || again.SetID != first.SetID || fw.strategies != 1 {
		t.Fatalf("repeat = %+v after %d generations", again, fw.strategies)
	}
}
