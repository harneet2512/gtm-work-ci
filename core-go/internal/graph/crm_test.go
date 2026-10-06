package graph_test

import (
	"testing"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/graph"
)

// fullOrg is Dana (owner), Leo (SE) and their manager Rachel.
func fullOrg() graph.Company {
	org := smallOrg()
	mgr := "person:rachel_ortiz"
	org.People = append(org.People, graph.CompanyPerson{Key: "person:leo_park", Kind: "employee", DisplayName: "Leo Park",
		Email: "leo@ghostvendor.com", Title: "SE", SlackUser: "U02LEO", ManagerKey: &mgr})
	return org
}

func seedOrg(t *testing.T) {
	t.Helper()
	if _, err := graph.SeedCompany(ctx, env.DB, fullOrg(), t0.Add(-time.Hour)); err != nil {
		t.Fatalf("seed org: %v", err)
	}
}

func personByEmail(t *testing.T, email string) string {
	t.Helper()
	return str(t, `SELECT entity_id::text FROM entity_source_mappings WHERE source_system = 'email' AND source_key = $1 AND valid_to IS NULL`, email)
}

func accountByCRM(t *testing.T, record string) string {
	t.Helper()
	return str(t, `SELECT entity_id::text FROM entity_source_mappings WHERE entity_type = 'account' AND source_system = 'crm' AND source_key = $1 AND valid_to IS NULL`, record)
}

func oppByCRM(t *testing.T, record string) string {
	t.Helper()
	return str(t, `SELECT entity_id::text FROM entity_source_mappings WHERE entity_type = 'opportunity' AND source_system = 'crm' AND source_key = $1 AND valid_to IS NULL`, record)
}

// openEdge counts open edges of a type between two entities.
func openEdge(t *testing.T, srcID, rel, dstID string) int {
	t.Helper()
	return num(t, `SELECT count(*) FROM relationships WHERE src_id = $1::uuid AND rel_type = $2 AND dst_id = $3::uuid AND valid_to IS NULL`, srcID, rel, dstID)
}

func TestCRMAccountCreatedCreatesTheAccountItsMappingsAndResolvesTheActivity(t *testing.T) {
	resetDB(t)
	svc := service(t)

	res, err := svc.Ingest(ctx, crmAccount(t, "account:AC-4", "Acme Corp", "https://www.Acme.com/"))
	if err != nil {
		t.Fatal(err)
	}

	acct := accountByCRM(t, "account:AC-4")
	if acct == "" {
		t.Fatal("no crm mapping for the account")
	}
	if res.AccountID == nil || *res.AccountID != acct {
		t.Errorf("activity resolved to %v, want the new account %s", res.AccountID, acct)
	}
	if got := str(t, `SELECT name || '|' || domain FROM accounts WHERE id = $1::uuid`, acct); got != "Acme Corp|acme.com" {
		t.Errorf("account row = %s", got)
	}
	for _, m := range []struct{ system, key, method string }{
		{"crm", "account:AC-4", "exact"}, {"domain", "acme.com", "exact"}, {"slack", "#deal-acme", "rule"},
	} {
		got := str(t, `SELECT method || '|' || coalesce(evidence_activity_id::text, '') FROM entity_source_mappings
			WHERE entity_id = $1::uuid AND source_system = $2 AND source_key = $3 AND valid_to IS NULL`, acct, m.system, m.key)
		if want := m.method + "|" + res.ActivityID; got != want {
			t.Errorf("mapping %s/%s = %q, want %q (method and evidence activity)", m.system, m.key, got, want)
		}
	}
}

func TestCRMAccountCreatedReusesAnAccountThatAlreadyHasThatDomain(t *testing.T) {
	resetDB(t)
	existing := newAccount(t, "Acme (manual)", "acme.com")

	ingestAll(t, service(t), crmAccount(t, "account:AC-4", "Acme Corp", "acme.com"))

	if got := accountByCRM(t, "account:AC-4"); got != existing {
		t.Errorf("crm mapping points at %s, want the pre-existing account %s", got, existing)
	}
	if n := num(t, `SELECT count(*) FROM accounts`); n != 1 {
		t.Errorf("%d accounts, want 1", n)
	}
}

