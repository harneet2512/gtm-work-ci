package knowledgebench

import (
	"fmt"

	"github.com/harneet2512/gtm-work/core-go/internal/knowledge"
)

// Row is one scored pair.
type Row struct {
	CaseID      string          `json:"case_id"`
	Knowledge   string          `json:"knowledge"`
	Gold        string          `json:"gold"`
	Predicted   string          `json:"predicted"`
	Source      string          `json:"gold_source"`
	Debatable   bool            `json:"debatable,omitempty"`
	Correct     bool            `json:"correct"`
	Entry       knowledge.Entry `json:"matcher_entry"`
	FailedProbe []string        `json:"exception_conditions_not_met,omitempty"`
	// Lint lists the knowledge's conditions that describe structure as text (knowledge.Lint).
	Lint []knowledge.LintFinding `json:"invalid_conditions,omitempty"`
	// MissCause classifies a wrong row: "invalid_condition" when the knowledge has lint findings,
	// otherwise "matcher_or_signals".
	MissCause string `json:"miss_cause,omitempty"`
}

// Metrics are the HAR-97 L4 applicability numbers. Ratios are nil when undefined (no denominator).
type Metrics struct {
	Pairs                  int            `json:"pairs"`
	Accuracy               *float64       `json:"label_accuracy"`
	ApplicabilityPrecision *float64       `json:"applicability_precision"`
	ApplicabilityRecall    *float64       `json:"applicability_recall"`
	ExceptionRecall        *float64       `json:"exception_recall"`
	ExceptionPrecision     *float64       `json:"exception_precision"`
	Confusion              map[string]int `json:"confusion_gold_to_predicted"`
	MissesInvalidCondition int            `json:"misses_from_invalid_conditions"`
}

// ModeReport is the benchmark output for one signal mode.
type ModeReport struct {
	Mode            string  `json:"mode"`
	Metrics         Metrics `json:"metrics"`
	MetricsNoDebate Metrics `json:"metrics_excluding_debatable"`
	Rows            []Row   `json:"rows"`
}

// Report is the benchmark output: both signal modes side by side.
type Report struct {
	Benchmark  string       `json:"benchmark"`
	GoldSource string       `json:"gold_source"`
	Modes      []ModeReport `json:"modes"`
}

// Run loads and scores both signal modes.
func Run(root string) (Report, error) {
	report := Report{Benchmark: "knowledge_applicability:legacy_fixture_gold",
		GoldSource: "fixtures/evals/cases knowledge_applicability labels (+ exception_awareness knowledge refs); invented demo accounts, to be replaced by CRMArena-based gold"}
	for _, mode := range Modes {
		pairs, err := LoadLegacyPairs(root, mode)
		if err != nil {
			return Report{}, err
		}
		m, err := Score(mode, pairs)
		if err != nil {
			return Report{}, err
		}
		report.Modes = append(report.Modes, m)
	}
	return report, nil
}

// Score runs the matcher on every pair. A matcher error is returned, never scored as a miss.
func Score(mode string, pairs []Pair) (ModeReport, error) {
	rows := make([]Row, 0, len(pairs))
	for _, p := range pairs {
		r, err := knowledge.Match(p.Knowledge, p.Situation)
		if err != nil {
			return ModeReport{}, fmt.Errorf("knowledgebench: case %s: %w", p.CaseID, err)
		}
		key := p.Knowledge.ID
		if p.Knowledge.Key != nil {
			key = *p.Knowledge.Key
		}
		row := Row{CaseID: p.CaseID, Knowledge: key, Gold: p.Gold, Predicted: r.Label, Source: p.Source,
			Debatable: p.Debatable, Correct: p.Gold == r.Label, Entry: r.Entry, FailedProbe: unmetExceptions(p, r),
			Lint: knowledge.Lint(p.Knowledge)}
		if !row.Correct {
			row.MissCause = "matcher_or_signals"
			if len(row.Lint) > 0 {
				row.MissCause = "invalid_condition"
			}
		}
		rows = append(rows, row)
	}
	return ModeReport{Mode: mode, Rows: rows, Metrics: metricsOf(rows, true), MetricsNoDebate: metricsOf(rows, false)}, nil
}

// unmetExceptions explains a missed exception: for gold EXCEPTION_TRIGGERED predicted otherwise,
// the exception conditions that did not hold in the state.
func unmetExceptions(p Pair, r knowledge.Result) []string {
	if p.Gold != knowledge.LabelExceptionTriggered || r.Label == knowledge.LabelExceptionTriggered {
		return nil
	}
	var out []string
	for _, x := range p.Knowledge.Exceptions {
		for _, c := range x.Conditions {
			probe := p.Knowledge
			probe.SituationSignature = []knowledge.Condition{c}
			probe.ApplicabilityConditions, probe.Exceptions = nil, nil
			if res, err := knowledge.Match(probe, p.Situation); err == nil && res.Label != knowledge.LabelApplies {
				out = append(out, x.Description+": "+res.Entry.UnmatchedConditions[0])
			}
		}
	}
	return out
}

func metricsOf(rows []Row, includeDebatable bool) Metrics {
	m := Metrics{Confusion: map[string]int{}}
	var correct, tp, fp, fn, excTP, excGold, excPred int
	for _, r := range rows {
		if r.Debatable && !includeDebatable {
			continue
		}
		m.Pairs++
		m.Confusion[r.Gold+"->"+r.Predicted]++
		m.MissesInvalidCondition += boolInt(r.MissCause == "invalid_condition")
		correct += boolInt(r.Correct)
		goldA, predA := r.Gold == knowledge.LabelApplies, r.Predicted == knowledge.LabelApplies
		tp += boolInt(goldA && predA)
		fp += boolInt(!goldA && predA)
		fn += boolInt(goldA && !predA)
		goldE, predE := r.Gold == knowledge.LabelExceptionTriggered, r.Predicted == knowledge.LabelExceptionTriggered
		excGold += boolInt(goldE)
		excPred += boolInt(predE)
		excTP += boolInt(goldE && predE)
	}
	m.Accuracy = ratio(correct, m.Pairs)
	m.ApplicabilityPrecision = ratio(tp, tp+fp)
	m.ApplicabilityRecall = ratio(tp, tp+fn)
	m.ExceptionRecall = ratio(excTP, excGold)
	m.ExceptionPrecision = ratio(excTP, excPred)
	return m
}

func ratio(num, den int) *float64 {
	if den == 0 {
		return nil
	}
	v := float64(num) / float64(den)
	return &v
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
