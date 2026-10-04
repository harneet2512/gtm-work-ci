package graph_test

import (
	"testing"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/graph"
)

func TestSupportsIsWrittenForNonOwnerEmployeesInOpportunityMeetings(t *testing.T) {
	callWorld(t, dana, leo, priya)

	op := oppByCRM(t, "opp:AC-4-EXP")
	leoID, danaID := personByEmail(t, leo.Email), personByEmail(t, dana.Email)
	if openEdge(t, leoID, "supports", op) != 1 {
		t.Error("Leo attends an opportunity meeting and does not own it: want supports")
	}
	if openEdge(t, danaID, "supports", op) != 0 {
		t.Error("the owner must not support her own opportunity")
	}
	if got := str(t, `SELECT standing FROM relationships WHERE src_id = $1::uuid AND rel_type = 'supports'`, leoID); got != "first_party_record" {
		t.Errorf("supports standing = %s, want first_party_record (a deterministic rule, not an AI inference)", got)
	}
}

func TestSupportsNeedsAMeetingAnOpportunityAndAKnownOwner(t *testing.T) {
	resetDB(t)
	seedOrg(t)
	svc := service(t)
	ingestAll(t, svc,
		crmAccount(t, "account:AC-4", "Acme Corp", "acme.com"),
		crmOpp(t, "opp:AC-4-EXP", "account:AC-4", "dana@ghostvendor.com"),
		// Not a meeting: an email from Leo about the opportunity.
		email(t, "m1", leo.Email, []string{priya.Email}, t0.Add(time.Hour)),
	)
	if n := num(t, `SELECT count(*) FROM relationships WHERE rel_type = 'supports'`); n != 0 {
		t.Errorf("an email produced %d supports edges", n)
	}

	// Opportunity without a known owner: attendance alone proves nothing.
	resetDB(t)
	svc = service(t)
	ingestAll(t, svc,
		crmAccount(t, "account:AC-4", "Acme Corp", "acme.com"),
		crmOpp(t, "opp:AC-4-EXP", "account:AC-4", ""),
		calendar(t, "gcal:x", "scheduled", t0.Add(24*time.Hour), "opp:AC-4-EXP", dana, priya),
	)
	if n := num(t, `SELECT count(*) FROM relationships WHERE rel_type = 'supports'`); n != 0 {
		t.Errorf("%d supports edges without a known owner", n)
	}
}

func TestSharedWithEdgesPointFromTheDocumentToEachRecipient(t *testing.T) {
	resetDB(t)
	seedOrg(t)
	svc := service(t)
	ingestAll(t, svc,
		crmAccount(t, "account:AC-4", "Acme Corp", "acme.com"),
		crmContact(t, "contact:817", "account:AC-4", "Priya", "Shah", priya.Email, t0),
		crmContact(t, "contact:829", "account:AC-4", "Tom", "Becker", tom.Email, t0.Add(time.Minute)),
		event(t, "docs", "gdrive:proposal", "shared:2026-08-28T15:01:00Z", map[string]any{
			"kind": "document", "document_id": "gdrive:proposal", "title": "Proposal", "shared_at": "2026-08-28T15:01:00Z",
			"shared_by": dana.Email, "shared_with": []string{priya.Email, tom.Email},
		}),
	)

	doc := graph.DocumentID("gdrive:proposal")
	for _, p := range []string{priya.Email, tom.Email} {
		if openEdge(t, doc, "shared_with", personByEmail(t, p)) != 1 {
			t.Errorf("missing shared_with edge to %s", p)
		}
	}
	if got := str(t, `SELECT src_type FROM relationships WHERE rel_type = 'shared_with' LIMIT 1`); got != "document" {
		t.Errorf("shared_with source type = %s, want document", got)
	}
}

func TestActivityEdgesParticipatedInAboutAndInvolves(t *testing.T) {
	svc := callWorld(t, dana, leo, priya)
	res, err := svc.Ingest(ctx, email(t, "m9", priya.Email, []string{dana.Email}, t0.Add(72*time.Hour)))
	if err != nil {
		t.Fatal(err)
	}
	priyaID, danaID := personByEmail(t, priya.Email), personByEmail(t, dana.Email)
	acct := accountByCRM(t, "account:AC-4")

	if openEdge(t, priyaID, "participated_in", res.ActivityID) != 1 || openEdge(t, danaID, "participated_in", res.ActivityID) != 1 {
		t.Error("sender and recipient must both participate in the email activity")
	}
	if openEdge(t, res.ActivityID, "about", acct) != 1 {
		t.Error("an activity without an opportunity is about its account")
	}
	// A meeting linked to the opportunity is about the opportunity and involves the contacts.
	cal := str(t, `SELECT id::text FROM activities WHERE source_object_id = 'gcal:scope' ORDER BY occurred_at LIMIT 1`)
	op := oppByCRM(t, "opp:AC-4-EXP")
	if openEdge(t, cal, "about", op) != 1 {
		t.Error("an opportunity meeting is about the opportunity")
	}
	// Activity -involves-> Person (HAR-96 section 5): every resolved participant, employees too.
	for name, id := range map[string]string{"Priya": priyaID, "Dana": danaID} {
		if openEdge(t, cal, "involves", id) != 1 {
			t.Errorf("the meeting activity must involve %s", name)
		}
	}
	if openEdge(t, op, "involves", priyaID) != 0 {
		t.Error("involves points from the activity to the person, not from the opportunity")
	}
}

