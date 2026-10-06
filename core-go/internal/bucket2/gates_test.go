package bucket2

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestR1NoEvidenceNeverPasses(t *testing.T) {
	for _, v := range []Verdict{Pass, Warn, Fail} {
		r := Result{Gate: "D1", JudgedType: "StrategySet", JudgedID: "s", SpanID: "candidates:s", Verdict: v, Observed: "o", Why: "w", Grader: Deterministic}.Finalize()
		if r.Verdict != Unknown {
			t.Fatalf("%s with no evidence became %s, want unknown", v, r.Verdict)
		}
		if err := r.Validate(); err != nil {
			t.Fatalf("a finalized unknown must validate: %v", err)
		}
	}
}

func TestVerdictSpellingsAreReadAsUnknownNeverPass(t *testing.T) {
	for _, s := range []string{"abstain", "unknown", "", "not_relevant", "PASSED", "excellent"} {
		if got := ParseVerdict(s); got != Unknown {
			t.Fatalf("%q read as %s", s, got)
		}
	}
	if ParseVerdict(" PASS ") != Pass || ParseVerdict("warn") != Warn || ParseVerdict("fail") != Fail {
		t.Fatal("known verdicts must round trip")
	}
}

func TestFinalizeFillsQuestionImprovesAndNeverCalibrates(t *testing.T) {
	r := Result{Gate: "D3", Verdict: Pass, EvidenceRefs: []string{"a", "a", "b"}, Calibrated: true}.Finalize()
	if r.Question == "" || r.Improves == "" || r.Calibrated || len(r.EvidenceRefs) != 2 {
		t.Fatalf("finalize = %+v", r)
	}
}

func TestValidateRefusesIncompleteResults(t *testing.T) {
	bad := []Result{{Gate: "X"}, {Gate: "D1"}, {Gate: "D1", JudgedType: "T", JudgedID: "i", SpanID: "a:b"},
		{Gate: "D1", JudgedType: "T", JudgedID: "i", SpanID: "a:b", Observed: "o", Why: "w"},
		{Gate: "D1", JudgedType: "T", JudgedID: "i", SpanID: "a:b", Observed: "o", Why: "w", Grader: Deterministic, Verdict: Pass}}
	for i, r := range bad {
		if r.Validate() == nil {
			t.Fatalf("case %d should be refused", i)
		}
	}
}

func TestGateMetaMatchesTheRegistry(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "contracts", "evals", "eval_registry.json"))
	if err != nil {
		t.Fatal(err)
	}
	var reg struct {
		Gates []struct{ ID, Question, Improves string } `json:"gates"`
	}
	if err := json.Unmarshal(raw, &reg); err != nil {
		t.Fatal(err)
	}
	seen := 0
	for _, g := range reg.Gates {
		if m, ok := gateMeta[g.ID]; ok {
			seen++
			if m.Question != g.Question || m.Improves != g.Improves {
				t.Errorf("%s drifted from the registry", g.ID)
			}
		}
	}
	if seen != 10 {
		t.Fatalf("matched %d gates, want 10", seen)
	}
}

func sel(pref, chosen string, pe, ce map[string]string, inf *Inference) Selection {
	return Selection{EpisodeID: "e", DecisionID: "d", Preferred: pref, Chosen: chosen, Order: []string{pref, "x", chosen},
		PreferredEvals: pe, ChosenEvals: ce, Inference: inf}
}

func TestSelectionClassification(t *testing.T) {
	inf := func(classes ...string) *Inference { return &Inference{ID: "i", EditClasses: classes} }
	warn, pass := map[string]string{"cta_calibration": "warn"}, map[string]string{"cta_calibration": "pass"}
	cases := []struct {
		name  string
		in    Selection
		label string
		v     Verdict
	}{
		{"agree", sel("a", "a", nil, nil, nil), SelAgree, Pass},
		{"no inference", sel("a", "b", warn, pass, nil), SelUnknown, Unknown},
		{"unknown inference", sel("a", "b", warn, pass, &Inference{ID: "i", Unknown: true}), SelUnknown, Unknown},
		{"corrects", sel("a", "b", warn, pass, inf()), SelCorrects, Fail},
		{"defensible", sel("a", "b", pass, pass, inf()), SelDefensibl, Pass},
		{"chosen worse is still defensible not a correction", sel("a", "b", pass, warn, inf()), SelDefensibl, Pass},
		{"missing context", sel("a", "b", warn, pass, inf("factual")), SelMissing, Warn},
		{"no choice", Selection{EpisodeID: "e", DecisionID: "d"}, SelUnknown, Unknown},
	}
	for _, c := range cases {
		r := ClassifySelection(c.in)
		if r.Label != c.label || r.Verdict != c.v || r.Gate != "D4" || r.Grader.Kind != "deterministic" {
			t.Errorf("%s: got %s/%s, want %s/%s", c.name, r.Label, r.Verdict, c.label, c.v)
		}
		if r.Verdict != Unknown && len(r.EvidenceRefs) == 0 {
			t.Errorf("%s: a verdict without evidence", c.name)
		}
	}
}