func TestCRMAccountWithoutAUsableWebsiteStillGetsAnAccount(t *testing.T) {
	for name, website := range map[string]string{"missing": "", "our own domain": "ghostvendor.com", "not a host": "n/a"} {
		t.Run(name, func(t *testing.T) {
			resetDB(t)
			ingestAll(t, service(t), crmAccount(t, "account:X-1", "Xeno Labs", website))

			acct := accountByCRM(t, "account:X-1")
			if acct == "" {
				t.Fatal("account not created")
			}
			if got := str(t, `SELECT coalesce(domain, 'none') FROM accounts WHERE id = $1::uuid`, acct); got != "none" {
				t.Errorf("domain = %s, want none", got)
			}
			if got := str(t, `SELECT source_key FROM entity_source_mappings WHERE entity_id = $1::uuid AND source_system = 'slack'`, acct); got != "#deal-xeno" {
				t.Errorf("slack channel mapping = %q, want one derived from the name", got)
			}
		})
	}
}

func TestCRMOpportunityCreatedCreatesTheOpportunityOwnerAndEdges(t *testing.T) {
	resetDB(t)
	seedOrg(t)
	svc := service(t)
	ingestAll(t, svc, crmAccount(t, "account:AC-4", "Acme Corp", "acme.com"))

	res, err := svc.Ingest(ctx, crmOpp(t, "opp:AC-4-EXP", "account:AC-4", "dana@ghostvendor.com"))
	if err != nil {
		t.Fatal(err)
	}

	acct, op := accountByCRM(t, "account:AC-4"), oppByCRM(t, "opp:AC-4-EXP")
	if op == "" {
		t.Fatal("opportunity not created")
	}
	dana := personByEmail(t, "dana@ghostvendor.com")
	if got := str(t, `SELECT account_id::text || '|' || motion || '|' || owner_person_id::text FROM opportunities WHERE id = $1::uuid`, op); got != acct+"|expansion|"+dana {
		t.Errorf("opportunity row = %s", got)
	}
	if res.AccountID == nil || *res.AccountID != acct {
		t.Errorf("opportunity activity resolved to %v", res.AccountID)
	}
	if got := str(t, `SELECT opportunity_id::text FROM activities WHERE id = $1::uuid`, res.ActivityID); got != op {
		t.Errorf("activity opportunity = %s, want %s", got, op)
	}
	if openEdge(t, op, "belongs_to", acct) != 1 {
		t.Error("missing belongs_to edge opportunity -> account")
	}
	if openEdge(t, dana, "owns", op) != 1 {
		t.Error("missing owns edge owner -> opportunity")
	}
	if got := str(t, `SELECT standing || '|' || source_activity_id::text FROM relationships WHERE rel_type = 'owns'`); got != "crm_explicit|"+res.ActivityID {
		t.Errorf("owns edge standing/source = %s", got)
	}
}

func TestCRMOpportunityMotionsAndUnknownOwner(t *testing.T) {
	for typ, want := range map[string]string{"Expansion": "expansion", "Renewal": "renewal", "New Business": "new_business", "": "new_business"} {
		t.Run(typ, func(t *testing.T) {
			resetDB(t) // org not seeded: the owner is unknown
			svc := service(t)
			ingestAll(t, svc, crmAccount(t, "account:AC-4", "Acme Corp", "acme.com"))
			ev := crmChange(t, "Opportunity", "opp:1", "account:AC-4", true, t0, fields{"Name": "Deal", "Type": typ, "StageName": "Discovery", "OwnerEmail": "ghost@ghostvendor.com"})

			ingestAll(t, svc, ev)

			if got := str(t, `SELECT motion || '|' || coalesce(owner_person_id::text, 'none') FROM opportunities`); got != want+"|none" {
				t.Errorf("opportunity = %s, want %s|none", got, want)
			}
			if n := num(t, `SELECT count(*) FROM people`); n != 0 {
				t.Errorf("an unknown owner at our own domain created %d people", n)
			}
		})
	}
}

func TestCRMOwnerChangeMovesTheOwnsEdge(t *testing.T) {
	resetDB(t)
	seedOrg(t)
	svc := service(t)
	ingestAll(t, svc,
		crmAccount(t, "account:AC-4", "Acme Corp", "acme.com"),
		crmOpp(t, "opp:AC-4-EXP", "account:AC-4", "dana@ghostvendor.com"),
		crmChange(t, "Opportunity", "opp:AC-4-EXP", "account:AC-4", false, t0.Add(time.Hour), fields{"OwnerEmail": "rachel@ghostvendor.com"}),
	)

	op := oppByCRM(t, "opp:AC-4-EXP")
	dana, rachel := personByEmail(t, "dana@ghostvendor.com"), personByEmail(t, "rachel@ghostvendor.com")
	if openEdge(t, dana, "owns", op) != 0 || openEdge(t, rachel, "owns", op) != 1 {
		t.Error("owns edge did not move from Dana to Rachel")
	}
	if got := num(t, `SELECT count(*) FROM relationships WHERE src_id = $1::uuid AND rel_type = 'owns' AND valid_to IS NOT NULL`, dana); got != 1 {
		t.Error("Dana's owns edge must be closed, not deleted")
	}
	if got := str(t, `SELECT owner_person_id::text FROM opportunities WHERE id = $1::uuid`, op); got != rachel {
		t.Errorf("owner_person_id = %s, want Rachel", got)
	}
}

