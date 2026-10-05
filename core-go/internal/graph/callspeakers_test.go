package graph_test

import (
	"testing"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/ingest"
)

var (
	dana  = attendee{"dana@vendor.example", "Dana Kim"}
	leo   = attendee{"leo@vendor.example", "Leo Park"}
	priya = attendee{"priya.shah@acme.com", "Priya Shah"}
	tom   = attendee{"tom.becker@acme.com", "Tom Becker"}
)

// callWorld seeds the org, Acme with its opportunity and one scheduled meeting, and returns
// the service. Contacts are created by the calendar attendees rule unless a test adds CRM ones.
func callWorld(t *testing.T, who ...attendee) *ingest.Service {
	t.Helper()
	resetDB(t)
	seedOrg(t)
	svc := service(t)
	ingestAll(t, svc,
		crmAccount(t, "account:AC-4", "Acme Corp", "acme.com"),
		crmOpp(t, "opp:AC-4-EXP", "account:AC-4", "dana@vendor.example"),
		crmContact(t, "contact:817", "account:AC-4", "Priya", "Shah", "priya.shah@acme.com", t0.Add(2*time.Minute)),
		calendar(t, "gcal:scope", "scheduled", t0.Add(24*time.Hour), "opp:AC-4-EXP", who...),
	)
	return svc
}

func callMapping(t *testing.T, callID, label string) string {
	t.Helper()
	return str(t, `SELECT entity_id::text FROM entity_source_mappings WHERE source_system = 'call' AND source_key = $1 AND valid_to IS NULL`, callID+":"+label)
}

func TestUnlabelledSpeakerIsResolvedFromTheAttendeesAndASelfIntroduction(t *testing.T) { // HAR-99 puzzle 1
	svc := callWorld(t, dana, leo, priya, tom)
	speakers := []speaker{
		{"speaker_01", dana.Email, dana.Name}, {"speaker_02", "", ""}, {"speaker_03", tom.Email, tom.Name}, {"speaker_04", leo.Email, leo.Name},
	}
	at := t0.Add(48 * time.Hour)
	ingestAll(t, svc, call(t, "C1", "gcal:scope", "ended", at, speakers, nil))
	if callMapping(t, "C1", "speaker_02") != "" {
		t.Fatal("without a transcript nothing can identify speaker_02")
	}

	res, err := svc.Ingest(ctx, call(t, "C1", "gcal:scope", "transcript_ready", at, speakers, []segment{
		{"speaker_01", "Quick round of intros?"},
		{"speaker_02", "Sure. Priya here - I run operations for the US sites."},
		{"speaker_03", "Tom Becker, I manage EU operations."},
	}))
	if err != nil {
		t.Fatal(err)
	}

	priyaID := personByEmail(t, "priya.shah@acme.com")
	if got := callMapping(t, "C1", "speaker_02"); got != priyaID {
		t.Fatalf("speaker_02 maps to %q, want Priya %s", got, priyaID)
	}
	if got := str(t, `SELECT method || '|' || confidence::text || '|' || evidence_activity_id::text FROM entity_source_mappings
		WHERE source_key = 'C1:speaker_02'`); got != "rule|0.800|"+res.ActivityID {
		t.Errorf("speaker mapping = %s, want rule / 0.8 / the transcript activity", got)
	}
	// One person: the CRM contact, the email and the speaker label are the same human.
	if got := num(t, `SELECT count(DISTINCT entity_id) FROM entity_source_mappings
		WHERE source_key IN ('contact:817', 'priya.shah@acme.com', 'C1:speaker_02') AND valid_to IS NULL`); got != 1 {
		t.Errorf("Priya's three identities resolve to %d people, want 1", got)
	}
	// Both call activities' speaker rows are linked, including the earlier "ended" one.
	if got := num(t, `SELECT count(*) FROM activity_participants WHERE raw_identity = 'call:C1:speaker_02' AND person_id = $1::uuid`, priyaID); got != 2 {
		t.Errorf("%d speaker_02 rows linked to Priya, want 2 (ended + transcript)", got)
	}
	// Labelled speakers are mapped through their emails.
	for label, mail := range map[string]string{"speaker_01": dana.Email, "speaker_03": tom.Email, "speaker_04": leo.Email} {
		if got := callMapping(t, "C1", label); got != personByEmail(t, mail) {
			t.Errorf("%s maps to %q, want the person with email %s", label, got, mail)
		}
	}
	if got := str(t, `SELECT method FROM entity_source_mappings WHERE source_key = 'C1:speaker_03'`); got != "exact" {
		t.Errorf("an email-labelled speaker has method %s, want exact", got)
	}
}

