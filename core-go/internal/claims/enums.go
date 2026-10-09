package claims

import (
	"encoding/json"
	"strings"
)

// Closed vocabularies of account_state.v1.json: champion_status and relationship_risk are enums in
// the contract, while the extractor writes free text ("wavering", "evaluating a competitor"). The
// claim keeps the original text (and its evidence); the state field shows the normalized enum value.

// ChampionStatuses is the contract's champion_status enum.
var ChampionStatuses = []string{"active", "weakening", "delegated", "departed", "inactive", Unknown}

// RelationshipRisks is the contract's relationship_risk enum.
var RelationshipRisks = []string{"low", "medium", "high", Unknown}

// enumSynonyms is the one explicit mapping table from normalized free text to the enum value. Every enum
// value maps to itself; text that is not listed is unmappable and the field becomes unknown.
var enumSynonyms = map[FieldPath]map[string]string{
	FieldChampionStatus: {
		"active": "active", "engaged": "active", "actively engaged": "active", "driving": "active", "driving the evaluation": "active",
		"enthusiastic": "active", "supportive": "active", "committed": "active", "advocating": "active", "responsive": "active",
		"weakening": "weakening", "wavering": "weakening", "less involved": "weakening", "less engaged": "weakening", "disengaging": "weakening",
		"losing interest": "weakening", "cooling": "weakening", "lukewarm": "weakening", "hesitant": "weakening", "pulling back": "weakening",
		"delegated": "delegated", "delegating": "delegated", "stepped back": "delegated", "stepping back": "delegated", "handed off": "delegated",
		"handed over": "delegated", "handing off": "delegated", "passed to a colleague": "delegated",
		"departed": "departed", "left": "departed", "left the company": "departed", "left company": "departed", "left the org": "departed",
		"no longer at the company": "departed", "resigned": "departed", "moved on": "departed", "gone": "departed",
		"inactive": "inactive", "dormant": "inactive", "silent": "inactive", "gone quiet": "inactive", "unresponsive": "inactive", "not responding": "inactive",
		Unknown: Unknown, "n/a": Unknown,
	},
	FieldRelationshipRisk: {
		"low": "low", "minimal": "low", "none": "low", "healthy": "low", "stable": "low", "safe": "low", "good": "low",
		"medium": "medium", "moderate": "medium", "mixed": "medium", "some": "medium", "elevated": "medium", "caution": "medium", "watch": "medium",
		"high": "high", "severe": "high", "critical": "high", "serious": "high", "churn risk": "high", "likely to churn": "high",
		"evaluating a competitor": "high", "evaluating competitors": "high", "considering a competitor": "high", "competitor in play": "high",
		Unknown: Unknown, "n/a": Unknown,
	},
}

// IsEnumField reports whether the state field of this claim path is a closed enum in the contract.
func IsEnumField(f FieldPath) bool { _, ok := enumSynonyms[f]; return ok }

// NormalizeEnum maps a claim value onto the contract enum of its field. ok is false when the value is
// not text or is not in the mapping table; callers then show the field as unknown while the claim keeps
// the original text.
func NormalizeEnum(f FieldPath, raw json.RawMessage) (value string, ok bool) {
	table, isEnum := enumSynonyms[f]
	if !isEnum {
		return "", false
	}
	s, isString := StringValue(raw)
	if !isString {
		return "", false
	}
	key := strings.ToLower(strings.Join(strings.Fields(strings.NewReplacer("_", " ", "-", " ").Replace(s)), " "))
	key = strings.TrimSuffix(key, " risk")
	v, found := table[key]
	return v, found
}
