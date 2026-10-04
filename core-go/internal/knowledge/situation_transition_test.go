package knowledge

import (
	"testing"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/reducer"
)

// ADR-0012: the AccountState carries the relationship state and the open transition, so a matcher built from the
// state alone (SituationAt passes none) can still match knowledge such as "after a reorg, do not push expansion".
func TestSituationReadsTheRelationshipStateFromTheAccountState(t *testing.T) {
	to := "EXPANSION"
	st := reducer.AccountState{AccountID: "a",
		RelationshipState: &reducer.RelationshipState{Value: "REORG"},
		OpenTransition:    &reducer.OpenTransition{TransitionID: "t1", FromState: "REORG", ToStateCandidate: &to, Status: "CANDIDATE"}}
	s := FromAccountState(st, time.Unix(0, 0), nil, "", nil)
	if s.RelationshipState != "REORG" || s.Transition == nil || s.Transition.ToState != "EXPANSION" || s.Transition.Status != "CANDIDATE" {
		t.Fatalf("situation = %+v, want REORG with the open CANDIDATE EXPANSION", s)
	}
	explicit := FromAccountState(st, time.Unix(0, 0), nil, "RENEWAL", &Transition{Status: "UNRESOLVED"})
	if explicit.RelationshipState != "RENEWAL" || explicit.Transition.Status != "UNRESOLVED" {
		t.Errorf("arguments win over the state: %+v", explicit)
	}
	unknown := FromAccountState(reducer.AccountState{AccountID: "a", RelationshipState: &reducer.RelationshipState{Value: "unknown"}}, time.Unix(0, 0), nil, "", nil)
	if unknown.RelationshipState != "" || unknown.Transition != nil {
		t.Errorf("an unknown state is the empty relationship state: %+v", unknown)
	}
}
