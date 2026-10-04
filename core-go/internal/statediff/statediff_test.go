package statediff_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/reducer"
	"github.com/harneet2512/gtm-work/core-go/internal/statediff"
)

func known(v any, claim string) reducer.Field {
	return reducer.Field{Value: v, Known: true, EvidenceRefs: []reducer.EvidenceRef{{ActivityID: "act-" + claim, ClaimID: claim}}}
}

func unknown() reducer.Field { return reducer.Field{Value: "unknown"} }

func state(version int, mut func(*reducer.AccountState)) reducer.AccountState {
	st := reducer.AccountState{AccountID: "acct", Version: version}
	for _, n := range reducer.FieldNames() {
		*st.Fields.Field(n) = unknown()
	}
	if mut != nil {
		mut(&st)
	}
	return st
}

func TestIdenticalStatesGiveNoChange(t *testing.T) {
	a := state(1, func(s *reducer.AccountState) { s.Fields.Stage = known("Discovery", "c1") })
	b := state(2, func(s *reducer.AccountState) { s.Fields.Stage = known("Discovery", "c2") }) // a re-asserting claim
	d := statediff.Compute(&a, b, []string{"x"})
	if d.IsMaterial || len(d.Changes) != 0 || d.FromVersion != 1 || d.ToVersion != 2 {
		t.Fatalf("diff = %+v", d)
	}
}

func TestFirstStateIsDiffedAgainstEmpty(t *testing.T) {
	next := state(1, func(s *reducer.AccountState) { s.Fields.Stage = known("Discovery", "c1") })
	d := statediff.Compute(nil, next, []string{"a1"})
	c, ok := d.Change("stage")
	if !d.IsMaterial || !ok || c.Op != statediff.OpSet || c.After != "Discovery" || d.FromVersion != 0 || len(c.EvidenceRefs) != 1 {
		t.Fatalf("diff = %+v", d)
	}
}

func TestOpsSetChangedAndBecameUnknown(t *testing.T) {
	a := state(1, func(s *reducer.AccountState) {
		s.Fields.Stage = known("Discovery", "c1")
		s.Fields.Health = known("on_track", "c2")
	})
	b := state(2, func(s *reducer.AccountState) {
		s.Fields.Stage = known("Commercial review", "c3")
		s.Fields.Owner = known("p1", "c4")
	})
	d := statediff.Compute(&a, b, nil)
	want := map[string]string{"stage": statediff.OpChanged, "health": statediff.OpBecameUnknown, "owner": statediff.OpSet}
	for f, op := range want {
		if c, ok := d.Change(f); !ok || c.Op != op {
			t.Errorf("%s: %+v", f, c)
		}
	}
}

func TestKnownAbsentDiffersFromUnknown(t *testing.T) {
	prev := state(1, nil)
	next := state(2, func(s *reducer.AccountState) { s.Fields.NextMeeting = known(nil, "c1") })
	d := statediff.Compute(&prev, next, nil)
	if c, ok := d.Change("next_meeting"); !ok || c.Op != statediff.OpSet {
		t.Fatalf("unknown -> null must be a change: %+v", d)
	}
}

func TestOnlyNonMaterialFieldsMakeAnImmaterialDiff(t *testing.T) {
	a := state(1, nil)
	b := state(2, func(s *reducer.AccountState) {
		s.Fields.LastCustomerInteraction = known("2026-09-01T10:00:00Z", "c1")
		s.Fields.Summary = known("text", "c2")
	})
	d := statediff.Compute(&a, b, nil)
	if d.IsMaterial || len(d.Changes) != 2 || len(d.MaterialFields()) != 0 {
		t.Fatalf("diff = %+v", d)
	}
}

func items(status string, claim string) reducer.Field {
	return known([]reducer.Item{{Text: "SOC2 Type II.", ClaimID: claim, Status: status}}, claim)
}

