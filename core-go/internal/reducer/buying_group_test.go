package reducer

import (
	"reflect"
	"testing"

	"github.com/harneet2512/gtm-work/core-go/internal/claims"
)

func member(st AccountState, person string) *Member {
	for i := range st.BuyingGroup {
		if st.BuyingGroup[i].PersonID == person {
			return &st.BuyingGroup[i]
		}
	}
	return nil
}

func memberClaim(person, value string, st claims.Standing, when int) claims.Claim {
	return about(claim(claims.FieldBuyingGroupMember, value, st, 0.9, day(when)), person)
}

func roleClaim(person, role string, st claims.Standing, when int) claims.Claim {
	return about(claim(claims.FieldStakeholderRole, `"`+role+`"`, st, 0.9, day(when)), person)
}

func TestBuyingGroupRolesComeFromRoleClaimsAndChampionAndEconomicBuyerFields(t *testing.T) {
	cs := []claims.Claim{
		memberClaim(priya, `{"title":"Director of Operations"}`, claims.CRMExplicit, 0),
		claim(claims.FieldChampion, `"`+priya+`"`, claims.FirstPartyAI, 0.9, day(1)),
		memberClaim(owen, `{"title":"Chief Financial Officer"}`, claims.CRMExplicit, 0),
		about(claim(claims.FieldEconomicBuyer, `"`+owen+`"`, claims.CRMExplicit, 1, day(0)), owen),
		memberClaim(marco, `{}`, claims.FirstPartyAI, 2),
		roleClaim(marco, "security", claims.CRMExplicit, 2),
		roleClaim(marco, "technical_evaluator", claims.FirstPartyAI, 3),
	}
	st, _ := Reduce(input(day(10), cs, nil))
	mustValidate(t, st)

	if m := member(st, priya); m == nil || !reflect.DeepEqual(m.Roles, []string{"champion"}) || *m.Title != "Director of Operations" || m.DisplayName != "Priya Shah" {
		t.Fatalf("priya = %+v", m)
	}
	if m := member(st, owen); m == nil || !reflect.DeepEqual(m.Roles, []string{"economic_buyer"}) {
		t.Fatalf("owen = %+v", m)
	}
	m := member(st, marco)
	if m == nil || !reflect.DeepEqual(m.Roles, []string{"technical_evaluator", "security"}) { // schema vocabulary order
		t.Fatalf("marco = %+v", m)
	}
	if len(m.EvidenceRefs) == 0 {
		t.Fatal("a member needs evidence")
	}
	if len(st.CoverageGaps) != 0 {
		t.Fatalf("economic buyer present: gaps = %v", st.CoverageGaps)
	}
}

func TestMemberWithoutAnyRoleIsUnknownRoleAndTitleFallsBackToThePersonRecord(t *testing.T) {
	st, _ := Reduce(input(day(10), []claims.Claim{memberClaim(priya, `{}`, claims.FirstPartyAI, 0)}, nil))
	mustValidate(t, st)
	m := member(st, priya)
	if !reflect.DeepEqual(m.Roles, []string{"unknown"}) || m.Title == nil || *m.Title != "Director of Operations" {
		t.Fatalf("%+v", m)
	}
	st, _ = Reduce(input(day(10), []claims.Claim{memberClaim(marco, `{}`, claims.FirstPartyAI, 0)}, nil))
	if m := member(st, marco); m.Title != nil {
		t.Fatalf("no title anywhere must be null, got %v", *m.Title)
	}
}

func TestFirstPartyTitleBeatsNewerEnrichmentAndThirdPartyOnlyPersonIsNotAMember(t *testing.T) {
	cs := []claims.Claim{
		memberClaim(ravi, `{"title":"Head of Platform Engineering"}`, claims.CRMExplicit, 0),
		memberClaim(ravi, `{"title":"Senior Engineering Manager","employer_domain":"globex.com"}`, claims.ThirdParty, 5),
		memberClaim(sam, `{"title":"CISO of Somewhere Else"}`, claims.ThirdParty, 5),
	}
	st, _ := Reduce(input(day(10), cs, nil))
	mustValidate(t, st)
	if m := member(st, ravi); m == nil || *m.Title != "Head of Platform Engineering" {
		t.Fatalf("ravi = %+v", m)
	}
	if member(st, sam) != nil {
		t.Fatal("enrichment alone must not put someone in the buying group")
	}
}

func TestDelegationSetsDelegatedToAndAddsTheDelegateAsMember(t *testing.T) {
	d := about(claim(claims.FieldDelegation, `{"from_person_id":"`+elena+`","to_person_id":"`+sam+`","scope":"evaluation"}`, claims.FirstPartyAI, 0.9, day(1)), elena)
	cs := []claims.Claim{memberClaim(elena, `{}`, claims.CRMExplicit, 0), roleClaim(elena, "executive_sponsor", claims.FirstPartyAI, 0), d}
	st, _ := Reduce(input(day(5), cs, nil))
	mustValidate(t, st)
	e, s := member(st, elena), member(st, sam)
	if e.DelegatedToPerson == nil || *e.DelegatedToPerson != sam {
		t.Fatalf("elena = %+v", e)
	}
	if s == nil || s.DelegatedToPerson != nil {
		t.Fatalf("sam = %+v", s)
	}
}

func TestMalformedDelegationIsSkippedWithWarning(t *testing.T) {
	d := about(claim(claims.FieldDelegation, `"nonsense"`, claims.FirstPartyAI, 0.9, day(1)), elena)
	st, warns := Reduce(input(day(5), []claims.Claim{memberClaim(elena, `{}`, claims.CRMExplicit, 0), d}, nil))
	mustValidate(t, st)
	if member(st, elena).DelegatedToPerson != nil || len(warns) != 1 {
		t.Fatalf("warnings = %v", warns)
	}
}

