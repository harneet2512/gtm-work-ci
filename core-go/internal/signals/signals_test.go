package signals_test

import (
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/claims"
	"github.com/harneet2512/gtm-work/core-go/internal/reducer"
	"github.com/harneet2512/gtm-work/core-go/internal/signals"
	"github.com/harneet2512/gtm-work/core-go/internal/statediff"
)

var t0 = time.Date(2026, 9, 29, 15, 42, 0, 0, time.UTC)

func ref(id string) []reducer.EvidenceRef {
	return []reducer.EvidenceRef{{ActivityID: "act-" + id, ClaimID: id, OccurredAt: t0}}
}

func known(v any, id string) reducer.Field {
	return reducer.Field{Value: v, Known: true, EvidenceRefs: ref(id)}
}

func state(version int, mut func(*reducer.AccountState)) reducer.AccountState {
	st := reducer.AccountState{AccountID: "acct", Version: version, AsOf: t0}
	for _, n := range reducer.FieldNames() {
		*st.Fields.Field(n) = reducer.Field{Value: "unknown"}
	}
	if mut != nil {
		mut(&st)
	}
	return st
}

// run diffs prev against next and evaluates the rules.
func run(prev *reducer.AccountState, next reducer.AccountState, acts []signals.ActivityFact, conflicts []claims.Conflict) []signals.Signal {
	return signals.Evaluate(signals.Input{Prev: prev, Next: next, Diff: statediff.Compute(prev, next, nil), Activities: acts, Conflicts: conflicts})
}

func types(ss []signals.Signal) []string {
	out := []string{}
	for _, s := range ss {
		out = append(out, s.Type)
	}
	sort.Strings(out)
	return out
}

func only(t *testing.T, ss []signals.Signal, typ string) signals.Signal {
	t.Helper()
	var found []signals.Signal
	for _, s := range ss {
		if s.Type == typ {
			found = append(found, s)
		}
	}
	if len(found) != 1 {
		t.Fatalf("want exactly one %s, got %v", typ, types(ss))
	}
	return found[0]
}

func blocker(text, status, claim string) reducer.Field {
	return known([]reducer.Item{{Text: text, Status: status, ClaimID: claim}}, claim)
}

func TestNewStakeholderEntersAndCarriesItsEvidence(t *testing.T) {
	prev := state(1, func(s *reducer.AccountState) {
		s.BuyingGroup = []reducer.Member{{PersonID: "priya", Roles: []string{"champion"}, Status: "active"}}
	})
	next := state(2, func(s *reducer.AccountState) {
		s.BuyingGroup = []reducer.Member{{PersonID: "priya", Roles: []string{"champion"}, Status: "active"},
			{PersonID: "marco", Roles: []string{"security"}, Status: "new", EvidenceRefs: ref("m1")}}
	})
	got := run(&prev, next, nil, nil)
	s := only(t, got, "new_stakeholder_entered")
	if s.SubjectPersonID != "marco" || s.Rule != "sig.new_stakeholder@1" || len(s.EvidenceRefs) != 1 || !s.OccurredAt.Equal(t0) {
		t.Fatalf("signal = %+v", s)
	}
	if s.ExpiresAt == nil || !s.ExpiresAt.Equal(t0.Add(signals.EventWindow)) {
		t.Fatalf("an EVENT signal carries its window: %v", s.ExpiresAt)
	}
}

