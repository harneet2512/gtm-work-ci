package controlplane

import "testing"

func str(s string) *string { return &s }

func TestOpForClassifiesStatusMovesIncludingDemotions(t *testing.T) {
	ev := evidenceRow{kind: "human_decision"}
	neg := evidenceRow{kind: "customer_reaction", note: str("polarity=negative")}
	move := func(from, to string) *historyRow {
		h := &historyRow{to: str(to)}
		if from != "" {
			h.from = str(from)
		}
		return h
	}
	cases := []struct {
		name string
		ev   evidenceRow
		link *historyRow
		want string
	}{
		{"no status change is support", ev, nil, opSupport},
		{"a negative reaction with no status change is a counterexample", neg, nil, opCounterexample},
		{"up the ladder", ev, move("candidate", "provisional"), opPromote},
		{"up the ladder to confirmed", ev, move("supported", "confirmed"), opPromote},
		{"the first status of an object", ev, move("", "candidate"), opPromote},
		{"back onto the ladder from disputed", ev, move("disputed", "provisional"), opPromote},
		{"back onto the ladder from stale", ev, move("stale", "supported"), opPromote},
		{"down the ladder is a demotion, not a promotion", neg, move("confirmed", "supported"), opDemote},
		{"down from supported", neg, move("supported", "provisional"), opDemote},
		{"down to candidate", neg, move("provisional", "candidate"), opDemote},
		{"into disputed", neg, move("supported", "disputed"), opDispute},
		{"into stale", ev, move("confirmed", "stale"), opStale},
	}
	for _, c := range cases {
		if got := opFor(c.ev, c.link); got != c.want {
			t.Errorf("%s: %s, want %s", c.name, got, c.want)
		}
	}
}

// TestDerivedOperationsAreHAR97Words: a derived mutation only ever carries a word of the contract's vocabulary, and a
// move up the ladder is told apart from a move down by direction (STRENGTHEN / WEAKEN), not by a private word.
func TestDerivedOperationsAreHAR97Words(t *testing.T) {
	allowed := map[string]bool{"CREATE": true, "STRENGTHEN": true, "WEAKEN": true, "DISPUTE": true, "MARK_STALE": true}
	for _, op := range []string{opCreate, opSupport, opCounterexample, opPromote, opDemote, opDispute, opStale} {
		if !allowed[op] {
			t.Errorf("%q is not an operation a derived mutation may carry", op)
		}
	}
	if opPromote == opDemote || opSupport == opCounterexample {
		t.Error("strengthening and weakening must differ")
	}
}
