package signalbench

import (
	"sort"
	"strings"

	"github.com/harneet2512/gtm-work/core-go/internal/signals"
)

// Row is one checkpoint's comparison.
type Row struct {
	Account    string `json:"account"`
	Checkpoint int    `json:"checkpoint"`
	// BurstEvents is how many events the checkpoint folds in; the gold trigger is about the last one only.
	BurstEvents int `json:"burst_events"`

	SignalsMissing []string `json:"signals_missing,omitempty"`
	SignalsExtra   []string `json:"signals_extra,omitempty"`
	MaterialMissed []string `json:"material_fields_missing,omitempty"`
	MaterialExtra  []string `json:"material_fields_extra,omitempty"`

	TriggerGold      Decision `json:"trigger_gold"`
	TriggerPredicted Decision `json:"trigger_predicted"`
	EligibleCorrect  bool     `json:"eligible_correct"`
	ReasonsCorrect   bool     `json:"reasons_correct"`
}

// Decision is a trigger outcome.
type Decision struct {
	Eligible bool     `json:"eligible"`
	Reasons  []string `json:"reasons"`
}

// TriggerScore is trigger decision accuracy.
type TriggerScore struct {
	Cases               int      `json:"cases"`
	EligibleAccuracy    *float64 `json:"eligible_accuracy"`
	ExactReasonAccuracy *float64 `json:"exact_reason_accuracy"`
	EligibleCorrect     int      `json:"eligible_correct"`
	ExactReasonsCorrect int      `json:"exact_reasons_correct"`
}

// Report is the benchmark output.
type Report struct {
	Benchmark      string         `json:"benchmark"`
	GoldSource     string         `json:"gold_source"`
	Caveats        []string       `json:"caveats"`
	Checkpoints    int            `json:"checkpoints"`
	Signals        Set            `json:"signals_micro"`
	SignalsByType  map[string]Set `json:"signals_by_type"`
	MaterialFields Set            `json:"material_diff_fields_micro"`
	NoMaterialDiff Set            `json:"no_material_change_checkpoints"`
	Trigger        TriggerScore   `json:"trigger"`
	// TriggerShortBursts is the same score over checkpoints that fold in at most ShortBurst events, where
	// burst and last-event semantics nearly coincide (production bursts last seconds).
	TriggerShortBursts TriggerScore `json:"trigger_short_bursts"`
	Rows               []Row        `json:"rows"`
}

// Run loads the gold under root, replays the rules and scores them.
func Run(root string) (Report, error) {
	cps, err := Load(root)
	if err != nil {
		return Report{}, err
	}
	return Score(Replay(cps)), nil
}