func TestSecurityBlockerAppearsOnceAndResolves(t *testing.T) {
	empty := state(1, nil)
	open := state(2, func(s *reducer.AccountState) {
		s.Fields.Blockers = blocker("Need SOC2 Type II report before sign-off", "open", "c1")
	})
	s := only(t, run(&empty, open, nil, nil), "security_blocker_appeared")
	if s.SubjectClaimID != "c1" || s.ExpiresAt != nil || s.Details["blocker"] == "" {
		t.Fatalf("a STANDING signal names its claim and has no window: %+v", s)
	}
	// Re-wording the same open blocker is not a second appearance.
	reworded := state(3, func(s *reducer.AccountState) {
		s.Fields.Blockers = blocker("need soc2 type ii report before sign-off.", "open", "c9")
	})
	if got := run(&open, reworded, nil, nil); len(got) != 0 {
		t.Fatalf("reworded blocker emitted %v", types(got))
	}
	resolved := state(3, func(s *reducer.AccountState) {
		s.Fields.Blockers = blocker("Need SOC2 Type II report before sign-off", "resolved", "c1")
	})
	got := run(&open, resolved, nil, nil)
	if r := only(t, got, "blocker_resolved"); r.SubjectClaimID != "c1" || r.ExpiresAt == nil {
		t.Fatalf("resolved = %+v", r)
	}
}

func TestSecurityVocabulary(t *testing.T) {
	empty := state(1, nil)
	for _, text := range []string{"Open vulnerability findings", "vulnerable dependencies", "Complete the security questionnaires",
		"Run a pentest", "pen-testing report", "Share the SOC 2 report"} {
		next := state(2, func(s *reducer.AccountState) { s.Fields.Blockers = blocker(text, "open", "c1") })
		if got := run(&empty, next, nil, nil); len(got) != 1 || got[0].Type != "security_blocker_appeared" {
			t.Errorf("%q emitted %v", text, types(got))
		}
	}
}

// A blocker's text has no length limit; the dedupe key must still fit its 300-character column.
func TestDedupeKeysAreBoundedForLongItemText(t *testing.T) {
	long := "We need the security review " + strings.Repeat("x", 2000)
	empty := state(1, nil)
	next := state(2, func(s *reducer.AccountState) { s.Fields.Blockers = blocker(long, "open", "c1") })
	s := only(t, run(&empty, next, nil, nil), "security_blocker_appeared")
	if len(s.DedupeKey) > 300 || len(s.DedupeKey) < 20 {
		t.Fatalf("dedupe key length = %d", len(s.DedupeKey))
	}
	if again := only(t, run(&empty, next, nil, nil), "security_blocker_appeared"); again.DedupeKey != s.DedupeKey {
		t.Fatal("the hashed key must be stable")
	}
}

// An activity folded into two recomputes still yields one signal key.
func TestActivitySignalKeysDoNotDependOnTheStateVersion(t *testing.T) {
	acts := []signals.ActivityFact{{ID: "a1", Type: "EmailReceived", OccurredAt: t0, FromCustomer: true}}
	a, b := state(2, nil), state(3, nil)
	p := state(1, nil)
	k1 := only(t, run(&p, a, acts, nil), "customer_replied").DedupeKey
	k2 := only(t, run(&a, b, acts, nil), "customer_replied").DedupeKey
	if k1 != k2 {
		t.Fatalf("%q vs %q", k1, k2)
	}
}

func TestSilenceTickWithoutAKnownLastInteractionKeysOnTheTick(t *testing.T) {
	prev, next := state(1, nil), state(2, nil)
	one := only(t, run(&prev, next, []signals.ActivityFact{{ID: "t1", Type: "CustomerWentSilent", OccurredAt: t0}}, nil), "customer_went_silent")
	two := only(t, run(&prev, next, []signals.ActivityFact{{ID: "t2", Type: "CustomerWentSilent", OccurredAt: t0}}, nil), "customer_went_silent")
	if one.DedupeKey == two.DedupeKey {
		t.Fatal("two ticks with no known last interaction are two episodes")
	}
}

func TestNonSecurityBlockerEmitsNoSecuritySignal(t *testing.T) {
	empty := state(1, nil)
	next := state(2, func(s *reducer.AccountState) {
		s.Fields.Blockers = blocker("EU budget line needs finance approval", "open", "c1")
	})
	if got := run(&empty, next, nil, nil); len(got) != 0 {
		t.Fatalf("emitted %v", types(got))
	}
}

