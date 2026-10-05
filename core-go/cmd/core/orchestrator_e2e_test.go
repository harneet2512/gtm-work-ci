package main

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/config"
	"github.com/harneet2512/gtm-work/core-go/internal/ctxfixture"
	"github.com/harneet2512/gtm-work/core-go/internal/providerbreaker"
)

// The end-to-end tests of the wiring (HAR-117): ingest, trigger, run, driver, orchestrator, published set, Slack.
// Their helpers are in orchestrator_e2e_helpers_test.go.

func TestAMaterialEventIsDrivenToAPublishedSetAndSlackPostsItsMessageTwo(t *testing.T) {
	account := seedAcme(t)
	worker := &scriptedWorker{}
	release := make(chan struct{})
	judging := make(chan struct{})
	releaseJudge := make(chan struct{})
	worker.onStrategies = func(call int) error {
		if call == 1 {
			<-release // hold generation so the status surface can be read while it runs
		}
		return nil
	}
	worker.onJudge = func(call int) {
		if call == 1 {
			close(judging)
			<-releaseJudge // hold evaluation
		}
	}
	base, stop := startCoreWith(t, orchestratorConfig(t, true), deps{worker: worker})
	run := ingestMaterialEvent(t, base, account)

	// status: generating while the worker holds the strategies call
	if v := waitForPhase(t, base, run, "generating"); (v.Status != "pending" && v.Status != "context_built") || v.Generation.Attempt != 1 {
		t.Fatalf("generating: %+v", v)
	}
	close(release)
	<-judging
	if v := waitForPhase(t, base, run, "evaluating"); v.Generation.StrategySetID != nil {
		t.Fatalf("evaluating but already has a set: %+v", v)
	}
	close(releaseJudge)
	published := waitForPhase(t, base, run, "published")
	if published.Status != "awaiting_human" || published.Generation.StrategySetID == nil {
		t.Fatalf("published: %+v", published)
	}

	// the set is the worker's three candidates, each with an eval bundle, readable by the surfaces
	var rs struct {
		StrategySet struct {
			Candidates []struct {
				CandidateID string `json:"candidate_id"`
			} `json:"candidates"`
			NoAcceptableCandidate bool `json:"no_acceptable_candidate"`
		} `json:"strategy_set"`
		EvalBundles []struct {
			Items []struct {
				EvalType string `json:"eval_type"`
				Verdict  string `json:"verdict"`
			} `json:"items"`
		} `json:"eval_bundles"`
	}
	if code := getJSON(t, base+"/runs/"+run+"/strategies", &rs); code != http.StatusOK {
		t.Fatalf("strategies = %d", code)
	}
	if len(rs.StrategySet.Candidates) != 3 || len(rs.EvalBundles) != 3 || rs.StrategySet.NoAcceptableCandidate {
		t.Fatalf("set: %d candidates, %d bundles, none acceptable = %v", len(rs.StrategySet.Candidates), len(rs.EvalBundles), rs.StrategySet.NoAcceptableCandidate)
	}
	strategies, judges, revises := worker.counts()
	if strategies != 1 || judges != 3 || revises != 0 {
		t.Fatalf("worker calls: strategies %d, judge %d, revise %d, want 1, 3, 0", strategies, judges, revises)
	}

	// Slack: the outbox announced the set; the relay posts Message 2 from the real set and acknowledges it
	relay, fs := relayTo(t, base, account)
	if n, err := relay.Drain(context.Background()); err != nil || n < 1 {
		t.Fatalf("relay drain = %d, %v", n, err)
	}
	posts := fs.CallsOf("chat.postMessage")
	if len(posts) != 1 || !strings.Contains(posts[0].Text, "Choose the next move (3 candidates)") {
		t.Fatalf("posts = %+v", posts)
	}
	ids, evals := chooserCards(t, posts[0].Blocks)
	if len(ids) != 3 {
		t.Fatalf("Message 2 has %d candidate cards, want 3", len(ids))
	}
	for _, c := range rs.StrategySet.Candidates {
		line, ok := evals[c.CandidateID]
		if !ok || !strings.Contains(line, "PASS") || strings.Contains(line, "No relevant evals") {
			t.Fatalf("candidate %s: evals line %q (cards %v)", c.CandidateID, line, ids)
		}
	}
	if n, _ := relay.Drain(context.Background()); n != 0 || len(fs.CallsOf("chat.postMessage")) != 1 {
		t.Fatalf("a second drain posted again (%d)", n)
	}
	if err := stop(); err != nil {
		t.Fatalf("core stopped with %v", err)
	}
}

