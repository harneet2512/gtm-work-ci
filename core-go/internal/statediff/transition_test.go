package statediff

import (
	"testing"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/reducer"
)

func withTransition(rel string, open *reducer.OpenTransition) reducer.AccountState {
	return reducer.AccountState{AccountID: "a", Version: 1, RelationshipState: &reducer.RelationshipState{Value: rel}, OpenTransition: open}
}

func candidate(to string) *reducer.OpenTransition {
	return &reducer.OpenTransition{TransitionID: "t1", FromState: "REORG", ToStateCandidate: &to, Status: "CANDIDATE", LastUpdatedAt: time.Unix(0, 0)}
}

func TestAChangeOfRelationshipStateIsAMaterialChange(t *testing.T) {
	prev, next := withTransition("unknown", nil), withTransition("REORG", nil)
	next.Version = 2
	d := Compute(&prev, next, []string{"x"})
	c, ok := d.Change(FieldRelationshipState)
	if !ok || !d.IsMaterial || !c.Material || c.Before != "unknown" || c.After != "REORG" {
		t.Fatalf("diff = %+v, want a material relationship_state change unknown -> REORG", d)
	}
}

func TestAnOpenTransitionAppearingChangingAndClosingIsReported(t *testing.T) {
	none, cand, other := withTransition("REORG", nil), withTransition("REORG", candidate("EXPANSION")), withTransition("REORG", candidate("RECOVERY"))
	for name, tc := range map[string]struct {
		prev, next reducer.AccountState
		op         string
	}{
		"opens": {none, cand, OpSet}, "retargets": {cand, other, OpChanged}, "closes": {cand, none, OpRemoved},
	} {
		d := Compute(&tc.prev, tc.next, nil)
		c, ok := d.Change(FieldOpenTransition)
		if !ok || c.Op != tc.op || !c.Material {
			t.Errorf("%s: change = %+v ok=%v, want a material %s", name, c, ok, tc.op)
		}
	}
}

func TestAnUnchangedTransitionOrNewFactsAreNotAChange(t *testing.T) {
	a, b := withTransition("REORG", candidate("EXPANSION")), withTransition("REORG", candidate("EXPANSION"))
	b.OpenTransition.Confidence = 0.8
	b.OpenTransition.MissingFacts = []reducer.MissingFact{{Key: "x", Required: true}}
	d := Compute(&a, b, nil)
	if _, ok := d.Change(FieldOpenTransition); ok {
		t.Error("the same open transition with different facts is not a change of state")
	}
	if _, ok := d.Change(FieldRelationshipState); ok {
		t.Error("an unchanged relationship state is not a change")
	}
}
