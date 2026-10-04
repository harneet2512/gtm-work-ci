package graph_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/graph"
	"github.com/harneet2512/gtm-work/core-go/internal/ingest"
	"github.com/harneet2512/gtm-work/core-go/internal/normalize"
)

// --- personal mail domains

func TestPersonalMailDomainNeverResolvesAnAccountOrMintsAContact(t *testing.T) {
	resetDB(t)
	svc := service(t)
	// An account (manually created, or from a careless CRM record) that claims gmail.com.
	manual := newAccount(t, "Gmail Fan Club", "gmail.com")
	ingestAll(t, svc, crmAccount(t, "account:G-1", "Careless Corp", "https://www.gmail.com"))

	res, err := svc.Ingest(ctx, email(t, "m1", "someone@gmail.com", []string{"dana@ghostvendor.com"}, t0.Add(time.Hour)))
	if err != nil {
		t.Fatal(err)
	}

	if res.AccountID != nil {
		t.Errorf("an email from gmail.com was attributed to account %s through its domain", *res.AccountID)
	}
	if n := num(t, `SELECT count(*) FROM people WHERE primary_email = 'someone@gmail.com'`); n != 0 {
		t.Errorf("a contact was minted for a gmail address under account %s", manual)
	}
	if got := str(t, `SELECT coalesce(domain, 'none') FROM accounts WHERE id = $1::uuid`, accountByCRM(t, "account:G-1")); got != "none" {
		t.Errorf("a CRM account with a personal website got domain %q", got)
	}
}

// --- speaker spoofing

func TestPossessivesAndRelationsAreNotSelfIntroductions(t *testing.T) {
	svc := callWorld(t, dana, priya, tom)
	speakers := []speaker{{"speaker_01", dana.Email, dana.Name}, {"speaker_02", "", ""}}

	ingestAll(t, svc, call(t, "C6", "gcal:scope", "transcript_ready", t0.Add(48*time.Hour), speakers, []segment{
		{"speaker_02", "It's Priya's turn to present, and I'm Tom's manager."},
	}))

	if got := callMapping(t, "C6", "speaker_02"); got != "" {
		t.Errorf("a possessive was taken as a self-introduction: speaker_02 -> %s", got)
	}
}

func TestSpeakerCandidatesMustBelongToTheCallsAccount(t *testing.T) {
	svc := callWorld(t, dana, priya)
	// Another account has a "Priya Shah" too, and she is on the same calendar event.
	ingestAll(t, svc,
		crmAccount(t, "account:BE-7", "Beta Inc", "beta.io"),
		crmContact(t, "contact:900", "account:BE-7", "Priya", "Shah", "priya.shah@beta.io", t0.Add(time.Hour)),
		calendar(t, "gcal:scope", "attendee_added:priya.shah@beta.io", t0.Add(30*time.Hour), "opp:AC-4-EXP",
			dana, priya, attendee{"priya.shah@beta.io", "Priya Shah"}),
	)

	speakers := []speaker{{"speaker_01", dana.Email, dana.Name}, {"speaker_02", "", ""}}
	ingestAll(t, svc, call(t, "C7", "gcal:scope", "transcript_ready", t0.Add(72*time.Hour), speakers, []segment{
		{"speaker_02", "Priya here."},
	}))

	// The other account's Priya is not a candidate, so the introduction is unambiguous.
	if got := callMapping(t, "C7", "speaker_02"); got != personByEmail(t, priya.Email) {
		t.Errorf("speaker_02 -> %q, want the call's own account's Priya (the other account's contact must not compete)", got)
	}
}

