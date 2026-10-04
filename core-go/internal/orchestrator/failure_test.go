package orchestrator_test

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/clock"
	"github.com/harneet2512/gtm-work/core-go/internal/ctxfixture"
	"github.com/harneet2512/gtm-work/core-go/internal/orchestrator"
	"github.com/harneet2512/gtm-work/core-go/internal/providerbreaker"
	"github.com/harneet2512/gtm-work/core-go/internal/signalstore"
	"github.com/harneet2512/gtm-work/core-go/internal/workerclient"
)

func openRun(t *testing.T, accountID string) bool {
	t.Helper()
	open, err := signalstore.OpenRunExists(bg, env.DB, accountID)
	if err != nil {
		t.Fatal(err)
	}
	return open
}

func stepError(t *testing.T, runID string) (kind, phase string) {
	t.Helper()
	row := scalar(t, `SELECT (detail -> 'error' ->> 'kind') || '|' || (detail -> 'error' ->> 'phase') FROM agent_run_steps
 WHERE agent_run_id = $1::uuid AND step = 'draft'`, runID)
	for i := range row {
		if row[i] == '|' {
			return row[:i], row[i+1:]
		}
	}
	return row, ""
}

func TestATransientFailureLeavesTheRunResumableAndResumeConvergesOnOneSet(t *testing.T) {
	for name, failure := range map[string]error{
		"worker down":       &workerclient.Error{Status: 503, Code: "unavailable", Message: "worker overloaded", Retryable: true},
		"rate limited":      &workerclient.Error{Status: 429, Code: "rate_limited", Message: "slow down", Retryable: true},
		"provider refused":  &workerclient.Error{Status: 424, Code: workerclient.CodeProviderUnavailable, Message: "out of credits"},
		"token expired":     &workerclient.Error{Status: 502, Code: "core_unavailable", Message: "core context pull failed", Retryable: true},
		"worker unreadable": &workerclient.Error{Message: "worker unreachable or timed out: connection refused", Retryable: true},
	} {
		t.Run(name, func(t *testing.T) {
			sc := newScene(t)
			fw := newFake(sc)
			var failed atomic.Bool // candidates are judged in parallel
			fw.judgeErr = func(workerclient.JudgeRequest) error {
				if failed.CompareAndSwap(false, true) {
					return failure
				}
				return nil
			}
			svc := service(t, fw)
			if _, err := svc.Run(bg, sc.RunID); !orchestrator.IsTransient(err) {
				t.Fatalf("first run = %v, want a transient failure", err)
			}
			if got := scalar(t, `SELECT status FROM agent_runs WHERE id = $1::uuid`, sc.RunID); got != "context_built" {
				t.Fatalf("a transient failure must leave the run context_built (resumable), got %s", got)
			}
			if kind, phase := stepError(t, sc.RunID); kind != "transient" || phase != "judge" {
				t.Fatalf("recorded failure = %s in %s", kind, phase)
			}
			if scalar(t, `SELECT status FROM agent_run_steps WHERE agent_run_id = $1::uuid AND step = 'draft'`, sc.RunID) != "failed" ||
				count(t, `SELECT count(*) FROM strategy_sets WHERE agent_run_id = $1::uuid`, sc.RunID) != 0 {
				t.Fatal("the draft step must be failed and nothing published")
			}
			// the account is not stranded: the same run resumes in place, without a new generation or new guidance
			out, err := svc.Resume(bg, sc.RunID)
			if err != nil || !out.Published {
				t.Fatalf("resume = %+v, %v", out, err)
			}
			if fw.strategies != 1 {
				t.Fatalf("resume regenerated: %d Strategies calls", fw.strategies)
			}
			if n := count(t, `SELECT count(*) FROM decision_guidance WHERE agent_run_id = $1::uuid`, sc.RunID); n != 1 {
				t.Fatalf("%d guidance rows", n)
			}
			if count(t, `SELECT count(*) FROM strategy_sets WHERE agent_run_id = $1::uuid`, sc.RunID) != 1 ||
				scalar(t, `SELECT status FROM agent_runs WHERE id = $1::uuid`, sc.RunID) != "awaiting_human" {
				t.Fatal("resume must publish exactly one set")
			}
		})
	}
}

