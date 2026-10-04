package abcrun

import (
	"context"
	"fmt"
	"github.com/harneet2512/gtm-work/core-go/internal/knowledge"
	"slices"
	"time"
)

// layout is a built situation with its arms' knowledge decided: B reads bSet, C reads cSet (nil: no arm C).
type layout struct {
	so   SituationOut
	ref  Ref
	bSet []LearnedItem
	cSet []LearnedItem
}

// lay builds the situation's world and decides, from the matcher alone, what each arm will read. It calls no model.
func (r *Runner) lay(ctx context.Context, s Situation) (layout, error) {
	l := layout{so: SituationOut{ID: s.ID, Arms: map[string]*ArmOut{}}}
	if err := SeedIDs(ctx, r.o.DB, s.ID+"|world"); err != nil {
		return l, err
	}
	var err error
	if l.ref, err = r.o.Builder.Build(ctx, s); err != nil {
		return l, err
	}
	asOf := s.Trigger.OccurredAt
	r.clk.Set(asOf.Add(time.Minute))
	if l.so.Labels, err = r.labelsFor(ctx, l.ref.AccountID, asOf); err != nil {
		return l, err
	}
	if l.so.Context, err = r.context(ctx, s, l.ref); err != nil {
		return l, err
	}
	applicable, irrelevant := Partition(r.o.Learning.Knowledge, l.so.Labels)
	target := targetItem(r.o.Learning.Knowledge, s.DecisionPoint)
	l.bSet = applicable
	switch s.Kind {
	case "discriminating":
		if target != nil && l.so.Labels[target.ID] != knowledge.LabelApplies {
			return l, fmt.Errorf("selection error: the learned item of %s does not apply in a discriminating situation (%s)", s.DecisionPoint, l.so.Labels[target.ID])
		}
		l.cSet = withoutPoint(irrelevant, s.DecisionPoint)
	case "exception":
		if target == nil || l.so.Labels[target.ID] == "" || l.so.Labels[target.ID] == knowledge.LabelApplies {
			return l, fmt.Errorf("selection error: the learned item of %s must be retrieved and not apply in an exception situation", s.DecisionPoint)
		}
		l.bSet = append(slices.Clone(applicable), *target) // retrieved, and the matcher must find it does not apply
	case "control":
		// the decision point has no learned item the lifecycle kept: whatever learned knowledge is retrieved here is irrelevant to it
		if target != nil && l.so.Labels[target.ID] != "" {
			return l, fmt.Errorf("selection error: the learned item of %s is retrievable, so this is not a control situation", s.DecisionPoint)
		}
		if len(applicable) > 0 || len(irrelevant) == 0 {
			return l, fmt.Errorf("selection error: a control situation needs retrieved knowledge and none that applies (applicable %d, irrelevant %d)", len(applicable), len(irrelevant))
		}
		l.bSet, l.cSet = nil, irrelevant
	}
	return l, nil
}

func (r *Runner) situation(ctx context.Context, s Situation) (SituationOut, error) {
	l, err := r.lay(ctx, s)
	if err != nil {
		return l.so, err
	}
	if s.Kind == "control" {
		c, a, err := r.armWithA(ctx, s, l.ref, l.cSet, "C")
		l.so.Arms["C"], l.so.Arms["A"] = c, a
		return l.so, err
	}
	b, a, err := r.armWithA(ctx, s, l.ref, l.bSet, "B")
	if err != nil {
		return l.so, err
	}
	l.so.Arms["B"], l.so.Arms["A"] = b, a
	if len(l.cSet) > 0 {
		if l.so.Arms["C"], err = r.armC(ctx, s, l.ref, l.cSet); err != nil {
			return l.so, err
		}
	}
	return l.so, nil
}

// PlanRow is what a run would do for one situation, decided without any model call.
type PlanRow struct {
	ID          string            `json:"id"`
	Kind        string            `json:"kind"`
	Labels      map[string]string `json:"labels"`
	BSet        []string          `json:"b_store"`
	CSet        []string          `json:"c_store"`
	Generations int               `json:"generations"`
	StateHeader string            `json:"state_header"`
	Error       string            `json:"error,omitempty"`
}

// Plan builds every selected situation's world and reports the knowledge each arm would read and how many strategy
// generations the run would make (B, the counterfactual A, and C when something irrelevant exists). It makes no model
// call, and fails on a selection error (an item that should or should not apply) before any call is made.
func (r *Runner) Plan(ctx context.Context) ([]PlanRow, []knowledge.Knowledge, error) {
	if err := InstallStore(ctx, r.o.DB, r.o.Rules, r.o.Learning.Knowledge); err != nil {
		return nil, nil, err
	}
	var err error
	if r.cache, err = ExportStore(ctx, r.o.DB); err != nil {
		return nil, nil, err
	}
	var rows []PlanRow
	for _, s := range r.o.Pack.Situations {
		if len(r.o.Only) > 0 && !slices.Contains(r.o.Only, s.ID) {
			continue
		}
		l, err := r.lay(ctx, s)
		if err != nil {
			rows = append(rows, PlanRow{ID: s.ID, Kind: s.Kind, Labels: l.so.Labels, StateHeader: l.so.Context.StateHeader, Error: err.Error()})
			continue
		}
		gens := 2
		if len(l.cSet) > 0 {
			gens = 3
		}
		rows = append(rows, PlanRow{ID: s.ID, Kind: s.Kind, Labels: l.so.Labels, BSet: ids(l.bSet), CSet: ids(l.cSet), Generations: gens, StateHeader: l.so.Context.StateHeader})
	}
	return rows, r.cache, nil
}

func targetItem(items []LearnedItem, point string) *LearnedItem {
	for i := range items {
		if items[i].DecisionPoint == point {
			return &items[i]
		}
	}
	return nil
}

func withoutPoint(items []LearnedItem, point string) []LearnedItem {
	var out []LearnedItem
	for _, it := range items {
		if it.DecisionPoint != point {
			out = append(out, it)
		}
	}
	return out
}
