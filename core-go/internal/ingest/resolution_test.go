package ingest_test

import (
	"context"
	"testing"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/clock"
	"github.com/harneet2512/gtm-work/core-go/internal/ingest"
	"github.com/harneet2512/gtm-work/core-go/internal/normalize"
)

func crmStage(t *testing.T, recordID, accountRef string) normalize.SourceEvent {
	t.Helper()
	payload := map[string]any{
		"kind": "crm_change", "object_type": "Opportunity", "record_id": recordID,
		"changed_at": t0.Format(time.RFC3339), "changed_by": "dana@vendor.example",
		"fields": map[string]any{"StageName": map[string]any{"old": "A", "new": "Negotiation"}},
	}
	if accountRef != "" {
		payload["account_record_id"] = accountRef
	}
	return rawEvent(t, "crm", recordID, "field:StageName:Negotiation", payload)
}

func slackMsg(t *testing.T, channel, ts string) normalize.SourceEvent {
	t.Helper()
	return rawEvent(t, "slack", channel+":"+ts, "posted", map[string]any{
		"kind": "slack_message", "channel": channel, "ts": ts, "user": "U1", "text": "hello",
	})
}

type seeded struct{ acct, other, opp string }

// seedForeignOpportunity maps "opp:OTHER" (crm) to an opportunity of the OTHER account.
func seedForeignOpportunity(t *testing.T, s seeded) {
	t.Helper()
	otherOpp := seedOpportunity(t, s.other, "Other deal")
	seedMapping(t, "opportunity", otherOpp, "crm", "opp:OTHER")
}

// plainEmail is an inbound email with no CC, so only the sender (and our own recipient)
// can produce an account hint.
func plainEmail(t *testing.T, id, from, oppRef string) normalize.SourceEvent {
	t.Helper()
	payload := map[string]any{
		"kind": "email", "message_id": id, "thread_id": "thr-" + id, "direction": "inbound",
		"from": map[string]any{"email": from}, "to": []map[string]any{{"email": "dana@vendor.example"}},
		"date": t0.Format(time.RFC3339), "subject": "s", "body_text": "b",
	}
	if oppRef != "" {
		payload["crm_opportunity_ref"] = oppRef
	}
	return rawEvent(t, "email", id, "received", payload)
}