func TestEdgesAreWrittenOnceOnReplayAndReceiveSourceActivities(t *testing.T) {
	svc := callWorld(t, dana, leo, priya)
	before := num(t, `SELECT count(*) FROM relationships`)
	ev := email(t, "m7", priya.Email, []string{dana.Email}, t0.Add(72*time.Hour))
	ingestAll(t, svc, ev)
	after := num(t, `SELECT count(*) FROM relationships`)
	ingestAll(t, svc, ev)

	if after == before {
		t.Fatal("setup: the email should add edges")
	}
	if got := num(t, `SELECT count(*) FROM relationships`); got != after {
		t.Errorf("redelivery changed the edge count %d -> %d", after, got)
	}
	if n := num(t, `SELECT count(*) FROM relationships WHERE source_activity_id IS NULL AND rel_type <> 'reports_to'`); n != 0 {
		t.Errorf("%d edges have no source activity", n)
	}
}

func TestOutOfOrderEventsAreReresolvedOnceTheAccountExists(t *testing.T) {
	resetDB(t)
	seedOrg(t)
	svc := service(t)

	early, err := svc.Ingest(ctx, email(t, "m1", priya.Email, []string{dana.Email}, t0))
	if err != nil {
		t.Fatal(err)
	}
	if early.AccountID != nil || num(t, `SELECT count(*) FROM unresolved_activities`) != 1 {
		t.Fatal("setup: the email must be parked before the account exists")
	}
	// A contact event that arrives before its account is parked too.
	ingestAll(t, svc, crmContact(t, "contact:817", "account:AC-4", "Priya", "Shah", priya.Email, t0.Add(time.Minute)))
	if num(t, `SELECT count(*) FROM unresolved_activities`) != 2 {
		t.Fatal("setup: the contact event must be parked")
	}

	ingestAll(t, svc, crmAccount(t, "account:AC-4", "Acme Corp", "acme.com"))

	if n := num(t, `SELECT count(*) FROM unresolved_activities`); n != 0 {
		t.Errorf("%d activities still unresolved after the account appeared", n)
	}
	acct := accountByCRM(t, "account:AC-4")
	if got := str(t, `SELECT account_id::text FROM activities WHERE id = $1::uuid`, early.ActivityID); got != acct {
		t.Errorf("early email account = %s, want %s", got, acct)
	}
	priyaID := personByEmail(t, priya.Email)
	if priyaID == "" || openEdge(t, priyaID, "works_at", acct) != 1 {
		t.Error("the late contact must be created and linked to the account")
	}
	if got := str(t, `SELECT person_id::text FROM activity_participants WHERE activity_id = $1::uuid AND role = 'from'`, early.ActivityID); got != priyaID {
		t.Errorf("the early email's sender is linked to %q, want Priya", got)
	}
	if num(t, `SELECT cardinality(activity_ids) FROM recompute_jobs WHERE account_id = $1::uuid`, acct) < 3 {
		t.Error("the recompute job must cover the re-resolved activities")
	}
}

func TestReplayingTheSameEventsTwiceLeavesIdenticalRowCounts(t *testing.T) {
	resetDB(t)
	seedOrg(t)
	svc := service(t)
	feed := func() {
		ingestAll(t, svc,
			crmAccount(t, "account:AC-4", "Acme Corp", "acme.com"),
			crmOpp(t, "opp:AC-4-EXP", "account:AC-4", dana.Email),
			crmContact(t, "contact:817", "account:AC-4", "Priya", "Shah", priya.Email, t0.Add(time.Minute)),
			calendar(t, "gcal:scope", "scheduled", t0.Add(24*time.Hour), "opp:AC-4-EXP", dana, leo, priya, tom),
			call(t, "C1", "gcal:scope", "transcript_ready", t0.Add(48*time.Hour),
				[]speaker{{"speaker_01", dana.Email, dana.Name}, {"speaker_02", "", ""}},
				[]segment{{"speaker_02", "Priya here."}}),
		)
	}
	counts := func() [5]int {
		return [5]int{num(t, `SELECT count(*) FROM people`), num(t, `SELECT count(*) FROM entity_source_mappings`),
			num(t, `SELECT count(*) FROM relationships`), num(t, `SELECT count(*) FROM activity_participants WHERE person_id IS NOT NULL`),
			num(t, `SELECT count(*) FROM accounts`)}
	}

	feed()
	first := counts()
	feed()

	if second := counts(); second != first {
		t.Errorf("row counts changed on replay: %v -> %v", first, second)
	}
}
