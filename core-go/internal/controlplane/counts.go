package controlplane

import "time"

// Counts are real tallies of persisted EvalResults (eval_run.v1.json counts). Never a percentage or a score. The same
// type carries a delta (this minus previous), where a count may be negative.
type Counts struct {
	Pass         int `json:"pass"`
	Warn         int `json:"warn"`
	Fail         int `json:"fail"`
	Unknown      int `json:"unknown"`
	BlockingFail int `json:"blocking_fail"`
	Total        int `json:"total"`
}

// Minus is c - o in every count.
func (c Counts) Minus(o Counts) Counts {
	return Counts{c.Pass - o.Pass, c.Warn - o.Warn, c.Fail - o.Fail, c.Unknown - o.Unknown, c.BlockingFail - o.BlockingFail, c.Total - o.Total}
}

// result is one persisted EvalResult (an eval_runs row), reduced to what the roll-ups read.
type result struct {
	ID        string
	EvalType  string
	Verdict   string // pass | warn | fail | abstain, as stored
	Blocking  bool
	CreatedAt time.Time
	SendTime  bool // written by the send-time re-evaluation (eval_runs.phase = send), not at generation
}

// Verdict words of the control plane: the stored `abstain` is `unknown` here.
const (
	vPass    = "pass"
	vWarn    = "warn"
	vFail    = "fail"
	vUnknown = "unknown"
)

func normalizeVerdict(v string) string {
	if v == "abstain" {
		return vUnknown
	}
	return v
}

// tally counts results. The verdict column is CHECK-constrained to pass, warn, fail, abstain, so anything else is
// folded into unknown rather than dropped: the total always equals the number of rows.
func tally(rs []result) Counts {
	var c Counts
	for _, r := range rs {
		switch normalizeVerdict(r.Verdict) {
		case vPass:
			c.Pass++
		case vWarn:
			c.Warn++
		case vFail:
			c.Fail++
			if r.Blocking {
				c.BlockingFail++
			}
		default:
			c.Unknown++
		}
		c.Total++
	}
	return c
}

// rank places a verdict on the ladder fail < warn < unknown < pass.
func rank(v string) int {
	switch normalizeVerdict(v) {
	case vFail:
		return 0
	case vWarn:
		return 1
	case vUnknown:
		return 2
	default:
		return 3
	}
}

// worstOf is the lowest verdict on the ladder among the results of one eval type, whether any of its fails blocks,
// and every result id (input order). The verdict is "" for no results.
func worstOf(rs []result) (verdict string, blocking bool, ids []string) {
	ids = []string{}
	for _, r := range rs {
		ids = append(ids, r.ID)
		if verdict == "" || rank(r.Verdict) < rank(verdict) {
			verdict = normalizeVerdict(r.Verdict)
		}
		blocking = blocking || (r.Blocking && normalizeVerdict(r.Verdict) == vFail)
	}
	return verdict, blocking, ids
}

// Changes of one eval type between two runs (eval_run_comparison.v1.json change).
const (
	changeImproved  = "improved"
	changeRegressed = "regressed"
	changeUnchanged = "unchanged"
	// changeInconclusive: one side is unknown (the eval abstained), so there is no way to say better or worse.
	changeInconclusive = "inconclusive"
	changeAdded        = "added"
	changeRemoved      = "removed"
)

// changeOf reads a move on the ladder; an empty verdict means that side did not check the eval type.
func changeOf(a, b string) string {
	switch {
	case a == "" && b == "":
		return changeUnchanged
	case a == "":
		return changeAdded
	case b == "":
		return changeRemoved
	case a != b && (a == vUnknown || b == vUnknown):
		return changeInconclusive
	case rank(b) > rank(a):
		return changeImproved
	case rank(b) < rank(a):
		return changeRegressed
	default:
		return changeUnchanged
	}
}