func TestAccountResolutionRules(t *testing.T) {
	cases := []struct {
		name      string
		seed      func(t *testing.T, s seeded)
		event     func(t *testing.T) normalize.SourceEvent
		wantAcct  func(s seeded) string // "" = unresolved
		wantOpp   func(s seeded) string // "" = none
		wantState string                // unresolved reason when unresolved
	}{
		{
			name:     "accounts.domain matches the hint",
			event:    func(t *testing.T) normalize.SourceEvent { return inboundEmail(t, "m1", "marco@acme.com", "", t0) },
			wantAcct: func(s seeded) string { return s.acct },
		},
		{
			name: "domain mapping matches when accounts.domain is unset",
			seed: func(t *testing.T, s seeded) {
				seedMapping(t, "account", s.other, "domain", "mapped.io")
			},
			event:    func(t *testing.T) normalize.SourceEvent { return inboundEmail(t, "m1", "x@mapped.io", "", t0) },
			wantAcct: func(s seeded) string { return s.other },
		},
		{
			name: "crm account reference resolves through a crm mapping",
			seed: func(t *testing.T, s seeded) {
				seedMapping(t, "account", s.acct, "crm", "account:AC-4")
			},
			event:    func(t *testing.T) normalize.SourceEvent { return crmStage(t, "opp:1", "account:AC-4") },
			wantAcct: func(s seeded) string { return s.acct },
		},
		{
			name: "slack deal channel resolves through a slack mapping",
			seed: func(t *testing.T, s seeded) {
				seedMapping(t, "account", s.acct, "slack", "#deal-acme")
			},
			event:    func(t *testing.T) normalize.SourceEvent { return slackMsg(t, "#deal-acme", "1759329720.000200") },
			wantAcct: func(s seeded) string { return s.acct },
		},
		{
			name: "created CRM account resolves through its Website domain when the record id is unknown",
			event: func(t *testing.T) normalize.SourceEvent {
				return rawEvent(t, "crm", "account:NEW-1", "created", map[string]any{
					"kind": "crm_change", "object_type": "Account", "record_id": "account:NEW-1", "account_record_id": "account:NEW-1",
					"changed_at": t0.Format(time.RFC3339), "changed_by": "dana@vendor.example", "created": true,
					"fields": map[string]any{"Name": map[string]any{"new": "Acme Corp"}, "Website": map[string]any{"new": "https://www.acme.com"}},
				})
			},
			wantAcct: func(s seeded) string { return s.acct },
		},
		{
			name: "the primary hint wins over the secondary domain hint",
			seed: func(t *testing.T, s seeded) {
				seedMapping(t, "account", s.other, "crm", "account:NEW-1")
			},
			event: func(t *testing.T) normalize.SourceEvent {
				return rawEvent(t, "crm", "account:NEW-1", "created", map[string]any{
					"kind": "crm_change", "object_type": "Account", "record_id": "account:NEW-1", "account_record_id": "account:NEW-1",
					"changed_at": t0.Format(time.RFC3339), "changed_by": "dana@vendor.example", "created": true,
					"fields": map[string]any{"Website": map[string]any{"new": "acme.com"}},
				})
			},
			wantAcct: func(s seeded) string { return s.other },
		},
		{
			name: "unknown sender plus a foreign opportunity ref stays unresolved",
			seed: seedForeignOpportunity,
			event: func(t *testing.T) normalize.SourceEvent {
				return plainEmail(t, "m1", "stranger@unknown.example", "opp:OTHER")
			},
			wantAcct:  func(seeded) string { return "" },
			wantState: "account_not_found",
		},
		{
			name:      "free-mail sender plus a foreign opportunity ref stays unresolved",
			seed:      seedForeignOpportunity,
			event:     func(t *testing.T) normalize.SourceEvent { return plainEmail(t, "m1", "someone@gmail.com", "opp:OTHER") },
			wantAcct:  func(seeded) string { return "" },
			wantState: "no_account_hint", // a personal mail domain is no account hint at all
		},
		{
			name:     "known sender plus a foreign opportunity ref gets the domain's account without the opportunity",
			seed:     seedForeignOpportunity,
			event:    func(t *testing.T) normalize.SourceEvent { return plainEmail(t, "m1", "marco@acme.com", "opp:OTHER") },
			wantAcct: func(s seeded) string { return s.acct },
		},
		{
			name: "an unknown sender cannot pin an account through a mapped email thread",
			seed: func(t *testing.T, s seeded) {
				seedMapping(t, "opportunity", s.opp, "email_thread", "thr-m1")
			},
			event:     func(t *testing.T) normalize.SourceEvent { return plainEmail(t, "m1", "stranger@unknown.example", "") },
			wantAcct:  func(seeded) string { return "" },
			wantState: "account_not_found",
		},
		{
			name: "explicit opportunity refs are looked up in crm mappings only",
			seed: func(t *testing.T, s seeded) {
				seedMapping(t, "opportunity", s.opp, "email", "opp:AC-4-EXP")
			},
			event:    func(t *testing.T) normalize.SourceEvent { return plainEmail(t, "m1", "marco@acme.com", "opp:AC-4-EXP") },
			wantAcct: func(s seeded) string { return s.acct },
		},
		{
			name: "thread ids are looked up in email_thread mappings only",
			seed: func(t *testing.T, s seeded) {
				seedMapping(t, "opportunity", s.opp, "crm", "thr-m1")
			},
			event:    func(t *testing.T) normalize.SourceEvent { return plainEmail(t, "m1", "marco@acme.com", "") },
			wantAcct: func(s seeded) string { return s.acct },
		},
		{
			name: "a calendar event's opportunity ref cannot pin an account either",
			seed: func(t *testing.T, s seeded) {
				seedMapping(t, "opportunity", s.opp, "crm", "opp:AC-4-EXP")
			},
			event: func(t *testing.T) normalize.SourceEvent {
				ev := rawEvent(t, "calendar", "ev-x", "scheduled", map[string]any{
					"kind": "calendar_event", "event_id": "ev-x", "title": "t", "status": "scheduled",
					"start": "2026-10-01T14:00:00Z", "end": "2026-10-01T14:45:00Z",
					"organizer":           map[string]any{"email": "dana@vendor.example"},
					"attendees":           []map[string]any{{"email": "x@unknown.example", "response": "accepted"}},
					"crm_opportunity_ref": "opp:AC-4-EXP",
				})
				ev.OccurredAt = &t0
				return ev
			},
			wantAcct:  func(seeded) string { return "" },
			wantState: "account_not_found",
		},
		{
			name: "mapping to a different entity type is ignored",
			seed: func(t *testing.T, s seeded) {
				seedMapping(t, "person", s.acct, "crm", "account:AC-4")
			},
			event:     func(t *testing.T) normalize.SourceEvent { return crmStage(t, "opp:1", "account:AC-4") },
			wantAcct:  func(seeded) string { return "" },
			wantState: "account_not_found",
		},
		{
			name: "opportunity hint resolves the opportunity under the account",
			seed: func(t *testing.T, s seeded) {
				seedMapping(t, "opportunity", s.opp, "crm", "opp:AC-4-EXP")
			},
			event: func(t *testing.T) normalize.SourceEvent {
				return inboundEmail(t, "m1", "marco@acme.com", "opp:AC-4-EXP", t0)
			},
			wantAcct: func(s seeded) string { return s.acct },
			wantOpp:  func(s seeded) string { return s.opp },
		},
		{
			name: "email thread id can map to an opportunity",
			seed: func(t *testing.T, s seeded) {
				seedMapping(t, "opportunity", s.opp, "email_thread", "thr-m1")
			},
			event:    func(t *testing.T) normalize.SourceEvent { return inboundEmail(t, "m1", "marco@acme.com", "", t0) },
			wantAcct: func(s seeded) string { return s.acct },
			wantOpp:  func(s seeded) string { return s.opp },
		},
		{
			name: "opportunity belonging to another account is not attached",
			seed: func(t *testing.T, s seeded) {
				otherOpp := seedOpportunity(t, s.other, "Other deal")
				seedMapping(t, "opportunity", otherOpp, "crm", "opp:OTHER")
			},
			event: func(t *testing.T) normalize.SourceEvent {
				return inboundEmail(t, "m1", "marco@acme.com", "opp:OTHER", t0)
			},
			wantAcct: func(s seeded) string { return s.acct },
		},
		{
			name: "opportunity alone resolves the account when the account hint is missing",
			seed: func(t *testing.T, s seeded) {
				seedMapping(t, "opportunity", s.opp, "crm", "opp:AC-4-EXP")
			},
			event:    func(t *testing.T) normalize.SourceEvent { return crmStage(t, "opp:AC-4-EXP", "") },
			wantAcct: func(s seeded) string { return s.acct },
			wantOpp:  func(s seeded) string { return s.opp },
		},
		{
			name: "our own domain is never used as a hint, even if an account carries it",
			seed: func(t *testing.T, s seeded) {
				if _, err := env.DB.Exec(`INSERT INTO accounts (name, domain) VALUES ('Us', 'vendor.example')`); err != nil {
					t.Fatal(err)
				}
			},
			event: func(t *testing.T) normalize.SourceEvent {
				return rawEvent(t, "email", "m9", "sent", map[string]any{
					"kind": "email", "message_id": "m9", "thread_id": "t9", "direction": "outbound",
					"from": map[string]any{"email": "dana@vendor.example"}, "to": []map[string]any{{"email": "lee@vendor.example"}},
					"date": t0.Format(time.RFC3339), "subject": "internal", "body_text": "x",
				})
			},
			wantAcct:  func(seeded) string { return "" },
			wantState: "no_account_hint",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resetDB(t)
			s := seeded{
				acct:  seedAccount(t, "Acme", "acme.com"),
				other: seedAccount(t, "Other", ""),
			}
			s.opp = seedOpportunity(t, s.acct, "Acme expansion")
			if tc.seed != nil {
				tc.seed(t, s)
			}
			svc := newService(t, clock.NewFixed(t0), ingest.Options{})

			res := mustIngest(t, svc, tc.event(t))

			wantAcct := tc.wantAcct(s)
			switch {
			case wantAcct == "" && res.AccountID != nil:
				t.Fatalf("resolved to %s, want unresolved", *res.AccountID)
			case wantAcct != "" && (res.AccountID == nil || *res.AccountID != wantAcct):
				t.Fatalf("account = %v, want %s", res.AccountID, wantAcct)
			}
			gotOpp := queryString(t, `SELECT opportunity_id::text FROM activities WHERE id = $1::uuid`, res.ActivityID)
			wantOpp := ""
			if tc.wantOpp != nil {
				wantOpp = tc.wantOpp(s)
			}
			if gotOpp != wantOpp {
				t.Errorf("opportunity_id = %q, want %q", gotOpp, wantOpp)
			}
			if wantAcct == "" {
				if got := queryString(t, `SELECT reason FROM unresolved_activities WHERE activity_id = $1::uuid`, res.ActivityID); got != tc.wantState {
					t.Errorf("unresolved reason = %q, want %q", got, tc.wantState)
				}
			}
		})
	}
}

