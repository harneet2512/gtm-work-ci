package knowledgeinfluence

import (
	"fmt"
	"slices"
	"strings"
)

// Grade scores a trace with E7.1 to E7.5. All five are deterministic: they read the retrieved / applicable /
// used / changed columns and nothing else. The four that need `changed` say `unknown` when the counterfactual was
// not compared; none of them invents a verdict from missing evidence.
func Grade(t Trace) []Result {
	return []Result{e71(t), e72(t), e73(t), e74(t), e75(t)}
}

func claimedBy(t Trace) []string {
	var out []string
	for _, c := range t.Candidates {
		for _, k := range c.Claimed {
			if !slices.Contains(out, k) {
				out = append(out, k)
			}
		}
	}
	slices.Sort(out)
	return out
}

func compared(t Trace) bool { return t.Counterfactual.Status == Compared }

// notCompared is the verdict of an eval that needs the counterfactual when there is none.
func notCompared(eval string, t Trace) Result {
	if t.Counterfactual.Status == NoKnowledge {
		return Result{eval, Pass, "no knowledge was retrieved, so knowledge cannot have changed anything"}
	}
	return Result{eval, Unknown, "the counterfactual (knowledge withheld) was not compared: " + t.Counterfactual.Status}
}

func hasClaim(c Candidate) bool { return len(c.Claimed) > 0 }

// E7.1: every knowledge id a candidate claims to have used was retrieved, judged applicable and not blocked by a
// triggered exception.
func e71(t Trace) Result {
	var bad []string
	for _, k := range claimedBy(t) {
		if !slices.Contains(t.Retrieved, k) || !slices.Contains(t.Applicable, k) || slices.Contains(t.ExceptionBlocked, k) {
			bad = append(bad, k)
		}
	}
	if len(bad) > 0 {
		return Result{"E7.1", Fail, "claimed influence without a retrieved, applicable, exception-free trace: " + strings.Join(bad, ", ")}
	}
	return Result{"E7.1", Pass, fmt.Sprintf("%d claimed knowledge ids are all supported by the trace", len(claimedBy(t)))}
}

// E7.2: a candidate that knowledge materially changed must say which knowledge it used.
func e72(t Trace) Result {
	if len(t.Applicable) == 0 {
		return Result{"E7.2", Pass, "no applicable knowledge, so no knowledge could have influenced a candidate"}
	}
	if !compared(t) {
		return notCompared("E7.2", t)
	}
	var silent []string
	for _, c := range t.Candidates {
		if c.Change != nil && c.Change.Changed && !hasClaim(c) {
			silent = append(silent, c.StrategyType)
		}
	}
	if len(silent) > 0 {
		return Result{"E7.2", Fail, "changed against the knowledge-withheld run but claims no knowledge: " + strings.Join(silent, ", ")}
	}
	return Result{"E7.2", Pass, "every candidate that changed names the knowledge it used"}
}

// E7.3: when the ranking moved because knowledge was available, a candidate whose rank moved cites knowledge.
func e73(t Trace) Result {
	if len(t.Applicable) == 0 {
		return Result{"E7.3", Pass, "no applicable knowledge, so no ranking can be attributed to it"}
	}
	if !compared(t) {
		return notCompared("E7.3", t)
	}
	if !t.Counterfactual.RankingChanged {
		return Result{"E7.3", Pass, "the ranking is the same with and without knowledge"}
	}
	for _, c := range t.Candidates {
		if c.Change != nil && c.Change.RankingChanged && hasClaim(c) {
			return Result{"E7.3", Pass, "the ranking moved and a candidate whose rank moved cites the knowledge: " + c.StrategyType}
		}
	}
	return Result{"E7.3", Fail, "the ranking moved against the knowledge-withheld run but no candidate whose rank moved cites knowledge"}
}

// E7.4: when the preferred action differs from the knowledge-withheld run's, the candidate now preferred cites knowledge.
func e74(t Trace) Result {
	if len(t.Applicable) == 0 {
		return Result{"E7.4", Pass, "no applicable knowledge, so no action can be attributed to it"}
	}
	if !compared(t) {
		return notCompared("E7.4", t)
	}
	if !t.Counterfactual.PreferredChanged {
		return Result{"E7.4", Pass, "the preferred action is the same with and without knowledge"}
	}
	pref, ok := preferred(t)
	switch {
	case !ok:
		return Result{"E7.4", Unknown, "the trace has no candidate ranked first"}
	case hasClaim(pref):
		return Result{"E7.4", Pass, "the preferred action changed and the preferred candidate cites knowledge: " + pref.StrategyType}
	}
	return Result{"E7.4", Fail, "the preferred action changed against the knowledge-withheld run but the preferred candidate cites no knowledge"}
}

func preferred(t Trace) (Candidate, bool) {
	var best Candidate
	found := false
	for _, c := range t.Candidates {
		if !found || c.Ranking < best.Ranking {
			best, found = c, true
		}
	}
	return best, found
}

// E7.5: knowledge that was retrieved but not applicable (or blocked by an exception) is never cited, and when
// nothing applicable was retrieved the decision does not move.
func e75(t Trace) Result {
	var irrelevant []string
	for _, k := range t.Retrieved {
		if !slices.Contains(t.Applicable, k) || slices.Contains(t.ExceptionBlocked, k) {
			irrelevant = append(irrelevant, k)
		}
	}
	if len(irrelevant) == 0 {
		return Result{"E7.5", Pass, "no irrelevant knowledge was retrieved"}
	}
	var cited []string
	for _, k := range claimedBy(t) {
		if slices.Contains(irrelevant, k) {
			cited = append(cited, k)
		}
	}
	if len(cited) > 0 {
		return Result{"E7.5", Fail, "irrelevant retrieved knowledge was cited as used: " + strings.Join(cited, ", ")}
	}
	if compared(t) && len(t.Applicable) == 0 && slices.ContainsFunc(t.Candidates, func(c Candidate) bool { return c.Change != nil && c.Change.Changed }) {
		return Result{"E7.5", Warn, "only irrelevant knowledge was retrieved yet a candidate differs from the knowledge-withheld run (sampling noise or influence)"}
	}
	return Result{"E7.5", Pass, "irrelevant retrieved knowledge is not cited and did not move the decision"}
}