func TestAnOpenBreakerParksTheRunWithoutAnyModelCallAndResumesAfterReset(t *testing.T) {
	sc := newScene(t)
	fw := newFake(sc)
	breaker, err := providerbreaker.New(1, time.Hour, clock.NewFixed(wallNow), nil)
	if err != nil {
		t.Fatal(err)
	}
	breaker.Failure("provider out of credits")
	svc := service(t, providerbreaker.WorkerGuard{Inner: fw, Breaker: breaker})
	_, err = svc.Run(bg, sc.RunID)
	if !orchestrator.IsTransient(err) || !errors.Is(err, providerbreaker.ErrOpen) {
		t.Fatalf("run with an open breaker = %v", err)
	}
	if fw.strategies != 0 || len(fw.judges) != 0 {
		t.Fatalf("an open breaker let %d generation and %d judge calls through", fw.strategies, len(fw.judges))
	}
	if !openRun(t, sc.AccountID) || scalar(t, `SELECT status FROM agent_runs WHERE id = $1::uuid`, sc.RunID) != "context_built" {
		t.Fatal("the parked run must stay open and resumable")
	}
	breaker.Reset()
	if _, err := svc.Run(bg, sc.RunID); err != nil {
		t.Fatalf("resume after the breaker closed: %v", err)
	}
}

func TestAPermanentFailureMarksTheRunFailedAndFreesTheAccount(t *testing.T) {
	sc := newScene(t)
	fw := newFake(sc)
	fw.strErr = func(int) error {
		return &workerclient.Error{Status: 502, Code: "invalid_strategies", Message: "not three distinct candidates"}
	}
	svc := service(t, fw)
	_, err := svc.Run(bg, sc.RunID)
	if !orchestrator.IsPermanent(err) {
		t.Fatalf("run = %v, want a permanent failure", err)
	}
	if fw.strategies != 2 {
		t.Fatalf("invalid output is retried exactly once, got %d attempts", fw.strategies)
	}
	if scalar(t, `SELECT status FROM agent_runs WHERE id = $1::uuid`, sc.RunID) != "failed" || scalar(t, `SELECT error FROM agent_runs WHERE id = $1::uuid`, sc.RunID) == "" {
		t.Fatal("the run must be failed with its reason")
	}
	if kind, phase := stepError(t, sc.RunID); kind != "permanent" || phase != "generate" {
		t.Fatalf("recorded failure = %s in %s", kind, phase)
	}
	if openRun(t, sc.AccountID) {
		t.Fatal("a failed run must not hold the account (open_run_exists)")
	}
	if _, _, err := ctxfixture.InsertRun(bg, env.DB, sc.AccountID, "pending"); err != nil {
		t.Fatalf("the account must be free for its next run: %v", err)
	}
	for _, q := range []string{`strategy_sets`, `decision_episodes`, `agent_run_drafts`, `eval_bundles`} {
		if n := count(t, `SELECT count(*) FROM `+q+` WHERE agent_run_id = $1::uuid`, sc.RunID); n != 0 {
			t.Fatalf("%d %s rows were written by a failed run", n, q)
		}
	}
	if _, err := svc.Run(bg, sc.RunID); !errors.Is(err, orchestrator.ErrNotRunnable) {
		t.Fatalf("a failed run is final, got %v", err)
	}
}

func TestNearDuplicateCandidatesAreRejectedAndNothingIsStored(t *testing.T) {
	sc := newScene(t)
	fw := newFake(sc)
	fw.build = func(int) []workerclient.Candidate {
		cs := fw.threeCandidates()
		cs[1].FullActionArtifact.Body = cs[0].FullActionArtifact.Body + " Thanks."
		cs[1].ActionType, cs[1].ActionClass, cs[1].Subject = cs[0].ActionType, cs[0].ActionClass, cs[0].Subject
		cs[1].FullActionArtifact.Channel, cs[1].FullActionArtifact.Subject = "email", cs[0].Subject
		cs[1].To, cs[1].CC = cs[0].To, cs[0].CC
		return cs
	}
	if _, err := service(t, fw).Run(bg, sc.RunID); !orchestrator.IsPermanent(err) {
		t.Fatalf("run = %v, want a permanent failure after the retry", err)
	}
	if fw.strategies != 2 || count(t, `SELECT count(*) FROM strategy_candidates WHERE agent_run_id = $1::uuid`, sc.RunID) != 0 {
		t.Fatalf("%d attempts; candidates must never be stored", fw.strategies)
	}
}

func TestInvalidOutputIsRetriedOnceAndASecondAnswerThatIsValidPublishes(t *testing.T) {
	sc := newScene(t)
	fw := newFake(sc)
	fw.build = func(call int) []workerclient.Candidate {
		cs := fw.threeCandidates()
		if call == 1 {
			cs[0].FiveQuestions.WhatRemainsUnknown = " "
		}
		return cs
	}
	out := mustRun(t, service(t, fw), sc.RunID)
	if !out.Published || fw.strategies != 2 {
		t.Fatalf("outcome %+v after %d generations", out, fw.strategies)
	}
}