func TestCallInheritsAccountAndOpportunityFromItsCalendarEvent(t *testing.T) {
	resetDB(t)
	acct := seedAccount(t, "Acme", "acme.com")
	opp := seedOpportunity(t, acct, "Acme expansion")
	seedMapping(t, "opportunity", opp, "crm", "opp:AC-4-EXP")
	svc := newService(t, clock.NewFixed(t0), ingest.Options{})

	callEv := func() normalize.SourceEvent {
		return rawEvent(t, "call", "C-9", "ended", map[string]any{
			"kind": "call", "call_id": "C-9", "calendar_event_id": "ev1", "started_at": "2026-10-01T14:00:00Z",
			"ended_at": "2026-10-01T14:30:00Z", "speakers": []map[string]any{{"label": "speaker_01"}},
		})
	}

	// The call arrives before its calendar event: unresolved, not guessed.
	early := mustIngest(t, svc, callEv())
	if early.AccountID != nil {
		t.Fatalf("call resolved without a calendar activity: %+v", early)
	}

	sched := rawEvent(t, "calendar", "ev1", "scheduled", map[string]any{
		"kind": "calendar_event", "event_id": "ev1", "title": "Sync", "status": "scheduled",
		"start": "2026-10-01T14:00:00Z", "end": "2026-10-01T14:45:00Z",
		"organizer":           map[string]any{"email": "dana@vendor.example"},
		"attendees":           []map[string]any{{"email": "marco@acme.com", "response": "accepted"}},
		"crm_opportunity_ref": "opp:AC-4-EXP",
	})
	sched.OccurredAt = &t0
	mustIngest(t, svc, sched)

	late := rawEvent(t, "call", "C-9", "transcript_ready", map[string]any{
		"kind": "call", "call_id": "C-9", "calendar_event_id": "ev1", "started_at": "2026-10-01T14:00:00Z",
		"ended_at": "2026-10-01T14:30:00Z", "speakers": []map[string]any{{"label": "speaker_01"}},
		"transcript": []map[string]any{{"speaker": "speaker_01", "offset_s": 0, "text": "hello"}},
	})
	res := mustIngest(t, svc, late)
	if res.AccountID == nil || *res.AccountID != acct {
		t.Fatalf("call did not follow its calendar event: %+v", res)
	}
	if got := queryString(t, `SELECT opportunity_id::text FROM activities WHERE id = $1::uuid`, res.ActivityID); got != opp {
		t.Errorf("call opportunity = %q, want %q", got, opp)
	}
}

