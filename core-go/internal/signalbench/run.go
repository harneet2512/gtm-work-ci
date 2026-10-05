package signalbench

import (
	"fmt"
	"math"
	"sort"

	"github.com/harneet2512/gtm-work/core-go/internal/reducer"
	"github.com/harneet2512/gtm-work/core-go/internal/signalhistory"
	"github.com/harneet2512/gtm-work/core-go/internal/signals"
	"github.com/harneet2512/gtm-work/core-go/internal/statediff"
	"github.com/harneet2512/gtm-work/core-go/internal/trigger"
)

// Result is what the WP8 rules produce for one checkpoint.
type Result struct {
	Checkpoint Checkpoint
	Diff       statediff.Diff
	Signals    []signals.Signal
	Decision   trigger.Evaluation
}

// Replay runs the rules over every account's checkpoints in order. The first checkpoint of an account
// is diffed against the empty state.
func Replay(cps []Checkpoint) []Result {
	var out []Result
	var prev *reducer.AccountState
	last := ""
	for _, cp := range cps {
		if cp.Account != last {
			prev, last = nil, cp.Account
		}
		ids := make([]string, 0, len(cp.Activities))
		for _, a := range cp.Activities {
			ids = append(ids, a.ID)
		}
		diff := statediff.Compute(prev, cp.State, ids)
		emitted := signals.Evaluate(signals.Input{Prev: prev, Next: cp.State, Diff: diff, Activities: cp.Activities})
		decision := trigger.Evaluate(trigger.Input{
			AccountID: cp.Account, Diff: diff, Signals: emitted, Activities: cp.Activities,
			ChampionKnown: cp.State.Fields.Champion.Known, Now: cp.AsOf,
		})
		out = append(out, Result{Checkpoint: cp, Diff: diff, Signals: emitted, Decision: decision})
		state := cp.State
		prev = &state
	}
	return out
}

// HistoryRecords is the persisted signal history the replay would have written: every signal of every
// checkpoint, timed at its occurrence and windowed, per account. This is what the knowledge benchmark's
// "persisted signal history" mode queries.
func HistoryRecords(results []Result) map[string][]signalhistory.Record {
	out := map[string][]signalhistory.Record{}
	for _, r := range results {
		for _, s := range r.Signals {
			out[r.Checkpoint.Account] = append(out[r.Checkpoint.Account], signalhistory.Record{
				ID:   fmt.Sprintf("%s-cp%d-%s", r.Checkpoint.Account, r.Checkpoint.Number, s.DedupeKey),
				Type: s.Type, OccurredAt: s.OccurredAt, ExpiresAt: s.ExpiresAt, Details: s.Details,
			})
		}
	}
	return out
}

// Set counts a predicted set against a gold set.
type Set struct {
	TP        int      `json:"tp"`
	FP        int      `json:"fp"`
	FN        int      `json:"fn"`
	Precision *float64 `json:"precision"`
	Recall    *float64 `json:"recall"`
	F1        *float64 `json:"f1"`
}

func score(tp, fp, fn int) Set {
	s := Set{TP: tp, FP: fp, FN: fn}
	if tp+fp > 0 {
		s.Precision = ratio(tp, tp+fp)
	}
	if tp+fn > 0 {
		s.Recall = ratio(tp, tp+fn)
	}
	if s.Precision != nil && s.Recall != nil && *s.Precision+*s.Recall > 0 {
		f := round(2 * *s.Precision * *s.Recall / (*s.Precision + *s.Recall))
		s.F1 = &f
	}
	return s
}

func ratio(n, d int) *float64 { v := round(float64(n) / float64(d)); return &v }

func round(v float64) float64 { return math.Round(v*1000) / 1000 }

// compare splits gold and predicted into missing (gold only) and extra (predicted only).
func compare(gold, predicted []string) (missing, extra []string, tp int) {
	g, p := map[string]bool{}, map[string]bool{}
	for _, x := range gold {
		g[x] = true
	}
	for _, x := range predicted {
		p[x] = true
	}
	for x := range g {
		if p[x] {
			tp++
		} else {
			missing = append(missing, x)
		}
	}
	for x := range p {
		if !g[x] {
			extra = append(extra, x)
		}
	}
	sort.Strings(missing)
	sort.Strings(extra)
	return missing, extra, tp
}

func distinct(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	sort.Strings(out)
	return out
}