func TestCRMContactCreatedCreatesAContactWithMappingsAndWorksAt(t *testing.T) {
	resetDB(t)
	svc := service(t)
	ingestAll(t, svc, crmAccount(t, "account:AC-4", "Acme Corp", "acme.com"))

	res, err := svc.Ingest(ctx, crmChange(t, "Contact", "contact:817", "account:AC-4", true, t0.Add(time.Minute),
		fields{"FirstName": "Priya", "LastName": "Shah", "Email": "Priya.Shah@Acme.com", "Title": "Director of Operations"}))
	if err != nil {
		t.Fatal(err)
	}

	priya := personByEmail(t, "priya.shah@acme.com")
	if priya == "" {
		t.Fatal("contact not created / email mapping missing")
	}
	acct := accountByCRM(t, "account:AC-4")
	if got := str(t, `SELECT kind || '|' || display_name || '|' || title || '|' || account_id::text FROM people WHERE id = $1::uuid`, priya); got != "contact|Priya Shah|Director of Operations|"+acct {
		t.Errorf("person row = %s", got)
	}
	if got := str(t, `SELECT entity_id::text FROM entity_source_mappings WHERE source_system = 'crm' AND source_key = 'contact:817' AND valid_to IS NULL`); got != priya {
		t.Errorf("crm mapping points at %s, want Priya", got)
	}
	if got := str(t, `SELECT person_id::text FROM activity_participants WHERE activity_id = $1::uuid AND raw_identity = 'crm:contact:817'`, res.ActivityID); got != priya {
		t.Errorf("the mentioned participant is linked to %s, want Priya", got)
	}
	if got := str(t, `SELECT standing FROM relationships WHERE src_id = $1::uuid AND rel_type = 'works_at' AND valid_to IS NULL`, priya); got != "crm_explicit" {
		t.Errorf("works_at standing = %q, want crm_explicit", got)
	}
}

func TestCRMContactWithoutEmailAndWithOurDomainEmail(t *testing.T) {
	resetDB(t)
	svc := service(t)
	ingestAll(t, svc, crmAccount(t, "account:AC-4", "Acme Corp", "acme.com"))

	ingestAll(t, svc,
		crmChange(t, "Contact", "contact:1", "account:AC-4", true, t0, fields{"FirstName": "No", "LastName": "Email"}),
		crmChange(t, "Contact", "contact:2", "account:AC-4", true, t0.Add(time.Minute), fields{"FirstName": "Our", "LastName": "Own", "Email": "own@ghostvendor.com"}),
	)

	if got := str(t, `SELECT display_name FROM people p JOIN entity_source_mappings m ON m.entity_id = p.id WHERE m.source_key = 'contact:1'`); got != "No Email" {
		t.Errorf("a contact without email must still be created, got %q", got)
	}
	if got := num(t, `SELECT count(*) FROM entity_source_mappings WHERE source_key = 'contact:2'`); got != 0 {
		t.Error("a CRM contact with an email at our own domain must not become a person")
	}
}

func TestCRMContactAttachesToAnExistingPersonWithTheSameEmail(t *testing.T) {
	resetDB(t)
	svc := service(t)
	ingestAll(t, svc,
		crmAccount(t, "account:AC-4", "Acme Corp", "acme.com"),
		email(t, "m1", "tom.becker@acme.com", []string{"dana@ghostvendor.com"}, t0.Add(time.Minute)), // creates Tom by rule
	)
	tom := personByEmail(t, "tom.becker@acme.com")
	if tom == "" {
		t.Fatal("setup: Tom should exist from the email")
	}

	ingestAll(t, svc, crmContact(t, "contact:829", "account:AC-4", "Tom", "Becker", "tom.becker@acme.com", t0.Add(time.Hour)))

	if n := num(t, `SELECT count(*) FROM people`); n != 1 {
		t.Errorf("%d people, want the CRM contact merged into the email-created one", n)
	}
	if got := str(t, `SELECT entity_id::text FROM entity_source_mappings WHERE source_key = 'contact:829' AND valid_to IS NULL`); got != tom {
		t.Errorf("crm mapping points at %s, want %s", got, tom)
	}
	acct := accountByCRM(t, "account:AC-4")
	if openEdge(t, tom, "works_at", acct) != 1 {
		t.Error("works_at edge missing")
	}
	if got := str(t, `SELECT standing FROM relationships WHERE src_id = $1::uuid AND rel_type = 'works_at' AND valid_to IS NULL`, tom); got != "crm_explicit" {
		t.Errorf("open works_at standing = %s, want the upgrade to crm_explicit", got)
	}
	if got := num(t, `SELECT count(*) FROM relationships WHERE src_id = $1::uuid AND rel_type = 'works_at' AND valid_to IS NOT NULL`, tom); got != 1 {
		t.Error("the first_party_record works_at edge must be closed, not deleted")
	}
}