func callEvent(t *testing.T, id, calendarID, speakerEmail string) normalize.SourceEvent {
	t.Helper()
	speaker := map[string]any{"label": "speaker_01"}
	if speakerEmail != "" {
		speaker["email"] = speakerEmail
	}
	var cal any
	if calendarID != "" {
		cal = calendarID
	}
	return rawEvent(t, "call", id, "ended", map[string]any{
		"kind": "call", "call_id": id, "calendar_event_id": cal, "started_at": "2026-10-01T14:00:00Z",
		"ended_at": "2026-10-01T14:30:00Z", "speakers": []map[string]any{speaker},
	})
}

func TestCallFollowsItsCalendarEventOnlyWhenCorroborated(t *testing.T) {
	resetDB(t)
	acme := seedAccount(t, "Acme", "acme.com")
	beta := seedAccount(t, "Beta", "beta.io")
	svc := newService(t, clock.NewFixed(t0), ingest.Options{})
	sched := rawEvent(t, "calendar", "ev1", "scheduled", calendarPayload("scheduled")) // attendee marco@acme.com
	sched.OccurredAt = &t0
	if res := mustIngest(t, svc, sched); res.AccountID == nil || *res.AccountID != acme {
		t.Fatalf("calendar event did not resolve to Acme: %+v", res)
	}
	reason := func(activityID string) string {
		return queryString(t, `SELECT reason FROM unresolved_activities WHERE activity_id = $1::uuid`, activityID)
	}

	// The calendar activity's account later disagrees with its attendees (e.g. a remap).
	if _, err := env.DB.Exec(`UPDATE activities SET account_id = $1::uuid WHERE source_system = 'calendar'`, beta); err != nil {
		t.Fatal(err)
	}

	mismatch := mustIngest(t, svc, callEvent(t, "C-1", "ev1", ""))
	if mismatch.AccountID != nil || reason(mismatch.ActivityID) != "calendar_account_mismatch" {
		t.Errorf("uncorroborated call attached: %+v reason %q", mismatch, reason(mismatch.ActivityID))
	}

	byAttendee := mustIngest(t, svc, callEvent(t, "C-2", "ev1", "someone@beta.io"))
	if byAttendee.AccountID == nil || *byAttendee.AccountID != beta {
		t.Errorf("an external speaker from the account's domain must corroborate: %+v", byAttendee)
	}

	// A call that names an unknown calendar event falls back to its speakers' domain.
	viaDomain := mustIngest(t, svc, callEvent(t, "C-3", "gcal-missing", "pat@acme.com"))
	if viaDomain.AccountID == nil || *viaDomain.AccountID != acme {
		t.Errorf("domain fallback failed: %+v", viaDomain)
	}
}

