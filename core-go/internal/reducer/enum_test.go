package reducer

import (
	"testing"

	"github.com/harneet2512/gtm-work/core-go/internal/claims"
)

func TestFreeTextEnumFieldsAreNormalizedAndSchemaValid(t *testing.T) {
	tests := []struct {
		field claims.FieldPath
		text  string
		want  string
	}{
		{claims.FieldChampionStatus, "wavering", "weakening"},
		{claims.FieldChampionStatus, "less involved", "weakening"},
		{claims.FieldChampionStatus, "left the company", "departed"},
		{claims.FieldChampionStatus, "delegated", "delegated"},
		{claims.FieldChampionStatus, "active", "active"},
		{claims.FieldChampionStatus, "inactive", "inactive"},
		{claims.FieldRelationshipRisk, "evaluating a competitor", "high"},
		{claims.FieldRelationshipRisk, "moderate", "medium"},
		{claims.FieldRelationshipRisk, "low", "low"},
	}
	for _, tt := range tests {
		c := claim(tt.field, `"`+tt.text+`"`, claims.FirstPartyAI, 0.9, day(0))
		st, _ := Reduce(input(day(1), []claims.Claim{c}, nil))
		mustValidate(t, st)
		f := st.Fields.ChampionStatus
		if tt.field == claims.FieldRelationshipRisk {
			f = st.Fields.RelationshipRisk
		}
		if !f.Known || f.Value != tt.want || *f.WinningClaimID != c.ID || len(f.EvidenceRefs) != 1 || f.EvidenceRefs[0].Quote == "" {
			t.Errorf("%s %q -> %+v, want %q with the claim and its evidence", tt.field, tt.text, f, tt.want)
		}
		if string(c.Value) != `"`+tt.text+`"` {
			t.Errorf("the claim must keep the original text")
		}
	}
}

func TestUnmappableEnumTextBecomesUnknownButTheClaimStaysVisible(t *testing.T) {
	c := claim(claims.FieldChampionStatus, `"vibes are off"`, claims.FirstPartyAI, 0.9, day(0))
	r := claim(claims.FieldRelationshipRisk, `{"level":3}`, claims.FirstPartyAI, 0.9, day(0))
	st, _ := Reduce(input(day(1), []claims.Claim{c, r}, nil))
	mustValidate(t, st)
	for name, want := range map[string]string{"champion_status": c.ID, "relationship_risk": r.ID} {
		f := st.Fields.Field(name)
		if f.Known || f.Value != "unknown" || f.WinningClaimID != nil {
			t.Errorf("%s = %+v, want unknown", name, f)
		}
		if len(f.CompetingClaimIDs) != 1 || f.CompetingClaimIDs[0] != want {
			t.Errorf("%s competing = %v, want the claim %s to stay inspectable", name, f.CompetingClaimIDs, want)
		}
	}
}
