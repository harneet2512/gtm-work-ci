package controlplane

import (
	"testing"
	"time"
)

func res(id, evalType, verdict string, blocking bool) result {
	return result{ID: id, EvalType: evalType, Verdict: verdict, Blocking: blocking, CreatedAt: time.Unix(0, 0).UTC()}
}

func TestTallyCountsEveryVerdictAndOnlyBlockingFailsAreBlocking(t *testing.T) {
	got := tally([]result{
		res("1", "grounding", "pass", false), res("2", "grounding", "pass", false), res("3", "cta_calibration", "warn", false),
		res("4", "champion_continuity", "fail", true), res("5", "champion_continuity", "fail", false),
		res("6", "evidence_sufficiency", "abstain", false),
	})
	want := Counts{Pass: 2, Warn: 1, Fail: 2, Unknown: 1, BlockingFail: 1, Total: 6}
	if got != want {
		t.Fatalf("tally = %+v, want %+v", got, want)
	}
}

func TestTallyOfNothingIsAllZeros(t *testing.T) {
	if got := tally(nil); got != (Counts{}) {
		t.Fatalf("tally(nil) = %+v", got)
	}
}

func TestMinusIsThisMinusPreviousAndMayBeNegative(t *testing.T) {
	this, prev := Counts{Pass: 3, Warn: 1, Fail: 0, Unknown: 1, BlockingFail: 0, Total: 5}, Counts{Pass: 1, Warn: 1, Fail: 2, Unknown: 0, BlockingFail: 1, Total: 4}
	want := Counts{Pass: 2, Warn: 0, Fail: -2, Unknown: 1, BlockingFail: -1, Total: 1}
	if got := this.Minus(prev); got != want {
		t.Fatalf("delta = %+v, want %+v", got, want)
	}
}

func TestLadderOrdersFailWarnUnknownPassAndFoldsAbstain(t *testing.T) {
	order := []string{"fail", "warn", "unknown", "pass"}
	for i := 1; i < len(order); i++ {
		if rank(order[i-1]) >= rank(order[i]) {
			t.Fatalf("%s must rank below %s", order[i-1], order[i])
		}
	}
	if rank("abstain") != rank("unknown") || normalizeVerdict("abstain") != "unknown" {
		t.Fatal("abstain is the unknown verdict")
	}
}

func TestWorstOfPicksTheLowestRungAndFlagsBlocking(t *testing.T) {
	verdict, blocking, ids := worstOf([]result{
		res("a", "grounding", "pass", false), res("b", "grounding", "warn", false), res("c", "grounding", "fail", true), res("d", "grounding", "fail", false),
	})
	if verdict != "fail" || !blocking || len(ids) != 4 || ids[0] != "a" {
		t.Fatalf("worstOf = %s %v %v", verdict, blocking, ids)
	}
	if v, b, _ := worstOf([]result{res("x", "grounding", "abstain", false), res("y", "grounding", "pass", false)}); v != "unknown" || b {
		t.Fatalf("an abstain outranks a pass: %s %v", v, b)
	}
	if v, _, ids := worstOf(nil); v != "" || len(ids) != 0 {
		t.Fatalf("worstOf(nil) = %q %v", v, ids)
	}
}

func TestOverallChange(t *testing.T) {
	cases := []struct {
		name string
		o    Overall
		want string
	}{
		{"nothing moved", Overall{Unchanged: 3}, "unchanged"},
		{"a regression wins", Overall{Improved: 2, Regressed: 1}, "regressed"},
		{"an eval type that is newly checked and fails is a regression", Overall{Unchanged: 2, AddedFail: 1, Added: 1}, "regressed"},
		{"a newly checked eval type that passes is not a change", Overall{Unchanged: 2, Added: 1}, "unchanged"},
		{"an improvement", Overall{Improved: 1, Unchanged: 1}, "improved"},
		{"only inconclusive moves", Overall{Inconclusive: 1, Unchanged: 1}, "inconclusive"},
		{"an improvement is not hidden by an inconclusive row", Overall{Improved: 1, Inconclusive: 1}, "improved"},
	}
	for _, c := range cases {
		if got := overallChange(c.o); got != c.want {
			t.Errorf("%s: %s, want %s", c.name, got, c.want)
		}
	}
}

func TestChangeReadsTheLadder(t *testing.T) {
	cases := []struct {
		a, b string
		want string
	}{
		{"fail", "pass", "improved"}, {"warn", "pass", "improved"}, {"fail", "warn", "improved"},
		{"pass", "fail", "regressed"}, {"warn", "fail", "regressed"},
		// unknown is not a step on the ladder: a move into or out of it says nothing about better or worse.
		{"unknown", "pass", "inconclusive"}, {"pass", "unknown", "inconclusive"}, {"warn", "unknown", "inconclusive"},
		{"unknown", "warn", "inconclusive"}, {"fail", "unknown", "inconclusive"}, {"unknown", "fail", "inconclusive"},
		{"unknown", "unknown", "unchanged"},
		{"pass", "pass", "unchanged"}, {"fail", "fail", "unchanged"},
		{"", "pass", "added"}, {"fail", "", "removed"},
	}
	for _, c := range cases {
		if got := changeOf(c.a, c.b); got != c.want {
			t.Errorf("changeOf(%q, %q) = %s, want %s", c.a, c.b, got, c.want)
		}
	}
}