func TestSpeakerMappingStoresTheMatchedCue(t *testing.T) {
	svc := callWorld(t, dana, priya)
	speakers := []speaker{{"speaker_01", dana.Email, dana.Name}, {"speaker_02", "", ""}}
	ingestAll(t, svc, call(t, "C8", "gcal:scope", "transcript_ready", t0.Add(48*time.Hour), speakers, []segment{
		{"speaker_02", "Sure. Priya here - I run operations."},
	}))

	raw := str(t, `SELECT evidence::text FROM entity_source_mappings WHERE source_key = 'C8:speaker_02'`)
	var ev map[string]string
	if err := json.Unmarshal([]byte(raw), &ev); err != nil {
		t.Fatalf("evidence %q: %v", raw, err)
	}
	if ev["cue"] != "intro" || ev["span"] != "Priya here" || ev["pattern"] != "intro:name-here" || ev["rule"] != "calendar_attendee_cue" {
		t.Errorf("evidence = %v, want the matched cue, its pattern id and its text span", ev)
	}
	// Rule-created email mappings say why too.
	ingestAll(t, svc, email(t, "mx", "newbie@acme.com", []string{dana.Email}, t0.Add(72*time.Hour)))
	if got := str(t, `SELECT evidence->>'rule' FROM entity_source_mappings WHERE source_key = 'newbie@acme.com'`); got != "email_domain_account" {
		t.Errorf("email mapping evidence rule = %q", got)
	}
}

// A speaker resolved late re-points the participant rows of past activities: they gain edges
// and their account gets a recompute.
func TestLateSpeakerMappingBackfillsEdgesAndEnqueuesRecomputes(t *testing.T) {
	svc := callWorld(t, dana, priya)
	speakers := []speaker{{"speaker_01", dana.Email, dana.Name}, {"speaker_02", "", ""}}
	at := t0.Add(48 * time.Hour)
	ended, err := svc.Ingest(ctx, call(t, "C9", "gcal:scope", "ended", at, speakers, nil))
	if err != nil {
		t.Fatal(err)
	}
	priyaID := personByEmail(t, priya.Email)
	if openEdge(t, priyaID, "participated_in", ended.ActivityID) != 0 {
		t.Fatal("setup: speaker_02 is unresolved at the ended event")
	}
	if _, err := env.DB.Exec(`DELETE FROM recompute_jobs`); err != nil {
		t.Fatal(err)
	}

	ingestAll(t, svc, call(t, "C9", "gcal:scope", "transcript_ready", at, speakers, []segment{{"speaker_02", "Priya here."}}))

	if openEdge(t, priyaID, "participated_in", ended.ActivityID) != 1 {
		t.Error("the earlier ended activity did not get Priya's participated_in edge")
	}
	if got := str(t, `SELECT $1::uuid = ANY(activity_ids) FROM recompute_jobs WHERE account_id = $2::uuid`, ended.ActivityID, accountByCRM(t, "account:AC-4")); got != "true" {
		t.Error("re-pointing a past activity must enqueue a recompute that covers it")
	}
}

// --- decode errors

func TestMalformedPayloadsInFinalizeAreErrors(t *testing.T) {
	resetDB(t)
	valid := crmAccount(t, "account:AC-4", "Acme Corp", "acme.com")
	act, err := normalize.Normalize(valid)
	if err != nil {
		t.Fatal(err)
	}
	for name, ev := range map[string]normalize.SourceEvent{
		"crm":   {SourceSystem: "crm", SourceObjectID: "x", SourceEventKey: "created", Payload: json.RawMessage(`{"kind":`)},
		"call":  {SourceSystem: "call", SourceObjectID: "x", SourceEventKey: "ended", Payload: json.RawMessage(`[]`)},
		"slack": {SourceSystem: "slack", SourceObjectID: "x", SourceEventKey: "posted", Payload: json.RawMessage(`"oops"`)},
		"docs":  {SourceSystem: "docs", SourceObjectID: "x", SourceEventKey: "shared:t", Payload: json.RawMessage(`{"kind":"email"}`)},
	} {
		t.Run(name, func(t *testing.T) {
			tx, err := env.DB.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = tx.Rollback() }()
			_, err = graph.NewExtension().Finalize(ctx, tx, ingest.FinalizeInput{Event: ev, Activity: act, ActivityID: "00000000-0000-0000-0000-000000000000"})
			if err == nil || !strings.Contains(err.Error(), "graph:") {
				t.Errorf("error = %v, want a wrapped decode error, not a silent skip", err)
			}
		})
	}
}

// --- champion_for from CRM

