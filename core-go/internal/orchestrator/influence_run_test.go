package orchestrator_test

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/orchestrator"
	"github.com/harneet2512/gtm-work/core-go/internal/workerclient"
)

// ---- E7: retrieved -> applicable -> used -> changed ----------------------------------------------------

func attribution(t *testing.T, runID string) map[string]any {
	t.Helper()
	raw := scalar(t, `SELECT detail -> 'knowledge_attribution' FROM agent_run_steps WHERE agent_run_id = $1::uuid AND step = 'build_context'`, runID)
	validDoc(t, "knowledge_attribution", []byte(raw))
	return decodeMap(t, []byte(raw))
}

func verdicts(t *testing.T, attr map[string]any) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, e := range attr["influence"].(map[string]any)["evals"].([]any) {
		m := e.(map[string]any)
		out[m["eval"].(string)] = m["verdict"].(string)
	}
	return out
}

// citeKnowledge makes the favourite cite the knowledge and, for arm A (the second generation), swaps the order.
func citeKnowledge(f *fakeWorker, known string) {
	f.build = func(call int) []workerclient.Candidate {
		cs := f.threeCandidates()
		if call == 2 { // arm A: the same strategies, ranked differently without the knowledge
			cs[0].Ranking, cs[0].PreferredByAgent = 2, false
			cs[1].Ranking, cs[1].PreferredByAgent = 1, true
			return cs
		}
		cs[0].KnowledgeRefs = []string{known}
		return cs
	}
}

func TestWithoutTheCounterfactualTheInfluenceTraceSaysChangedIsUnknown(t *testing.T) {
	sc := newScene(t)
	known := newUUID()
	learn(t, matchingKnowledge(known, sc.EventTime.Add(-72*time.Hour)), sc.EventTime.Add(-48*time.Hour))
	fw := newFake(sc)
	citeKnowledge(fw, known)
	mustRun(t, service(t, fw), sc.RunID)
	if fw.strategies != 1 {
		t.Fatalf("the counterfactual is flag-gated and off by default, got %d generations", fw.strategies)
	}
	attr := attribution(t, sc.RunID)
	infl := attr["influence"].(map[string]any)
	if infl["counterfactual"].(map[string]any)["status"] != "not_run" {
		t.Fatalf("counterfactual = %v", infl["counterfactual"])
	}
	want := map[string]string{"E7.1": "pass", "E7.2": "unknown", "E7.3": "unknown", "E7.4": "unknown", "E7.5": "pass"}
	for k, v := range verdicts(t, attr) {
		if want[k] != v {
			t.Errorf("%s = %s, want %s", k, v, want[k])
		}
	}
	for _, c := range infl["candidates"].([]any) {
		if c.(map[string]any)["change"] != nil {
			t.Fatalf("no arm A, so no change column: %v", c)
		}
	}
}