func engage(n int, person string, typ, role string, when int) Activity {
	return Activity{ID: id(n), Type: typ, OccurredAt: day(when), Participants: []Participant{{PersonID: person, Role: role, RawIdentity: "x@c.com"}}}
}

func TestMemberStatusRule(t *testing.T) {
	asOf := 200
	acts := func(extra ...Activity) []Activity {
		return append([]Activity{{ID: id(900), Type: "EmailSent", OccurredAt: day(asOf)}}, extra...)
	}
	cs := func(p string, firstSeen int) []claims.Claim {
		return []claims.Claim{memberClaim(p, `{}`, claims.CRMExplicit, firstSeen)}
	}
	tests := []struct {
		name   string
		claims []claims.Claim
		acts   []Activity
		want   string
	}{
		{"unknown, not new: first seen just over the 6-day window ago (Northstar Grace)",
			cs(owen, asOf-7), acts(), "unknown"},
		{"new: first seen exactly 6 days ago",
			cs(owen, asOf-6), acts(), "new"},
		{"new: first seen within the window, spoke on a call but wrote no email",
			cs(marco, asOf-5), acts(engage(901, marco, "TranscriptReady", "speaker", asOf-4)), "new"},
		{"active once the person authored an email, even if first seen this week",
			cs(marco, asOf-2), acts(engage(902, marco, "EmailReceived", "from", asOf-1)), "active"},
		{"unknown: first seen long ago and never directly engaged",
			cs(owen, asOf-60), acts(), "unknown"},
		{"new beats unknown for a contact added this week",
			cs(owen, asOf-3), acts(), "new"},
		{"active: engaged 44 days ago",
			cs(tom, 0), acts(engage(903, tom, "TranscriptReady", "speaker", asOf-44)), "active"},
		{"weakening: engaged 45 days ago",
			cs(tom, 0), acts(engage(904, tom, "TranscriptReady", "speaker", asOf-45)), "weakening"},
		{"weakening: engaged 89 days ago",
			cs(tom, 0), acts(engage(905, tom, "EmailReceived", "from", asOf-89)), "weakening"},
		{"inactive: engaged 90 days ago",
			cs(tom, 0), acts(engage(906, tom, "EmailReceived", "from", asOf-90)), "inactive"},
		{"accepting a meeting is engagement",
			cs(tom, 0), acts(engage(907, tom, "MeetingAccepted", "actor", asOf-2)), "active"},
		{"being added to an invite is not engagement",
			cs(tom, 0), acts(engage(908, tom, "MeetingParticipantAdded", "attendee", asOf-2)), "unknown"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			st, _ := Reduce(input(day(asOf+1), tt.claims, tt.acts))
			mustValidate(t, st)
			p := tt.claims[0].SubjectPersonID
			m := member(st, p)
			if m == nil || m.Status != tt.want {
				t.Fatalf("status = %+v, want %s", m, tt.want)
			}
		})
	}
}

func TestLastEngagedAtIsTheLatestEngagement(t *testing.T) {
	cs := []claims.Claim{memberClaim(tom, `{}`, claims.CRMExplicit, 0)}
	acts := []Activity{engage(910, tom, "EmailReceived", "from", 5), engage(911, tom, "TranscriptReady", "speaker", 9), engage(912, tom, "MeetingParticipantAdded", "attendee", 20)}
	st, _ := Reduce(input(day(30), cs, acts))
	m := member(st, tom)
	if m.LastEngagedAt == nil || !m.LastEngagedAt.Equal(day(9)) {
		t.Fatalf("%+v", m.LastEngagedAt)
	}
}

func TestCoverageGapsNameRolesRequiredButUnfilled(t *testing.T) {
	// A role claim whose person is unresolved says the role is needed; nobody holds it yet.
	unfilled := claim(claims.FieldStakeholderRole, `"security"`, claims.FirstPartyAI, 0.9, day(0))
	eb := about(claim(claims.FieldEconomicBuyer, `"`+owen+`"`, claims.CRMExplicit, 1, day(0)), owen)
	st, _ := Reduce(input(day(3), []claims.Claim{unfilled, eb}, nil))
	mustValidate(t, st)
	if !reflect.DeepEqual(st.CoverageGaps, []string{"security"}) {
		t.Fatalf("gaps = %v", st.CoverageGaps)
	}
	filled := roleClaim(marco, "security", claims.CRMExplicit, 1)
	st, _ = Reduce(input(day(3), []claims.Claim{unfilled, eb, filled, memberClaim(marco, `{}`, claims.CRMExplicit, 1)}, nil))
	if len(st.CoverageGaps) != 0 {
		t.Fatalf("a held role is not a gap: %v", st.CoverageGaps)
	}
}

func TestBuyingGroupOrderIsDeterministicByFirstSeenThenID(t *testing.T) {
	cs := []claims.Claim{memberClaim(owen, `{}`, claims.CRMExplicit, 5), memberClaim(priya, `{}`, claims.CRMExplicit, 1), memberClaim(tom, `{}`, claims.CRMExplicit, 5)}
	st, _ := Reduce(input(day(30), cs, nil))
	var got []string
	for _, m := range st.BuyingGroup {
		got = append(got, m.PersonID)
	}
	want := []string{priya, owen, tom}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("order = %v, want %v", got, want)
	}
}
