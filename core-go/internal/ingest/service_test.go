package ingest_test

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/clock"
	"github.com/harneet2512/gtm-work/core-go/internal/dedupe"
	"github.com/harneet2512/gtm-work/core-go/internal/ingest"
	"github.com/harneet2512/gtm-work/core-go/internal/normalize"
)

func TestNewServiceValidatesOptions(t *testing.T) {
	cases := []struct {
		name string
		db   bool
		opts ingest.Options
	}{
		{"nil database", false, ingest.Options{Debounce: time.Second, MaxWait: time.Second}},
		{"negative debounce", true, ingest.Options{Debounce: -time.Second, MaxWait: time.Second}},
		{"max wait below debounce", true, ingest.Options{Debounce: 2 * time.Second, MaxWait: time.Second}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			db := env.DB
			if !tc.db {
				db = nil
			}
			if _, err := ingest.NewService(db, tc.opts); err == nil {
				t.Fatal("invalid options accepted")
			}
		})
	}
	if _, err := ingest.NewService(env.DB, ingest.Options{}); err != nil {
		t.Fatalf("zero debounce/max wait with defaults must be valid: %v", err)
	}
}

func TestIngestNewEventWritesAllRows(t *testing.T) {
	resetDB(t)
	acct := seedAccount(t, "Acme", "acme.com")
	clk := clock.NewFixed(t0)
	svc := newService(t, clk, ingest.Options{})
	ev := inboundEmail(t, "m1", "Marco.Ruiz@acme.com", "opp:AC-4-EXP", t0.Add(-time.Hour))

	res := mustIngest(t, svc, ev)

	if res.Duplicate || res.ActivityID == "" || res.SourceEventID == "" {
		t.Fatalf("unexpected result %+v", res)
	}
	if res.AccountID == nil || *res.AccountID != acct {
		t.Fatalf("account_id = %v, want %s", res.AccountID, acct)
	}
	if res.RecomputeDueAt == nil || !res.RecomputeDueAt.Equal(t0.Add(testDebounce)) {
		t.Fatalf("recompute_due_at = %v, want %v", res.RecomputeDueAt, t0.Add(testDebounce))
	}

	// source_events: payload retained, key derived, clock used.
	var key, system, object, event string
	var delivery int
	var received time.Time
	var payload []byte
	err := env.DB.QueryRow(`SELECT idempotency_key, source_system, source_object_id, source_event_key, delivery_count, received_at, payload
		FROM source_events WHERE id = $1::uuid`, res.SourceEventID).Scan(&key, &system, &object, &event, &delivery, &received, &payload)
	if err != nil {
		t.Fatal(err)
	}
	if key != dedupe.IdempotencyKey("email", "m1", "received") || system != "email" || object != "m1" || event != "received" {
		t.Errorf("source event identity wrong: %s %s %s %s", key, system, object, event)
	}
	if delivery != 1 || !received.Equal(t0) {
		t.Errorf("delivery_count=%d received_at=%v", delivery, received)
	}
	var gotPayload, wantPayload map[string]any
	if err := json.Unmarshal(payload, &gotPayload); err != nil {
		t.Fatal(err)
	}
	_ = json.Unmarshal(ev.Payload, &wantPayload)
	if !reflect.DeepEqual(gotPayload, wantPayload) {
		t.Errorf("payload not retained verbatim:\n got %v\nwant %v", gotPayload, wantPayload)
	}

	// activities
	var actType, hint, oppHint, summary, body, perms, prov, srcEvent string
	var occurred, ingested time.Time
	var actAccount string
	err = env.DB.QueryRow(`SELECT activity_type, account_hint, opportunity_hint, summary, body_text, permissions::text, provenance::text,
		source_event_id::text, occurred_at, ingested_at, account_id::text FROM activities WHERE id = $1::uuid`, res.ActivityID).
		Scan(&actType, &hint, &oppHint, &summary, &body, &perms, &prov, &srcEvent, &occurred, &ingested, &actAccount)
	if err != nil {
		t.Fatal(err)
	}
	if actType != "EmailReceived" || hint != "acme.com" || oppHint != "opp:AC-4-EXP" || body != "Body of m1" || srcEvent != res.SourceEventID || actAccount != acct {
		t.Errorf("activity row wrong: %s %s %s %q %s %s", actType, hint, oppHint, body, srcEvent, actAccount)
	}
	if !strings.HasPrefix(summary, "Email from marco.ruiz@acme.com") {
		t.Errorf("summary = %q", summary)
	}
	if !occurred.Equal(t0.Add(-time.Hour)) || !ingested.Equal(t0) {
		t.Errorf("occurred_at=%v ingested_at=%v", occurred, ingested)
	}
	var permsJSON, provJSON map[string]string
	_ = json.Unmarshal([]byte(perms), &permsJSON)
	_ = json.Unmarshal([]byte(prov), &provJSON)
	if permsJSON["visibility"] != "org" || provJSON["source_system"] != "email" || provJSON["source_object_id"] != "m1" || provJSON["connector"] != "test" {
		t.Errorf("permissions=%s provenance=%s", perms, prov)
	}

	// participants
	if got := count(t, "activity_participants"); got != 3 {
		t.Errorf("participants = %d, want 3 (from, to, cc)", got)
	}
	if got := queryString(t, `SELECT string_agg(role || '=' || raw_identity, ',' ORDER BY role, raw_identity) FROM activity_participants`); got !=
		"cc=priya.shah@acme.com,from=marco.ruiz@acme.com,to=dana@vendor.example" {
		t.Errorf("participants = %s", got)
	}

	// outbox
	var ids string
	var due, first time.Time
	err = env.DB.QueryRow(`SELECT activity_ids::text, due_at, first_enqueued_at FROM recompute_jobs WHERE claimed_at IS NULL`).Scan(&ids, &due, &first)
	if err != nil {
		t.Fatal(err)
	}
	if ids != "{"+res.ActivityID+"}" || !first.Equal(t0) || !due.Equal(t0.Add(testDebounce)) {
		t.Errorf("job ids=%s first=%v due=%v", ids, first, due)
	}
	if count(t, "unresolved_activities") != 0 {
		t.Error("resolved activity listed as unresolved")
	}
}

