package main

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/config"
	"github.com/harneet2512/gtm-work/core-go/internal/orchestrator"
	"github.com/harneet2512/gtm-work/core-go/internal/runtoken"
)

func TestADisabledOrchestratorBuildsNoDriver(t *testing.T) {
	logger, logs := quietLogger()
	d, err := newOrchestratorDriver(env.DB, orchestratorConfig(t, false), logger, newTestBreaker(t), deps{})
	if err != nil || d != nil {
		t.Fatalf("driver %v, err %v: GHOST_ORCHESTRATOR=off must build nothing", d, err)
	}
	if !strings.Contains(logs.String(), "run orchestrator is off") {
		t.Fatalf("the off state must be said out loud: %s", logs.String())
	}
}

func TestAnEnabledOrchestratorNeedsAWorkerAndItsRuleFiles(t *testing.T) {
	logger, _ := quietLogger()
	noWorker := orchestratorConfig(t, true)
	noWorker.WorkerURL = ""
	if _, err := newOrchestratorDriver(env.DB, noWorker, logger, newTestBreaker(t), deps{}); err == nil || !strings.Contains(err.Error(), "WORKER_URL") {
		t.Fatalf("no worker: %v", err)
	}
	badRules := orchestratorConfig(t, true)
	badRules.Orchestrator.KnowledgeRulesPath = "does-not-exist.json"
	if _, err := newOrchestratorDriver(env.DB, badRules, logger, newTestBreaker(t), deps{}); err == nil || !strings.Contains(err.Error(), "GHOST_KNOWLEDGE_RULES") {
		t.Fatalf("missing knowledge rules: %v", err)
	}
	badRouting := orchestratorConfig(t, true)
	badRouting.Orchestrator.RoutingPath = "does-not-exist.json"
	if _, err := newOrchestratorDriver(env.DB, badRouting, logger, newTestBreaker(t), deps{}); err == nil || !strings.Contains(err.Error(), "GHOST_TRANSITION_ROUTING") {
		t.Fatalf("missing routing: %v", err)
	}
	badURL := orchestratorConfig(t, true)
	badURL.WorkerURL = "not a url"
	if _, err := newOrchestratorDriver(env.DB, badURL, logger, newTestBreaker(t), deps{}); err == nil {
		t.Fatal("an invalid worker URL must fail startup")
	}
}

func TestAnEnabledOrchestratorBuildsADriverAndSaysSo(t *testing.T) {
	logger, logs := quietLogger()
	cfg := orchestratorConfig(t, true)
	cfg.RunMode, cfg.AllowExternalWrites = "live", true
	cfg.RunTokenSecret = strings.Repeat("s3cret-", 6)
	d, err := newOrchestratorDriver(env.DB, cfg, logger, newTestBreaker(t), deps{})
	if err != nil || d == nil {
		t.Fatalf("driver %v, err %v", d, err)
	}
	for _, want := range []string{"run orchestrator on", "dry_run runs only"} {
		if !strings.Contains(logs.String(), want) {
			t.Errorf("log lacks %q: %s", want, logs.String())
		}
	}
	// the driver must start and stop cleanly with nothing to do
	stop := d.Start(context.Background())
	time.Sleep(50 * time.Millisecond)
	stop()
}

func TestRunRefusesToStartWithTheOrchestratorOnAndNoWorkerURL(t *testing.T) {
	cfg := testConfig("127.0.0.1:0")
	cfg.WorkerURL = ""
	cfg.Orchestrator = config.Orchestrator{Enabled: true, Concurrency: 1}
	logger, _ := quietLogger()
	if err := run(context.Background(), cfg, logger, nil); err == nil || !strings.Contains(err.Error(), "GHOST_ORCHESTRATOR") {
		t.Fatalf("err = %v", err)
	}
}

func TestTheOrchestratorSignerSharesTheContextAPIsKeyAndOutlivesOneRun(t *testing.T) {
	cfg := testConfig("127.0.0.1:0")
	api, err := newRunSigner(cfg)
	if err != nil {
		t.Fatal(err)
	}
	orch, err := newOrchestratorSigner(cfg)
	if err != nil {
		t.Fatal(err)
	}
	const run = "0f0a0000-0000-4000-8000-000000000601"
	now := time.Now()
	tok, err := orch.Issue(run, now)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := api.Verify(tok, now.Add(time.Minute)); err != nil || got != run {
		t.Fatalf("the context API must accept the orchestrator's token: %q, %v", got, err)
	}
	if need := orchestrator.RequiredTokenTTL(false); orch.TTL() <= need || orch.TTL() > runtoken.MaxTTL {
		t.Fatalf("token life %s must exceed what one run needs (%s) and stay within %s", orch.TTL(), need, runtoken.MaxTTL)
	}
	// the token still verifies where the shorter-lived API signer's own tokens would have expired
	if _, err := api.Verify(tok, now.Add(api.TTL()+time.Minute)); err != nil {
		t.Fatalf("a long run's token expired with the API signer's life: %v", err)
	}
	if _, err := api.Verify(tok, now.Add(orch.TTL()+time.Minute)); err == nil {
		t.Fatal("a token must expire")
	}
}