func TestEvalGapClasses(t *testing.T) {
	inf := &Inference{ID: "i", EditClasses: []string{"cta", "recipient", "style", "timing"}}
	r := DetectEvalGaps(EvalGapInput{EpisodeID: "e", Inference: inf, EditCount: 1, StoredEvals: []StoredEval{
		{"1", "cta_calibration", "warn"},       // covers cta -> COVERED
		{"2", "recipient_correctness", "pass"}, // covers recipient, passed -> MISGRADED
		{"3", "momentum", "pass"},              // irrelevant
	}})
	got := map[string]string{}
	for _, x := range r {
		got[x.SubGate] = x.Label
	}
	want := map[string]string{"cta": GapCovered, "recipient": GapMisgrade, "style": GapPrefOnly, "timing": GapMissing}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %s, want %s", k, got[k], v)
		}
	}
}

func TestEvalGapSemanticVerdictsCountAsCoverage(t *testing.T) {
	r := DetectEvalGaps(EvalGapInput{EpisodeID: "e", Inference: &Inference{ID: "i", EditClasses: []string{"stakeholder"}}, EditCount: 2,
		StoredEvals: []StoredEval{{"9", "economic_buyer_coverage", "fail"}}})
	if len(r) != 1 || r[0].Label != GapCovered || r[0].Verdict != Pass || r[0].EvidenceRefs[0] != "eval:9" {
		t.Fatalf("got %+v", r)
	}
}

func TestEvalGapUnknownWithoutEdits(t *testing.T) {
	for _, in := range []EvalGapInput{{EpisodeID: "e"}, {EpisodeID: "e", Inference: &Inference{ID: "i"}, EditCount: 1},
		{EpisodeID: "e", Inference: &Inference{ID: "i", EditClasses: []string{"cta"}}, EditCount: 0}} {
		r := DetectEvalGaps(in)
		if len(r) != 1 || r[0].Verdict != Unknown || r[0].Label != GapUnknown {
			t.Fatalf("got %+v", r)
		}
	}
}

func TestEvalMapHelpers(t *testing.T) {
	if len(EvalTypesFor("cta")) != 3 && len(EvalTypesFor("cta")) < 2 {
		t.Fatal("cta evals missing")
	}
	if EvalClasses("nope") != nil || LiteralKindClasses("nope") != nil {
		t.Fatal("unmapped must be nil")
	}
	for _, k := range []string{"recipient_added", "cta_changed", "timing_changed", "subject_changed", "paragraph_edited", "channel_changed", "crm_next_step_changed"} {
		if len(LiteralKindClasses(k)) == 0 {
			t.Fatalf("%s unmapped", k)
		}
	}
}

func ref(kind, id, verdict string) SpanRef {
	return SpanRef{Kind: kind, RefID: id, Label: id, Verdict: verdict}
}

func inv(status Status, preserved *bool, entries ...Entry) Invalidation {
	one := 1
	return Invalidation{RunID: "r", Status: status, Edited: true, Entries: entries, AccountID: "a", VersionBefore: &one, VersionAfter: &one, Preserved: preserved}
}

func byGate(rs []Result) map[string]Result {
	m := map[string]Result{}
	for _, r := range rs {
		m[r.SubGate] = r
	}
	return m
}

func TestPropagationPassesOnAProvenRecompute(t *testing.T) {
	yes := true
	e := Entry{Index: 1, Invalidated: []SpanRef{ref("eval_result", "ev1", "pass")},
		Recomputed:    []SpanRef{ref("eval_result", "ev2", "pass")},
		NotRecomputed: []SpanRef{ref("ranking_rationale", "rk", "")}}
	m := byGate(PropagationResults(PropagationInput{Invalidation: inv(StatusReevaluated, &yes, e)}))
	for _, k := range []string{"dependents_invalidated", "no_stale_descendant", "state_preserved", "recomputed_before_send"} {
		if m[k].Verdict != Pass || len(m[k].EvidenceRefs) == 0 {
			t.Errorf("%s = %+v", k, m[k])
		}
	}
	if _, ok := m["intent_rejudged"]; ok {
		t.Fatal("intent unchanged: no re-judge result")
	}
}

