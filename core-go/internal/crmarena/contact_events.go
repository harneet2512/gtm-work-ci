package crmarena

import (
	"strings"
	"time"
)

// appearance is where and when a contact first shows up in the records.
type appearance struct {
	at   time.Time
	deal string // the deal whose email names the contact; "" for a case or a contact role
}

// contactAppearances dates each contact at its first appearance: the earliest record that names it.
//
//   - an email naming its address (from, to or cc);
//   - a quote naming it as Quote.ContactId, at the quote's CreatedDate (filled on every quote);
//   - a task naming it as WhoId, at the task's day; an order naming it as BillToContactId or
//     ShipToContactId, at the order's effective date (both empty in the current snapshot);
//   - a support case naming it, at the case's CreatedDate.
//
// A contact that appears in none of these falls back to the start of the earliest deal it holds a
// contact role on (the roles carry only the 2025 load date, so they cannot place a contact in time any
// other way), and failing that to its account's first record (masterData). Contacts are not created at
// their account's first record: that would put stakeholders who only join a deal later in the store from
// the start, so "new stakeholder entered" could never fire on replay, and a contact already named on a
// quote would be delivered as new.
func (b *builder) contactAppearances() map[string]appearance {
	byEmail := map[string]string{}
	for _, c := range b.snap.Contacts {
		byEmail[strings.ToLower(strings.TrimSpace(c.Email))] = c.ID
	}
	out := map[string]appearance{}
	seen := func(contact string, at time.Time, deal string) {
		if contact == "" {
			return
		}
		if cur, ok := out[contact]; !ok || at.Before(cur.at) {
			out[contact] = appearance{at: at, deal: deal}
		}
	}
	b.emailAppearances(byEmail, seen)
	b.recordAppearances(seen)
	b.roleFallback(out)
	return out
}

type seenFn func(contact string, at time.Time, deal string)

func (b *builder) emailAppearances(byEmail map[string]string, seen seenFn) {
	for _, e := range b.snap.Emails {
		at, err := parseDateTime("EmailMessage.MessageDate", e.MessageDate)
		if err != nil {
			continue // Build has failed on this record already
		}
		for _, list := range []string{e.FromAddress, e.ToAddress, e.CcAddress} {
			for _, addr := range splitAddresses(list) {
				seen(byEmail[addr], at, e.RelatedToID)
			}
		}
	}
}

func (b *builder) recordAppearances(seen seenFn) {
	for _, q := range b.snap.Quotes {
		if at, err := parseDateTime("Quote.CreatedDate", q.CreatedDate); err == nil {
			seen(q.ContactID, at, q.OpportunityID)
		}
	}
	for _, t := range b.snap.Tasks {
		if at, err := parseDate("Task.ActivityDate", t.ActivityDate); err == nil {
			seen(t.WhoID, at, t.WhatID)
		}
	}
	for _, o := range b.snap.Orders {
		if at, err := parseDate("Order.EffectiveDate", o.EffectiveDate); err == nil {
			seen(o.BillToContact, at, "")
			seen(o.ShipToContact, at, "")
		}
	}
	for _, c := range b.snap.Cases {
		if at, err := parseDateTime("Case.CreatedDate", c.CreatedDate); err == nil {
			seen(c.ContactID, at, "")
		}
	}
}

// roleFallback places contacts that appear nowhere else at the start of the earliest deal they hold a
// role on (not the first role in file order).
func (b *builder) roleFallback(out map[string]appearance) {
	fallback := map[string]appearance{}
	for _, r := range b.snap.Roles {
		if _, ok := out[r.ContactID]; ok {
			continue
		}
		w, ok := b.windows[r.OpportunityID]
		if !ok || w.Start.IsZero() {
			continue
		}
		if cur, ok := fallback[r.ContactID]; !ok || w.Start.Before(cur.at) || (w.Start.Equal(cur.at) && r.OpportunityID < cur.deal) {
			fallback[r.ContactID] = appearance{at: w.Start, deal: r.OpportunityID}
		}
	}
	for id, ap := range fallback {
		out[id] = ap
	}
}
