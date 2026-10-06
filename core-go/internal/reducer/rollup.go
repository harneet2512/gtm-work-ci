package reducer

import (
	"slices"
	"sort"

	"github.com/harneet2512/gtm-work/core-go/internal/claims"
)

// reduceAccount rolls the deals up into the AccountState (ADR-0016). Its deal-scoped fields are the headline
// of the primary opportunity, falling back, field by field, to the claims that name no deal; the derived
// fields span the whole account. The buying group and coverage gaps are those of the OPEN deals plus the
// account-scoped people facts: a person known only from a closed deal drops out, so an economic buyer from an
// old closed-lost deal cannot hide the current deal's gap.
func reduceAccount(in Input, deals []OpportunityState) (AccountState, []string) {
	r, last := newRun(in, in.Adjudication, in.Activities, in.ComputedAt)
	st := AccountState{AccountID: in.AccountID, AccountName: in.AccountName, Version: in.Version, ComputedAt: in.ComputedAt.UTC(), AsOf: r.asOf.UTC()}
	if last != nil {
		st.LastActivityID = ptr(last.ID)
	}
	st.Fields = r.foldFields()
	var groupWarnings []string
	st.BuyingGroup, st.CoverageGaps, groupWarnings = openGroup(in, deals)
	r.warnings = append(r.warnings, groupWarnings...)

	primary := primaryDeal(deals)
	if primary != nil {
		st.OpportunityID = ptr(primary.OpportunityID)
		projectHeadline(&st.Fields, primary)
	}
	st.Opportunities = summaries(deals, primary)
	return st, r.warnings
}

// openGroup builds the account's buying group and coverage gaps from the account-scoped slots and the slots of
// the open deals. With no deal at all the whole adjudication is the account's (nothing is tied to a deal); with
// deals but none open there is no deal that needs a role, so there are no gaps.
func openGroup(in Input, deals []OpportunityState) (group []Member, gaps, warnings []string) {
	open := map[string]bool{}
	for _, d := range deals {
		if d.IsOpen {
			open[d.OpportunityID] = true
		}
	}
	view := claims.Adjudication{Scope: in.Adjudication.Scope}
	for _, s := range in.Adjudication.Slots {
		if s.Scope == "" || open[s.Scope] {
			view.Slots = append(view.Slots, s)
			view.Conflicts = append(view.Conflicts, s.Conflicts...)
		}
	}
	r, _ := newRun(in, view, in.Activities, in.ComputedAt)
	group, gaps = r.buyingGroup()
	if len(deals) > 0 && len(open) == 0 {
		gaps = []string{}
	}
	return group, gaps, r.warnings
}

// primaryDeal is the open deal with the latest as_of (ties: lowest id); nil when no deal is open.
func primaryDeal(deals []OpportunityState) *OpportunityState {
	var best *OpportunityState
	for i := range deals {
		d := &deals[i]
		if !d.IsOpen {
			continue
		}
		if best == nil || d.evidenceAt.After(best.evidenceAt) || (d.evidenceAt.Equal(best.evidenceAt) && d.OpportunityID < best.OpportunityID) {
			best = d
		}
	}
	return best
}

// projectHeadline replaces each claim-fed field the primary deal knows with the deal's value, naming the deal.
func projectHeadline(into *Fields, primary *OpportunityState) {
	var names []string
	for _, sf := range scalarFields {
		names = append(names, sf.name)
	}
	for _, lf := range listFields {
		names = append(names, lf.name)
	}
	for _, name := range names {
		// A deal's value wins when known; an unknown one still lends its inspectable claims (competing or
		// suggested) when the account-scoped fold has no value of its own.
		f := primary.Fields.Field(name)
		if f.Known || (!into.Field(name).Known && len(f.CompetingClaimIDs)+len(f.SuggestedClaimIDs) > 0) {
			headline := cloneField(*f)
			headline.OpportunityID = ptr(primary.OpportunityID)
			*into.Field(name) = headline
		}
	}
	if cs := primary.Fields.ChampionSince; cs != nil { // ADR-0012: the headline champion's start comes from the same deal
		headline := cloneField(*cs)
		headline.OpportunityID = ptr(primary.OpportunityID)
		into.ChampionSince = &headline
	}
}

// cloneField copies a field's slices so the account's headline and the deal's own field never share memory.
func cloneField(f Field) Field {
	f.EvidenceRefs = slices.Clone(f.EvidenceRefs)
	f.CompetingClaimIDs = slices.Clone(f.CompetingClaimIDs)
	f.SuggestedClaimIDs = slices.Clone(f.SuggestedClaimIDs)
	f.Conflicts = slices.Clone(f.Conflicts)
	if items, ok := f.Value.([]Item); ok {
		f.Value = slices.Clone(items)
	}
	return f
}

// summaries lists every deal: primary first, then open before closed, then newest first, then by id.
func summaries(deals []OpportunityState, primary *OpportunityState) []OpportunitySummary {
	out := make([]OpportunitySummary, 0, len(deals))
	for _, d := range deals {
		s := OpportunitySummary{
			OpportunityID: d.OpportunityID, IsOpen: d.IsOpen, IsPrimary: primary != nil && primary.OpportunityID == d.OpportunityID,
			Stage: textOrUnknown(d.Fields.Stage), Owner: textOrUnknown(d.Fields.Owner), Health: textOrUnknown(d.Fields.Health),
			AsOf: d.AsOf, LastActivityID: d.LastActivityID, evidenceAt: d.evidenceAt,
		}
		if v, ok := d.Fields.Amount.Value.(float64); ok && d.Fields.Amount.Known {
			s.Amount = ptr(v)
		}
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		switch {
		case a.IsPrimary != b.IsPrimary:
			return a.IsPrimary
		case a.IsOpen != b.IsOpen:
			return a.IsOpen
		case !a.evidenceAt.Equal(b.evidenceAt):
			return a.evidenceAt.After(b.evidenceAt)
		}
		return a.OpportunityID < b.OpportunityID
	})
	return out
}

func textOrUnknown(f Field) string {
	if s, ok := f.Value.(string); ok && f.Known {
		return s
	}
	return claims.Unknown
}