func TestPropagationFailures(t *testing.T) {
	no := false
	stale := Entry{Index: 1, NotRecomputed: []SpanRef{ref("eval_result", "old", "fail")}}
	m := byGate(PropagationResults(PropagationInput{Invalidation: inv(StatusReevaluated, &no, stale), IntentChanged: true}))
	for _, k := range []string{"dependents_invalidated", "no_stale_descendant", "state_preserved", "recomputed_before_send", "intent_rejudged"} {
		if m[k].Verdict != Fail {
			t.Errorf("%s = %s, want fail", k, m[k].Verdict)
		}
	}
}

func TestPropagationUnknownBeforeSendAndNothingWhenUnedited(t *testing.T) {
	m := byGate(PropagationResults(PropagationInput{Invalidation: inv(StatusEditsPend, nil, Entry{Index: 1, Invalidated: []SpanRef{ref("eval_result", "e", "")}})}))
	if m["recomputed_before_send"].Verdict != Unknown || m["state_preserved"].Verdict != Unknown || m["no_stale_descendant"].Verdict != Unknown {
		t.Fatalf("got %+v", m)
	}
	un := Invalidation{Status: StatusUnedited}
	if PropagationResults(PropagationInput{Invalidation: un}) != nil {
		t.Fatal("an unedited episode is not measured")
	}
	d := inv(StatusDiscarded, nil)
	if PropagationResults(PropagationInput{Invalidation: d}) != nil {
		t.Fatal("a discarded episode is not measured")
	}
}

func TestPropagationIntentChangeNeedsRejudge(t *testing.T) {
	yes := true
	e := Entry{Index: 1, Invalidated: []SpanRef{ref("eval_result", "a", "")}, Recomputed: []SpanRef{ref("eval_result", "b", "")}}
	m := byGate(PropagationResults(PropagationInput{Invalidation: inv(StatusReevaluated, &yes, e), IntentChanged: true, RejudgedAfterEdit: []string{"gate_result:x"}}))
	if m["intent_rejudged"].Verdict != Pass {
		t.Fatalf("got %+v", m["intent_rejudged"])
	}
}

func fa() FinalArtifact {
	return FinalArtifact{EpisodeID: "e", CandidateID: "c", To: []string{"p1"}, CC: []string{"p2"},
		AccountPeople: map[string]bool{"p1": true, "p2": true, "p3": true}, InternalOnly: map[string]bool{"p3": true},
		Body: "Hello there. We will keep the champion in the loop before any pricing conversation starts.", SelectedBody: "Hello there.",
		SendEvals: []SendEval{{ID: "s1", EvalType: "recipient_correctness", Verdict: "pass", Blocking: true}}}
}

func TestFinalArtifactPasses(t *testing.T) {
	m := byGate(FinalArtifactResults(fa()))
	for _, k := range []string{"recipients", "no_stale_content", "policy_gates"} {
		if m[k].Verdict != Pass {
			t.Errorf("%s = %+v", k, m[k])
		}
	}
}

func TestFinalArtifactFailures(t *testing.T) {
	f := fa()
	f.To = []string{"p1", "p9"}
	f.OtherBodies = []string{"Intro. We will keep the champion in the loop before any pricing conversation starts."}
	f.SendEvals = []SendEval{{ID: "s1", EvalType: "pricing_integrity", Verdict: "fail", Blocking: true}}
	m := byGate(FinalArtifactResults(f))
	for _, k := range []string{"recipients", "no_stale_content", "policy_gates"} {
		if m[k].Verdict != Fail {
			t.Errorf("%s = %s, want fail", k, m[k].Verdict)
		}
	}
	g := fa()
	g.CC = []string{"p3"}
	if byGate(FinalArtifactResults(g))["recipients"].Verdict != Fail {
		t.Fatal("internal-only recipient must fail")
	}
	g = fa()
	g.CC = []string{"p1"}
	if byGate(FinalArtifactResults(g))["recipients"].Verdict != Fail {
		t.Fatal("duplicate recipient must fail")
	}
	g = fa()
	g.To, g.CC, g.SendEvals = nil, nil, nil
	m = byGate(FinalArtifactResults(g))
	if m["recipients"].Verdict != Fail || m["policy_gates"].Verdict != Unknown {
		t.Fatalf("got %+v", m)
	}
}

