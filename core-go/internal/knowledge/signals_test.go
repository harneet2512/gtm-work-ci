package knowledge

import (
	"testing"
	"time"
)

const day = 24 * time.Hour

func strp(s string) *string { return &s }

func TestEventSignalsAreOpenForFourteenDays(t *testing.T) {
	cases := []struct {
		name string
		age  time.Duration
		want bool
	}{
		{"just fired", 0, true},
		{"thirteen days", 13 * day, true},
		{"exactly fourteen days", 14 * day, true},
		{"fourteen days and a minute", 14*day + time.Minute, false},
		{"not fired yet", -time.Hour, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := with(situation(map[string]Value{}), func(s *Situation) {
				s.Signals = []Signal{signal("new_stakeholder_entered", tc.age)}
			})
			if got := holdsIn(t, s, cond("diff.new_stakeholder_entered", OpExists)); got != tc.want {
				t.Fatalf("open = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestSignalsOfAnotherOpportunityDoNotMatch(t *testing.T) {
	other, same := signal("champion_delegated", day), signal("champion_delegated", day)
	other.OpportunityID, same.OpportunityID = strp("other-opp"), strp(opp)
	accountLevel := signal("champion_delegated", day)
	for name, tc := range map[string]struct {
		sig  Signal
		want bool
	}{"other opportunity": {other, false}, "same opportunity": {same, true}, "account level": {accountLevel, true}} {
		s := with(situation(map[string]Value{}), func(s *Situation) { s.Signals = []Signal{tc.sig} })
		if got := holdsIn(t, s, cond("diff.champion_delegated", OpExists)); got != tc.want {
			t.Fatalf("%s: open = %v, want %v", name, got, tc.want)
		}
	}
}

func TestStandingBlockerSignalFollowsItsItem(t *testing.T) {
	blockers := items(Item{Text: "Security review of the  SOC2 report", Status: "open", ClaimID: "c-new"},
		Item{Text: "Budget approval", Status: "resolved", ClaimID: "c-budget"})
	byKey := signal("security_blocker_appeared", 40*day)
	byKey.SubjectItemKey = "security review of the soc2 report"
	byClaim := signal("security_blocker_appeared", 40*day)
	byClaim.SubjectClaimID = strp("c-budget")
	unresolved := signal("security_blocker_appeared", 40*day)
	cases := []struct {
		name string
		sig  Signal
		want bool
	}{
		{"item key still open (older than the event window)", byKey, true},
		{"subject item resolved", byClaim, false},
		{"no subject: any open blocker keeps it open", unresolved, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := with(situation(map[string]Value{"blockers": blockers}), func(s *Situation) { s.Signals = []Signal{tc.sig} })
			if got := holdsIn(t, s, cond("diff.security_blocker_appeared", OpExists)); got != tc.want {
				t.Fatalf("open = %v, want %v", got, tc.want)
			}
		})
	}
	closed := situation(map[string]Value{"blockers": items(Item{Text: "x", Status: "resolved"})})
	closed.Signals = []Signal{unresolved}
	if holdsIn(t, closed, cond("diff.security_blocker_appeared", OpExists)) {
		t.Fatal("no open blocker: signal must be closed")
	}
}

func TestOtherStandingSignals(t *testing.T) {
	contradicted := signal("field_contradicted", 5*day)
	contradicted.Details = map[string]any{"field": "stage", "contradicting_claim_id": "c9"}
	gap := signal("stakeholder_gap", 5*day)
	gap.Details = map[string]any{"gap": "legal"}
	cases := []struct {
		name   string
		fields map[string]Value
		sig    Signal
		mutate func(*Situation)
		want   bool
	}{
		{"commitment overdue", map[string]Value{"current_commitments": items(Item{Text: "send docs", Status: "overdue"})}, signal("commitment_overdue", 30*day), nil, true},
		{"commitment fulfilled", map[string]Value{"current_commitments": items(Item{Text: "send docs", Status: "fulfilled"})}, signal("commitment_overdue", day), nil, false},
		{"risk high", map[string]Value{"relationship_risk": known("high")}, signal("support_risk_spike", 60*day), nil, true},
		{"risk back to low", map[string]Value{"relationship_risk": known("low")}, signal("support_risk_spike", day), nil, false},
		{"gap still listed", map[string]Value{"coverage_gaps": items(Item{Text: "legal"})}, gap, nil, true},
		{"gap closed", map[string]Value{"coverage_gaps": items(Item{Text: "security"})}, gap, nil, false},
		{"no meeting", map[string]Value{"next_meeting": {Known: true}}, signal("next_meeting_missing", 20*day), nil, true},
		{"meeting booked", map[string]Value{"next_meeting": known("2026-10-03T10:00:00Z")}, signal("next_meeting_missing", day), nil, false},
		{"silent, no later interaction", map[string]Value{"last_customer_interaction": known("2026-09-01T00:00:00Z")}, signal("customer_went_silent", 20*day), nil, true},
		{"silence broken", map[string]Value{"last_customer_interaction": known("2026-09-30T00:00:00Z")}, signal("customer_went_silent", 20*day), nil, false},
		{"silence, interaction unknown", map[string]Value{}, signal("customer_went_silent", 20*day), nil, true},
		{"contradiction standing", map[string]Value{}, contradicted, func(s *Situation) { s.Conflicts["stage"] = []string{"c9"} }, true},
		{"contradiction confirmed away", map[string]Value{}, contradicted, nil, false},
		{"contradiction without details, any conflict", map[string]Value{}, signal("field_contradicted", day), func(s *Situation) { s.Conflicts["owner"] = []string{"c1"} }, true},
		{"contradiction without details, no conflict", map[string]Value{}, signal("field_contradicted", day), nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := with(situation(tc.fields), func(s *Situation) {
				s.Signals = []Signal{tc.sig}
				if tc.mutate != nil {
					tc.mutate(s)
				}
			})
			if got := holdsIn(t, s, cond("diff."+tc.sig.Type, OpExists)); got != tc.want {
				t.Fatalf("open = %v, want %v", got, tc.want)
			}
			if got := holdsIn(t, s, cond("diff."+tc.sig.Type, OpNotExists)); got == tc.want {
				t.Fatal("not_exists must negate exists")
			}
		})
	}
}
