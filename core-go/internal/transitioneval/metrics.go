package transitioneval

import (
	"slices"
	"sort"
)

// PR is a precision/recall pair with its counts (a ratio is nil when its denominator is 0).
type PR struct {
	TP        int      `json:"tp"`
	FP        int      `json:"fp"`
	FN        int      `json:"fn"`
	Precision *float64 `json:"precision"`
	Recall    *float64 `json:"recall"`
}

// Ratio is a count over a total.
type Ratio struct {
	Num   int      `json:"num"`
	Den   int      `json:"den"`
	Value *float64 `json:"value"`
}

// Disagreement is one step where the detector differs from the gold.
type Disagreement struct {
	Scenario  string   `json:"scenario"`
	Step      string   `json:"step"`
	Slice     string   `json:"slice"`
	GoldState string   `json:"gold_status"`
	GoldTo    string   `json:"gold_to_state,omitempty"`
	PredState string   `json:"predicted_status"`
	PredTo    string   `json:"predicted_to_state,omitempty"`
	Facts     []string `json:"fact_differences,omitempty"`
	Why       string   `json:"why"`
}

// Metrics scores one set of steps.
type Metrics struct {
	Steps              int                       `json:"steps"`
	Scenarios          int                       `json:"scenarios"`
	StatusAccuracy     Ratio                     `json:"status_accuracy"`
	StrictAccuracy     Ratio                     `json:"status_and_target_accuracy"`
	Detection          PR                        `json:"transition_detection_any_status"`
	StrictDetection    PR                        `json:"transition_detection_status_and_target"`
	PerStatus          map[string]PR             `json:"per_status"`
	Confusion          map[string]map[string]int `json:"confusion_gold_to_predicted"`
	PrematurePromotion Ratio                     `json:"premature_promotion"`
	UncertaintyMarking Ratio                     `json:"uncertainty_marking"`
	RelationshipState  Ratio                     `json:"relationship_state_accuracy"`
	SupportingFacts    PR                        `json:"supporting_required_facts"`
	ContradictingFacts PR                        `json:"contradicting_facts_preserved"`
	StaleClosure       Ratio                     `json:"stale_closure"`
	Disagreements      []Disagreement            `json:"disagreements"`
}

var statuses = []string{"CONFIRMED", "CANDIDATE", "UNRESOLVED", "REJECTED"}

func ratio(num, den int) Ratio {
	r := Ratio{Num: num, Den: den}
	if den > 0 {
		v := round3(float64(num) / float64(den))
		r.Value = &v
	}
	return r
}

func round3(v float64) float64 { return float64(int(v*1000+0.5)) / 1000 }

func finish(c PR) PR {
	if c.TP+c.FP > 0 {
		p := round3(float64(c.TP) / float64(c.TP+c.FP))
		c.Precision = &p
	}
	if c.TP+c.FN > 0 {
		r := round3(float64(c.TP) / float64(c.TP+c.FN))
		c.Recall = &r
	}
	return c
}

// setCounts adds the overlap of two key sets to a PR (gold g, predicted p) and names the differences.
func setCounts(c *PR, g, p []string) []string {
	var diff []string
	for _, k := range p {
		if slices.Contains(g, k) {
			c.TP++
		} else {
			c.FP++
			diff = append(diff, "unexpected:"+k)
		}
	}
	for _, k := range g {
		if !slices.Contains(p, k) {
			c.FN++
			diff = append(diff, "missing:"+k)
		}
	}
	return diff
}

// tally accumulates the counts behind Metrics while steps are scored.
type tally struct {
	statusOK, strictOK, promoNum, promoDen, uncNum, uncDen, relOK, staleNum, staleDen int
	detection, strict, supporting, contradicting                                      PR
	per                                                                               map[string]*PR
}