func TestAnOpenBreakerLeavesTheRunResumableAndItCompletesAfterTheReset(t *testing.T) {
	account := seedAcme(t)
	// A first core without the driver (the behaviour before this wiring) opens the run and leaves it queued: the
	// breaker also pauses extraction, so an open breaker would stop the run from ever being created.
	first, stopFirst := startCoreWith(t, orchestratorConfig(t, false), deps{})
	run := ingestMaterialEvent(t, first, account)
	if err := stopFirst(); err != nil {
		t.Fatal(err)
	}

	// Core restarts with the driver on and the provider breaker open.
	breaker, err := providerbreaker.New(1, time.Hour, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	breaker.Failure("worker 424 provider_unavailable_nonretryable")
	worker := &scriptedWorker{}
	base, stop := startCoreWith(t, orchestratorConfig(t, true), deps{worker: worker, breaker: breaker})

	time.Sleep(500 * time.Millisecond) // twenty polls with the breaker open
	if s, j, r := worker.counts(); s+j+r != 0 {
		t.Fatalf("the worker was called %d times through an open breaker", s+j+r)
	}
	if v := runPhase(t, base, run); v.Status != "pending" || v.Generation.Phase != "queued" {
		t.Fatalf("an open breaker must leave the run queued and open, got %+v", v)
	}

	if code, body := request(t, http.MethodPost, base+"/provider-breaker/reset", apiToken, ""); code != http.StatusOK {
		t.Fatalf("reset = %d %s", code, body)
	}
	if v := waitForPhase(t, base, run, "published"); v.Status != "awaiting_human" {
		t.Fatalf("after the reset: %+v", v)
	}
	if s, j, _ := worker.counts(); s != 1 || j != 3 {
		t.Fatalf("worker calls after the reset: strategies %d, judge %d, want 1 and 3", s, j)
	}
	if err := stop(); err != nil {
		t.Fatalf("core stopped with %v", err)
	}
}

func TestABreakerThatOpensMidRunPausesTheRunAndTheResetResumesIt(t *testing.T) {
	account := seedAcme(t)
	breaker, err := providerbreaker.New(1, time.Hour, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	worker := &scriptedWorker{}
	worker.onStrategies = func(call int) error {
		if call == 1 {
			return providerDown{} // the provider refuses the first generation: the breaker trips on it
		}
		return nil
	}
	base, stop := startCoreWith(t, orchestratorConfig(t, true), deps{worker: worker, breaker: breaker})
	run := ingestMaterialEvent(t, base, account)

	paused := waitForPhase(t, base, run, "paused")
	if paused.Status != "context_built" || paused.Generation.Reason == nil || *paused.Generation.Reason == "" || !breaker.Open() {
		t.Fatalf("paused: %+v breaker open %v", paused, breaker.Open())
	}
	time.Sleep(300 * time.Millisecond)
	if s, _, _ := worker.counts(); s != 1 {
		t.Fatalf("strategies calls = %d while the breaker is open, want 1", s)
	}
	breaker.Reset()
	if v := waitForPhase(t, base, run, "published"); v.Generation.Attempt != 2 {
		t.Fatalf("the run must be resumed in place (attempt 2): %+v", v)
	}
	if err := stop(); err != nil {
		t.Fatalf("core stopped with %v", err)
	}
}

func TestTheOrchestratorIsOffByDefaultSoAnOpenedRunIsNotDriven(t *testing.T) {
	account := seedAcme(t)
	worker := &scriptedWorker{}
	base, stop := startCoreWith(t, orchestratorConfig(t, false), deps{worker: worker})
	run := ingestMaterialEvent(t, base, account)

	time.Sleep(400 * time.Millisecond)
	if s, j, r := worker.counts(); s+j+r != 0 {
		t.Fatalf("the worker was called %d times with GHOST_ORCHESTRATOR off", s+j+r)
	}
	if v := runPhase(t, base, run); v.Status != "pending" || v.Generation.Phase != "queued" {
		t.Fatalf("an unwired run must stay queued: %+v", v)
	}
	if err := stop(); err != nil {
		t.Fatalf("core stopped with %v", err)
	}
}

func TestAHistoricalPlaceholderRunIsNeverDrivenUnderThePlayScopeNorPostedToSlack(t *testing.T) {
	account := seedAcme(t)
	worker := &scriptedWorker{}
	cfg := orchestratorConfig(t, true)
	cfg.Orchestrator.Scope = config.ScopePlay // the code default and the demo's
	base, stop := startCoreWith(t, cfg, deps{worker: worker})
	run := ingestMaterialEvent(t, base, account) // an open run that no Play made: what a crmarena import leaves

	time.Sleep(500 * time.Millisecond) // twenty polls
	if s, j, r := worker.counts(); s+j+r != 0 {
		t.Fatalf("the worker was called %d times for a historical run", s+j+r)
	}
	if v := runPhase(t, base, run); v.Status != "pending" || v.Generation.Phase != "queued" {
		t.Fatalf("a historical run must stay queued: %+v", v)
	}
	relay, fs := relayTo(t, base, account)
	if n, err := relay.Drain(context.Background()); err != nil || n != 0 || len(fs.CallsOf("chat.postMessage")) != 0 {
		t.Fatalf("relay drained %d (%v), Slack posts %d: a historical run must never reach Slack", n, err, len(fs.CallsOf("chat.postMessage")))
	}

	// the same run, once a Play owns its trigger activity, is driven and reaches Slack exactly once
	ctxfixture.MarkRunAsPlay(t, env.DB, run)
	if v := waitForPhase(t, base, run, "published"); v.Status != "awaiting_human" {
		t.Fatalf("the play's run: %+v", v)
	}
	if n, err := relay.Drain(context.Background()); err != nil || n != 1 || len(fs.CallsOf("chat.postMessage")) != 1 {
		t.Fatalf("relay drained %d (%v), Slack posts %d, want 1 and 1", n, err, len(fs.CallsOf("chat.postMessage")))
	}
	if err := stop(); err != nil {
		t.Fatalf("core stopped with %v", err)
	}
}