func ex() Execution {
	return Execution{EpisodeID: "e", RunID: "r", DecisionID: "d", SendDecision: "send", ExpectedTargets: []string{"p2", "p1"},
		Effects: []Effect{{Kind: "email", IdempotencyKey: "k1", Targets: []string{"p1", "p2"}}}, EpisodeLinked: true,
		RequiredWrites: map[string]bool{"human_decision": true, "episode_decided": true}}
}

func TestExecutionPasses(t *testing.T) {
	for _, r := range ExecutionResults(ex()) {
		if r.Verdict != Pass {
			t.Errorf("%s = %s (%s)", r.SubGate, r.Verdict, r.Why)
		}
	}
}

func TestExecutionFailuresAndUnknowns(t *testing.T) {
	x := ex()
	x.Effects = append(x.Effects, Effect{Kind: "email", IdempotencyKey: "k1", Targets: []string{"p1", "p2"}})
	x.BlockingFailAtSend = true
	x.EpisodeLinked = false
	x.RequiredWrites["episode_decided"] = false
	m := byGate(ExecutionResults(x))
	for _, k := range []string{"delivered_once", "blocked_not_executed", "linked_to_episode", "required_writes"} {
		if m[k].Verdict != Fail {
			t.Errorf("%s = %s, want fail", k, m[k].Verdict)
		}
	}
	y := ex()
	y.Effects[0].Targets = []string{"p1"}
	if byGate(ExecutionResults(y))["correct_target"].Verdict != Fail {
		t.Fatal("wrong target must fail")
	}
	z := ex()
	z.SendDecision, z.Effects = "pending", nil
	m = byGate(ExecutionResults(z))
	if m["delivered_once"].Verdict != Unknown || m["correct_target"].Verdict != Unknown {
		t.Fatalf("pending must be unknown: %+v", m)
	}
	d := ex()
	d.SendDecision, d.Effects = "discard", nil
	if byGate(ExecutionResults(d))["delivered_once"].Verdict != Pass {
		t.Fatal("a discarded action with no effect passes")
	}
	s := ex()
	s.Effects = nil
	if byGate(ExecutionResults(s))["delivered_once"].Verdict != Fail {
		t.Fatal("a send with no effect fails")
	}
	r := ex()
	r.RequiredWrites = nil
	if byGate(ExecutionResults(r))["required_writes"].Verdict != Unknown {
		t.Fatal("no declared writes is unknown")
	}
}

func TestFeedbackKeepsRecordsSeparate(t *testing.T) {
	f := Feedback{EpisodeID: "e", Chose: true, Edited: true, Sent: true, SelectionID: "s", SemanticEditID: "i", ExecutedActionID: "x"}
	m := byGate(FeedbackResults(f))
	for _, k := range []string{"selection", "semantic_edit", "executed_action", "not_collapsed"} {
		if m[k].Verdict != Pass {
			t.Errorf("%s = %+v", k, m[k])
		}
	}
	for _, k := range []string{"explicit_explanation", "customer_reaction", "business_outcome"} {
		if m[k].Verdict != Unknown {
			t.Errorf("%s = %s, want unknown (absent is not a pass)", k, m[k].Verdict)
		}
	}
	f.ReactionID, f.OutcomeID = "r", "o"
	m = byGate(FeedbackResults(f))
	if m["customer_reaction"].Verdict != Pass || m["business_outcome"].Verdict != Pass {
		t.Fatal("later records pass when they arrive")
	}
}

func TestFeedbackFailsWhenDueRecordIsMissingOrCollapsed(t *testing.T) {
	m := byGate(FeedbackResults(Feedback{EpisodeID: "e", Chose: true, Edited: true, Sent: true}))
	for _, k := range []string{"selection", "semantic_edit", "executed_action"} {
		if m[k].Verdict != Fail {
			t.Errorf("%s = %s, want fail", k, m[k].Verdict)
		}
	}
	if m["not_collapsed"].Verdict != Unknown {
		t.Fatalf("nothing emitted is unknown, got %s", m["not_collapsed"].Verdict)
	}
	c := byGate(FeedbackResults(Feedback{EpisodeID: "e", Chose: true, SelectionID: "same", ReactionID: "same"}))
	if c["not_collapsed"].Verdict != Fail {
		t.Fatalf("a shared record must fail: %+v", c["not_collapsed"])
	}
}

func TestWorstNeverReportsPassOnPartialInformation(t *testing.T) {
	if worst(Pass, Unknown) != Unknown || worst(Pass, Warn, Fail) != Fail || worst() != Unknown || worst(Pass) != Pass {
		t.Fatal("worst misorders verdicts")
	}
}