func TestDuplicateDeliveryChangesNothingButTheCounter(t *testing.T) {
	resetDB(t)
	seedAccount(t, "Acme", "acme.com")
	clk := clock.NewFixed(t0)
	svc := newService(t, clk, ingest.Options{})
	ev := inboundEmail(t, "m1", "marco@acme.com", "", t0)

	first := mustIngest(t, svc, ev)
	before := tableCounts(t)
	var jobDue time.Time
	if err := env.DB.QueryRow(`SELECT due_at FROM recompute_jobs`).Scan(&jobDue); err != nil {
		t.Fatal(err)
	}

	clk.Advance(5 * time.Second)
	second := mustIngest(t, svc, ev)
	clk.Advance(5 * time.Second)
	third := mustIngest(t, svc, ev)

	if !second.Duplicate || !third.Duplicate {
		t.Fatalf("replays not flagged duplicate: %+v %+v", second, third)
	}
	for _, r := range []ingest.Result{second, third} {
		if r.ActivityID != first.ActivityID || r.SourceEventID != first.SourceEventID {
			t.Errorf("replay returned different ids: %+v vs %+v", r, first)
		}
		if r.AccountID == nil || first.AccountID == nil || *r.AccountID != *first.AccountID {
			t.Errorf("replay lost the account id: %+v", r)
		}
		if r.RecomputeDueAt != nil {
			t.Errorf("replay must not schedule recompute, got %v", r.RecomputeDueAt)
		}
	}
	if after := tableCounts(t); !reflect.DeepEqual(before, after) {
		t.Fatalf("row counts changed on replay: before %v after %v", before, after)
	}

	var delivery int
	var last time.Time
	if err := env.DB.QueryRow(`SELECT delivery_count, last_delivered_at FROM source_events`).Scan(&delivery, &last); err != nil {
		t.Fatal(err)
	}
	if delivery != 3 || !last.Equal(t0.Add(10*time.Second)) {
		t.Errorf("delivery_count=%d last_delivered_at=%v", delivery, last)
	}
	var dueAfter time.Time
	var nIDs int
	if err := env.DB.QueryRow(`SELECT due_at, cardinality(activity_ids) FROM recompute_jobs`).Scan(&dueAfter, &nIDs); err != nil {
		t.Fatal(err)
	}
	if !dueAfter.Equal(jobDue) || nIDs != 1 {
		t.Errorf("outbox touched by replay: due %v -> %v, %d ids", jobDue, dueAfter, nIDs)
	}
}

func TestSameObjectDifferentEventKeyIsANewActivity(t *testing.T) {
	resetDB(t)
	seedAccount(t, "Acme", "acme.com")
	svc := newService(t, clock.NewFixed(t0), ingest.Options{})
	sched := rawEvent(t, "calendar", "ev1", "scheduled", calendarPayload("scheduled"))
	sched.OccurredAt = &t0
	done := rawEvent(t, "calendar", "ev1", "completed", calendarPayload("completed"))

	a := mustIngest(t, svc, sched)
	b := mustIngest(t, svc, done)

	if a.Duplicate || b.Duplicate || a.ActivityID == b.ActivityID {
		t.Fatalf("distinct event keys collapsed: %+v %+v", a, b)
	}
	if got := count(t, "source_events"); got != 2 {
		t.Errorf("source_events = %d, want 2", got)
	}
}

