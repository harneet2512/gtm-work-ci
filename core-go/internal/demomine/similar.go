package demomine

import (
	"fmt"
	"math"
	"sort"
	"strings"
)

// SimilarCount is how many similar cases the report lists per case.
const SimilarCount = 3

// Weights of the two halves of the similarity score. They are equal because a demo needs a second case that
// resembles the first in how its history unfolded AND in what Event N does; neither half is presumed to matter more.
const (
	historyShapeWeight = 0.5
	heldOutShapeWeight = 0.5
)

// Signature is a case's dimension fingerprint: how many history events moved each dimension, and which
// dimensions the held-out event moves. It is all similarity looks at, so a reader can check a score by eye.
type Signature struct {
	// History maps a dimension to the number of history events that moved it (dimensions never moved are absent).
	History map[string]int `json:"history"`
	// HeldOut lists the dimensions Event N moves, in contract order.
	HeldOut []string `json:"held_out"`
}

// Similar is one entry of a case's similar_to list.
type Similar struct {
	Rank          int     `json:"rank"`
	OpportunityID string  `json:"opportunity_id"`
	Similarity    float64 `json:"similarity"`
	// Explanation shows both halves of the score and the arithmetic.
	Explanation string `json:"explanation"`
}

// SignatureOf is the fingerprint of a split into history and held-out event.
func SignatureOf(history []EventRecord, held EventRecord) Signature {
	sig := Signature{History: map[string]int{}, HeldOut: append([]string{}, held.Dimensions...)}
	for _, e := range history {
		for _, d := range e.Dimensions {
			sig.History[d]++
		}
	}
	return sig
}

// Similarity scores two signatures in [0, 1] (symmetric) and explains the number. The history half is the
// weighted Jaccard of the per-dimension event counts (sum of minimums over sum of maximums); the held-out half
// is the Jaccard of the dimension sets; the score is their equal-weight mean.
func Similarity(a, b Signature) (float64, string) {
	histScore, histParts := historyShape(a.History, b.History)
	heldScore, shared, onlyA, onlyB := heldOutShape(a.HeldOut, b.HeldOut)
	total := math.Round((historyShapeWeight*histScore+heldOutShapeWeight*heldScore)*10000) / 10000
	why := fmt.Sprintf("history shape %.2f (events per dimension, shared/larger count: %s); held-out %.2f (shared: %s; only this case: %s; only the other: %s); similarity = %.1f x %.2f + %.1f x %.2f = %.2f",
		histScore, listOrDash(histParts), heldScore, listOrNone(shared), listOrNone(onlyA), listOrNone(onlyB),
		historyShapeWeight, histScore, heldOutShapeWeight, heldScore, total)
	return total, why
}

func historyShape(a, b map[string]int) (float64, []string) {
	var sumMin, sumMax int
	var parts []string
	for _, d := range Dimensions {
		lo, hi := min(a[d], b[d]), max(a[d], b[d])
		if hi == 0 {
			continue
		}
		sumMin, sumMax = sumMin+lo, sumMax+hi
		parts = append(parts, fmt.Sprintf("%s %d/%d", d, lo, hi))
	}
	if sumMax == 0 {
		return 0, nil
	}
	return float64(sumMin) / float64(sumMax), parts
}

func heldOutShape(a, b []string) (score float64, shared, onlyA, onlyB []string) {
	inB := map[string]bool{}
	for _, d := range b {
		inB[d] = true
	}
	inA := map[string]bool{}
	for _, d := range a {
		inA[d] = true
		if inB[d] {
			shared = append(shared, d)
		} else {
			onlyA = append(onlyA, d)
		}
	}
	for _, d := range b {
		if !inA[d] {
			onlyB = append(onlyB, d)
		}
	}
	union := len(shared) + len(onlyA) + len(onlyB)
	if union == 0 {
		return 0, nil, nil, nil
	}
	return float64(len(shared)) / float64(union), shared, onlyA, onlyB
}

func listOrDash(items []string) string {
	if len(items) == 0 {
		return "none"
	}
	return strings.Join(items, ", ")
}

// attachSimilar fills every case's similar_to: the SimilarCount other ranked cases with the most similar
// signature, best first (ties go to the better-ranked case), over all ranked candidates and not only the
// ones the report lists in full. A case with nothing in common with any other has an empty list.
func attachSimilar(cases []Case) {
	for i := range cases {
		var found []Similar
		for j := range cases {
			if i == j {
				continue
			}
			score, why := Similarity(cases[i].Signature, cases[j].Signature)
			if score > 0 {
				found = append(found, Similar{Rank: cases[j].Rank, OpportunityID: cases[j].OpportunityID, Similarity: score, Explanation: why})
			}
		}
		sort.SliceStable(found, func(x, y int) bool {
			if found[x].Similarity != found[y].Similarity {
				return found[x].Similarity > found[y].Similarity
			}
			return found[x].Rank < found[y].Rank
		})
		cases[i].SimilarTo = append([]Similar{}, found[:min(SimilarCount, len(found))]...)
	}
}
