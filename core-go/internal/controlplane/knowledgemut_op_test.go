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
