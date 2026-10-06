package claimstore

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/claims"
	"github.com/harneet2512/gtm-work/core-go/internal/store/storetest"
)

var env *storetest.Env

func TestMain(m *testing.M) {
	os.Exit(storetest.Main(m, func(e *storetest.Env) { env = e }))
}

var t0 = time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)

type fixture struct{ account, person, activity, otherActivity, opportunity string }

func scalarQuery(t *testing.T, q string, args ...any) string {
	t.Helper()
	var s string
	if err := env.DB.QueryRow(q, args...).Scan(&s); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
	return s
}

// seed resets the tables claimstore touches and creates one account, one contact, one opportunity
// and two activities.
func seed(t *testing.T) fixture {
	t.Helper()
	if err := storetest.Purge(context.Background(), env.DB, `TRUNCATE claims, extraction_cache, activities, source_events, entity_source_mappings, opportunities, people, accounts RESTART IDENTITY CASCADE`); err != nil {
		t.Fatal(err)
	}
	f := fixture{}
	f.account = scalarQuery(t, `INSERT INTO accounts (name, domain) VALUES ('Acme', 'acme.com') RETURNING id::text`)
	f.person = scalarQuery(t, `INSERT INTO people (kind, display_name, primary_email, account_id) VALUES ('contact', 'Priya Shah', 'priya.shah@acme.com', $1::uuid) RETURNING id::text`, f.account)
	f.opportunity = scalarQuery(t, `INSERT INTO opportunities (account_id, name, motion) VALUES ($1::uuid, 'EU rollout', 'expansion') RETURNING id::text`, f.account)
	mkActivity := func(obj string) string {
		ev := scalarQuery(t, `INSERT INTO source_events (source_system, source_object_id, source_event_key, idempotency_key, payload)
			VALUES ('email', $1::text, 'received', encode(sha256(convert_to($1::text, 'UTF8')), 'hex'), '{}'::jsonb) RETURNING id::text`, obj)
		return scalarQuery(t, `INSERT INTO activities (source_event_id, activity_type, source_system, source_object_id, occurred_at, account_id, opportunity_id, provenance)
			VALUES ($1::uuid, 'EmailReceived', 'email', $2, $3, $4::uuid, $5::uuid, '{"source_system":"email","source_object_id":"x"}'::jsonb) RETURNING id::text`,
			ev, obj, t0, f.account, f.opportunity)
	}
	f.activity, f.otherActivity = mkActivity("msg-1"), mkActivity("msg-2")
	return f
}

func aiClaim(f fixture, field claims.FieldPath, value string, when time.Time) claims.Claim {
	return claims.Claim{AccountID: f.account, OpportunityID: f.opportunity, FieldPath: field, Value: json.RawMessage(value),
		Standing: claims.FirstPartyAI, Confidence: 0.9, SourceActivityID: f.activity, EvidenceQuote: "a quote", OccurredAt: when,
		Extractor: "llm:fake@extract-v1", Status: claims.StatusActive, SpeakerPersonID: f.person, SubjectPersonID: f.person}
}

func TestInsertClaimsIsIdempotentPerActivityFieldExtractorValueAndSubject(t *testing.T) {
	f := seed(t)
	ctx := context.Background()
	c := aiClaim(f, claims.FieldHealth, `"at_risk"`, t0)
	if n, err := InsertClaims(ctx, env.DB, []claims.Claim{c}); err != nil || n != 1 {
		t.Fatalf("first insert: %d %v", n, err)
	}
	if n, err := InsertClaims(ctx, env.DB, []claims.Claim{c}); err != nil || n != 0 {
		t.Fatalf("a re-run must insert nothing (ON CONFLICT DO NOTHING): %d %v", n, err)
	}
	other := aiClaim(f, claims.FieldHealth, `"on_track"`, t0)
	nullSubject := c
	nullSubject.SubjectPersonID = ""
	nullSubjectAgain := nullSubject
	if n, err := InsertClaims(ctx, env.DB, []claims.Claim{other, nullSubject, nullSubjectAgain}); err != nil || n != 2 {
		t.Fatalf("a different value and a NULL subject are distinct claims, but NULL subjects dedupe among themselves: %d %v", n, err)
	}
	if got := scalarQuery(t, `SELECT count(*)::text FROM claims`); got != "3" {
		t.Fatalf("claims = %s, want 3", got)
	}
}