func TestParallelCallsGenerateOnceAndConvergeOnOneSet(t *testing.T) {
	sc := newScene(t)
	fw := newFake(sc)
	fw.onGen = func() { time.Sleep(300 * time.Millisecond) } // hold the claim while the others arrive
	svc := service(t, fw)
	var wg sync.WaitGroup
	errs := make([]error, 6)
	outs := make([]orchestrator.Outcome, 6)
	for i := range errs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			outs[i], errs[i] = svc.Run(bg, sc.RunID)
		}()
	}
	wg.Wait()
	published := 0
	for i, err := range errs {
		switch {
		case err == nil && outs[i].Published:
			published++
		case err == nil, errors.Is(err, orchestrator.ErrInProgress), errors.Is(err, orchestrator.ErrNotRunnable):
		default:
			t.Fatalf("caller %d: %v", i, err)
		}
	}
	fw.mu.Lock()
	generations := fw.strategies
	fw.mu.Unlock()
	if published != 1 || generations != 1 || count(t, `SELECT count(*) FROM strategy_sets WHERE agent_run_id = $1::uuid`, sc.RunID) != 1 {
		t.Fatalf("%d published, %d generations: parallel callers must converge on one set", published, generations)
	}
}

func TestACrashedCallersClaimIsTakenOverAfterItsLease(t *testing.T) {
	sc := newScene(t)
	fw := newFake(sc)
	if _, err := env.DB.Exec(`UPDATE agent_run_steps SET status = 'running', started_at = now() WHERE agent_run_id = $1::uuid AND step = 'draft'`, sc.RunID); err != nil {
		t.Fatal(err)
	}
	svc := service(t, fw)
	if _, err := svc.Run(bg, sc.RunID); !errors.Is(err, orchestrator.ErrInProgress) {
		t.Fatalf("a live claim must be honoured, got %v", err)
	}
	if _, err := env.DB.Exec(`UPDATE agent_run_steps SET started_at = now() - interval '3 hours' WHERE agent_run_id = $1::uuid AND step = 'draft'`, sc.RunID); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Run(bg, sc.RunID); err != nil {
		t.Fatalf("an expired claim must be taken over: %v", err)
	}
}

func TestOnlyPendingDryRunsAreOrchestrated(t *testing.T) {
	sc := newScene(t)
	svc := service(t, newFake(sc))
	if _, err := svc.Run(bg, "not-a-uuid"); !errors.Is(err, orchestrator.ErrNotRunnable) {
		t.Fatalf("bad id: %v", err)
	}
	if _, err := svc.Run(bg, newUUID()); !errors.Is(err, orchestrator.ErrNotRunnable) {
		t.Fatalf("unknown run: %v", err)
	}
	if _, err := env.DB.Exec(`UPDATE agent_runs SET status = 'cancelled' WHERE id = $1::uuid`, sc.RunID); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Run(bg, sc.RunID); !errors.Is(err, orchestrator.ErrNotRunnable) {
		t.Fatalf("cancelled run: %v", err)
	}
	live := newScene(t)
	if _, err := env.DB.Exec(`DELETE FROM agent_run_steps WHERE agent_run_id = $1::uuid`, live.RunID); err != nil {
		t.Fatal(err)
	}
	if _, err := env.DB.Exec(`UPDATE agent_runs SET run_mode = 'live' WHERE id = $1::uuid`, live.RunID); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Run(bg, live.RunID); !errors.Is(err, orchestrator.ErrLiveRun) {
		t.Fatalf("live run: %v (invariant I6: nothing reaches a sender)", err)
	}
}

func TestTheRunTokenOutlivesGenerationJudgingAndOneRevision(t *testing.T) {
	sc := newScene(t)
	fw := newFake(sc)
	mustRun(t, service(t, fw), sc.RunID)
	token := fw.requests[0].RunToken
	if token == "" || token != fw.judges[0].RunToken {
		t.Fatal("generation and judging must share one run token")
	}
	need := orchestrator.RequiredTokenTTL(true)
	s := signer(t)
	if _, err := s.Verify(token, wallNow.Add(need)); err != nil {
		t.Fatalf("the token expired within the %s the run may take: %v", need, err)
	}
	if _, err := s.Verify(token, wallNow.Add(tokenTTL+time.Second)); err == nil {
		t.Fatal("the token must still expire")
	}
	short := signerFor(t, orchestrator.RequiredTokenTTL(false)-time.Minute)
	if _, err := orchestrator.New(env.DB, fw, short, nil, config(t), nil); err == nil {
		t.Fatal("a token life shorter than generation plus judging must be refused at startup")
	}
}
