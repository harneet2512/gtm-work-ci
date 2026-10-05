// Package dealbench measures, on the CRMArena sample and with no model call, whether the reducer's state of
// a deal matches the CRM record of that deal (ADR-0016). The only claims are the deterministic CRM rules'
// (stage, owner, amount), so the check is exact and reproducible: the CRM is the answer key.
//
// Two arms fold the same claims. "Account-wide" clears every claim's opportunity_id, which is what the
// reducer did before ADR-0016: one state per account, a mixture of its deals. "Per deal" folds them as
// shipped. Each is scored against every deal's CRM owner and stage.
package dealbench

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/claims"
	"github.com/harneet2512/gtm-work/core-go/internal/crmarena"
	"github.com/harneet2512/gtm-work/core-go/internal/reducer"
)

// Score is matches over the deals that have a CRM value for the field.
type Score struct {
	Match int     `json:"match"`
	Of    int     `json:"of"`
	Rate  float64 `json:"rate"`
}

func score(match, of int) Score {
	s := Score{Match: match, Of: of}
	if of > 0 {
		s.Rate = round(float64(match)/float64(of), 4)
	}
	return s
}

func round(v float64, places int) float64 {
	scale := 1.0
	for i := 0; i < places; i++ {
		scale *= 10
	}
	return float64(int(v*scale+0.5)) / scale
}

// Report is the outcome of one measurement.
type Report struct {
	Accounts        int     `json:"accounts"`
	Deals           int     `json:"deals"`
	DealsPerAccount float64 `json:"deals_per_account"`
	Claims          int     `json:"claims"`
	OpenDeals       int     `json:"open_deals"`
	// AccountWideBefore: the one account state, scored against each of its deals (the pre-ADR-0016 reducer).
	AccountWideBefore struct {
		Owner Score `json:"owner"`
		Stage Score `json:"stage"`
	} `json:"account_wide_before"`
	// PerDealAfter: each deal's own OpportunityState, scored against that deal.
	PerDealAfter struct {
		Owner  Score `json:"owner"`
		Stage  Score `json:"stage"`
		Amount Score `json:"amount"`
	} `json:"per_deal_after"`
	// HeadlineMatchesPrimary: the AccountState's owner and stage equal the primary deal's CRM owner and stage.
	HeadlineMatchesPrimary Score `json:"account_headline_matches_its_primary_deal"`
	AccountsWithoutOpen    int   `json:"accounts_without_open_deal"`
}

type truth struct {
	owner, stage string
	amount       *float64
}

// tally counts matches per scored field.
type tally struct{ wideOwner, wideStage, owner, stage, amount, ownerOf, stageOf, amountOf, deals int }

// Measure folds the sample's CRM claims both ways and scores them against the CRM.
func Measure(snap crmarena.Snapshot) (Report, error) {
	res, err := crmarena.Build(snap)
	if err != nil {
		return Report{}, err
	}
	byAccount, acts, n, err := claimsByAccount(res)
	if err != nil {
		return Report{}, err
	}
	answers := answerKey(snap, res.Reps)
	r := Report{Claims: n}
	var t tally
	var headline, headlineOf int
	for _, acct := range sortedKeys(byAccount) {
		now := lastTime(acts[acct]).Add(time.Hour)
		wide := reducer.ReduceAll(input(acct, clearScope(byAccount[acct]), acts[acct], now)).Account
		per := reducer.ReduceAll(input(acct, byAccount[acct], acts[acct], now))
		r.Accounts++
		for _, d := range per.Opportunities {
			if want, ok := answers[d.OpportunityID]; ok {
				t.add(wide, d, want)
				r.OpenDeals += b2i(d.IsOpen)
			}
		}
		if per.Account.OpportunityID == nil {
			r.AccountsWithoutOpen++
			continue
		}
		want := answers[*per.Account.OpportunityID]
		headlineOf++
		headline += b2i(per.Account.Fields.Owner.Value == want.owner && per.Account.Fields.Stage.Value == want.stage)
	}
	r.Deals = t.deals
	r.DealsPerAccount = round(float64(r.Deals)/float64(r.Accounts), 2)
	r.AccountWideBefore.Owner, r.AccountWideBefore.Stage = score(t.wideOwner, t.ownerOf), score(t.wideStage, t.stageOf)
	r.PerDealAfter.Owner, r.PerDealAfter.Stage, r.PerDealAfter.Amount = score(t.owner, t.ownerOf), score(t.stage, t.stageOf), score(t.amount, t.amountOf)
	r.HeadlineMatchesPrimary = score(headline, headlineOf)
	return r, nil
}