func TestInsertedClaimsRoundTripThroughLoad(t *testing.T) {
	f := seed(t)
	ctx := context.Background()
	exp := t0.Add(48 * time.Hour)
	crm := claims.Claim{AccountID: f.account, FieldPath: claims.FieldNextMeeting, Value: json.RawMessage(`{"event_id":"e1"}`), Standing: claims.CRMExplicit,
		Confidence: 1, SourceActivityID: f.otherActivity, OccurredAt: t0.Add(time.Hour), Extractor: claims.RuleCalendar, ExpiresAt: &exp}
	ai := aiClaim(f, claims.FieldBlockers, `{"text":"SOC2","status":"open"}`, t0)
	if _, err := InsertClaims(ctx, env.DB, []claims.Claim{crm, ai}); err != nil {
		t.Fatal(err)
	}
	got, err := LoadAccountClaims(ctx, env.DB, f.account)
	if err != nil || len(got) != 2 {
		t.Fatalf("%v %v", got, err)
	}
	a, b := got[0], got[1] // ordered by occurred_at
	if a.FieldPath != claims.FieldBlockers || a.Standing != claims.FirstPartyAI || a.Confidence != 0.9 || a.EvidenceQuote != "a quote" ||
		a.SubjectPersonID != f.person || a.SpeakerPersonID != f.person || a.OpportunityID != f.opportunity || a.Status != claims.StatusActive || a.ID == "" || a.ExpiresAt != nil {
		t.Fatalf("ai claim = %+v", a)
	}
	if !claims.SameValue(a.Value, json.RawMessage(`{"status":"open","text":"SOC2"}`)) {
		t.Fatalf("value = %s", a.Value)
	}
	if b.ExpiresAt == nil || !b.ExpiresAt.Equal(exp) || b.OpportunityID != "" || b.SubjectPersonID != "" || b.EvidenceQuote != "" || !b.OccurredAt.Equal(t0.Add(time.Hour)) {
		t.Fatalf("crm claim = %+v", b)
	}
}

