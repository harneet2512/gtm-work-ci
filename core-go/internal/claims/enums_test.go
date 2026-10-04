package claims

import (
	"encoding/json"
	"testing"
)

func TestEveryContractEnumValueMapsToItself(t *testing.T) {
	for _, v := range ChampionStatuses {
		if got, ok := NormalizeEnum(FieldChampionStatus, json.RawMessage(`"`+v+`"`)); !ok || got != v {
			t.Errorf("champion_status %q -> %q, %v", v, got, ok)
		}
	}
	for _, v := range RelationshipRisks {
		if got, ok := NormalizeEnum(FieldRelationshipRisk, json.RawMessage(`"`+v+`"`)); !ok || got != v {
			t.Errorf("relationship_risk %q -> %q, %v", v, got, ok)
		}
	}
}

func TestExtractorFreeTextMapsOntoTheEnum(t *testing.T) {
	tests := []struct {
		field FieldPath
		text  string
		want  string
	}{
		{FieldChampionStatus, "wavering", "weakening"},
		{FieldChampionStatus, "Less involved", "weakening"},
		{FieldChampionStatus, "losing_interest", "weakening"},
		{FieldChampionStatus, "left the company", "departed"},
		{FieldChampionStatus, "  Left   The Company ", "departed"},
		{FieldChampionStatus, "stepped back", "delegated"},
		{FieldChampionStatus, "gone quiet", "inactive"},
		{FieldChampionStatus, "Actively engaged", "active"},
		{FieldRelationshipRisk, "evaluating a competitor", "high"},
		{FieldRelationshipRisk, "High risk", "high"},
		{FieldRelationshipRisk, "moderate", "medium"},
		{FieldRelationshipRisk, "medium-risk", "medium"},
		{FieldRelationshipRisk, "minimal", "low"},
		{FieldRelationshipRisk, "N/A", "unknown"},
	}
	for _, tt := range tests {
		got, ok := NormalizeEnum(tt.field, json.RawMessage(`"`+tt.text+`"`))
		if !ok || got != tt.want {
			t.Errorf("%s %q -> %q, %v; want %q", tt.field, tt.text, got, ok, tt.want)
		}
	}
}

func TestUnmappableTextAndNonTextAreNotOK(t *testing.T) {
	for _, raw := range []string{`"vibes are off"`, `""`, `null`, `42`, `{"x":1}`, `["active"]`} {
		if got, ok := NormalizeEnum(FieldChampionStatus, json.RawMessage(raw)); ok {
			t.Errorf("%s mapped to %q", raw, got)
		}
	}
	if _, ok := NormalizeEnum(FieldHealth, json.RawMessage(`"at_risk"`)); ok || IsEnumField(FieldHealth) {
		t.Error("health is free text in the contract and must not be normalized")
	}
	if !IsEnumField(FieldChampionStatus) || !IsEnumField(FieldRelationshipRisk) {
		t.Error("IsEnumField")
	}
}
