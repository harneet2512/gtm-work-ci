package deterministic

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/reducer"
)

// movedOpenlyRE is wording, in the sentence that carries the new date, that tells the customer the
// date moves. Generic politeness ("sorry for the slow reply", "without delay") does not count.
var movedOpenlyRE = regexp.MustCompile(`(?i)\b(push(?:ing|ed)? (?:it |this |that |the [a-z ]{1,30})?(?:back|out)|reschedul\w*|postpon\w*|slip(?:s|ped|ping)?|moving (?:it|this|that) (?:to|out|back)|need(?:s)? (?:a bit |a little )?more time|new date|revised date|(?:is|are|has been|have been|will be) (?:delayed|moved|pushed))\b`)

// A commitment is "about" a clause only when the clause repeats its object (the asset or topic:
// "security questionnaire"), not just its verb. A full match of the whole object blocks; a partial
// match (one or two shared object words) that fails without blocking and is left to the semantic
// commitment_consistency judge (deterministic evals block only on high-precision matches).
var commitmentVerbs = toSet(strings.Fields(`send share review provide deliver schedule set book get give prepare confirm
follow update finish complete submit book make arrange organise organize circulate`))

type aboutness int

const (
	notAbout aboutness = iota
	weakAbout
	strongAbout
)

// objectTokens are the content words of a commitment minus its leading verb ("Book the security
// review call" keeps "review" and "call": they are its object).
func objectTokens(commitment string) []string {
	toks := contentTokens(commitment, nil)
	if len(toks) > 0 && commitmentVerbs[toks[0]] {
		toks = toks[1:]
	}
	return toks
}

func aboutCommitment(commitment, text string) aboutness {
	obj := objectTokens(commitment)
	if len(obj) == 0 {
		return notAbout
	}
	shared := overlap(obj, contentTokens(text, nil))
	switch {
	case shared == 0:
		return notAbout
	case shared == len(toSet(obj)):
		return strongAbout
	}
	return weakAbout
}

// CommitmentNotContradicted: the draft does not wait past, or move out, a dated commitment our
// side already made (state current_commitments owned by one of our employees).
func CommitmentNotContradicted(in Input) []Finding {
	var out []Finding
	items, err := ourOpenCommitments(in)
	if err != nil {
		out = append(out, failure(CheckCommitmentHonored, fmt.Sprintf("the current_commitments state is unreadable: %v", err),
			"Repair the account state before the draft relies on it.").blocking().withState("current_commitments"))
	}
	for _, it := range items {
		out = append(out, waitsPast(in, it)...)
		out = append(out, movesOut(in, it)...)
	}
	return out
}

func waitsPast(in Input, it reducer.Item) []Finding {
	if !isPassiveAction(in.Draft.ProposedActionType) {
		return nil
	}
	until := in.EvaluatedAt
	if in.Draft.WaitUntil != nil && in.Draft.WaitUntil.After(until) {
		until = *in.Draft.WaitUntil
	}
	if !it.DueAt.Before(until) {
		return nil
	}
	return []Finding{failure(CheckCommitmentHonored,
		fmt.Sprintf("our commitment %q comes due %s while the draft waits until %s", it.Text, it.DueAt.Format(day), until.Format(day)),
		fmt.Sprintf("Fulfil %q (or tell the customer why it moves) instead of waiting.", it.Text)).
		blocking().withState("current_commitments")}
}

// movesOut: a forward-looking clause or the CRM next step that is about the commitment sets a later
// date, and the sentence does not say that the date moves.
func movesOut(in Input, it reducer.Item) []Finding {
	committed := civilDay(*it.DueAt)
	type move struct {
		day time.Time
		how aboutness
	}
	var later []move
	for _, d := range draftDates(in) {
		if how := aboutCommitment(it.Text, d.Clause); how != notAbout && futureRE.MatchString(d.Clause) &&
			d.Day.After(committed) && !movedOpenlyRE.MatchString(sentenceContaining(draftText(in.Draft), d.Clause)) {
			later = append(later, move{d.Day, how})
		}
	}
	c := in.Draft.CRMNextStepIntent
	if how := aboutCommitment(it.Text, c.NextStep); c.DueAt != nil && how != notAbout && civilDay(*c.DueAt).After(committed) &&
		!movedOpenlyRE.MatchString(c.NextStep) {
		later = append(later, move{civilDay(*c.DueAt), how})
	}
	var out []Finding
	for _, m := range later {
		f := failure(CheckCommitmentHonored,
			fmt.Sprintf("moves our commitment %q from %s to %s", it.Text, committed.Format(day), m.day.Format(day)),
			fmt.Sprintf("Keep the committed date %s for %q, or say explicitly that it moves.", committed.Format(day), it.Text)).
			withState("current_commitments")
		if m.how == strongAbout {
			f = f.blocking()
		}
		out = append(out, f)
	}
	return out
}

func overlap(a, b []string) int {
	bs := toSet(b)
	n := 0
	for t := range toSet(a) {
		if bs[t] {
			n++
		}
	}
	return n
}

var sentenceSplitRE = regexp.MustCompile(`[.!?
]+`)

// sentenceContaining is the sentence of text that holds clause; the clause itself when none does.
func sentenceContaining(text, clause string) string {
	for _, sn := range sentenceSplitRE.Split(text, -1) {
		if strings.Contains(sn, clause) {
			return sn
		}
	}
	return clause
}