func TestChampionStatusSignals(t *testing.T) {
	member := []reducer.Member{{PersonID: "elena", Roles: []string{"champion"}, Status: "active"}}
	at := func(status string) reducer.AccountState {
		return state(1, func(s *reducer.AccountState) {
			s.Fields.ChampionStatus = known(status, "c-"+status)
			s.BuyingGroup = member
		})
	}
	cases := []struct{ from, to, want string }{
		{"active", "delegated", "champion_delegated"},
		{"active", "weakening", "champion_weakened"},
		{"active", "departed", "champion_weakened"},
		{"weakening", "active", "champion_reactivated"},
		{"inactive", "active", "champion_reactivated"},
	}
	for _, c := range cases {
		prev, next := at(c.from), at(c.to)
		next.Version = 2
		got := run(&prev, next, nil, nil)
		if s := only(t, got, c.want); s.SubjectPersonID != "elena" {
			t.Errorf("%s -> %s: %+v", c.from, c.to, s)
		}
	}
	prev, same := at("active"), at("active")
	same.Version = 2
	if got := run(&prev, same, nil, nil); len(got) != 0 {
		t.Fatalf("unchanged status emitted %v", types(got))
	}
}

func TestStageMovesAlongTheOrderOnly(t *testing.T) {
	at := func(stage string) reducer.AccountState {
		return state(1, func(s *reducer.AccountState) { s.Fields.Stage = known(stage, "c-"+stage) })
	}
	a, b, c := at("Discovery"), at("Commercial review"), at("Mystery stage")
	b.Version, c.Version = 2, 2
	only(t, run(&a, b, nil, nil), "stage_advanced")
	only(t, run(&b, a, nil, nil), "stage_regressed")
	if got := run(&a, c, nil, nil); len(got) != 0 {
		t.Fatalf("an unordered stage emitted %v", types(got))
	}
}

func TestExpansionPricingGapAndRiskSignals(t *testing.T) {
	prev := state(1, nil)
	next := state(2, func(s *reducer.AccountState) {
		s.Fields.Motion = known("expansion", "m")
		s.Fields.CommercialIssue = known("Customer asks for a volume discount", "ci")
		s.Fields.RelationshipRisk = known("high", "rr")
		s.CoverageGaps = []string{"economic_buyer"}
	})
	got := types(run(&prev, next, nil, nil))
	want := []string{"expansion_interest", "pricing_interest", "stakeholder_gap", "support_risk_spike"}
	if len(got) != len(want) {
		t.Fatalf("got %v want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v want %v", got, want)
		}
	}
}

func TestNextMeetingMissingNeedsABookedMeetingFirst(t *testing.T) {
	booked := state(1, func(s *reducer.AccountState) { s.Fields.NextMeeting = known("2026-10-01 review", "n1") })
	gone := state(2, func(s *reducer.AccountState) { s.Fields.NextMeeting = known(nil, "n2") })
	only(t, run(&booked, gone, nil, nil), "next_meeting_missing")
	never := state(1, nil)
	if got := run(&never, gone, nil, nil); len(got) != 0 {
		t.Fatalf("no meeting was ever booked: %v", types(got))
	}
}

func TestCommitmentOverdueFollowsTheItem(t *testing.T) {
	mk := func(status string) reducer.AccountState {
		return state(1, func(s *reducer.AccountState) {
			s.Fields.CurrentCommitments = known([]reducer.Item{{Text: "send docs", Status: status, ClaimID: "c1", OwnerPersonID: "dana"}}, "c1")
		})
	}
	prev, next := mk("open"), mk("overdue")
	next.Version = 2
	s := only(t, run(&prev, next, nil, nil), "commitment_overdue")
	if s.SubjectClaimID != "c1" || s.SubjectPersonID != "dana" {
		t.Fatalf("signal = %+v", s)
	}
	if got := run(&next, next, nil, nil); len(got) != 0 {
		t.Fatalf("already overdue emitted %v", types(got))
	}
}
