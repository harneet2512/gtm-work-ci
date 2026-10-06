package crmarena

import (
	"fmt"
	"time"
)

// SplitConfig are the inputs of a split.
type SplitConfig struct {
	Cutoff               string // YYYY-MM-DD; "" applies CutoffRule
	Seed                 int64
	PreviousMinQuietDays int // quiet days a deal needs at the cutoff to be previous; 0 disables the filter
}

// NewSplit freezes the partition with the default quiet period; cutoff "" applies CutoffRule.
func NewSplit(windows map[string]Window, cutoff string, seed int64) (Split, error) {
	return NewSplitWith(windows, SplitConfig{Cutoff: cutoff, Seed: seed, PreviousMinQuietDays: DefaultPreviousMinQuietDays})
}

// NewSplitWith freezes the partition at the configured cutoff.
func NewSplitWith(windows map[string]Window, cfg SplitConfig) (Split, error) {
	if cfg.PreviousMinQuietDays < 0 {
		return Split{}, fmt.Errorf("crmarena: previous_min_quiet_days %d is negative", cfg.PreviousMinQuietDays)
	}
	candidates, err := Candidates(windows, cfg.PreviousMinQuietDays)
	if err != nil {
		return Split{}, err
	}
	cutoff, rule := cfg.Cutoff, CutoffRule
	if cutoff == "" {
		best, err := ChooseCutoff(candidates)
		if err != nil {
			return Split{}, err
		}
		cutoff = best.Cutoff
	} else {
		rule = "cutoff given explicitly; " + roleDefinitions
	}
	at, err := parseDate("cutoff", cutoff)
	if err != nil {
		return Split{}, err
	}
	counts, previous, ambiguous, current := CountAt(windows, at, cfg.PreviousMinQuietDays)
	prevDeals, prevByStage := quietDeals(windows, previous, at)
	ambDeals, ambByStage := quietDeals(windows, ambiguous, at)
	return Split{Cutoff: cutoff, Rule: rule, Seed: cfg.Seed, PreviousMinQuietDays: cfg.PreviousMinQuietDays,
		Counts: counts, Candidates: candidates, PreviousByStage: prevByStage, AmbiguousByStage: ambByStage,
		Previous: nonNil(previous), PreviousDeals: prevDeals, Ambiguous: nonNil(ambiguous), AmbiguousDeals: ambDeals,
		Current: nonNil(current)}, nil
}

func quietDeals(windows map[string]Window, ids []string, cutoff time.Time) ([]DealQuiet, map[string]int) {
	out, byStage := make([]DealQuiet, 0, len(ids)), map[string]int{}
	for _, id := range ids {
		w := windows[id]
		out = append(out, DealQuiet{DealID: id, Stage: w.Stage, DaysQuiet: w.QuietDaysAt(cutoff)})
		byStage[w.Stage]++
	}
	return out, byStage
}

func nonNil(ids []string) []string {
	if ids == nil {
		return []string{}
	}
	return ids
}
