package transitions

import "testing"

func TestMarkFirstPartyReadsTheStandingOfTheClaimsASignalCites(t *testing.T) {
	rs := loadRules(t)
	cite := func(id string) Signal {
		return Signal{ID: id, EvidenceRefs: []Ref{{ActivityID: activity, ClaimID: id}}}
	}
	signals := []Signal{{ID: "activity-only", EvidenceRefs: []Ref{{ActivityID: activity}}}, cite("c-first"), cite("c-third"), cite("c-unknown")}
	got := MarkFirstParty(rs, signals, map[string]string{"c-first": "first_party_ai", "c-third": "third_party"})
	want := map[string]bool{"activity-only": true, "c-first": true, "c-third": false, "c-unknown": false}
	for _, s := range got {
		if s.FirstParty == nil || *s.FirstParty != want[s.ID] {
			t.Errorf("%s first_party = %v, want %v", s.ID, s.FirstParty, want[s.ID])
		}
	}
	if signals[1].FirstParty != nil {
		t.Error("the input slice must not be modified")
	}
	if ids := CitedClaimIDs(signals); len(ids) != 3 {
		t.Errorf("cited ids = %v", ids)
	}
}

func TestAThirdPartySignalNeverEarnsAFactButStillCounts(t *testing.T) {
	file, sets := loadParity(t)
	no := false
	for _, c := range file.Cases {
		if c.Rules != "v1" || c.Expected.Status == nil || *c.Expected.Status != StatusCandidate {
			continue
		}
		in := c.Input
		in.Signals = append([]Signal{}, in.Signals...)
		for i := range in.Signals {
			in.Signals[i].FirstParty = &no
		}
		before, after := Evaluate(sets["v1"], c.Input), Evaluate(sets["v1"], in)
		if len(after.Supporting) > len(before.Supporting) {
			t.Fatalf("%s: marking every signal third-party must not add supporting facts", c.Name)
		}
	}
}