func TestCRMRoleChangesWriteRoleEdges(t *testing.T) {
	resetDB(t)
	seedOrg(t)
	svc := service(t)
	ingestAll(t, svc,
		crmAccount(t, "account:AC-4", "Acme Corp", "acme.com"),
		crmOpp(t, "opp:AC-4-EXP", "account:AC-4", "dana@ghostvendor.com"),
		crmContact(t, "contact:1", "account:AC-4", "Owen", "Clarke", "owen@acme.com", t0.Add(2*time.Minute)),
	)
	op, owen := oppByCRM(t, "opp:AC-4-EXP"), personByEmail(t, "owen@acme.com")

	cases := []struct {
		role    string
		wantRel string
	}{
		{"Economic buyer", "economic_buyer_for"},
		{"Security approver", "influences"},
		{"Technical approver", "influences"},
		{"Evaluation owner", "influences"},
		{"Champion", ""}, // not a role WP5 maps: claims (WP6) decide champions
	}
	for i, tc := range cases {
		ingestAll(t, svc, crmChange(t, "Contact", "contact:1", "account:AC-4", false, t0.Add(time.Duration(i+1)*time.Hour), fields{"Role__c": tc.role}))

		open := num(t, `SELECT count(*) FROM relationships WHERE src_id = $1::uuid AND dst_id = $2::uuid AND standing = 'crm_explicit'
			AND rel_type IN ('economic_buyer_for', 'influences') AND valid_to IS NULL`, owen, op)
		if tc.wantRel == "" {
			if open != 0 {
				t.Errorf("role %q left %d open role edges, want none", tc.role, open)
			}
			continue
		}
		if open != 1 || openEdge(t, owen, tc.wantRel, op) != 1 {
			t.Errorf("role %q: want exactly one open %s edge, found %d role edges", tc.role, tc.wantRel, open)
		}
	}
	if got := num(t, `SELECT count(*) FROM relationships WHERE src_id = $1::uuid AND rel_type IN ('economic_buyer_for', 'influences') AND valid_to IS NOT NULL`, owen); got < 2 {
		t.Errorf("switching to a different relationship type must close the old edge and keep history, closed=%d", got)
	}
}

func TestCRMRoleEdgeNeedsExactlyOneOpportunityAtTheAccount(t *testing.T) {
	resetDB(t)
	svc := service(t)
	ingestAll(t, svc,
		crmAccount(t, "account:AC-4", "Acme Corp", "acme.com"),
		crmOpp(t, "opp:A", "account:AC-4", ""),
		crmChange(t, "Opportunity", "opp:B", "account:AC-4", true, t0.Add(time.Hour), fields{"Name": "Second", "StageName": "Discovery"}),
		crmContact(t, "contact:1", "account:AC-4", "Owen", "Clarke", "owen@acme.com", t0.Add(2*time.Hour)),
		crmChange(t, "Contact", "contact:1", "account:AC-4", false, t0.Add(3*time.Hour), fields{"Role__c": "Economic buyer"}),
	)

	if n := num(t, `SELECT count(*) FROM relationships WHERE rel_type = 'economic_buyer_for'`); n != 0 {
		t.Errorf("an ambiguous opportunity produced %d economic_buyer_for edges", n)
	}
}

func TestCRMTitleChangeUpdatesThePerson(t *testing.T) {
	resetDB(t)
	svc := service(t)
	ingestAll(t, svc,
		crmAccount(t, "account:AC-4", "Acme Corp", "acme.com"),
		crmContact(t, "contact:1", "account:AC-4", "Owen", "Clarke", "owen@acme.com", t0),
		crmChange(t, "Contact", "contact:1", "account:AC-4", false, t0.Add(time.Hour), fields{"Title": "CFO"}),
	)

	if got := str(t, `SELECT title FROM people WHERE primary_email = 'owen@acme.com'`); got != "CFO" {
		t.Errorf("title = %q, want CFO", got)
	}
}
