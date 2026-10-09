package knowledge

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

func contractRules(t *testing.T) Rules {
	t.Helper()
	r, err := LoadRules(repoFile(t, "contracts/knowledge/lifecycle.v1.json"))
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func candidate() Knowledge {
	kn := keepChampion()
	kn.Status, kn.CreatedAt = StatusCandidate, now.Add(-30*day)
	return kn
}

// evidenceSeq keeps evidence ids unique across feed calls.
var evidenceSeq int

// feed records n pieces of evidence of one kind and returns the knowledge and every change.
func feed(t *testing.T, kn Knowledge, rules Rules, n int, kind, detail string, at time.Time) (Knowledge, []Change) {
	t.Helper()
	var changes []Change
	for i := 0; i < n; i++ {
		evidenceSeq++
		ev := Evidence{Kind: kind, RefID: fmt.Sprintf("%s-%d", kind, evidenceSeq), At: at}
		switch kind {
		case EvidenceCustomerReaction:
			ev.Polarity = detail
		case EvidenceBusinessOutcome:
			ev.OutcomeType = detail
		case EvidenceCounterexample:
			ev.Note = "direct handoff worked: " + detail
		}
		next, ch, err := Record(kn, ev, rules)
		if err != nil {
			t.Fatal(err)
		}
		if ch != nil {
			changes = append(changes, *ch)
		}
		kn = next
	}
	return kn, changes
}

func statuses(cs []Change) string {
	parts := make([]string, 0, len(cs))
	for _, c := range cs {
		parts = append(parts, c.From+">"+c.To)
	}
	return strings.Join(parts, ",")
}

// HAR-118 seeded history: 8 decisions, 6 positive reactions, 3 advanced outcomes, 1 counterexample.
func TestK17LikeHistoryIsSupported(t *testing.T) {
	rules := contractRules(t)
	kn, c1 := feed(t, candidate(), rules, 1, EvidenceDecisionEpisode, "", now)
	if len(c1) != 0 {
		t.Fatalf("a decision alone must not promote: %v", c1)
	}
	kn, c2 := feed(t, kn, rules, 1, EvidenceCustomerReaction, "positive", now)
	kn, c3 := feed(t, kn, rules, 7, EvidenceDecisionEpisode, "", now)
	kn, c4 := feed(t, kn, rules, 5, EvidenceCustomerReaction, "positive", now)
	kn, _ = feed(t, kn, rules, 3, EvidenceBusinessOutcome, "stage_advanced", now)
	kn, _ = feed(t, kn, rules, 1, EvidenceCounterexample, "champion left", now)
	all := append(append(append(c1, c2...), c3...), c4...)
	if kn.Status != StatusSupported || statuses(all) != "candidate>provisional,provisional>supported" {
		t.Fatalf("status %s after %s", kn.Status, statuses(all))
	}
	want := Counts{Decisions: 8, PositiveReactions: 6, OutcomesAdvanced: 3, Counterexamples: 1}
	if kn.Counts != want || len(kn.SupportingDecisionEpisodeIDs) != 8 || len(kn.Counterexamples) != 1 {
		t.Fatalf("counts %+v episodes %d", kn.Counts, len(kn.SupportingDecisionEpisodeIDs))
	}
	if !strings.Contains(all[1].Reason, "earned supported: 8 decisions (>= 5), 3 positive reactions (>= 3)") {
		t.Fatalf("reason = %q", all[1].Reason)
	}
}

func TestLadderRungs(t *testing.T) {
	rules := contractRules(t)
	cases := []struct {
		name string
		c    Counts
		want string
	}{
		{"nothing", Counts{}, StatusCandidate},
		{"one edit and one positive reaction", Counts{Decisions: 1, PositiveReactions: 1}, StatusProvisional},
		{"4 decisions, 3 positive (HAR-97 §18 card)", Counts{Decisions: 4, PositiveReactions: 3}, StatusProvisional},
		{"supported", Counts{Decisions: 5, PositiveReactions: 3}, StatusSupported},
		{"confirmation needs outcomes", Counts{Decisions: 12, PositiveReactions: 9, OutcomesAdvanced: 2}, StatusSupported},
		{"confirmed", Counts{Decisions: 10, PositiveReactions: 6, OutcomesAdvanced: 3, Counterexamples: 1}, StatusConfirmed},
		{"too many counterexamples for supported", Counts{Decisions: 5, PositiveReactions: 3, Counterexamples: 2}, StatusProvisional},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			kn := candidate()
			kn.Counts = tc.c
			if got, _ := EvaluateLifecycle(kn, now, rules); got.Status != tc.want {
				t.Fatalf("status %s, want %s", got.Status, tc.want)
			}
		})
	}
}

func TestDisputeAndRecovery(t *testing.T) {
	rules := contractRules(t)
	kn := candidate()
	kn.Status, kn.Counts = StatusSupported, Counts{Decisions: 8, PositiveReactions: 6, Counterexamples: 3}
	if _, ch := EvaluateLifecycle(kn, now, rules); ch != nil {
		t.Fatalf("share 0.27 must not dispute: %+v", ch)
	}
	kn, ch := feed(t, kn, rules, 1, EvidenceCounterexample, "x", now)
	if kn.Status != StatusDisputed || len(ch) != 1 || !strings.HasPrefix(ch[0].Reason, "disputed: 4 counterexamples") {
		t.Fatalf("status %s changes %+v", kn.Status, ch)
	}
	kn, ch = feed(t, kn, rules, 2, EvidenceDecisionEpisode, "", now)
	if kn.Status != StatusProvisional || !strings.HasPrefix(ch[0].Reason, "recovered from disputed: earned provisional") {
		t.Fatalf("status %s changes %+v", kn.Status, ch)
	}
}