func TestCRMChampionRoleWritesAnExclusiveCRMExplicitChampionEdge(t *testing.T) {
	resetDB(t)
	svc := service(t)
	ingestAll(t, svc,
		crmAccount(t, "account:AC-4", "Acme Corp", "acme.com"),
		crmOpp(t, "opp:AC-4-EXP", "account:AC-4", ""),
		crmContact(t, "contact:1", "account:AC-4", "Priya", "Shah", priya.Email, t0.Add(2*time.Minute)),
		crmContact(t, "contact:2", "account:AC-4", "Tom", "Becker", tom.Email, t0.Add(3*time.Minute)),
		crmChange(t, "Contact", "contact:1", "account:AC-4", false, t0.Add(time.Hour), fields{"Role__c": "Champion"}),
	)
	op, p, tm := oppByCRM(t, "opp:AC-4-EXP"), personByEmail(t, priya.Email), personByEmail(t, tom.Email)
	if got := str(t, `SELECT standing FROM relationships WHERE rel_type = 'champion_for' AND src_id = $1::uuid AND valid_to IS NULL`, p); got != "crm_explicit" {
		t.Fatalf("champion standing = %q, want crm_explicit", got)
	}

	ingestAll(t, svc, crmChange(t, "Contact", "contact:2", "account:AC-4", false, t0.Add(2*time.Hour), fields{"Role__c": "Champion"}))

	if openEdge(t, tm, "champion_for", op) != 1 || openEdge(t, p, "champion_for", op) != 0 {
		t.Error("a new CRM champion must replace the previous one (history kept)")
	}
}

// --- company file

func TestSeedCompanyRecordsItsSourceOnMappingsAndEdges(t *testing.T) {
	resetDB(t)
	c := smallOrg()
	c.Source = "fixtures/world/org.json"
	if _, err := graph.SeedCompany(ctx, env.DB, c, t0); err != nil {
		t.Fatal(err)
	}
	if got := str(t, `SELECT evidence->>'source' FROM entity_source_mappings WHERE source_system = 'email' AND source_key = 'dana@ghostvendor.com'`); got != "fixtures/world/org.json" {
		t.Errorf("mapping evidence source = %q", got)
	}
	if got := str(t, `SELECT evidence->>'source' || '|' || standing FROM relationships WHERE rel_type = 'reports_to'`); got != "fixtures/world/org.json|first_party_record" {
		t.Errorf("reports_to evidence/standing = %q", got)
	}
}

func TestSeedCompanyDoesNotMutateItsInput(t *testing.T) {
	resetDB(t)
	c := smallOrg()
	if _, err := graph.SeedCompany(ctx, env.DB, c, t0); err != nil {
		t.Fatal(err)
	}
	if c.People[0].Email != "Dana@GhostVendor.com" {
		t.Errorf("the caller's slice was normalized in place: %q", c.People[0].Email)
	}
}

func TestSeedCompanyRefusesAnEmailThatBelongsToAContact(t *testing.T) {
	resetDB(t)
	newPerson(t, "contact", "Impostor", "dana@ghostvendor.com")

	_, err := graph.SeedCompany(ctx, env.DB, smallOrg(), t0)

	if err == nil || !strings.Contains(err.Error(), "not an employee") {
		t.Fatalf("err = %v, want a loud refusal", err)
	}
	if n := num(t, `SELECT count(*) FROM people WHERE kind = 'employee'`); n != 0 {
		t.Errorf("%d employees written by a failed seed", n)
	}
}

// --- provenance on remaps

func TestRemapKeepsEvidenceUnlessOverridden(t *testing.T) {
	resetDB(t)
	a, b := newPerson(t, "contact", "A", "a@x.com"), newPerson(t, "contact", "B", "b@x.com")
	k := graph.SourceKey{System: "call", Key: "C:speaker_02"}
	if _, err := insertMapping(t, graph.Mapping{EntityType: graph.EntityPerson, EntityID: a, SourceKey: k,
		Confidence: 0.8, Method: graph.MethodRule, Evidence: json.RawMessage(`{"cue":"intro"}`), ValidFrom: t0}); err != nil {
		t.Fatal(err)
	}
	moved, err := remap(t, k, b, t0.Add(time.Hour), nil)
	if err != nil {
		t.Fatal(err)
	}
	if string(moved.Evidence) != `{"cue": "intro"}` {
		t.Errorf("evidence after remap = %s", moved.Evidence)
	}
	if err := graph.CloseMapping(ctx, env.DB, moved.ID, t0.Add(2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := graph.CurrentMapping(ctx, env.DB, k); ok {
		t.Error("CloseMapping left the identity mapped")
	}
}