func TestTheCounterfactualArmComparesCandidatesAndIsGradedByE7(t *testing.T) {
	sc := newScene(t)
	known := newUUID()
	learn(t, matchingKnowledge(known, sc.EventTime.Add(-72*time.Hour)), sc.EventTime.Add(-48*time.Hour))
	fw := newFake(sc)
	citeKnowledge(fw, known)
	var failed atomic.Bool
	fw.judgeErr = func(workerclient.JudgeRequest) error {
		if failed.CompareAndSwap(false, true) {
			return &workerclient.Error{Status: 503, Code: "unavailable", Message: "overloaded", Retryable: true}
		}
		return nil
	}
	svc := serviceWith(t, fw, func(c *orchestrator.Config) { c.KnowledgeCounterfactual = true })
	if _, err := svc.Run(bg, sc.RunID); !orchestrator.IsTransient(err) {
		t.Fatalf("first run = %v", err)
	}
	mustRunAgain := func() {
		if _, err := svc.Resume(bg, sc.RunID); err != nil {
			t.Fatal(err)
		}
	}
	mustRunAgain()
	if fw.strategies != 2 {
		t.Fatalf("one generation with knowledge, one without, and never again on resume: %d", fw.strategies)
	}
	if len(fw.requests[0].DecisionGuidance) == 0 || len(fw.requests[1].DecisionGuidance) != 0 {
		t.Fatalf("arm A withholds the DecisionGuidance: %d / %d bytes", len(fw.requests[0].DecisionGuidance), len(fw.requests[1].DecisionGuidance))
	}
	attr := attribution(t, sc.RunID)
	infl := attr["influence"].(map[string]any)
	cf := infl["counterfactual"].(map[string]any)
	if cf["status"] != "compared" || cf["ranking_changed"] != true || cf["preferred_action_changed"] != true {
		t.Fatalf("counterfactual = %v", cf)
	}
	changes := map[string]bool{}
	for _, c := range infl["candidates"].([]any) {
		m := c.(map[string]any)
		changes[m["strategy_type"].(string)] = m["change"].(map[string]any)["changed"].(bool)
	}
	if !changes["send_context_and_wait"] || !changes["bring_in_technical_lead"] || changes["ask_internal_owner"] {
		t.Fatalf("per-candidate changed = %v", changes)
	}
	// the favourite cites the knowledge and moved (E7.1, E7.3, E7.4 pass); the other candidate moved silently (E7.2 fails)
	want := map[string]string{"E7.1": "pass", "E7.2": "fail", "E7.3": "pass", "E7.4": "pass", "E7.5": "pass"}
	for k, v := range verdicts(t, attr) {
		if want[k] != v {
			t.Errorf("%s = %s, want %s", k, v, want[k])
		}
	}
	if scalar(t, `SELECT detail -> 'generated' -> 'counterfactual' ->> 'status' FROM agent_run_steps WHERE agent_run_id = $1::uuid AND step = 'draft'`, sc.RunID) != "generated" {
		t.Fatal("arm A is stored on the draft step so a resume never regenerates it")
	}
}

func TestACounterfactualThatCannotBeGeneratedIsRecordedNotFatal(t *testing.T) {
	sc := newScene(t)
	known := newUUID()
	learn(t, matchingKnowledge(known, sc.EventTime.Add(-72*time.Hour)), sc.EventTime.Add(-48*time.Hour))
	fw := newFake(sc)
	fw.build = func(call int) []workerclient.Candidate {
		cs := fw.threeCandidates()
		if call > 1 {
			cs[0].FiveQuestions.WhatChanged = " " // invalid in both attempts of arm A
		}
		return cs
	}
	svc := serviceWith(t, fw, func(c *orchestrator.Config) { c.KnowledgeCounterfactual = true })
	if out := mustRun(t, svc, sc.RunID); !out.Published {
		t.Fatal("an optional analysis must not fail the run")
	}
	attr := attribution(t, sc.RunID)
	if attr["influence"].(map[string]any)["counterfactual"].(map[string]any)["status"] != "unavailable" || verdicts(t, attr)["E7.2"] != "unknown" {
		t.Fatalf("influence = %v", attr["influence"])
	}
	if fw.strategies != 3 {
		t.Fatalf("arm A is retried once: %d generations", fw.strategies)
	}
}

func TestWithNoKnowledgeAtAllNoCounterfactualIsGenerated(t *testing.T) {
	sc := newScene(t)
	fw := newFake(sc)
	svc := serviceWith(t, fw, func(c *orchestrator.Config) { c.KnowledgeCounterfactual = true })
	mustRun(t, svc, sc.RunID)
	if fw.strategies != 1 {
		t.Fatalf("with nothing retrieved arm A would equal the run: %d generations", fw.strategies)
	}
	attr := attribution(t, sc.RunID)
	if attr["influence"].(map[string]any)["counterfactual"].(map[string]any)["status"] != "no_knowledge" || verdicts(t, attr)["E7.2"] != "pass" {
		t.Fatalf("influence = %v", attr["influence"])
	}
}
