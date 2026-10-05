package knowledgebench

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/knowledge"
)

const (
	A = knowledge.LabelApplies
	N = knowledge.LabelDoesNotApply
	E = knowledge.LabelExceptionTriggered
)

func TestMetricsDefinitions(t *testing.T) {
	rows := []Row{
		{Gold: A, Predicted: A, Correct: true}, {Gold: A, Predicted: N}, {Gold: N, Predicted: A},
		{Gold: E, Predicted: A}, {Gold: E, Predicted: E, Correct: true}, {Gold: N, Predicted: E},
		{Gold: E, Predicted: N, Debatable: true},
	}
	m := metricsOf(rows, true)
	// TP=1 (A->A); FP=2 (N->A, E->A); FN=1 (A->N). Exceptions: gold 3, caught 1, predicted 2.
	if *m.ApplicabilityPrecision != 1.0/3 || *m.ApplicabilityRecall != 0.5 || *m.ExceptionRecall != 1.0/3 ||
		*m.ExceptionPrecision != 0.5 || *m.Accuracy != 2.0/7 || m.Pairs != 7 || m.Confusion[E+"->"+A] != 1 {
		t.Fatalf("metrics = p%v r%v er%v ep%v acc%v", *m.ApplicabilityPrecision, *m.ApplicabilityRecall, *m.ExceptionRecall, *m.ExceptionPrecision, *m.Accuracy)
	}
	if nd := metricsOf(rows, false); nd.Pairs != 6 || *nd.ExceptionRecall != 0.5 {
		t.Fatalf("without debatable = %+v", nd)
	}
	if empty := metricsOf(nil, true); empty.ApplicabilityPrecision != nil || empty.ExceptionRecall != nil {
		t.Fatal("undefined ratios must be nil, not 0")
	}
}

func neutralCase(expected string) legacyCase {
	var c legacyCase
	raw := `{"id":"c1","based_on":{"account":"x"},"context":{"now":"2026-10-01T00:00:00Z",
	  "state":{"account_id":"a","opportunity_id":"o","fields":{"motion":"expansion","champion_status":"unknown",
	    "next_meeting":null,"blockers":[{"text":"Security review","status":"open"}],"health":2},
	    "buying_group":[{"person_id":"p","roles":["champion"],"status":"active"}],"coverage_gaps":["legal"],
	    "conflicts":[{"field":"stage"}]},
	  "recent_changes":{"signals":["customer_replied"]},
	  "offered_knowledge":[{"id":"k1","situation_signature":[{"field":"motion","op":"eq","value":"expansion"}],
	    "exceptions":[{"description":"blocker","conditions":[{"field":"blockers","op":"contains","value":"security"}]}]},
	    {"id":"k2","situation_signature":[{"field":"motion","op":"eq","value":"renewal"}]}]},
	  "expected":` + expected + `}`
	if err := json.Unmarshal([]byte(raw), &c); err != nil {
		panic(err)
	}
	return c
}

func TestPairsOfLabelSources(t *testing.T) {
	history := []checkpointSignals{{AsOf: time.Date(2026, 9, 20, 0, 0, 0, 0, time.UTC), Signals: []string{"new_stakeholder_entered"}},
		{AsOf: time.Date(2026, 10, 9, 0, 0, 0, 0, time.UTC), Signals: []string{"stage_advanced"}}}
	c := neutralCase(`[{"eval_type":"exception_awareness","label":"EXCEPTION_MISSED","knowledge_refs":["k1"]},
		{"eval_type":"knowledge_applicability","label":"DOES_NOT_APPLY","knowledge_refs":["k2"],"debatable":true},
		{"eval_type":"grounding","label":"GROUNDED"}]`)
	s, err := situationOf(c, goldSignals(c, history))
	if err != nil {
		t.Fatal(err)
	}
	pairs := pairsOf(c, s)
	if len(pairs) != 2 || pairs[0].Gold != E || pairs[0].Source != "exception_awareness" || pairs[1].Gold != N || !pairs[1].Debatable {
		t.Fatalf("pairs = %+v", pairs)
	}
	if len(s.Signals) != 2 || s.Fields["champion_status"].Known || !s.Fields["next_meeting"].Known || len(s.Conflicts["stage"]) != 1 {
		t.Fatalf("situation = %+v", s)
	}
	report, err := Score(ModeOwnDiff, pairs)
	if err != nil || !report.Rows[0].Correct || !report.Rows[1].Correct {
		t.Fatalf("report = %+v err %v", report.Rows, err)
	}
	unlabelled := neutralCase(`[{"eval_type":"knowledge_applicability","label":"APPLIES"}]`)
	if got := pairsOf(unlabelled, s); len(got) != 0 {
		t.Fatalf("a judgment that names no knowledge must label nothing: %+v", got)
	}
}

func TestMissedExceptionsAreExplained(t *testing.T) {
	c := neutralCase(`[{"eval_type":"knowledge_applicability","label":"EXCEPTION_TRIGGERED","knowledge_refs":["k1"]}]`)
	s, _ := situationOf(c, nil)
	s.Fields["blockers"] = knowledge.Value{Known: true, List: true, Items: []knowledge.Item{{Text: "Budget", Status: "resolved"}}}
	report, err := Score(ModeOwnDiff, pairsOf(c, s))
	if err != nil {
		t.Fatal(err)
	}
	row := report.Rows[0]
	if row.Correct || len(row.FailedProbe) != 1 || row.FailedProbe[0] != `blocker: blockers contains "security"` {
		t.Fatalf("row = %+v", row)
	}
}

func TestScoreReturnsMatcherErrors(t *testing.T) {
	c := neutralCase(`[{"eval_type":"knowledge_applicability","label":"APPLIES","knowledge_refs":["k2"]}]`)
	s, _ := situationOf(c, nil)
	pairs := pairsOf(c, s)
	pairs[0].Knowledge.SituationSignature[0].Field = "mood"
	if _, err := Score(ModeOwnDiff, pairs); !errors.Is(err, knowledge.ErrInvalidCondition) {
		t.Fatalf("err = %v", err)
	}
	if _, err := valueOf(json.RawMessage(`{`)); err == nil {
		t.Fatal("bad value must be an error")
	}
	if _, err := valueOf(json.RawMessage(`[1]`)); err == nil {
		t.Fatal("a list of non-items must be an error")
	}
}
