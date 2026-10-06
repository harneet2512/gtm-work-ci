package crmarena

import (
	"fmt"
	"time"
)

// crmPayload is source_payloads.v1.json#/$defs/crmChange.
type crmPayload struct {
	Kind                string                 `json:"kind"`
	ObjectType          string                 `json:"object_type"`
	RecordID            string                 `json:"record_id"`
	AccountRecordID     *string                `json:"account_record_id"`
	OpportunityRecordID *string                `json:"opportunity_record_id,omitempty"`
	ChangedAt           time.Time              `json:"changed_at"`
	ChangedBy           string                 `json:"changed_by"`
	Created             bool                   `json:"created"`
	Fields              map[string]fieldChange `json:"fields,omitempty"`
	Description         *string                `json:"description,omitempty"`
}

type fieldChange struct {
	New any `json:"new"`
}

// Record ids: one namespace per object type, as the CRM fixtures use ("account:AC-4", "opp:AC-4-EXP").
func accountRef(id string) string  { return "account:" + id }
func contactRef(id string) string  { return "contact:" + id }
func oppRef(id string) string      { return "opp:" + id }
func taskRef(id string) string     { return "task:" + id }
func quoteRef(id string) string    { return "quote:" + id }
func contractRef(id string) string { return "contract:" + id }
func orderRef(id string) string    { return "order:" + id }

// fields keeps the non-empty values only: a null "new" would be a change to nothing.
func fields(kv ...any) map[string]fieldChange {
	out := map[string]fieldChange{}
	for i := 0; i+1 < len(kv); i += 2 {
		name, _ := kv[i].(string)
		switch v := kv[i+1].(type) {
		case string:
			if v != "" {
				out[name] = fieldChange{New: v}
			}
		case *float64:
			if v != nil {
				out[name] = fieldChange{New: *v}
			}
		case *int:
			if v != nil {
				out[name] = fieldChange{New: *v}
			}
		}
	}
	return out
}

func (b *builder) crm(p crmPayload, key, acct, deal string, phase int) error {
	p.Kind = "crm_change"
	if acct != "" {
		p.AccountRecordID = strPtr(accountRef(acct))
	}
	if deal != "" && p.ObjectType != "Opportunity" {
		p.OpportunityRecordID = strPtr(oppRef(deal))
	}
	return b.add("crm", p.RecordID, key, p.ChangedAt, p, acct, deal, phase)
}

// opportunities: creation at CreatedDate (or the deal's first activity, if earlier) without a stage, then
// the snapshot stage (the source has no stage history) dated at the deal's last activity, so replay
// never sees a stage before it could hold.
func (b *builder) opportunities() error {
	for _, o := range b.snap.Opportunities {
		created, err := parseDateTime("Opportunity.CreatedDate", o.CreatedDate)
		if err != nil {
			return err
		}
		if start := b.windows[o.ID].Start; start.Before(created) {
			created = start
			b.stats.DealsCreatedAtFirstActivity++
		}
		owner := b.actor(o.OwnerID)
		p := crmPayload{ObjectType: "Opportunity", RecordID: oppRef(o.ID), ChangedAt: created, ChangedBy: owner, Created: true,
			Fields: fields("Name", o.Name, "Description", o.Description)}
		if owner != IntegrationActor {
			p.Fields["OwnerEmail"] = fieldChange{New: owner}
		}
		if err := b.crm(p, "created", o.AccountID, o.ID, phaseOpportunity); err != nil {
			return err
		}
		if o.StageName == "" {
			continue
		}
		at := later(created, b.windows[o.ID].Last)
		stage := crmPayload{ObjectType: "Opportunity", RecordID: oppRef(o.ID), ChangedAt: at, ChangedBy: IntegrationActor,
			Fields: fields("StageName", o.StageName, "Amount", o.Amount, "CloseDate", o.CloseDate)}
		if err := b.crm(stage, "field:StageName:"+o.StageName, o.AccountID, o.ID, phaseSnapshot); err != nil {
			return err
		}
	}
	return nil
}

func (b *builder) tasks() error {
	for _, t := range b.snap.Tasks {
		at, err := parseDate("Task.ActivityDate", t.ActivityDate)
		if err != nil {
			return err
		}
		acct := t.AccountID
		if acct == "" {
			acct = b.idx.accountOf(t.WhatID)
		}
		p := crmPayload{ObjectType: "Task", RecordID: taskRef(t.ID), ChangedAt: at, ChangedBy: b.actor(t.OwnerID), Created: true,
			Fields:      fields("Subject", t.Subject, "Priority", t.Priority, "Type", t.Type, "ActivityDate", t.ActivityDate),
			Description: strPtr(t.Description)}
		if err := b.crm(p, "created", acct, t.WhatID, phaseActivity); err != nil {
			return err
		}
	}
	return nil
}

