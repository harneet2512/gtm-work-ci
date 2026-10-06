package statediff_test

import (
	"testing"

	"github.com/harneet2512/gtm-work/core-go/internal/statediff"
)

func TestIsMaterialFieldFollowsTheDiffRule(t *testing.T) {
	for field, want := range map[string]bool{
		"stage": true, "blockers": true, statediff.FieldBuyingGroup: true,
		"summary": false, "last_customer_interaction": false, statediff.FieldPrimaryChanged: false,
	} {
		if got := statediff.IsMaterialField(field); got != want {
			t.Errorf("%s: %v, want %v", field, got, want)
		}
	}
}