func TestDatabaseRanksFirstPartyRecordBetweenCRMAndAI(t *testing.T) {
	f := seed(t)
	ctx := context.Background()
	var cs []claims.Claim
	for i, st := range []claims.Standing{claims.HumanApproved, claims.CRMExplicit, claims.FirstPartyRecord, claims.ThirdParty} {
		c := claims.Claim{AccountID: f.account, FieldPath: claims.FieldStage, Value: json.RawMessage(fmt.Sprintf(`"v%d"`, i)), Standing: st,
			Confidence: 1, SourceActivityID: f.activity, OccurredAt: t0, Extractor: "rule:test@1"}
		cs = append(cs, c)
	}
	cs = append(cs, aiClaim(f, claims.FieldStage, `"ai"`, t0))
	if _, err := InsertClaims(ctx, env.DB, cs); err != nil {
		t.Fatalf("the claims table must accept every standing of ADR-0009: %v", err)
	}
	rows, err := env.DB.Query(`SELECT standing, standing_rank FROM claims ORDER BY standing_rank DESC`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var order []string
	for rows.Next() {
		var s string
		var r int
		if err := rows.Scan(&s, &r); err != nil {
			t.Fatal(err)
		}
		if claims.Standing(s).Rank() != r {
			t.Errorf("%s: Go rank %d, database standing_rank %d", s, claims.Standing(s).Rank(), r)
		}
		order = append(order, s)
	}
	if strings.Join(order, ">") != "human_approved>crm_explicit>first_party_record>first_party_ai>third_party" {
		t.Fatalf("order = %v", order)
	}
}

func TestDatabaseEnforcesEvidenceForAIClaims(t *testing.T) {
	f := seed(t)
	c := aiClaim(f, claims.FieldHealth, `"x"`, t0)
	c.EvidenceQuote = ""
	if _, err := InsertClaims(context.Background(), env.DB, []claims.Claim{c}); err == nil {
		t.Fatal("the claims_ai_needs_evidence constraint must reject an AI claim without a quote")
	}
}

func TestApplyUpdatesRetainsLosersAndLoadStillReturnsThem(t *testing.T) {
	f := seed(t)
	ctx := context.Background()
	crm := claims.Claim{AccountID: f.account, OpportunityID: f.opportunity, FieldPath: claims.FieldStage, Value: json.RawMessage(`"Commercial review"`), Standing: claims.CRMExplicit, // same deal as the AI claim: claims of different deals never compete (ADR-0016)
		Confidence: 1, SourceActivityID: f.otherActivity, OccurredAt: t0, Extractor: claims.RuleCRM}
	ai := aiClaim(f, claims.FieldStage, `"Negotiation"`, t0.Add(-24*time.Hour))
	if _, err := InsertClaims(ctx, env.DB, []claims.Claim{crm, ai}); err != nil {
		t.Fatal(err)
	}
	loaded, _ := LoadAccountClaims(ctx, env.DB, f.account)
	adj := claims.Adjudicate(loaded, t0.Add(time.Hour))
	if len(adj.Updates) != 1 {
		t.Fatalf("updates = %+v", adj.Updates)
	}
	if err := ApplyUpdates(ctx, env.DB, adj.Updates); err != nil {
		t.Fatal(err)
	}
	if got := scalarQuery(t, `SELECT status FROM claims WHERE field_path = 'stage' AND standing = 'first_party_ai'`); got != "outranked" {
		t.Fatalf("status = %s", got)
	}
	reloaded, _ := LoadAccountClaims(ctx, env.DB, f.account)
	if len(reloaded) != 2 {
		t.Fatalf("an outranked claim must be retained and loaded, got %d", len(reloaded))
	}
	// A superseded claim carries superseded_by.
	newer := aiClaim(f, claims.FieldStage, `"Technical evaluation"`, t0.Add(-12*time.Hour))
	newer.Extractor = "llm:fake@extract-v2"
	_, _ = InsertClaims(ctx, env.DB, []claims.Claim{newer})
	loaded, _ = LoadAccountClaims(ctx, env.DB, f.account)
	adj = claims.Adjudicate(loaded, t0.Add(time.Hour))
	if err := ApplyUpdates(ctx, env.DB, adj.Updates); err != nil {
		t.Fatal(err)
	}
	if got := scalarQuery(t, `SELECT status || ':' || (superseded_by IS NOT NULL)::text FROM claims WHERE evidence_quote IS NOT NULL AND value = '"Negotiation"'::jsonb`); got != "outranked:false" {
		t.Fatalf("outranked claims carry no superseded_by, got %s", got)
	}
	if got := scalarQuery(t, `SELECT status || ':' || (superseded_by IS NOT NULL)::text FROM claims WHERE value = '"Technical evaluation"'::jsonb`); got != "outranked:false" {
		t.Fatalf("got %s", got)
	}
	if err := ApplyUpdates(ctx, env.DB, nil); err != nil {
		t.Fatalf("no updates must be a no-op: %v", err)
	}
}

func TestApplyUpdatesNeverTouchesRejectedClaims(t *testing.T) {
	f := seed(t)
	ctx := context.Background()
	c := aiClaim(f, claims.FieldHealth, `"x"`, t0)
	c.Status = claims.StatusRejected
	_, _ = InsertClaims(ctx, env.DB, []claims.Claim{c})
	id := scalarQuery(t, `SELECT id::text FROM claims`)
	if err := ApplyUpdates(ctx, env.DB, map[string]claims.StatusUpdate{id: {Status: claims.StatusActive}}); err != nil {
		t.Fatal(err)
	}
	if got := scalarQuery(t, `SELECT status FROM claims`); got != "rejected" {
		t.Fatalf("a human rejection was overwritten: %s", got)
	}
	if loaded, _ := LoadAccountClaims(ctx, env.DB, f.account); len(loaded) != 0 {
		t.Fatalf("rejected claims must not be loaded: %+v", loaded)
	}
}

func TestSupersededByIsStored(t *testing.T) {
	f := seed(t)
	ctx := context.Background()
	older := aiClaim(f, claims.FieldHealth, `"on_track"`, t0)
	newer := aiClaim(f, claims.FieldHealth, `"at_risk"`, t0.Add(time.Hour))
	_, _ = InsertClaims(ctx, env.DB, []claims.Claim{older, newer})
	loaded, _ := LoadAccountClaims(ctx, env.DB, f.account)
	adj := claims.Adjudicate(loaded, t0.Add(2*time.Hour))
	if err := ApplyUpdates(ctx, env.DB, adj.Updates); err != nil {
		t.Fatal(err)
	}
	if got := scalarQuery(t, `SELECT status FROM claims WHERE value = '"on_track"'::jsonb`); got != "superseded" {
		t.Fatalf("status = %s", got)
	}
	if got := scalarQuery(t, `SELECT (c.superseded_by = n.id)::text FROM claims c, claims n WHERE c.value = '"on_track"'::jsonb AND n.value = '"at_risk"'::jsonb`); got != "true" {
		t.Fatalf("superseded_by not set to the winner: %s", got)
	}
}

func TestExtractionCacheRoundTripAndKeying(t *testing.T) {
	f := seed(t)
	ctx := context.Background()
	cache := Cache{DB: env.DB}
	if _, ok, err := cache.Get(ctx, f.activity, "extract-v1"); err != nil || ok {
		t.Fatalf("empty cache: %v %v", ok, err)
	}
	resp := claims.ExtractResponse{Model: "fake-model", ExtractorVersion: "extract-v1", Dropped: 1,
		Claims: []claims.Candidate{{FieldPath: claims.FieldHealth, Value: json.RawMessage(`"at_risk"`), Confidence: 0.8, EvidenceQuote: "q"}}}
	if err := cache.Put(ctx, f.activity, "extract-v1", resp); err != nil {
		t.Fatal(err)
	}
	if err := cache.Put(ctx, f.activity, "extract-v1", claims.ExtractResponse{Model: "other"}); err != nil {
		t.Fatalf("a second writer must not fail: %v", err)
	}
	got, ok, err := cache.Get(ctx, f.activity, "extract-v1")
	if err != nil || !ok || got.Model != "fake-model" || got.Dropped != 1 || len(got.Claims) != 1 || got.Claims[0].EvidenceQuote != "q" || string(got.Claims[0].Value) != `"at_risk"` {
		t.Fatalf("%+v %v %v", got, ok, err)
	}
	if _, ok, _ := cache.Get(ctx, f.activity, "extract-v2"); ok {
		t.Fatal("a new extractor version must miss the cache")
	}
	if _, ok, _ := cache.Get(ctx, f.otherActivity, "extract-v1"); ok {
		t.Fatal("another activity must miss the cache")
	}
}

func TestExtractionCacheReportsCorruptEntries(t *testing.T) {
	f := seed(t)
	if _, err := env.DB.Exec(`INSERT INTO extraction_cache (activity_id, extractor_version, model, output) VALUES ($1::uuid, 'v', 'm', '"not an object"'::jsonb)`, f.activity); err != nil {
		t.Fatal(err)
	}
	if _, _, err := (Cache{DB: env.DB}).Get(context.Background(), f.activity, "v"); err == nil {
		t.Fatal("a corrupt cache row must be an error, not a silent miss")
	}
}

func TestDirectoryResolvesByMappingThenByPersonRecord(t *testing.T) {
	f := seed(t)
	ctx := context.Background()
	dir := Directory{DB: env.DB}
	if id, ok, err := dir.PersonIDByEmail(ctx, " Priya.Shah@ACME.com "); err != nil || !ok || id != f.person {
		t.Fatalf("by people.primary_email: %q %v %v", id, ok, err)
	}
	mapped := scalarQuery(t, `INSERT INTO people (kind, display_name, primary_email) VALUES ('employee', 'Dana Kim', 'dana.old@ghostvendor.com') RETURNING id::text`)
	if _, err := env.DB.Exec(`INSERT INTO entity_source_mappings (entity_type, entity_id, source_system, source_key, confidence, method) VALUES ('person', $1::uuid, 'email', 'dana@ghostvendor.com', 1, 'seed')`, mapped); err != nil {
		t.Fatal(err)
	}
	if id, ok, err := dir.PersonIDByEmail(ctx, "dana@ghostvendor.com"); err != nil || !ok || id != mapped {
		t.Fatalf("by mapping: %q %v %v", id, ok, err)
	}
	for _, in := range []string{"nobody@acme.com", "", "  "} {
		if id, ok, err := dir.PersonIDByEmail(ctx, in); err != nil || ok || id != "" {
			t.Errorf("%q resolved to %q", in, id)
		}
	}
}

func TestErrorsNameTheirContext(t *testing.T) {
	_, err := LoadAccountClaims(context.Background(), env.DB, "not-a-uuid")
	if err == nil || !strings.Contains(err.Error(), "claimstore") {
		t.Fatalf("err = %v", err)
	}
	if _, err := InsertClaims(context.Background(), env.DB, []claims.Claim{{AccountID: "nope"}}); err == nil {
		t.Fatal("bad account id accepted")
	}
}