func TestListItemsCompareByKeyAndStatusNotClaimID(t *testing.T) {
	a := state(1, func(s *reducer.AccountState) { s.Fields.Blockers = items("open", "c1") })
	same := state(2, func(s *reducer.AccountState) {
		s.Fields.Blockers = known([]reducer.Item{{Text: "soc2  type ii", ClaimID: "c9", Status: "open"}}, "c9")
	})
	if d := statediff.Compute(&a, same, nil); len(d.Changes) != 0 {
		t.Fatalf("reworded item must not change: %+v", d)
	}
	resolved := state(2, func(s *reducer.AccountState) { s.Fields.Blockers = items("resolved", "c1") })
	if d := statediff.Compute(&a, resolved, nil); !d.IsMaterial || d.Changes[0].Field != "blockers" {
		t.Fatalf("status change must be material: %+v", d)
	}
}

func TestDueDateAndOwnerAreState(t *testing.T) {
	due := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	mk := func(owner string) reducer.AccountState {
		return state(1, func(s *reducer.AccountState) {
			s.Fields.CurrentCommitments = known([]reducer.Item{{Text: "send", ClaimID: "c", DueAt: &due, OwnerPersonID: owner}}, "c")
		})
	}
	a, b := mk("p1"), mk("p2")
	if d := statediff.Compute(&a, b, nil); !d.IsMaterial {
		t.Fatal("owner change is material")
	}
}

func member(id, status string, roles ...string) reducer.Member {
	return reducer.Member{PersonID: id, Roles: roles, Status: status, EvidenceRefs: []reducer.EvidenceRef{{ActivityID: "act-" + id}}}
}

func TestBuyingGroupAndGaps(t *testing.T) {
	a := state(1, func(s *reducer.AccountState) { s.BuyingGroup = []reducer.Member{member("p1", "active", "champion")} })
	title := "Director"
	b := state(2, func(s *reducer.AccountState) {
		m := member("p1", "active", "champion")
		m.Title = &title // a title is not state
		s.BuyingGroup = []reducer.Member{m}
	})
	if d := statediff.Compute(&a, b, nil); len(d.Changes) != 0 {
		t.Fatalf("title change must be ignored: %+v", d)
	}
	c := state(2, func(s *reducer.AccountState) {
		s.BuyingGroup = []reducer.Member{member("p1", "active", "champion"), member("p2", "new", "security")}
		s.CoverageGaps = []string{"economic_buyer"}
	})
	d := statediff.Compute(&a, c, nil)
	bg, _ := d.Change("buying_group")
	gaps, _ := d.Change("coverage_gaps")
	if bg.Op != statediff.OpChanged || len(bg.EvidenceRefs) != 1 || bg.EvidenceRefs[0].ActivityID != "act-p2" || gaps.Op != statediff.OpSet || !d.IsMaterial {
		t.Fatalf("diff = %+v", d)
	}
	gone := state(3, nil)
	d = statediff.Compute(&c, gone, nil)
	if g, _ := d.Change("coverage_gaps"); g.Op != statediff.OpRemoved {
		t.Fatalf("diff = %+v", d)
	}
}

func TestComputeDoesNotAliasItsInputs(t *testing.T) {
	ids := []string{"a"}
	d := statediff.Compute(nil, state(1, nil), ids)
	ids[0] = "mutated"
	if d.ActivityIDs[0] != "a" {
		t.Fatal("activity ids must be copied")
	}
}

// The previous state comes back from JSON (account_state), where list items are generic maps; it must
// compare equal to the same state in its typed form (a bug found by the first pipeline run).
func TestAStateReadBackFromJSONDiffsAsUnchanged(t *testing.T) {
	typed := state(1, func(s *reducer.AccountState) {
		s.Fields.Blockers = items("open", "c1")
		s.BuyingGroup = []reducer.Member{member("p1", "active", "champion")}
	})
	raw, err := json.Marshal(typed)
	if err != nil {
		t.Fatal(err)
	}
	var decoded reducer.AccountState
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	next := typed
	next.Version = 2
	if d := statediff.Compute(&decoded, next, nil); len(d.Changes) != 0 {
		t.Fatalf("diff = %+v", d)
	}
}