// Compute scores results; keep selects the steps to include.
func Compute(results []StepResult, keep func(StepResult) bool) Metrics {
	m := Metrics{PerStatus: map[string]PR{}, Confusion: map[string]map[string]int{}, Disagreements: []Disagreement{}}
	t := &tally{per: map[string]*PR{}}
	for _, s := range statuses {
		t.per[s] = &PR{}
	}
	scenarios := map[string]bool{}
	for _, r := range results {
		if !keep(r) {
			continue
		}
		m.Steps++
		scenarios[r.Scenario] = true
		g, p := r.GoldStatus(), r.Pred.Status
		if m.Confusion[g] == nil {
			m.Confusion[g] = map[string]int{}
		}
		m.Confusion[g][p]++
		facts := t.score(r)
		if differs(r, facts) {
			m.Disagreements = append(m.Disagreements, Disagreement{Scenario: r.Scenario, Step: r.Label, Slice: r.Slice, GoldState: g, GoldTo: goldTo(r.Gold),
				PredState: p, PredTo: r.Pred.ToState, Facts: facts, Why: why(r, facts)})
		}
	}
	m.Scenarios = len(scenarios)
	m.StatusAccuracy, m.StrictAccuracy = ratio(t.statusOK, m.Steps), ratio(t.strictOK, m.Steps)
	m.Detection, m.StrictDetection = finish(t.detection), finish(t.strict)
	for _, s := range statuses {
		m.PerStatus[s] = finish(*t.per[s])
	}
	m.PrematurePromotion, m.UncertaintyMarking = ratio(t.promoNum, t.promoDen), ratio(t.uncNum, t.uncDen)
	m.RelationshipState, m.StaleClosure = ratio(t.relOK, m.Steps), ratio(t.staleNum, t.staleDen)
	m.SupportingFacts, m.ContradictingFacts = finish(t.supporting), finish(t.contradicting)
	sort.SliceStable(m.Disagreements, func(i, j int) bool { return m.Disagreements[i].Scenario < m.Disagreements[j].Scenario })
	return m
}

// score adds one step to the tally and returns its fact differences.
func (t *tally) score(r StepResult) []string {
	g, p := r.GoldStatus(), r.Pred.Status
	gTo, pTo := goldTo(r.Gold), r.Pred.ToState
	sameTarget := g == p && gTo == pTo
	if g == p {
		t.statusOK++
	}
	if sameTarget {
		t.strictOK++
	}
	for _, s := range statuses {
		switch {
		case g == s && p == s:
			t.per[s].TP++
		case g != s && p == s:
			t.per[s].FP++
		case g == s && p != s:
			t.per[s].FN++
		}
	}
	switch {
	case g != None && p != None:
		t.detection.TP++
	case g == None && p != None:
		t.detection.FP++
	case g != None && p == None:
		t.detection.FN++
	}
	if g != None && sameTarget {
		t.strict.TP++
	}
	if p != None && !sameTarget {
		t.strict.FP++
	}
	if g != None && !sameTarget {
		t.strict.FN++
	}
	if p == "CONFIRMED" {
		t.promoDen++
		if g != "CONFIRMED" {
			t.promoNum++
		}
	}
	if g == "CANDIDATE" || g == "UNRESOLVED" {
		t.uncDen++
		if p == "CANDIDATE" || p == "UNRESOLVED" {
			t.uncNum++
		}
	}
	if r.Pred.RelationshipStateAfter == r.Gold.RelationshipStateAfter {
		t.relOK++
	}
	if r.Gold.Closed {
		t.staleDen++
		if r.Pred.Closed {
			t.staleNum++
		}
	}
	var facts []string
	// The pitch does not say which facts an UNRESOLVED transition keeps, so those are not scored at fact level.
	if g != "UNRESOLVED" && (g != None || p != None) && (g == None || r.Gold.SupportingRequired != nil) {
		facts = append(facts, setCounts(&t.supporting, r.Gold.SupportingRequired, r.Pred.SupportingRequired)...)
	}
	if (g != None || p != None) && (g == None || r.Gold.Contradicting != nil) {
		facts = append(facts, setCounts(&t.contradicting, r.Gold.Contradicting, r.Pred.Contradicting)...)
	}
	return facts
}

func differs(r StepResult, facts []string) bool {
	return r.GoldStatus() != r.Pred.Status || goldTo(r.Gold) != r.Pred.ToState || r.Gold.Closed != r.Pred.Closed || len(facts) > 0 ||
		r.Pred.RelationshipStateAfter != r.Gold.RelationshipStateAfter
}

func why(r StepResult, facts []string) string {
	g, p := r.GoldStatus(), r.Pred.Status
	switch {
	case g != p && p == None:
		return "the detector recorded nothing where the gold expects " + g
	case g != p && g == None:
		return "the detector recorded " + p + " where the gold expects nothing"
	case g != p:
		return "status differs"
	case goldTo(r.Gold) != r.Pred.ToState:
		return "target differs"
	case len(facts) > 0:
		return "status agrees, facts differ"
	}
	return "closure or relationship state differs"
}
