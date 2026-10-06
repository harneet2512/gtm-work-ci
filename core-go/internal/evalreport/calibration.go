package evalreport

import (
	"fmt"
	"strings"
)

// calibBuckets are the expected-calibration-error bins of eval_runs.confidence.
var calibBuckets = []struct{ lo, hi float64 }{{0, 0.5}, {0.5, 0.7}, {0.7, 0.9}, {0.9, 1.0000001}}

// calibCorrect reports whether one eval row's verdict is human-confirmed. Only supervision that
// provably settles the row's verdict counts: a pass is confirmed by an unchanged approval and wrong on
// a rejected draft; a fail is confirmed when the delta's explanations cite this very row (the edit
// repaired exactly it) or the human rejected, and wrong on an unchanged approval. On an edited
// episode EVERY pass is dropped as unusable — attributed to this axis or not: eval_runs.created_at is
// the replay clock, not the write order, so which artifact (candidate vs edited final) the row judged
// cannot be told apart, and a pass on an edit this axis was never responsible for confirms nothing
// (counting it correct is what inflated calibration for axes the attribution map never reaches).
// Uncited fails on edits are dropped for the same reason.
func (w *world) calibCorrect(r *evalRow, ep *episodeRow) (correct, usable bool) {
	if ep.HumanAction == "IGNORE" || (r.Verdict != "pass" && r.Verdict != "fail") {
		return false, false
	}
	d := w.deltaOf(ep)
	edited := ep.HumanAction == "APPROVE_WITH_EDIT" || ep.HumanAction == "MANUAL_REPLACEMENT"
	switch r.Verdict {
	case "pass":
		switch {
		case ep.HumanAction == "APPROVE_UNCHANGED":
			return true, true
		case ep.HumanAction == "REJECT" && w.rejectionPointsAt(ep, r.Evaluator):
			return false, true
		}
	case "fail":
		switch {
		case ep.HumanAction == "APPROVE_UNCHANGED":
			return false, true
		case ep.HumanAction == "REJECT" && w.rejectionPointsAt(ep, r.Evaluator):
			return true, true
		case edited && d != nil:
			for _, x := range w.explains[d.ID] {
				if x.EvalRunID == r.ID {
					return true, true
				}
			}
			return false, false // an unresolved flag: the edit neither confirms nor refutes it
		}
	}
	return false, false
}

// rejectionPointsAt reports whether a rejected episode's stated reason (or its delta) points at the
// axis. A bare rejection says nothing about WHICH axis was wrong, so scoring every fail on it "correct"
// and every pass "wrong" would credit and blame axes at random.
func (w *world) rejectionPointsAt(ep *episodeRow, axis string) bool {
	if d := w.deltaOf(ep); d != nil && attributedAxes(d)[axis] {
		return true
	}
	return ep.Reason != "" && strings.Contains(normWords(ep.Reason), normWords(axis))
}

// normWords lowercases and collapses separators so "next_step_quality" matches "next step quality".
func normWords(s string) string {
	return strings.Join(strings.Fields(strings.NewReplacer("_", " ", "-", " ").Replace(strings.ToLower(s))), " ")
}

func (w *world) calibration(k versionKey, byEpisode map[string][]*evalRow) Metric[Calibration] {
	c := Calibration{Basis: "binned eval_runs.confidence vs the human-confirmed correctness of the same " +
		"verdict on the decided episode's final draft (expected calibration error over populated bins)"}
	var sums [4]float64
	var corr, ns [4]int
	// Iterate the sorted episode list (not the map): float sums are order-dependent, so a map walk made
	// the ECE bit-nondeterministic across runs.
	for _, ev := range w.attachEpisodes(byEpisode) {
		for _, r := range ev.rows {
			ep := w.byRun[r.RunID]
			if ep == nil {
				continue
			}
			if r.Confidence == nil {
				if r.Score != nil {
					c.Scored++
				}
				continue
			}
			correct, usable := w.calibCorrect(r, ep)
			if ep.HumanAction == "REJECT" && (r.Verdict == "pass" || r.Verdict == "fail") {
				if usable {
					c.Rejections.AxisScoped++
				} else {
					c.Rejections.Unscoped++
				}
			}
			if !usable {
				continue
			}
			c.Rows++
			for i, b := range calibBuckets {
				if *r.Confidence >= b.lo && *r.Confidence < b.hi {
					ns[i]++
					sums[i] += *r.Confidence
					if correct {
						corr[i]++
					}
				}
			}
		}
	}
	if c.Rows == 0 {
		if c.Scored > 0 {
			return na[Calibration]("no eval_runs tagged " + k.tag +
				" carry confidence (score is present but is not a calibrated confidence)")
		}
		if c.Rejections.Unscoped > 0 {
			return na[Calibration](fmt.Sprintf("the only confidence rows of %s sit on rejected episodes whose "+
				"rejection reason does not point at %s (%d rows dropped)", k.tag, k.evaluator, c.Rejections.Unscoped))
		}
		return na[Calibration]("no eval_runs tagged " + k.tag + " carry confidence on a decided episode")
	}
	var ece float64
	for i, b := range calibBuckets {
		if ns[i] == 0 {
			continue
		}
		mean := sums[i] / float64(ns[i])
		obs := float64(corr[i]) / float64(ns[i])
		c.Buckets = append(c.Buckets, Bucket{
			Range: fmt.Sprintf("[%g,%g]", b.lo, minF(b.hi, 1)), N: ns[i], Mean: mean, Observed: obs})
		ece += float64(ns[i]) / float64(c.Rows) * absF(mean-obs)
	}
	c.ECE = &ece
	return avail(c)
}

func minF(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}

func absF(f float64) float64 {
	if f < 0 {
		return -f
	}
	return f
}
