package abcrun

import "github.com/harneet2512/gtm-work/core-go/internal/knowledge"

// Partition splits the learned items for one situation by what the deterministic matcher says (labels: knowledge id ->
// APPLIES, DOES_NOT_APPLY or EXCEPTION_TRIGGERED, from the situation as of the replay clock). Applicable: APPLIES.
// Irrelevant: retrieved (applicable by status) but the matcher says it does not apply here. An item missing from labels
// is not retrievable at the clock (not yet created, stale or disputed) and belongs to neither set.
func Partition(items []LearnedItem, labels map[string]string) (applicable, irrelevant []LearnedItem) {
	for _, it := range items {
		switch labels[it.ID] {
		case "":
		case knowledge.LabelApplies:
			applicable = append(applicable, it)
		default:
			irrelevant = append(irrelevant, it)
		}
	}
	return applicable, irrelevant
}
