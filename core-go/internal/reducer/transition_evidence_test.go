package reducer

import (
	"testing"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/claims"
)

func championClaim(person string, when time.Time) claims.Claim {
	return claim(claims.FieldChampion, `"`+person+`"`, claims.FirstPartyAI, 0.9, when)
}

func TestChampionSinceIsTheStartOfTheCurrentRunOfClaimsNamingTheChampion(t *testing.T) {
	first, again := championClaim(marco, day(2)), championClaim(marco, day(9))
	st, _ := Reduce(input(day(20), []claims.Claim{championClaim(priya, day(0)), first, again}, nil))
	mustValidate(t, st)
	f := st.Fields.ChampionSince
	if f == nil || !f.Known || !f.Derived || f.Value != day(2).UTC().Format(time.RFC3339) {
		t.Fatalf("champion_since = %+v, want the first claim naming Marco (day 2)", f)
	}
	if f.AsOf == nil || !f.AsOf.Equal(day(2)) || len(f.EvidenceRefs) != 1 || f.EvidenceRefs[0].ActivityID != first.SourceActivityID {
		t.Errorf("champion_since must cite the claim's activity and as_of the same moment: %+v", f)
	}
}

func TestChampionSinceIsAbsentWhenThereIsNoChampion(t *testing.T) {
	st, _ := Reduce(input(day(5), []claims.Claim{claim(claims.FieldStage, `"discovery"`, claims.FirstPartyAI, 0.9, day(0))}, nil))
	mustValidate(t, st)
	if st.Fields.ChampionSince != nil {
		t.Errorf("champion_since = %+v, want absent without a champion", st.Fields.ChampionSince)
	}
	unknown := claim(claims.FieldChampion, `"unknown"`, claims.FirstPartyAI, 0.9, day(1))
	if st, _ := Reduce(input(day(5), []claims.Claim{unknown}, nil)); st.Fields.ChampionSince != nil {
		t.Errorf("an unknown champion has no start: %+v", st.Fields.ChampionSince)
	}
}

func titleClaim(person, title, quote string, when time.Time) claims.Claim {
	c := about(claim(claims.FieldBuyingGroupMember, `{"title":"`+title+`"}`, claims.FirstPartyAI, 0.9, when), person)
	c.EvidenceQuote = quote
	return c
}

func TestMemberTenureIsInterimForActingAndInterimHoldersAndPermanentForATitledOne(t *testing.T) {
	tests := []struct {
		name, title, quote, want string
	}{
		{"acting in the title", "Acting Head of Support", "verbatim quote", "interim"},
		{"interim in the title", "Interim VP", "verbatim quote", "interim"},
		{"until a permanent hire, said in the quote", "Head of Support", "Hannah is covering until a permanent VP of Support is hired", "interim"},
		{"a plain title", "Director of Operations", "verbatim quote", "permanent"},
		{"actually is not acting", "Actuary", "verbatim quote", "permanent"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := titleClaim(marco, tt.title, tt.quote, day(1))
			st, _ := Reduce(input(day(5), []claims.Claim{claim(claims.FieldChampion, `"`+marco+`"`, claims.FirstPartyAI, 0.9, day(0)), c}, nil))
			mustValidate(t, st)
			var got string
			for _, m := range st.BuyingGroup {
				if m.PersonID == marco {
					got = m.Tenure
				}
			}
			if got != tt.want {
				t.Errorf("tenure = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestMemberTenureIsAbsentWithoutATitleClaim(t *testing.T) {
	st, _ := Reduce(input(day(5), []claims.Claim{claim(claims.FieldChampion, `"`+marco+`"`, claims.FirstPartyAI, 0.9, day(0))}, nil))
	for _, m := range st.BuyingGroup {
		if m.PersonID == marco && m.Tenure != "" {
			t.Errorf("tenure = %q, want absent (unknown) without a title", m.Tenure)
		}
	}
}

func TestMemberTenureIsInterimFromTheQuoteEvenWithoutATitle(t *testing.T) {
	c := about(claim(claims.FieldBuyingGroupMember, `{"role":"champion"}`, claims.FirstPartyAI, 0.9, day(1)), marco)
	c.EvidenceQuote = "Marco is covering until a permanent head is hired"
	st, _ := Reduce(input(day(5), []claims.Claim{claim(claims.FieldChampion, `"`+marco+`"`, claims.FirstPartyAI, 0.9, day(0)), c}, nil))
	for _, m := range st.BuyingGroup {
		if m.PersonID == marco && m.Tenure != "interim" {
			t.Errorf("tenure = %q, want interim from the quote", m.Tenure)
		}
	}
}

func TestChampionSinceIsNotRestartedByAHigherStandingReconfirmation(t *testing.T) {
	ai := championClaim(marco, day(2))
	human := claim(claims.FieldChampion, `"`+marco+`"`, claims.HumanApproved, 1, day(10))
	st, _ := Reduce(input(day(20), []claims.Claim{championClaim(priya, day(0)), ai, human}, nil))
	f := st.Fields.ChampionSince
	if f == nil || f.Value != day(2).UTC().Format(time.RFC3339) {
		t.Fatalf("champion_since = %+v, want day 2: a human re-confirmation of the same person does not restart the run", f)
	}
}
