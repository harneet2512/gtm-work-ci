package demomine

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// Sequence is one opportunity's timeline in replay order, with what each event did.
type Sequence struct {
	AccountID       string // Salesforce ids
	OpportunityID   string
	AccountName     string
	OpportunityName string
	Events          []EventRecord
}

// Case is the best candidate of one opportunity: events 1..N-1 as history and event N as the held-out
// candidate, with the score and the reason it was selected.
type Case struct {
	Rank            int           `json:"rank"`
	AccountID       string        `json:"account_id"`
	AccountName     string        `json:"account_name"`
	OpportunityID   string        `json:"opportunity_id"`
	OpportunityName string        `json:"opportunity_name"`
	Score           Score         `json:"score"`
	History         []EventRecord `json:"event_sequence"`
	HeldOut         EventRecord   `json:"held_out_candidate"`
	// HeldOutNote states that the held-out record is the miner's view of Event N, kept for audit; the
	// frozen manifest carries none of it as state.
	HeldOutNote string `json:"held_out_note"`
	WhySelected string `json:"why_selected"`
	// Signature is the case's dimension fingerprint (history and held-out), the input of SimilarTo.
	Signature Signature `json:"dimension_signature"`
	// SimilarTo lists the most similar other candidate cases, best first, each with its score explained.
	SimilarTo []Similar `json:"similar_to"`
	// Provenance counts the sequence's events (history and held-out) by layer.
	Provenance map[string]int `json:"provenance_counts"`
}

// BestCase picks the highest-scoring split of the sequence into history and held-out event. Ties go to the
// shorter history (the smaller world to explain). ok is false when no split meets the requirements.
func (c Config) BestCase(seq Sequence) (Case, bool) {
	var best Case
	found := false
	for n := 1; n < len(seq.Events); n++ {
		history, held := seq.Events[:n], seq.Events[n]
		later := occurredAt(held).After(occurredAt(history[n-1]))
		score, ok, _ := c.ScoreSequence(history, held, later)
		if !ok || (found && score.Total <= best.Score.Total) {
			continue
		}
		found = true
		best = Case{AccountID: seq.AccountID, AccountName: seq.AccountName, OpportunityID: seq.OpportunityID,
			OpportunityName: seq.OpportunityName, Score: score,
			History: append([]EventRecord{}, history...), HeldOut: held}
	}
	if !found {
		return Case{}, false
	}
	best.HeldOutNote = "The miner's view of Event N, kept for audit. A frozen manifest holds Event N bare: no state, diff or material flag."
	best.WhySelected = whySelected(best)
	best.Signature = SignatureOf(best.History, best.HeldOut)
	best.Provenance = map[string]int{}
	for _, e := range append(append([]EventRecord{}, best.History...), best.HeldOut) {
		best.Provenance[e.Layer]++
	}
	return best, true
}

// Rank scores every sequence and returns the candidates best first, numbered from 1. Ties go to fewer
// events, then to the opportunity id, so the order is a function of the data alone.
func (c Config) Rank(seqs []Sequence) []Case {
	var cases []Case
	for _, s := range seqs {
		if cs, ok := c.BestCase(s); ok {
			cases = append(cases, cs)
		}
	}
	sort.SliceStable(cases, func(i, j int) bool {
		a, b := cases[i], cases[j]
		switch {
		case a.Score.Total != b.Score.Total:
			return a.Score.Total > b.Score.Total
		case len(a.History) != len(b.History):
			return len(a.History) < len(b.History)
		}
		return a.OpportunityID < b.OpportunityID
	})
	for i := range cases {
		cases[i].Rank = i + 1
	}
	attachSimilar(cases)
	return cases
}

// NoCaseReason is the requirement that rules out the most splits of an opportunity that has no candidate case
// (ties go to the reason that sorts first), so the report can say why scanned opportunities did not qualify.
func (c Config) NoCaseReason(seq Sequence) string {
	counts := map[string]int{}
	for n := 1; n < len(seq.Events); n++ {
		held := seq.Events[n]
		later := occurredAt(held).After(occurredAt(seq.Events[n-1]))
		if _, ok, why := c.ScoreSequence(seq.Events[:n], held, later); !ok {
			counts[reasonKind(why)]++
		}
	}
	if len(counts) == 0 {
		return "fewer than two events"
	}
	best, bestN := "", 0
	for k, n := range counts {
		if n > bestN || (n == bestN && k < best) {
			best, bestN = k, n
		}
	}
	return best
}

// reasonKind drops the numbers from a requirement failure so equal causes group together.
func reasonKind(why string) string {
	for _, k := range []string{"history has", "held-out event is of an excluded kind", "held-out event changes no dimension", "held-out event is not dated"} {
		if strings.HasPrefix(why, k) {
			if k == "history has" {
				if strings.Contains(why, "distinct dimensions") {
					return "history has too few distinct dimensions"
				}
				return "history has too few events"
			}
			return k
		}
	}
	return why
}

func occurredAt(r EventRecord) time.Time {
	t, err := time.Parse(time.RFC3339, r.OccurredAt)
	if err != nil {
		return time.Time{}
	}
	return t
}

// whySelected states the reason in terms of the mined transitions only (never a hand-authored story).
func whySelected(c Case) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%d of %d history events moved a dimension, covering %s",
		c.Score.HistoryMaterialCount, len(c.History), listOrNone(c.Score.HistoryDimensions))
	if len(c.Score.Revisited) > 0 {
		fmt.Fprintf(&b, " (changed more than once: %s)", strings.Join(c.Score.Revisited, ", "))
	}
	fmt.Fprintf(&b, ". The held-out event at %s moves %s", c.HeldOut.OccurredAt, listOrNone(c.HeldOut.Dimensions))
	if len(c.Score.NovelDimensions) > 0 {
		fmt.Fprintf(&b, ", new to this timeline: %s", strings.Join(c.Score.NovelDimensions, ", "))
	}
	if c.Score.DecisionChange {
		b.WriteString("; it makes the trigger eligible, so the recommended next action would change")
	}
	fmt.Fprintf(&b, ". Score %.2f.", c.Score.Total)
	return b.String()
}

func listOrNone(items []string) string {
	if len(items) == 0 {
		return "no dimension"
	}
	return strings.Join(items, ", ")
}
