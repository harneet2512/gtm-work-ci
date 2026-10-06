package signals_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/claims"
	"github.com/harneet2512/gtm-work/core-go/internal/reducer"
	"github.com/harneet2512/gtm-work/core-go/internal/signals"
)

func TestActivityRules(t *testing.T) {
	prev := state(1, nil)
	next := state(2, nil)
	acts := []signals.ActivityFact{
		{ID: "a1", Type: "EmailReceived", OccurredAt: t0, FromCustomer: true},
		{ID: "a2", Type: "EmailSent", OccurredAt: t0, FromRep: true},
		{ID: "a3", Type: "MeetingAccepted", OccurredAt: t0.Add(time.Minute)},
		{ID: "a4", Type: "CustomerWentSilent", OccurredAt: t0.Add(2 * time.Minute)},
		{ID: "a5", Type: "EmailReceived", OccurredAt: t0, FromCustomer: false},
	}
	got := run(&prev, next, acts, nil)
	if g := types(got); len(g) != 3 || g[0] != "customer_replied" || g[1] != "customer_went_silent" || g[2] != "meeting_accepted" {
		t.Fatalf("got %v", g)
	}
	if s := only(t, got, "customer_replied"); s.EvidenceRefs[0].ActivityID != "a1" || !s.OccurredAt.Equal(t0) {
		t.Fatalf("signal = %+v", s)
	}
}

func TestFieldContradictionNamesBothClaimsAndKeysOnTheContradictingClaim(t *testing.T) {
	win := claims.Claim{ID: "w", Standing: claims.Standing("crm_explicit")}
	con := claims.Claim{ID: "x", Standing: claims.Standing("first_party_ai"), SourceActivityID: "act-x", EvidenceQuote: "we will review next week", OccurredAt: t0}
	conflicts := []claims.Conflict{{Field: claims.FieldNextMilestone, Winner: win, Contradicting: con, Reason: "newer lower-standing"}}
	prev, next := state(1, nil), state(2, nil)
	s := only(t, run(&prev, next, nil, conflicts), "field_contradicted")
	d := s.Details
	if d["field"] != "next_milestone" || d["winning_claim_id"] != "w" || d["contradicting_claim_id"] != "x" || d["contradicting_standing"] != "first_party_ai" {
		t.Fatalf("details = %v", d)
	}
	if len(s.EvidenceRefs) != 1 || s.EvidenceRefs[0].Quote == "" {
		t.Fatal("a contradiction cites the contradicting claim's evidence")
	}
	later := state(3, nil)
	again := only(t, run(&next, later, nil, conflicts), "field_contradicted")
	if again.DedupeKey != s.DedupeKey {
		t.Fatalf("the same contradiction must keep one key across recomputes: %q vs %q", again.DedupeKey, s.DedupeKey)
	}
}

func TestImmaterialDiffRunsNoStateRules(t *testing.T) {
	prev := state(1, nil)
	next := state(2, func(s *reducer.AccountState) { s.Fields.LastCustomerInteraction = known("2026-09-29T15:42:00Z", "l") })
	if got := run(&prev, next, nil, nil); len(got) != 0 {
		t.Fatalf("emitted %v", types(got))
	}
}

func TestEvaluateIsDeterministicAndKeysAreUnique(t *testing.T) {
	prev := state(1, nil)
	next := state(2, func(s *reducer.AccountState) {
		s.BuyingGroup = []reducer.Member{{PersonID: "a", Status: "new"}, {PersonID: "b", Status: "new"}}
		s.CoverageGaps = []string{"economic_buyer", "legal"}
	})
	a, b := run(&prev, next, nil, nil), run(&prev, next, nil, nil)
	if len(a) != 4 || len(a) != len(b) {
		t.Fatalf("got %d, %d", len(a), len(b))
	}
	seen := map[string]bool{}
	for i := range a {
		if a[i].DedupeKey != b[i].DedupeKey || seen[a[i].DedupeKey] {
			t.Fatalf("dedupe keys must be stable and unique: %q", a[i].DedupeKey)
		}
		seen[a[i].DedupeKey] = true
	}
}

