package readmodel

import (
	"strings"
	"testing"
)

func TestPhaseOfCoversEveryRunTheOrchestratorOwns(t *testing.T) {
	cases := []struct {
		name string
		f    draftFacts
		want string
	}{
		{"a published set wins over every other fact", draftFacts{runStatus: "awaiting_human", stepStatus: "succeeded", hasSet: true}, PhasePublished},
		{"a set on a run that moved on is still published", draftFacts{runStatus: "approved", hasSet: true}, PhasePublished},
		{"a failed run is failed", draftFacts{runStatus: "failed", stepStatus: "failed"}, PhaseFailed},
		{"pending with an untouched draft step is queued", draftFacts{runStatus: "pending", stepStatus: "pending"}, PhaseQueued},
		{"pending with no draft step is queued", draftFacts{runStatus: "pending"}, PhaseQueued},
		{"context_built with an untouched draft step is queued", draftFacts{runStatus: "context_built", stepStatus: "pending"}, PhaseQueued},
		{"a running draft step with nothing generated is generating", draftFacts{runStatus: "context_built", stepStatus: "running"}, PhaseGenerating},
		{"a running draft step with stored candidates is evaluating", draftFacts{runStatus: "context_built", stepStatus: "running", hasGenerated: true}, PhaseEvaluating},
		{"a failed draft step on an open run is paused, not failed", draftFacts{runStatus: "context_built", stepStatus: "failed", hasGenerated: true}, PhasePaused},
		{"a pending run whose first attempt failed is paused", draftFacts{runStatus: "pending", stepStatus: "failed"}, PhasePaused},
		{"awaiting_human without a set is a pre-orchestrator run", draftFacts{runStatus: "awaiting_human", stepStatus: "succeeded"}, PhaseNotOrchestrated},
		{"a cancelled run is not orchestrated", draftFacts{runStatus: "cancelled", stepStatus: "pending"}, PhaseNotOrchestrated},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := phaseOf(c.f); got != c.want {
				t.Fatalf("phase = %s, want %s", got, c.want)
			}
		})
	}
}

func TestReasonIsOnlyGivenToPausedAndFailedRunsAndIsClipped(t *testing.T) {
	long := strings.Repeat("é", maxReasonRunes+50)
	if r := reasonOf(PhasePaused, draftFacts{failReason: "provider down"}); r == nil || *r != "provider down" {
		t.Fatalf("paused reason = %v", r)
	}
	if r := reasonOf(PhaseFailed, draftFacts{runError: "invalid output", failReason: "other"}); r == nil || *r != "invalid output" {
		t.Fatalf("failed reason must prefer the run's error, got %v", r)
	}
	if r := reasonOf(PhaseFailed, draftFacts{failReason: "step reason"}); r == nil || *r != "step reason" {
		t.Fatalf("failed reason falls back to the step's, got %v", r)
	}
	for _, phase := range []string{PhaseQueued, PhaseGenerating, PhaseEvaluating, PhasePublished, PhaseNotOrchestrated} {
		if r := reasonOf(phase, draftFacts{failReason: "stale", runError: "stale"}); r != nil {
			t.Fatalf("%s must carry no reason, got %q", phase, *r)
		}
	}
	if r := reasonOf(PhasePaused, draftFacts{failReason: long}); r == nil || len([]rune(*r)) != maxReasonRunes {
		t.Fatalf("a long reason must be clipped to %d runes", maxReasonRunes)
	}
	if r := reasonOf(PhasePaused, draftFacts{}); r != nil {
		t.Fatalf("an empty reason must be null, got %q", *r)
	}
}
