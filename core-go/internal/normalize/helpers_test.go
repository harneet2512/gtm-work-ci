package normalize

import (
	"errors"
	"reflect"
	"testing"
	"time"
)

// event builds a SourceEvent from literals. occurred is RFC3339 or "" for none.
func event(t *testing.T, system, object, key, occurred, payload string) SourceEvent {
	t.Helper()
	ev := SourceEvent{
		SourceSystem:   system,
		SourceObjectID: object,
		SourceEventKey: key,
		Connector:      "test-connector",
		Payload:        []byte(payload),
	}
	if occurred != "" {
		ts, err := time.Parse(time.RFC3339, occurred)
		if err != nil {
			t.Fatalf("bad occurred literal %q: %v", occurred, err)
		}
		ev.OccurredAt = &ts
	}
	return ev
}

// want is the expected shape of a normalized activity. Empty fields mean "empty".
type want struct {
	typ          string
	occurred     string
	participants []Participant
	accountHint  string
	accountKind  HintKind // default HintDomain
	moreHints    []Hint   // further account hints after the first, in priority order
	oppHint      string
	oppKind      HintKind // default HintCRM
	moreOpp      []Hint   // further opportunity hints after the first
	summary      string
	body         string
}

type normCase struct {
	name    string
	ev      SourceEvent
	want    want
	errCode string // non-empty: expect a *ValidationError with this code
}

func runCases(t *testing.T, cases []normCase) {
	t.Helper()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Normalize(tc.ev)
			if tc.errCode != "" {
				var verr *ValidationError
				if !errors.As(err, &verr) {
					t.Fatalf("want *ValidationError(%s), got %v (activity %v)", tc.errCode, err, got.Type())
				}
				if verr.Code != tc.errCode {
					t.Fatalf("error code = %s (%v), want %s", verr.Code, verr, tc.errCode)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			assertActivity(t, tc.ev, got, tc.want)
		})
	}
}

func assertActivity(t *testing.T, ev SourceEvent, got Activity, w want) {
	t.Helper()
	if got.Type() != w.typ {
		t.Errorf("type = %q, want %q", got.Type(), w.typ)
	}
	wantTime, err := time.Parse(time.RFC3339Nano, w.occurred)
	if err != nil {
		t.Fatalf("bad want.occurred %q: %v", w.occurred, err)
	}
	if !got.OccurredAt().Equal(wantTime) || got.OccurredAt().Location() != time.UTC {
		t.Errorf("occurred_at = %v, want %v (UTC)", got.OccurredAt(), wantTime)
	}
	wantParts := w.participants
	if wantParts == nil {
		wantParts = []Participant{}
	}
	if !reflect.DeepEqual(got.Participants(), wantParts) {
		t.Errorf("participants =\n  %+v\nwant\n  %+v", got.Participants(), wantParts)
	}
	if got.AccountHint() != w.accountHint {
		t.Errorf("account_hint = %q, want %q", got.AccountHint(), w.accountHint)
	}
	wantHints := []Hint{}
	if w.accountHint != "" {
		kind := w.accountKind
		if kind == "" {
			kind = HintDomain
		}
		wantHints = append(wantHints, Hint{Kind: kind, Value: w.accountHint})
	}
	wantHints = append(wantHints, w.moreHints...)
	if !reflect.DeepEqual(got.AccountHints(), wantHints) {
		t.Errorf("account hints = %v, want %v", got.AccountHints(), wantHints)
	}
	wantOpp := []Hint{}
	if w.oppHint != "" {
		kind := w.oppKind
		if kind == "" {
			kind = HintCRM
		}
		wantOpp = append(wantOpp, Hint{Kind: kind, Value: w.oppHint})
	}
	wantOpp = append(wantOpp, w.moreOpp...)
	if !reflect.DeepEqual(got.OpportunityHints(), wantOpp) {
		t.Errorf("opportunity hints = %v, want %v", got.OpportunityHints(), wantOpp)
	}
	if got.OpportunityHint() != w.oppHint {
		t.Errorf("opportunity_hint = %q, want %q", got.OpportunityHint(), w.oppHint)
	}
	if w.summary != "" && got.Summary() != w.summary {
		t.Errorf("summary = %q, want %q", got.Summary(), w.summary)
	}
	if got.Summary() == "" {
		t.Error("summary is empty")
	}
	if got.BodyText() != w.body {
		t.Errorf("body_text = %q, want %q", got.BodyText(), w.body)
	}
	if got.Permissions().Visibility != "org" {
		t.Errorf("permissions = %+v, want visibility org", got.Permissions())
	}
	wantProv := Provenance{
		SourceSystem:   ev.SourceSystem,
		SourceObjectID: ev.SourceObjectID,
		Connector:      ev.Connector,
	}
	if got.Provenance() != wantProv {
		t.Errorf("provenance = %+v, want %+v", got.Provenance(), wantProv)
	}
	if got.SourceSystem() != ev.SourceSystem || got.SourceObjectID() != ev.SourceObjectID || got.SourceEventKey() != ev.SourceEventKey {
		t.Errorf("identity triple not carried through: %s/%s/%s", got.SourceSystem(), got.SourceObjectID(), got.SourceEventKey())
	}
	if len(got.IdempotencyKey()) != 64 {
		t.Errorf("idempotency key = %q", got.IdempotencyKey())
	}
}

func part(raw, name, role string) Participant {
	return Participant{RawIdentity: raw, DisplayName: name, Role: role}
}

func dom(v string) Hint { return Hint{Kind: HintDomain, Value: v} }
