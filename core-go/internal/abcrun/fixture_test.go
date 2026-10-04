package abcrun_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/abcrun"
	"github.com/harneet2512/gtm-work/core-go/internal/claims"
	"github.com/harneet2512/gtm-work/core-go/internal/graph"
	"github.com/harneet2512/gtm-work/core-go/internal/normalize"
	"github.com/harneet2512/gtm-work/core-go/internal/store/storetest"
)

var env *storetest.Env

func TestMain(m *testing.M) { os.Exit(storetest.Main(m, func(e *storetest.Env) { env = e })) }

var bg = context.Background()

// purgeSQL empties every table a built world writes, so a test can build worlds again.
const purgeSQL = `TRUNCATE accounts, people, products, source_events, knowledge, trigger_evaluations CASCADE`

func purge(t testing.TB) {
	t.Helper()
	tx, err := env.DB.BeginTx(bg, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	for _, q := range []string{`SET LOCAL ghost.purge_transitions = 'on'`, `SET LOCAL ghost.purge_knowledge = 'on'`,
		`DELETE FROM knowledge_status_history`, purgeSQL} {
		if _, err := tx.ExecContext(bg, q); err != nil {
			t.Fatalf("purge: %v", err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

var (
	t0      = time.Date(2023, 11, 20, 9, 0, 0, 0, time.UTC)
	trigger = t0.Add(48 * time.Hour)
)

func at(d time.Duration) *time.Time { v := t0.Add(d); return &v }

func crm(sfx, object, id, key string, when *time.Time, created bool, fields map[string]any) normalize.SourceEvent {
	f := map[string]any{}
	for k, v := range fields {
		f[k] = map[string]any{"new": v}
	}
	payload, _ := json.Marshal(map[string]any{"kind": "crm_change", "object_type": object, "record_id": id, "account_record_id": "account:001ORBIT" + sfx,
		"changed_at": when.Format(time.RFC3339), "changed_by": "integration:crmarena", "created": created, "fields": f})
	return normalize.SourceEvent{SourceSystem: "crm", SourceObjectID: id, SourceEventKey: key, OccurredAt: when, Connector: "crmarena-loader",
		ConnectorVersion: "wp31-v1", Payload: payload}
}

func email(sfx, id string, when *time.Time, inbound bool, from, to, body string) normalize.SourceEvent {
	dir, key := "outbound", "sent"
	if inbound {
		dir, key = "inbound", "received"
	}
	payload, _ := json.Marshal(map[string]any{"kind": "email", "message_id": id, "thread_id": "opp:006D1" + sfx, "in_reply_to": nil, "direction": dir,
		"from": map[string]any{"email": from, "name": from}, "to": []map[string]any{{"email": to, "name": to}}, "date": when.Format(time.RFC3339),
		"subject": "Re: proposal", "body_text": body, "crm_opportunity_ref": "opp:006D1" + sfx})
	return normalize.SourceEvent{SourceSystem: "email", SourceObjectID: id, SourceEventKey: key, OccurredAt: when, Connector: "crmarena-loader",
		ConnectorVersion: "wp31-v1", Payload: payload}
}

const priceBody = "Hi Sam,\n\nThank you for the proposal. I have to be candid that the pricing for Orbit Suite is higher than what we had planned for this year, and it will be difficult to justify.\n\nBest, Dana"

func company() graph.Company {
	people := []graph.CompanyPerson{
		{Key: "u1", Kind: "employee", DisplayName: "Sam Rep", Email: "sam.rep@ghostvendor.com"},
		{Key: "u2", Kind: "employee", DisplayName: "Priya Colleague", Email: "priya.colleague@ghostvendor.com"},
	}
	return graph.Company{Organization: graph.CompanyInfo{Name: "Vendor", Domain: "ghostvendor.com"}, People: people}
}

// worldOf is a small world: an account, a finance contact, an opportunity at the given stage and a price-worded objection.
func worldOf(id, kind, sfx, stage string) abcrun.Situation {
	buyer := "dana.buyer" + sfx + "@orbit" + sfx + ".example"
	events := []normalize.SourceEvent{
		crm(sfx, "Account", "account:001ORBIT"+sfx, "created", at(-24*time.Hour*30), true, map[string]any{"Name": "Orbit Dynamics " + sfx, "Domain": "orbit" + sfx + ".example"}),
		crm(sfx, "Contact", "contact:003DANA"+sfx, "created", at(-24*time.Hour*30), true, map[string]any{"FirstName": "Dana", "LastName": "Buyer",
			"Email": buyer, "Title": "Finance Manager", "Department": "Finance"}),
		crm(sfx, "Opportunity", "opp:006D1"+sfx, "created", at(-24*time.Hour*20), true, map[string]any{"Name": "Orbit Suite", "OwnerEmail": "sam.rep@ghostvendor.com"}),
		crm(sfx, "Opportunity", "opp:006D1"+sfx, "field:StageName:"+stage, at(24*time.Hour), false, map[string]any{"StageName": stage}),
		email(sfx, "02sPRICE"+sfx, at(48*time.Hour), true, buyer, "sam.rep@ghostvendor.com", priceBody),
	}
	return abcrun.Situation{
		ID: id, Kind: kind, DecisionPoint: "price_pushback", DealID: "006D1" + sfx, AccountID: "001ORBIT" + sfx, SelectionReason: "fixture",
		Trigger: abcrun.Trigger{SourceObjectID: "02sPRICE" + sfx, SourceEventKey: "received", OccurredAt: trigger},
		Events:  events, Truth: map[string][]claims.Candidate{"02sPRICE" + sfx: abcrun.TruthFromBody(priceBody)},
		People: []abcrun.Person{{Email: buyer, Name: "Dana Buyer", Title: "Finance Manager", Side: "buyer"}},
	}
}

// situation is in Quote with a pricing objection.
func situation(id, kind string) abcrun.Situation { return worldOf(id, kind, "", "Quote") }

// pack numbers its situations S-1...; the first is the base world, later exceptions are the same words one stage earlier.
func pack(kinds ...string) abcrun.Pack {
	p := abcrun.Pack{Version: abcrun.PackVersion, Company: company()}
	for i, k := range kinds {
		id := fmt.Sprintf("S-%d", i+1)
		switch {
		case i == 0:
			p.Situations = append(p.Situations, situation(id, k))
		case k == "exception" || k == "control":
			p.Situations = append(p.Situations, worldOf(id, k, fmt.Sprint(i+1), "Qualification"))
		default:
			p.Situations = append(p.Situations, worldOf(id, k, fmt.Sprint(i+1), "Quote"))
		}
	}
	return p
}
