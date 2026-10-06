package graph_test

import (
	"sync"
	"testing"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/normalize"
)

func TestEmailFromAnUnknownPersonAtAKnownDomainCreatesAContactByRule(t *testing.T) {
	resetDB(t)
	seedOrg(t)
	svc := service(t)
	ingestAll(t, svc, crmAccount(t, "account:AC-4", "Acme Corp", "acme.com"))

	res, err := svc.Ingest(ctx, email(t, "m1", "marco.ruiz@acme.com", []string{"dana@vendor.example"}, t0.Add(time.Hour)))
	if err != nil {
		t.Fatal(err)
	}

	marco := personByEmail(t, "marco.ruiz@acme.com")
	if marco == "" {
		t.Fatal("no contact created for the sender")
	}
	acct := accountByCRM(t, "account:AC-4")
	if got := str(t, `SELECT kind || '|' || display_name || '|' || account_id::text FROM people WHERE id = $1::uuid`, marco); got != "contact|Sender m1|"+acct {
		t.Errorf("person row = %s (display name comes from the email's from.name)", got)
	}
	if got := str(t, `SELECT method || '|' || confidence::text || '|' || evidence_activity_id::text FROM entity_source_mappings
		WHERE source_system = 'email' AND source_key = 'marco.ruiz@acme.com'`); got != "rule|0.900|"+res.ActivityID {
		t.Errorf("mapping = %s, want rule / 0.9 / the email activity", got)
	}
	if got := str(t, `SELECT person_id::text FROM activity_participants WHERE activity_id = $1::uuid AND role = 'from'`, res.ActivityID); got != marco {
		t.Errorf("sender participant person_id = %s, want the new contact", got)
	}
	if got := str(t, `SELECT standing || '|' || confidence::text FROM relationships WHERE src_id = $1::uuid AND rel_type = 'works_at'`, marco); got != "first_party_record|0.900" {
		t.Errorf("works_at = %s, want first_party_record / 0.900", got)
	}
	dana := personByEmail(t, "dana@vendor.example")
	if got := str(t, `SELECT person_id::text FROM activity_participants WHERE activity_id = $1::uuid AND role = 'to'`, res.ActivityID); got != dana {
		t.Errorf("recipient person_id = %s, want the seeded employee", got)
	}
}

func TestEmailParticipantsNeverCreatePeopleForOurOwnDomainOrUnknownDomains(t *testing.T) {
	resetDB(t) // org NOT seeded
	svc := service(t)
	ingestAll(t, svc, crmAccount(t, "account:AC-4", "Acme Corp", "acme.com"))

	cases := map[string]string{
		"unseeded employee":      "stranger@vendor.example",
		"subdomain of ours":      "x@eu.vendor.example",
		"free mail":              "someone@gmail.com",
		"domain with no account": "buyer@nowhere.example",
	}
	for name, from := range cases {
		t.Run(name, func(t *testing.T) {
			before := num(t, `SELECT count(*) FROM people`)
			res, err := svc.Ingest(ctx, email(t, "m-"+name, from, []string{"priya@acme.com"}, t0.Add(time.Hour)))
			if err != nil {
				t.Fatal(err)
			}
			if got := num(t, `SELECT count(*) FROM activity_participants WHERE activity_id = $1::uuid AND role = 'from' AND person_id IS NOT NULL`, res.ActivityID); got != 0 {
				t.Error("sender was linked to a person")
			}
			// priya@acme.com is a valid external recipient and may be created once; nobody else may.
			if got := num(t, `SELECT count(*) FROM people`) - before; got > 1 {
				t.Errorf("%d people created, want at most the one acme.com recipient", got)
			}
			if got := num(t, `SELECT count(*) FROM people WHERE primary_email = $1`, from); got != 0 {
				t.Errorf("a person was created for %s", from)
			}
		})
	}
}