// quotes: creation at CreatedDate; the final Status (no status history) at the deal's last activity.
func (b *builder) quotes() error {
	for _, q := range b.snap.Quotes {
		created, err := parseDateTime("Quote.CreatedDate", q.CreatedDate)
		if err != nil {
			return err
		}
		acct := q.AccountID
		if acct == "" {
			acct = b.idx.accountOf(q.OpportunityID)
		}
		p := crmPayload{ObjectType: "Quote", RecordID: quoteRef(q.ID), ChangedAt: created, ChangedBy: IntegrationActor, Created: true,
			Fields: fields("Name", q.Name, "QuoteNumber", q.QuoteNumber, "GrandTotal", q.GrandTotal, "Discount", q.Discount,
				"ExpirationDate", q.ExpirationDate),
			Description: strPtr(q.Description)}
		if err := b.crm(p, "created", acct, q.OpportunityID, phaseActivity); err != nil {
			return err
		}
		if q.Status == "" {
			continue
		}
		status := crmPayload{ObjectType: "Quote", RecordID: quoteRef(q.ID), ChangedAt: later(created, b.windows[q.OpportunityID].Last),
			ChangedBy: IntegrationActor, Fields: fields("Status", q.Status)}
		if err := b.crm(status, "field:Status:"+q.Status, acct, q.OpportunityID, phaseSnapshot); err != nil {
			return err
		}
	}
	return nil
}

// contracts are signed when both parties have signed; the opportunity is named by ContractId__c.
func (b *builder) contracts() error {
	for _, c := range b.snap.Contracts {
		at, err := signedAt(c)
		if err != nil {
			return err
		}
		p := crmPayload{ObjectType: "Contract", RecordID: contractRef(c.ID), ChangedAt: at, ChangedBy: IntegrationActor, Created: true,
			Fields: fields("ContractNumber", c.ContractNumber, "StartDate", c.StartDate, "EndDate", c.EndDate,
				"ContractTerm", c.ContractTerm, "Status", c.Status),
			Description: strPtr(c.Description)}
		if err := b.crm(p, "created", c.AccountID, b.idx.contractOpp[c.ID], phaseActivity); err != nil {
			return err
		}
	}
	return nil
}

func signedAt(c Contract) (time.Time, error) {
	var at time.Time
	for _, d := range []struct{ name, v string }{{"CustomerSignedDate", c.CustomerSignedDate}, {"CompanySignedDate", c.CompanySignedDate}} {
		if d.v == "" {
			continue
		}
		t, err := parseDate("Contract."+d.name, d.v)
		if err != nil {
			return time.Time{}, err
		}
		at = later(at, t)
	}
	if at.IsZero() {
		return parseDate("Contract.StartDate", c.StartDate)
	}
	return at, nil
}

// orders are account-level: the source links them to no opportunity.
func (b *builder) orders() error {
	for _, o := range b.snap.Orders {
		at, err := parseDate("Order.EffectiveDate", o.EffectiveDate)
		if err != nil {
			return err
		}
		p := crmPayload{ObjectType: "Order", RecordID: orderRef(o.ID), ChangedAt: at, ChangedBy: b.actor(o.OwnerID), Created: true,
			Fields: fields("OrderNumber", o.OrderNumber, "Status", o.Status, "EffectiveDate", o.EffectiveDate)}
		if err := b.crm(p, "created", o.AccountID, "", phaseActivity); err != nil {
			return err
		}
	}
	return nil
}

// masterData creates accounts at the account's first dated record and each contact at its own first
// appearance (contactAppearances): the source's CreatedDate on these objects is the org's load date
// (2025), after every activity. A contact that appears nowhere falls back to its account's first record.
func (b *builder) masterData() error {
	first := map[string]time.Time{}
	var global time.Time
	for _, e := range b.events {
		at := e.OccurredAt()
		if t, ok := first[e.AccountID]; !ok || at.Before(t) {
			first[e.AccountID] = at
		}
		if global.IsZero() || at.Before(global) {
			global = at
		}
	}
	for _, a := range b.snap.Accounts {
		at, ok := first[a.ID]
		if !ok {
			at = global
		}
		if err := b.account(a, at); err != nil {
			return err
		}
	}
	appeared := b.contactAppearances()
	for _, c := range b.snap.Contacts {
		at, deal := global, ""
		if t, ok := first[c.AccountID]; ok {
			at = t
		}
		if ap, ok := appeared[c.ID]; ok {
			at, deal = ap.at, ap.deal
		}
		p := crmPayload{ObjectType: "Contact", RecordID: contactRef(c.ID), ChangedAt: at, ChangedBy: IntegrationActor, Created: true,
			Fields: fields("FirstName", c.FirstName, "LastName", c.LastName, "Email", c.Email, "Title", c.Title, "Department", c.Department)}
		if err := b.crm(p, "created", c.AccountID, "", phaseContact); err != nil {
			return err
		}
		b.events[len(b.events)-1].introducedBy = deal
	}
	return nil
}

// account: the source has no Website, so the domain is the one email domain all its contacts share
// (derived, counted in Stats); without it emails could not be resolved to the account.
func (b *builder) account(a Account, at time.Time) error {
	if at.IsZero() {
		return fmt.Errorf("crmarena: account %s has no dated record and the snapshot has none either", a.ID)
	}
	f := fields("Name", a.Name, "Industry", a.Industry, "NumberOfEmployees", a.NumberOfEmployees, "Description", a.Description)
	if d := b.idx.domains[a.ID]; d != "" {
		f["Domain"] = fieldChange{New: d}
		b.stats.DerivedDomains++
	} else {
		b.stats.AccountsWithoutDomain++
	}
	p := crmPayload{ObjectType: "Account", RecordID: accountRef(a.ID), ChangedAt: at, ChangedBy: IntegrationActor, Created: true, Fields: f}
	return b.crm(p, "created", a.ID, "", phaseAccount)
}