func calendarPayload(status string) map[string]any {
	return map[string]any{
		"kind": "calendar_event", "event_id": "ev1", "title": "Sync", "status": status,
		"start": "2026-10-01T14:00:00Z", "end": "2026-10-01T14:45:00Z",
		"organizer": map[string]any{"email": "dana@vendor.example"},
		"attendees": []map[string]any{{"email": "marco@acme.com", "response": "accepted"}},
	}
}

func TestUnresolvedActivityIsRecordedWithReason(t *testing.T) {
	resetDB(t)
	svc := newService(t, clock.NewFixed(t0), ingest.Options{})

	unknown := mustIngest(t, svc, inboundEmail(t, "m1", "sam@newco.io", "", t0))
	internal := rawEvent(t, "email", "m2", "sent", map[string]any{
		"kind": "email", "message_id": "m2", "thread_id": "t2", "direction": "outbound",
		"from": map[string]any{"email": "dana@vendor.example"}, "to": []map[string]any{{"email": "lee@vendor.example"}},
		"date": t0.Format(time.RFC3339), "subject": "internal", "body_text": "x",
	})
	noHint := mustIngest(t, svc, internal)

	for name, res := range map[string]ingest.Result{"unknown domain": unknown, "internal only": noHint} {
		if res.AccountID != nil || res.RecomputeDueAt != nil || res.Duplicate {
			t.Errorf("%s: result %+v", name, res)
		}
	}
	reasons := queryString(t, `SELECT string_agg(a.source_object_id || '=' || u.reason, ',' ORDER BY a.source_object_id)
		FROM unresolved_activities u JOIN activities a ON a.id = u.activity_id`)
	if reasons != "m1=account_not_found,m2=no_account_hint" {
		t.Errorf("unresolved reasons = %s", reasons)
	}
	if got := count(t, "recompute_jobs"); got != 0 {
		t.Errorf("unresolved activities must not enqueue recompute, found %d jobs", got)
	}
	if got := count(t, "activities"); got != 2 {
		t.Errorf("activities = %d, want 2 (still stored)", got)
	}
}

func TestParticipantsLinkToPeopleViaCurrentMappings(t *testing.T) {
	resetDB(t)
	acct := seedAccount(t, "Acme", "acme.com")
	marco := seedPerson(t, "Marco Ruiz", "marco.ruiz@acme.com")
	priya := seedPerson(t, "Priya Shah", "priya.shah@acme.com")
	caller := seedPerson(t, "Caller Two", "caller2@acme.com")
	seedMapping(t, "person", marco, "email", "marco.ruiz@acme.com")
	seedMapping(t, "person", caller, "call", "C-1:speaker_02")
	// A closed mapping must be ignored.
	if _, err := env.DB.Exec(`INSERT INTO entity_source_mappings (entity_type, entity_id, source_system, source_key, confidence, method, valid_from, valid_to)
		VALUES ('person', $1::uuid, 'email', 'priya.shah@acme.com', 1, 'seed', now() - interval '2 days', now() - interval '1 day')`, priya); err != nil {
		t.Fatal(err)
	}
	_ = acct
	svc := newService(t, clock.NewFixed(t0), ingest.Options{})

	mustIngest(t, svc, inboundEmail(t, "m1", "marco.ruiz@acme.com", "", t0))
	callEv := rawEvent(t, "call", "C-1", "ended", map[string]any{
		"kind": "call", "call_id": "C-1", "calendar_event_id": nil, "started_at": "2026-10-01T14:00:00Z", "ended_at": "2026-10-01T14:30:00Z",
		"speakers": []map[string]any{{"label": "speaker_01", "email": "marco.ruiz@acme.com"}, {"label": "speaker_02"}},
	})
	mustIngest(t, svc, callEv)

	got := queryString(t, `SELECT string_agg(a.source_object_id || '/' || p.raw_identity || '/' || p.role || '=' || coalesce(p.person_id::text, 'null'), E'\n' ORDER BY a.source_object_id, p.raw_identity, p.role)
		FROM activity_participants p JOIN activities a ON a.id = p.activity_id`)
	want := strings.Join([]string{
		"C-1/call:C-1:speaker_01/speaker=null",
		"C-1/call:C-1:speaker_02/speaker=" + caller,
		"C-1/marco.ruiz@acme.com/speaker=" + marco,
		"m1/dana@vendor.example/to=null",
		"m1/marco.ruiz@acme.com/from=" + marco,
		"m1/priya.shah@acme.com/cc=null",
	}, "\n")
	if got != want {
		t.Errorf("participant links:\n got:\n%s\nwant:\n%s", got, want)
	}
}