func TestMentionedEmailsAreLinkedButNeverCreated(t *testing.T) {
	resetDB(t)
	svc := service(t)
	ingestAll(t, svc, crmAccount(t, "account:AC-4", "Acme Corp", "acme.com"))
	enrich := func(day, who string) normalize.SourceEvent {
		observed := "2026-09-" + day + "T06:00:00Z"
		return event(t, "enrichment", "peoplegraph:"+who+":"+observed, "observed", map[string]any{
			"kind": "enrichment", "provider": "peoplegraph", "subject": map[string]any{"email": who, "domain": nil},
			"observed_at": observed, "facts": map[string]any{"title": "Director"},
		})
	}

	ingestAll(t, svc, enrich("15", "ravi.menon@acme.com"))
	if n := num(t, `SELECT count(*) FROM people`); n != 0 {
		t.Fatalf("a third-party enrichment mention created %d people", n)
	}

	ingestAll(t, svc, crmContact(t, "contact:911", "account:AC-4", "Ravi", "Menon", "ravi.menon@acme.com", t0.Add(time.Hour)))
	if _, err := svc.Ingest(ctx, enrich("16", "ravi.menon@acme.com")); err != nil {
		t.Fatal(err)
	}
	ravi := personByEmail(t, "ravi.menon@acme.com")
	if got := num(t, `SELECT count(*) FROM activity_participants WHERE person_id = $1::uuid AND role = 'mentioned' AND raw_identity = 'ravi.menon@acme.com'`, ravi); got == 0 {
		t.Error("the enrichment subject was not linked to the existing person")
	}
}

func TestSlackUserIsPairedWithTheEmailPersonOnFirstSight(t *testing.T) {
	resetDB(t)
	svc := service(t)
	ingestAll(t, svc,
		crmAccount(t, "account:AC-4", "Acme Corp", "acme.com"),
		crmContact(t, "contact:1", "account:AC-4", "Sam", "Okafor", "sam@acme.com", t0),
	)
	slack := func(ts, user, mail string) normalize.SourceEvent {
		return event(t, "slack", "#deal-acme:"+ts, "posted", map[string]any{
			"kind": "slack_message", "channel": "#deal-acme", "ts": ts, "thread_ts": nil, "user": user,
			"user_email": mail, "text": "hello", "is_decision": false,
		})
	}

	res, err := svc.Ingest(ctx, slack("1787241600.000100", "U777", "sam@acme.com"))
	if err != nil {
		t.Fatal(err)
	}

	sam := personByEmail(t, "sam@acme.com")
	if got := str(t, `SELECT entity_id::text || '|' || method FROM entity_source_mappings WHERE source_system = 'slack' AND source_key = 'U777' AND valid_to IS NULL`); got != sam+"|rule" {
		t.Errorf("slack mapping = %s, want Sam / rule", got)
	}
	if got := str(t, `SELECT person_id::text FROM activity_participants WHERE activity_id = $1::uuid`, res.ActivityID); got != sam {
		t.Errorf("slack actor person_id = %s", got)
	}

	// A Slack user already mapped elsewhere is never overridden by a claimed email.
	other := newPerson(t, "contact", "Else", "else@acme.com")
	if _, err := env.DB.Exec(`INSERT INTO entity_source_mappings (entity_type, entity_id, source_system, source_key, confidence, method)
		VALUES ('person', $1::uuid, 'slack', 'U888', 1, 'seed')`, other); err != nil {
		t.Fatal(err)
	}
	ingestAll(t, svc, slack("1787241700.000100", "U888", "sam@acme.com"))
	if got := str(t, `SELECT entity_id::text FROM entity_source_mappings WHERE source_system = 'slack' AND source_key = 'U888' AND valid_to IS NULL`); got != other {
		t.Error("an existing slack mapping was overridden")
	}
}

func TestConcurrentEventsForTheSameNewContactCreateOnePerson(t *testing.T) {
	resetDB(t)
	svc := service(t)
	ingestAll(t, svc, crmAccount(t, "account:AC-4", "Acme Corp", "acme.com"))

	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := svc.Ingest(ctx, email(t, "c"+string(rune('a'+i)), "newbie@acme.com", []string{"dana@vendor.example"}, t0.Add(time.Duration(i+1)*time.Minute)))
			errs <- err
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent ingest failed: %v", err)
		}
	}

	if n := num(t, `SELECT count(*) FROM people WHERE primary_email = 'newbie@acme.com'`); n != 1 {
		t.Errorf("%d people for one email, want 1", n)
	}
	if n := num(t, `SELECT count(*) FROM entity_source_mappings WHERE source_key = 'newbie@acme.com' AND valid_to IS NULL`); n != 1 {
		t.Errorf("%d current mappings for one email, want 1", n)
	}
}