func (t *tally) add(wide reducer.AccountState, d reducer.OpportunityState, want truth) {
	t.deals++
	if want.owner != "" { // a deal the CRM gives no rep owner has nothing to match
		t.ownerOf++
		t.wideOwner += b2i(wide.Fields.Owner.Value == want.owner)
		t.owner += b2i(d.Fields.Owner.Value == want.owner)
	}
	if want.stage != "" {
		t.stageOf++
		t.wideStage += b2i(wide.Fields.Stage.Value == want.stage)
		t.stage += b2i(d.Fields.Stage.Value == want.stage)
	}
	if want.amount != nil {
		got, ok := d.Fields.Amount.Value.(float64)
		t.amountOf++
		t.amount += b2i(ok && d.Fields.Amount.Known && got == *want.amount)
	}
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

// oppRef names a deal the way the reducer sees it: one id per Salesforce opportunity.
func oppRef(salesforceID string) string { return "opp:" + salesforceID }

func answerKey(snap crmarena.Snapshot, reps crmarena.Reps) map[string]truth {
	out := map[string]truth{}
	for _, o := range snap.Opportunities {
		owner, _ := reps.ByUser(o.OwnerID)
		out[oppRef(o.ID)] = truth{owner: owner, stage: o.StageName, amount: o.Amount}
	}
	return out
}

type emailDirectory struct{}

// PersonIDByEmail uses the address itself as the person id: the CRM names reps by address.
func (emailDirectory) PersonIDByEmail(_ context.Context, email string) (string, bool, error) {
	return email, email != "", nil
}

// claimsByAccount runs the CRM rules over every Opportunity event and groups the claims by account; the
// activities of each account (every event, tied to its deal when it has one) feed the reducer.
func claimsByAccount(res crmarena.Result) (map[string][]claims.Claim, map[string][]reducer.Activity, int, error) {
	byAccount := map[string][]claims.Claim{}
	acts := map[string][]reducer.Activity{}
	count := 0
	for i, e := range res.Events {
		if e.AccountID == "" {
			continue
		}
		actID, deal := fmt.Sprintf("act-%05d", i), ""
		if e.DealID != "" {
			deal = oppRef(e.DealID)
		}
		acts[e.AccountID] = append(acts[e.AccountID], reducer.Activity{ID: actID, Type: "CRMFieldChanged", OccurredAt: e.OccurredAt(), OpportunityID: deal})
		if e.Source.SourceSystem != "crm" || !isOpportunityRecord(e) {
			continue
		}
		in := claims.ActivityInput{ID: actID, AccountID: e.AccountID, OpportunityID: deal, Type: "CRMFieldChanged",
			SourceSystem: "crm", OccurredAt: e.OccurredAt(), Payload: e.Source.Payload}
		out, err := (claims.RuleExtractor{Dir: emailDirectory{}}).Extract(context.Background(), in)
		if err != nil {
			return nil, nil, 0, fmt.Errorf("dealbench: %s: %w", e.Source.SourceObjectID, err)
		}
		for _, c := range out.Claims {
			count++
			c.ID = fmt.Sprintf("claim-%05d", count)
			byAccount[e.AccountID] = append(byAccount[e.AccountID], c)
		}
	}
	return byAccount, acts, count, nil
}

func isOpportunityRecord(e crmarena.Event) bool {
	var p struct {
		ObjectType string `json:"object_type"`
	}
	return json.Unmarshal(e.Source.Payload, &p) == nil && p.ObjectType == "Opportunity"
}

// clearScope returns the claims as the reducer saw them before ADR-0016: no deal named.
func clearScope(cs []claims.Claim) []claims.Claim {
	out := make([]claims.Claim, len(cs))
	for i, c := range cs {
		c.OpportunityID = ""
		out[i] = c
	}
	return out
}

func input(acct string, cs []claims.Claim, acts []reducer.Activity, now time.Time) reducer.Input {
	return reducer.Input{AccountID: acct, Version: 1, ComputedAt: now, Adjudication: claims.Adjudicate(cs, now), Activities: acts}
}

func lastTime(acts []reducer.Activity) time.Time {
	var last time.Time
	for _, a := range acts {
		if a.OccurredAt.After(last) {
			last = a.OccurredAt
		}
	}
	return last
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
