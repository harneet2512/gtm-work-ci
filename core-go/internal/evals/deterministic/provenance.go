package deterministic

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/reducer"
)

// HAR-97 §4 "Provenance / source coverage" sub-check.
const CheckCriticalStatementsSupported Check = "provenance.critical_statements_supported"

var provenanceChecks = []Check{CheckCriticalStatementsSupported}

// proposalRE marks a date the draft proposes rather than asserts; a proposal is not a fact to source.
var proposalRE = regexp.MustCompile(`(?i)\b(propose|how about|could we|can we|let's|would .{0,30} work|are you (?:free|available)|works for|suggest)\b`)

// ProvenanceCoverage is the HAR-97 §4 "Provenance / source coverage" eval.
func ProvenanceCoverage(in Input) Judgment {
	f := CriticalStatementsSupported(in)
	return judge(in, TypeProvenance, provenanceChecks, []string{"commercial_issue", "current_commitments"}, f)
}

// facts are the critical statements of a text: figures, dates and titles.
type facts struct {
	amounts     []float64
	percents    []float64
	percentsAll []float64 // every percentage of the evidence, requests included
	quantities  []Quantity
	days        map[time.Time]bool
	roles       map[string]bool
}

func factsOf(text string, now time.Time) facts {
	f := facts{days: map[time.Time]bool{}, roles: map[string]bool{}}
	for _, a := range moneyAmounts(text) {
		f.amounts = append(f.amounts, a.Value)
	}
	f.percents = percents(text)
	f.quantities = quantities(text)
	for _, d := range findDates(text, now) {
		f.days[d.Day] = true
	}
	for _, r := range roles(text) {
		f.roles[strings.ToLower(r)] = true
	}
	return f
}

// sourceFacts are the facts the draft's cited evidence, the states and the quotes establish.
func sourceFacts(in Input) facts {
	f := factsOf(citedText(in), in.EvaluatedAt)
	f.percents = supportPercents(citedText(in), catalogProducts(in)) // a requested discount is not approval
	f.percentsAll = append(percents(citedText(in)), in.Commercial.ApprovedDiscountPercents...)
	f.amounts = append(f.amounts, supportedQuotedAmounts(in)...)
	f.percents = append(f.percents, in.Commercial.ApprovedDiscountPercents...)
	for _, q := range in.Commercial.Quoted {
		if q.Quantity != nil {
			unit := ""
			if q.Unit != nil {
				unit = singular(*q.Unit)
			}
			f.quantities = append(f.quantities, Quantity{Value: *q.Quantity, Unit: unit})
		}
	}
	booked, _ := meetingDays(in) // an unreadable calendar supports nothing
	for d := range booked {
		f.days[d] = true
	}
	for _, it := range append(listItems(in.State.Fields.CurrentCommitments), listItems(latestState(in).Fields.CurrentCommitments)...) {
		if it.DueAt != nil {
			f.days[civilDay(*it.DueAt)] = true
		}
	}
	return f
}

func supportedQuotedAmounts(in Input) []float64 {
	var out []float64
	for _, q := range in.Commercial.Quoted {
		for _, v := range []*float64{q.UnitPrice, q.Total} {
			if v != nil {
				out = append(out, *v)
			}
		}
		if q.UnitPrice != nil && q.Quantity != nil {
			out = append(out, *q.UnitPrice**q.Quantity)
		}
	}
	return out
}

// citedText is the text of the activities the draft cites, its quotes, and the known,
// evidence-backed state fields.
func citedText(in Input) string {
	acts := activityIndex(in)
	var parts []string
	for _, r := range in.Draft.EvidenceRefs {
		if a, ok := acts[r.ActivityID]; ok && (a.AccountID == nil || *a.AccountID == in.AccountID) {
			parts = append(parts, a.Text)
		}
		parts = append(parts, r.Quote)
	}
	for _, s := range []int{0, 1} {
		st := in.State
		if s == 1 {
			st = latestState(in)
		}
		for _, name := range reducer.FieldNames() {
			fld := st.Fields.Field(name)
			if fld.Known && (fld.WinningClaimID != nil || len(fld.EvidenceRefs) > 0) {
				parts = append(parts, fieldText(*fld))
			}
		}
	}
	return strings.Join(parts, "\n")
}