// Score compares every result to its checkpoint's gold.
func Score(results []Result) Report {
	rep := Report{
		Benchmark:  "wp8_signals_triggers:gold_checkpoints",
		GoldSource: "fixtures/gold/*/cp*.json (expected.signals, expected.material_diff_fields, expected.trigger)",
		Caveats: []string{
			"Upstream state is the gold state (oracle extraction): these numbers isolate the WP8 rules, not extraction.",
			"expected.trigger is the evaluation right after the checkpoint's LAST event; the replay has one state per checkpoint, so it evaluates the whole burst since the previous checkpoint (the production semantics: one coalesced burst, one evaluation). Multi-event checkpoints can therefore differ for reasons that are not rule errors.",
			"field_contradicted needs the adjudication conflicts of a real recompute (ADR-0008); the oracle has none, so it is always missed here.",
			"product_usage_increased has no rule (the activity carries no direction).",
		},
		SignalsByType: map[string]Set{},
	}
	var tp, fp, fn, mtp, mfp, mfn int
	typeCounts := map[string][3]int{}
	var noMat [3]int
	for _, r := range results {
		predicted := distinct(signalTypes(r.Signals))
		missing, extra, hit := compare(r.Checkpoint.Expected.Signals, predicted)
		tp, fp, fn = tp+hit, fp+len(extra), fn+len(missing)
		tally(typeCounts, r.Checkpoint.Expected.Signals, predicted)
		mMissing, mExtra, mHit := compare(r.Checkpoint.Expected.MaterialDiffFields, r.Diff.MaterialFields())
		mtp, mfp, mfn = mtp+mHit, mfp+len(mExtra), mfn+len(mMissing)
		goldNone, predNone := len(r.Checkpoint.Expected.MaterialDiffFields) == 0, !r.Diff.IsMaterial
		switch {
		case goldNone && predNone:
			noMat[0]++
		case !goldNone && predNone:
			noMat[2]++
		case goldNone && !predNone:
			noMat[1]++
		}
		rep.Rows = append(rep.Rows, row(r, missing, extra, mMissing, mExtra))
	}
	rep.Checkpoints = len(results)
	rep.Signals = score(tp, fp, fn)
	rep.MaterialFields = score(mtp, mfp, mfn)
	rep.NoMaterialDiff = score(noMat[0], noMat[1], noMat[2])
	for t, c := range typeCounts {
		rep.SignalsByType[t] = score(c[0], c[1], c[2])
	}
	rep.Trigger = triggerScore(rep.Rows, 0)
	rep.TriggerShortBursts = triggerScore(rep.Rows, ShortBurst)
	return rep
}

func signalTypes(ss []signals.Signal) []string {
	out := make([]string, 0, len(ss))
	for _, s := range ss {
		out = append(out, s.Type)
	}
	return out
}

func row(r Result, missing, extra, mMissing, mExtra []string) Row {
	gold := Decision{Eligible: r.Checkpoint.Expected.Trigger.Eligible, Reasons: sorted(r.Checkpoint.Expected.Trigger.ReasonCodes)}
	pred := Decision{Eligible: r.Decision.Eligible, Reasons: sorted(r.Decision.ReasonCodes)}
	return Row{Account: r.Checkpoint.Account, Checkpoint: r.Checkpoint.Number, BurstEvents: len(r.Checkpoint.Activities), SignalsMissing: missing, SignalsExtra: extra,
		MaterialMissed: mMissing, MaterialExtra: mExtra, TriggerGold: gold, TriggerPredicted: pred,
		EligibleCorrect: gold.Eligible == pred.Eligible, ReasonsCorrect: strings.Join(gold.Reasons, ",") == strings.Join(pred.Reasons, ",")}
}

func sorted(in []string) []string {
	out := append([]string{}, in...)
	sort.Strings(out)
	return out
}

// ShortBurst is the largest burst (in events) counted as a short burst.
const ShortBurst = 2

// triggerScore scores the rows; maxEvents > 0 keeps only bursts of at most that many events.
func triggerScore(rows []Row, maxEvents int) TriggerScore {
	s := TriggerScore{}
	for _, r := range rows {
		if maxEvents > 0 && r.BurstEvents > maxEvents {
			continue
		}
		s.Cases++
		if r.EligibleCorrect {
			s.EligibleCorrect++
		}
		if r.ReasonsCorrect {
			s.ExactReasonsCorrect++
		}
	}
	if s.Cases > 0 {
		s.EligibleAccuracy = ratio(s.EligibleCorrect, s.Cases)
		s.ExactReasonAccuracy = ratio(s.ExactReasonsCorrect, s.Cases)
	}
	return s
}

// tally adds one checkpoint to the per-type tp/fp/fn counters.
func tally(counts map[string][3]int, gold, predicted []string) {
	g, p := map[string]bool{}, map[string]bool{}
	for _, t := range gold {
		g[t] = true
	}
	for _, t := range predicted {
		p[t] = true
	}
	for t := range g {
		c := counts[t]
		if p[t] {
			c[0]++
		} else {
			c[2]++
		}
		counts[t] = c
	}
	for t := range p {
		if !g[t] {
			c := counts[t]
			c[1]++
			counts[t] = c
		}
	}
}