func TestTickEmitsSilenceOnceThresholdPassedAndIsIdempotent(t *testing.T) {
	last := "2026-09-01T10:00:00Z"
	st := state(3, func(s *reducer.AccountState) { s.Fields.LastCustomerInteraction = known(last, "l") })
	if got := signals.Tick(st, time.Date(2026, 9, 5, 0, 0, 0, 0, time.UTC)); len(got) != 0 {
		t.Fatalf("too early: %v", types(got))
	}
	a := signals.Tick(st, time.Date(2026, 9, 10, 0, 0, 0, 0, time.UTC))
	b := signals.Tick(st, time.Date(2026, 9, 20, 0, 0, 0, 0, time.UTC))
	if len(a) != 1 || len(b) != 1 || a[0].DedupeKey != b[0].DedupeKey || !a[0].OccurredAt.Equal(b[0].OccurredAt) {
		t.Fatalf("a=%v b=%v", a, b)
	}
	if a[0].ExpiresAt != nil || a[0].Rule == "" {
		t.Fatalf("silence is a STANDING signal with a rule: %+v", a[0])
	}
	if got := signals.Tick(state(1, nil), t0); len(got) != 0 {
		t.Fatalf("unknown last interaction emitted %v", types(got))
	}
}

func TestSilenceFromTheClockActivityConvergesWithTick(t *testing.T) {
	last := "2026-09-01T10:00:00Z"
	next := state(2, func(s *reducer.AccountState) { s.Fields.LastCustomerInteraction = known(last, "l") })
	prev := state(1, nil)
	viaActivity := only(t, run(&prev, next, []signals.ActivityFact{{ID: "tick", Type: "CustomerWentSilent", OccurredAt: t0}}, nil), "customer_went_silent")
	viaTick := signals.Tick(next, t0)
	if viaTick[0].DedupeKey != viaActivity.DedupeKey {
		t.Fatalf("one silence episode, one key: %q vs %q", viaTick[0].DedupeKey, viaActivity.DedupeKey)
	}
}

func TestIsCustomerIdentity(t *testing.T) {
	cases := map[string]bool{"priya@acme.com": true, "dana@ghostvendor.com": false, "x@eu.ghostvendor.com": false, "call:speaker_01": false, "": false}
	for raw, want := range cases {
		if got := signals.IsCustomerIdentity(raw, "ghostvendor.com"); got != want {
			t.Errorf("%q = %v", raw, got)
		}
	}
}

// The event/standing partition and the window mirror signal.v1.json (parity with the contract).
func TestKindsAndWindowMatchTheContract(t *testing.T) {
	dir, _ := os.Getwd()
	var raw []byte
	for {
		b, err := os.ReadFile(filepath.Join(dir, "contracts", "schemas", "signal.v1.json"))
		if err == nil {
			raw = b
			break
		}
		if filepath.Dir(dir) == dir {
			t.Fatal("signal.v1.json not found")
		}
		dir = filepath.Dir(dir)
	}
	var c struct {
		Defs struct {
			Window struct{ Const int }     `json:"eventWindowDays"`
			Event  struct{ Enum []string } `json:"eventSignalType"`
		} `json:"$defs"`
	}
	if err := json.Unmarshal(raw, &c); err != nil {
		t.Fatal(err)
	}
	if time.Duration(c.Defs.Window.Const)*24*time.Hour != signals.EventWindow || len(c.Defs.Event.Enum) != len(signals.EventTypes) {
		t.Fatalf("window or event list drifted from the contract")
	}
	for _, e := range c.Defs.Event.Enum {
		if !signals.EventTypes[e] {
			t.Errorf("%s is an EVENT signal in the contract", e)
		}
	}
}

func TestEveryRuleEmitsOnlyDeclaredTypesAndEveryTypeHasARule(t *testing.T) {
	declared := map[string]bool{}
	for _, r := range signals.Table {
		for _, e := range r.Emits {
			declared[e] = true
		}
	}
	// product_usage_increased has no rule yet (documented gap); every other contract type does.
	if len(declared) != 18 || declared["product_usage_increased"] {
		t.Fatalf("declared = %d types", len(declared))
	}
}
