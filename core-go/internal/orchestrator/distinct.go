package orchestrator

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
)

// SimilarityLimit is the share of content words two bodies must have in common (Jaccard) to count as the same
// message. It is the worker's constant (worker-py/ghost_worker/strategies/distinct.py); the shared fixture
// fixtures/orchestrator/distinctness_cases.json pins both implementations to the same verdicts (contract I10).
const SimilarityLimit = 0.6

var (
	wordPattern = regexp.MustCompile(`[a-z0-9']+`)
	stopWords   = map[string]bool{}
)

func init() {
	for _, w := range strings.Fields(`a an and are as at be but by can for from have i if in is it of on or our so that the
their them then there these they this to us we will with you your`) {
		stopWords[w] = true
	}
}

// shape is what distinctness compares for one candidate.
type shape struct {
	StrategyType string
	ActionType   string
	People       []string // to + cc person ids
	Body         string
}

func contentWords(text string) map[string]bool {
	out := map[string]bool{}
	for _, w := range wordPattern.FindAllString(strings.ToLower(text), -1) {
		if !stopWords[w] && len(w) > 1 {
			out[w] = true
		}
	}
	return out
}

func similarity(a, b string) float64 {
	wa, wb := contentWords(a), contentWords(b)
	if len(wa) == 0 && len(wb) == 0 {
		return 1
	}
	shared := 0
	for w := range wa {
		if wb[w] {
			shared++
		}
	}
	return float64(shared) / float64(len(wa)+len(wb)-shared)
}

func samePeople(a, b []string) bool {
	x, y := slices.Clone(a), slices.Clone(b)
	slices.Sort(x)
	slices.Sort(y)
	return slices.Equal(slices.Compact(x), slices.Compact(y))
}

// notDistinct says why a and b are not materially distinct, or "".
func notDistinct(a, b shape) string {
	if a.StrategyType == b.StrategyType {
		return "share the strategy_type " + a.StrategyType
	}
	if a.ActionType == b.ActionType && samePeople(a.People, b.People) && similarity(a.Body, b.Body) >= SimilarityLimit {
		return fmt.Sprintf("%s and %s are the same %s to the same people, reworded", a.StrategyType, b.StrategyType, a.ActionType)
	}
	return ""
}

// distinctnessViolations lists every pair of shapes that are not materially distinct.
func distinctnessViolations(shapes []shape) []string {
	var out []string
	for i, a := range shapes {
		for _, b := range shapes[i+1:] {
			if why := notDistinct(a, b); why != "" {
				out = append(out, why)
			}
		}
	}
	return out
}
