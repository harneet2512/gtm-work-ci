package transitioneval

import (
	"fmt"
	"slices"

	"github.com/harneet2512/gtm-work/core-go/internal/transitions"
)

// None is the status of a step that records no transition.
const None = "NONE"

// Prediction is what the detector said at one step.
type Prediction struct {
	Status                 string // CONFIRMED, CANDIDATE, UNRESOLVED, REJECTED or None
	ToState                string
	SupportingRequired     []string
	Contradicting          []string
	Closed                 bool
	RelationshipStateAfter string
}

// StepResult pairs a gold step with the detector's prediction.
type StepResult struct {
	Scenario  string
	Slice     string
	Origin    string
	Contested bool
	Label     string
	Gold      Label
	Pred      Prediction

	input  transitions.Input   // the input as the detector saw it (relationship state and open transition chained)
	record *transitions.Record // the transition as stored after this step; nil when none
}

// GoldStatus is the gold status of a step, None when no transition is expected.
func (r StepResult) GoldStatus() string {
	if r.Gold.Status == nil {
		return None
	}
	return *r.Gold.Status
}

func goldTo(l Label) string {
	if l.ToState == nil {
		return ""
	}
	return *l.ToState
}

// Run replays every scenario through the detector. The first step's relationship state is the scenario's
// starting state; every later step uses the state the detector confirmed so far and the open transition it
// left, exactly as the recompute transaction chains them.
func Run(rules transitions.RuleSet, gold Gold) ([]StepResult, error) {
	required := requiredFacts(rules)
	var out []StepResult
	for _, sc := range gold.Scenarios {
		rel := sc.Steps[0].Input.State.RelationshipState
		var open *transitions.Record
		for i, st := range sc.Steps {
			in := st.Input
			in.Signals = transitions.MarkFirstParty(rules, in.Signals, claimStandings(in.Claims))
			in.State.RelationshipState = rel
			if open != nil {
				in.Open = open.AsOpen()
			} else {
				in.Open = nil
			}
			outcome := transitions.Evaluate(rules, in)
			computed := in.Now
			if in.ComputedAt != nil {
				computed = *in.ComputedAt
			}
			current := rel.Value
			if current == "" {
				current = "unknown"
			}
			meta := transitions.Meta{AccountID: sc.ID, CurrentState: current, StateVersion: i + 1, RuleSetVersion: rules.Version,
				AsOf: in.Now, ComputedAt: computed, TriggerActivityIDs: []string{sc.ID}}
			next, changed, err := transitions.Plan(open, outcome, meta)
			if err != nil {
				return nil, fmt.Errorf("transitioneval: %s step %q: %w", sc.ID, st.Label, err)
			}
			stored := open // an evaluation that changes nothing leaves the open record as it was
			if changed {
				stored = next
				open, rel = apply(next, rel)
			}
			if outcome.None() {
				stored = nil
			}
			out = append(out, StepResult{input: in, record: stored, Scenario: sc.ID, Slice: sc.Slice, Origin: sc.Origin, Contested: sc.IsContested(), Label: st.Label, Gold: st.Gold,
				Pred: predict(outcome, rel, required)})
		}
	}
	return out, nil
}

// apply updates the chained state after a written record: a CONFIRMED record moves the relationship state.
func apply(rec *transitions.Record, rel transitions.RelationshipState) (*transitions.Record, transitions.RelationshipState) {
	if rec.Status == transitions.StatusConfirmed && rec.ToStateCandidate != nil {
		rel = transitions.RelationshipState{Value: *rec.ToStateCandidate, ConfirmedAt: rec.ConfirmedAt}
	}
	if rec.Open() {
		return rec, rel
	}
	return nil, rel
}

func predict(o transitions.Outcome, rel transitions.RelationshipState, required map[string]bool) Prediction {
	p := Prediction{Status: None, RelationshipStateAfter: rel.Value}
	if p.RelationshipStateAfter == "" {
		p.RelationshipStateAfter = "unknown"
	}
	if o.None() {
		return p
	}
	p.Status, p.ToState, p.Closed = o.Status, o.ToState, o.Closed
	for _, f := range o.Supporting {
		if required[f.Key] {
			p.SupportingRequired = append(p.SupportingRequired, f.Key)
		}
	}
	for _, f := range o.Contradicting {
		p.Contradicting = append(p.Contradicting, f.Key)
	}
	slices.Sort(p.SupportingRequired)
	slices.Sort(p.Contradicting)
	return p
}

func requiredFacts(rules transitions.RuleSet) map[string]bool {
	out := map[string]bool{}
	for _, r := range rules.Transitions {
		for _, k := range r.Confirmed.RequiresAll {
			out[k] = true
		}
	}
	return out
}

// claimStandings maps the claims of a step to their standing, as the store does for the claims a signal cites.
func claimStandings(cs []transitions.Claim) map[string]string {
	out := make(map[string]string, len(cs))
	for _, c := range cs {
		out[c.ID] = c.Standing
	}
	return out
}