// An unsupported price or percentage is an invented figure and blocks; an unsupported quantity,
// date or title is a coverage gap (the gold marks it fail, non-blocking: the semantic evals and the
// reviewer see it). A fabricated citation (a ref that points nowhere or quotes
// what the activity does not say) blocks.
// CriticalStatementsSupported: every evidence ref resolves to an activity of this account that
// contains its quote, and every price, percentage, quantity, asserted date and job title the draft
// states appears in the cited evidence, the state or the quote on file.
func CriticalStatementsSupported(in Input) []Finding {
	out := unresolvedRefs(in)
	if isPassiveAction(in.Draft.ProposedActionType) {
		return out
	}
	src := sourceFacts(in)
	draft := factsOf(draftText(in.Draft), in.EvaluatedAt)
	plainPercents, offerPercents := draftPercentChecks(draftText(in.Draft), catalogProducts(in))
	unsupported := func(what, text string, blocks bool) {
		f := failure(CheckCriticalStatementsSupported,
			fmt.Sprintf("states %s %s with no supporting state or evidence ref", what, text),
			fmt.Sprintf("Cite the evidence for %s, or remove the statement.", text))
		if blocks {
			f = f.blocking()
		}
		out = append(out, f)
	}
	for _, v := range draft.amounts {
		if !hasValue(src.amounts, v) {
			unsupported("the amount", fmt.Sprintf("%g", v), true)
		}
	}
	for _, v := range plainPercents {
		if !hasValue(src.percentsAll, v) {
			unsupported("the percentage", fmt.Sprintf("%g%%", v), true)
		}
	}
	for _, v := range offerPercents {
		if !hasValue(src.percents, v) {
			unsupported("the percentage", fmt.Sprintf("%g%%", v), true)
		}
	}
	for _, q := range draft.quantities {
		if !quantitySupported(src.quantities, q) {
			unsupported("the quantity", fmt.Sprintf("%q", q.Text), false)
		}
	}
	for _, d := range assertedDays(in) {
		if !src.days[d.Day] {
			unsupported("the date", d.Day.Format(day), false)
		}
	}
	for r := range draft.roles {
		if !src.roles[r] {
			unsupported("the title", r, false)
		}
	}
	return out
}

// assertedDays are dates the draft states as upcoming facts: not proposals, and not reports of
// something that already happened (those are checkable against the activities themselves).
func assertedDays(in Input) []clauseDate {
	var out []clauseDate
	for _, d := range draftDates(in) {
		if !proposalRE.MatchString(d.Clause) && !d.pastContext() {
			out = append(out, d)
		}
	}
	return out
}

// unresolvedRefs finds evidence refs that point nowhere or quote what the activity does not say.
func unresolvedRefs(in Input) []Finding {
	acts := activityIndex(in)
	var out []Finding
	for _, r := range in.Draft.EvidenceRefs {
		a, ok := acts[r.ActivityID]
		switch {
		case !ok:
			out = append(out, failure(CheckCriticalStatementsSupported,
				fmt.Sprintf("evidence ref %s is not an activity the run read", r.ActivityID),
				"Cite only activities from the run's context.").blocking().withEvidence(r))
		case a.AccountID != nil && *a.AccountID != in.AccountID:
			out = append(out, failure(CheckCriticalStatementsSupported,
				fmt.Sprintf("evidence ref %s belongs to another account", r.ActivityID),
				"Cite only this account's activities.").blocking().withEvidence(r))
		case r.Quote != "" && !containsText(a.Text, r.Quote):
			out = append(out, failure(CheckCriticalStatementsSupported,
				fmt.Sprintf("the quote in evidence ref %s does not occur in the activity", r.ActivityID),
				"Quote the activity verbatim or drop the quote.").blocking().withEvidence(r))
		}
	}
	return out
}