func TestPartialFailureRollsBackEverything(t *testing.T) {
	resetDB(t)
	seedAccount(t, "Acme", "acme.com")
	boom := errors.New("resolver exploded")
	svc := newService(t, clock.NewFixed(t0), ingest.Options{Resolver: failingResolver{err: boom}})

	_, err := svc.Ingest(context.Background(), inboundEmail(t, "m1", "marco@acme.com", "", t0))

	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want wrapped resolver error", err)
	}
	if got := tableCounts(t); got["source_events"] != 0 || got["activities"] != 0 || got["recompute_jobs"] != 0 {
		t.Fatalf("partial writes survived: %v", got)
	}

	// A resolver that points at a non-existent account violates the FK after the source
	// event is inserted; nothing may remain.
	svc = newService(t, clock.NewFixed(t0), ingest.Options{Resolver: staticResolver{res: ingest.Resolution{AccountID: "00000000-0000-0000-0000-000000000bad"}}})
	if _, err := svc.Ingest(context.Background(), inboundEmail(t, "m2", "marco@acme.com", "", t0)); err == nil {
		t.Fatal("dangling account id accepted")
	}
	if got := tableCounts(t); got["source_events"] != 0 || got["activities"] != 0 {
		t.Fatalf("partial writes survived FK failure: %v", got)
	}
}

func TestInvalidEventsAreRejectedBeforeAnyWrite(t *testing.T) {
	resetDB(t)
	svc := newService(t, clock.NewFixed(t0), ingest.Options{})
	cases := map[string]normalize.SourceEvent{
		"unknown system": {SourceSystem: "fax", SourceObjectID: "1", SourceEventKey: "x", Payload: json.RawMessage(`{}`)},
		"wrong kind":     {SourceSystem: "email", SourceObjectID: "1", SourceEventKey: "received", Payload: json.RawMessage(`{"kind":"call"}`)},
		"unmapped combo": {SourceSystem: "marketing", SourceObjectID: "1", SourceEventKey: "x", Payload: json.RawMessage(`{}`)},
	}
	for name, ev := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := svc.Ingest(context.Background(), ev)
			if !normalize.IsValidation(err) {
				t.Fatalf("err = %v, want a validation error", err)
			}
		})
	}
	if got := count(t, "source_events"); got != 0 {
		t.Fatalf("rejected events left %d source_events", got)
	}
}

func TestCancelledContextFailsWithoutWrites(t *testing.T) {
	resetDB(t)
	seedAccount(t, "Acme", "acme.com")
	svc := newService(t, clock.NewFixed(t0), ingest.Options{})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := svc.Ingest(ctx, inboundEmail(t, "m1", "marco@acme.com", "", t0)); err == nil {
		t.Fatal("cancelled context accepted")
	}
	if got := count(t, "source_events"); got != 0 {
		t.Fatalf("source_events = %d", got)
	}
}

func TestSpecialCharactersSurviveStorage(t *testing.T) {
	resetDB(t)
	seedAccount(t, "Acme", "acme.com")
	svc := newService(t, clock.NewFixed(t0), ingest.Options{})
	ev := rawEvent(t, "email", "m'; DROP TABLE accounts;--", "received", map[string]any{
		"kind": "email", "message_id": "m'; DROP TABLE accounts;--", "thread_id": "t", "direction": "inbound",
		"from": map[string]any{"email": "marco@acme.com", "name": "Zoë \U0001F680 O'Brien"}, "to": []map[string]any{{"email": "dana@vendor.example"}},
		"date": t0.Format(time.RFC3339), "subject": "ünïcode \U0001F680", "body_text": "line1\nline2 \"quoted\" \\ back",
	})
	res := mustIngest(t, svc, ev)
	if got := queryString(t, `SELECT body_text FROM activities WHERE id = $1::uuid`, res.ActivityID); got != "line1\nline2 \"quoted\" \\ back" {
		t.Errorf("body_text = %q", got)
	}
	if got := queryString(t, `SELECT display_name FROM activity_participants WHERE role = 'from'`); got != "Zoë \U0001F680 O'Brien" {
		t.Errorf("display_name = %q", got)
	}
	if count(t, "accounts") != 1 {
		t.Fatal("accounts table was tampered with")
	}
}

type failingResolver struct{ err error }

func (f failingResolver) Resolve(context.Context, ingest.Querier, normalize.Activity) (ingest.Resolution, error) {
	return ingest.Resolution{}, f.err
}

type staticResolver struct{ res ingest.Resolution }

func (s staticResolver) Resolve(context.Context, ingest.Querier, normalize.Activity) (ingest.Resolution, error) {
	return s.res, nil
}