func TestCustomResolverIsHonoured(t *testing.T) {
	resetDB(t)
	acct := seedAccount(t, "Acme", "")
	svc := newService(t, clock.NewFixed(t0), ingest.Options{Resolver: staticResolver{res: ingest.Resolution{AccountID: acct}}})

	res := mustIngest(t, svc, inboundEmail(t, "m1", "stranger@unknown.example", "", t0))

	if res.AccountID == nil || *res.AccountID != acct {
		t.Fatalf("custom resolver ignored: %+v", res)
	}
	if got := count(t, "recompute_jobs"); got != 1 {
		t.Errorf("recompute_jobs = %d, want 1", got)
	}
	// An unresolved custom resolution carries its own reason.
	svc = newService(t, clock.NewFixed(t0), ingest.Options{Resolver: staticResolver{res: ingest.Resolution{Reason: "needs_human"}}})
	r2, err := svc.Ingest(context.Background(), inboundEmail(t, "m2", "stranger@unknown.example", "", t0))
	if err != nil || r2.AccountID != nil {
		t.Fatalf("unexpected: %+v %v", r2, err)
	}
	if got := queryString(t, `SELECT reason FROM unresolved_activities WHERE activity_id = $1::uuid`, r2.ActivityID); got != "needs_human" {
		t.Errorf("reason = %q", got)
	}
}