func TestSpeakerStaysUnresolvedWhenTwoAttendeesShareAFirstName(t *testing.T) {
	tom2 := attendee{"tom.brandt@acme.com", "Tom Brandt"}
	svc := callWorld(t, dana, tom, tom2)
	speakers := []speaker{{"speaker_01", dana.Email, dana.Name}, {"speaker_02", "", ""}}
	at := t0.Add(48 * time.Hour)

	ingestAll(t, svc, call(t, "C2", "gcal:scope", "transcript_ready", at, speakers, []segment{
		{"speaker_02", "Tom here, I manage EU operations."},
	}))

	if got := callMapping(t, "C2", "speaker_02"); got != "" {
		t.Errorf("ambiguous speaker mapped to %s", got)
	}
	if got := num(t, `SELECT count(*) FROM activity_participants WHERE raw_identity = 'call:C2:speaker_02' AND person_id IS NOT NULL`); got != 0 {
		t.Error("an unresolved speaker was linked to a person")
	}

	// The full name breaks the tie.
	ingestAll(t, svc, call(t, "C3", "gcal:scope", "transcript_ready", at.Add(time.Hour), speakers, []segment{
		{"speaker_02", "Tom Brandt here, I run finance."},
	}))
	if got := callMapping(t, "C3", "speaker_02"); got != personByEmail(t, tom2.Email) {
		t.Errorf("full-name introduction mapped to %s, want Tom Brandt", got)
	}
}

func TestAttendeesAlreadyIdentifiedAsOtherSpeakersAreNotCandidates(t *testing.T) {
	svc := callWorld(t, dana, priya, tom)
	speakers := []speaker{{"speaker_01", dana.Email, dana.Name}, {"speaker_02", "", ""}, {"speaker_03", tom.Email, tom.Name}}

	ingestAll(t, svc, call(t, "C4", "gcal:scope", "transcript_ready", t0.Add(48*time.Hour), speakers, []segment{
		{"speaker_02", "Tom here, hello."}, // Tom is already speaker_03
	}))

	if got := callMapping(t, "C4", "speaker_02"); got != "" {
		t.Errorf("speaker_02 was mapped to %s although that attendee is speaker_03", got)
	}
}

func TestSpeakerWithoutACalendarLinkOrAnUnknownEventIsLeftAlone(t *testing.T) {
	svc := callWorld(t, dana, priya)
	speakers := []speaker{{"speaker_01", dana.Email, dana.Name}, {"speaker_02", "", ""}, {"speaker_03", priya.Email, priya.Name}}
	segs := []segment{{"speaker_02", "Priya here."}}

	for i, cal := range []string{"", "gcal:unknown"} {
		id := "CN" + string(rune('0'+i))
		if _, err := svc.Ingest(ctx, call(t, id, cal, "transcript_ready", t0.Add(48*time.Hour), speakers, segs)); err != nil {
			t.Fatalf("calendar %q: %v", cal, err)
		}
		if got := callMapping(t, id, "speaker_02"); got != "" {
			t.Errorf("calendar %q: speaker mapped to %s", cal, got)
		}
	}
}

func TestLaterEmailEvidenceRemapsASpeakerAndKeepsTheHistory(t *testing.T) {
	svc := callWorld(t, dana, priya, tom)
	at := t0.Add(48 * time.Hour)
	unlabelled := []speaker{{"speaker_01", dana.Email, dana.Name}, {"speaker_02", "", ""}}
	ingestAll(t, svc, call(t, "C5", "gcal:scope", "transcript_ready", at, unlabelled, []segment{{"speaker_02", "Priya here."}}))
	priyaID := personByEmail(t, priya.Email)
	if callMapping(t, "C5", "speaker_02") != priyaID {
		t.Fatal("setup: the rule should have mapped speaker_02 to Priya")
	}

	// The recorder later re-delivers the call with the email it matched: Tom.
	labelled := []speaker{{"speaker_01", dana.Email, dana.Name}, {"speaker_02", tom.Email, tom.Name}}
	ingestAll(t, svc, call(t, "C5", "gcal:scope", "ended", at, labelled, nil))

	if got := callMapping(t, "C5", "speaker_02"); got != personByEmail(t, tom.Email) {
		t.Fatalf("speaker_02 maps to %s, want Tom after the email evidence", got)
	}
	if got := num(t, `SELECT count(*) FROM entity_source_mappings WHERE source_key = 'C5:speaker_02'`); got != 2 {
		t.Errorf("%d rows for the identity, want old (closed) + new", got)
	}
	if got := str(t, `SELECT method FROM entity_source_mappings WHERE source_key = 'C5:speaker_02' AND valid_to IS NULL`); got != "exact" {
		t.Errorf("the new mapping method = %s, want exact (stronger evidence)", got)
	}
}
