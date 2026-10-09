package orchestrator

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/evals/deterministic"
	"github.com/harneet2512/gtm-work/core-go/internal/workerclient"
)

// Rule R1 (decision-evals audit): no evidence, no pass. Core refuses a semantic pass that cites nothing, and a result
// that does not say what it judged or where it attaches; unknown is accepted for a result with no evidence.

const (
	r1Run       = "0d000000-0000-4000-8000-0000000000aa"
	r1Candidate = "0d000000-0000-4000-8000-0000000000c1"
)

func r1Item(t *testing.T, mutate func(r *deterministic.EvalResult)) workerclient.BundleItem {
	t.Helper()
	r := deterministic.EvalResult{ID: "0d000000-0000-4000-8000-0000000000e1", AgentRunID: r1Run, DraftIndex: 1, EvalType: "grounding", EvalVersion: "grounding:v1",
		Kind: "semantic", Verdict: "pass", Diagnostics: []string{}, Reason: "r", StateRefs: []string{}, ActivityRefs: []string{},
		EvidenceRefs: []deterministic.EvidenceRef{}, KnowledgeRefs: []string{}, EvidenceClass: "deal_data",
		CreatedAt:    time.Date(2026, 10, 3, 0, 0, 0, 0, time.UTC),
		JudgedObject: deterministic.JudgedObject{Type: "StrategyCandidate", ID: r1Candidate}, SpanID: "candidates:" + r1Run}
	mutate(&r)
	raw, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	return workerclient.BundleItem{EvalType: "grounding", Verdict: r.Verdict, Result: raw}
}

func checkR1(t *testing.T, mutate func(r *deterministic.EvalResult)) error {
	t.Helper()
	s := &Service{}
	_, err := s.checkJudged(evalContext{run: runRow{ID: r1Run}}, 1, r1Item(t, mutate))
	return err
}

func TestCheckJudgedRefusesASemanticPassThatCitesNothing(t *testing.T) {
	err := checkR1(t, func(r *deterministic.EvalResult) {})
	if err == nil || !strings.Contains(err.Error(), "cites no evidence") {
		t.Fatalf("a semantic pass with no refs was accepted: %v", err)
	}
}

func TestCheckJudgedAcceptsAPassThatCitesAnyOneKindOfEvidence(t *testing.T) {
	cases := map[string]func(r *deterministic.EvalResult){
		"state":    func(r *deterministic.EvalResult) { r.StateRefs = []string{"champion"} },
		"activity": func(r *deterministic.EvalResult) { r.ActivityRefs = []string{"0ac70000-0000-4000-8000-000000000101"} },
		"quote": func(r *deterministic.EvalResult) {
			r.EvidenceRefs = []deterministic.EvidenceRef{{ActivityID: "0ac70000-0000-4000-8000-000000000101"}}
		},
		"knowledge": func(r *deterministic.EvalResult) { r.KnowledgeRefs = []string{"0c17c000-0000-4000-8000-000000000017"} },
	}
	for name, mutate := range cases {
		if err := checkR1(t, mutate); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestCheckJudgedAcceptsUnknownWithoutEvidenceAndNonPassVerdicts(t *testing.T) {
	for _, verdict := range []string{"unknown", "warn", "fail"} {
		if err := checkR1(t, func(r *deterministic.EvalResult) { r.Verdict = verdict }); err != nil {
			t.Errorf("%s: %v", verdict, err)
		}
	}
}

func TestCheckJudgedDoesNotApplyR1ToDeterministicResults(t *testing.T) {
	if err := checkR1(t, func(r *deterministic.EvalResult) { r.Kind = "deterministic" }); err != nil {
		t.Fatalf("a deterministic pass cites its findings, not refs: %v", err)
	}
}

func TestCheckJudgedRequiresTheJudgedObjectAndTheSpan(t *testing.T) {
	for name, mutate := range map[string]func(r *deterministic.EvalResult){
		"no judged object": func(r *deterministic.EvalResult) { r.JudgedObject = deterministic.JudgedObject{} },
		"no object id":     func(r *deterministic.EvalResult) { r.JudgedObject.ID = "" },
		"no span":          func(r *deterministic.EvalResult) { r.SpanID = "" },
	} {
		mutate := mutate
		err := checkR1(t, func(r *deterministic.EvalResult) { r.StateRefs = []string{"champion"}; mutate(r) })
		if err == nil || !strings.Contains(err.Error(), "does not say") {
			t.Errorf("%s was accepted: %v", name, err)
		}
	}
}