func TestNegativeReactionsDispute(t *testing.T) {
	rules := contractRules(t)
	kn := candidate()
	kn.Status, kn.Counts = StatusProvisional, Counts{Decisions: 3, PositiveReactions: 2}
	kn, ch := feed(t, kn, rules, 3, EvidenceCustomerReaction, "negative", now)
	if kn.Status != StatusDisputed || !strings.Contains(ch[0].Reason, "3 negative reactions") {
		t.Fatalf("status %s changes %+v", kn.Status, ch)
	}
}

func TestStaleAndRevalidation(t *testing.T) {
	rules := contractRules(t)
	kn := candidate()
	validated := now.Add(-181 * day)
	kn.Status, kn.Counts, kn.LastValidatedAt = StatusSupported, Counts{Decisions: 5, PositiveReactions: 3}, &validated
	kn, ch := EvaluateLifecycle(kn, now, rules)
	if kn.Status != StatusStale || ch == nil || ch.Reason != "stale: not validated for 181 days (>= 180)" {
		t.Fatalf("status %s change %+v", kn.Status, ch)
	}
	kn, chs := feed(t, kn, rules, 1, EvidenceCustomerReaction, "neutral", now)
	if kn.Status != StatusStale || len(chs) != 0 {
		t.Fatal("a neutral reaction does not re-validate")
	}
	kn, chs = feed(t, kn, rules, 1, EvidenceCustomerReaction, "positive", now)
	if kn.Status != StatusSupported || !kn.LastValidatedAt.Equal(now) || !strings.HasPrefix(chs[0].Reason, "recovered from stale") {
		t.Fatalf("status %s changes %+v", kn.Status, chs)
	}
	fresh := candidate()
	fresh.CreatedAt = time.Time{}
	if got, _ := EvaluateLifecycle(fresh, now, rules); got.Status != StatusCandidate {
		t.Fatal("no anchor time: never stale")
	}
}

func TestLadderNeverMovesDown(t *testing.T) {
	rules := contractRules(t)
	kn := candidate()
	kn.Status, kn.Counts = StatusConfirmed, Counts{Decisions: 10, PositiveReactions: 6, OutcomesAdvanced: 3, Counterexamples: 2}
	if got, ch := EvaluateLifecycle(kn, now, rules); got.Status != StatusConfirmed || ch != nil {
		t.Fatalf("share 0.17 demoted confirmed: %s", got.Status)
	}
}

func TestEvidenceThatMovesNoCount(t *testing.T) {
	rules := contractRules(t)
	kn := candidate()
	for _, ev := range []Evidence{
		{Kind: EvidenceHumanDecision, RefID: "d1", At: now},
		{Kind: EvidenceBusinessOutcome, RefID: "o1", OutcomeType: "closed_lost", At: now},
		{Kind: EvidenceCustomerReaction, RefID: "r1", Polarity: "neutral", At: now},
	} {
		next, ch, err := Record(kn, ev, rules)
		if err != nil || ch != nil || next.Counts != (Counts{}) || next.LastValidatedAt != nil {
			t.Fatalf("%s: counts %+v change %+v err %v", ev.Kind, next.Counts, ch, err)
		}
	}
}

func TestInvalidEvidence(t *testing.T) {
	rules := contractRules(t)
	kn := candidate()
	kn.SupportingDecisionEpisodeIDs = []string{"e1"}
	cases := map[string]Evidence{
		"unknown kind":            {Kind: "rumour", RefID: "x", At: now},
		"missing ref":             {Kind: EvidenceDecisionEpisode, At: now},
		"missing time":            {Kind: EvidenceDecisionEpisode, RefID: "x"},
		"bad polarity":            {Kind: EvidenceCustomerReaction, RefID: "x", Polarity: "happy", At: now},
		"bad outcome":             {Kind: EvidenceBusinessOutcome, RefID: "x", OutcomeType: "won", At: now},
		"counterexample, no note": {Kind: EvidenceCounterexample, RefID: "x", At: now},
		"duplicate episode":       {Kind: EvidenceDecisionEpisode, RefID: "e1", At: now},
	}
	for name, ev := range cases {
		if _, _, err := Record(kn, ev, rules); !errors.Is(err, ErrInvalidEvidence) {
			t.Fatalf("%s: err = %v", name, err)
		}
	}
}

func TestApplyEvidenceDoesNotWriteThroughItsInput(t *testing.T) {
	kn := candidate()
	kn.SupportingDecisionEpisodeIDs = make([]string, 0, 4)
	validated := now.Add(-day)
	kn.LastValidatedAt = &validated
	next, err := ApplyEvidence(kn, Evidence{Kind: EvidenceDecisionEpisode, RefID: "e9", At: now})
	if err != nil {
		t.Fatal(err)
	}
	if len(kn.SupportingDecisionEpisodeIDs) != 0 || kn.Counts.Decisions != 0 || !kn.LastValidatedAt.Equal(validated) {
		t.Fatal("input changed")
	}
	if next.Counts.Decisions != 1 || !next.LastValidatedAt.Equal(validated) {
		t.Fatalf("next = %+v", next.Counts)
	}
}
