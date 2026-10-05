package orchestrator

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/harneet2512/gtm-work/core-go/internal/stageevents"
)

func TestSummarizeEvals(t *testing.T) {
	r := func(id, verdict string, blocking bool) evalRow {
		return evalRow{ID: id, Verdict: verdict, Blocking: blocking}
	}
	cases := []struct {
		name         string
		rows         []evalRow
		noAcceptable bool
		want         stageevents.Status
		wantIDs      int
	}{
		{"all pass", []evalRow{r("a", "pass", false), r("b", "pass", false)}, false, stageevents.Passed, 2},
		{"a warning", []evalRow{r("a", "pass", false), r("b", "warn", false)}, false, stageevents.Warning, 2},
		{"a non-blocking fail is a warning: the run still produced a set", []evalRow{r("a", "fail", false)}, false, stageevents.Warning, 1},
		{"a blocking fail on one candidate is a warning when another is acceptable", []evalRow{r("a", "fail", true), r("b", "pass", false)}, false, stageevents.Warning, 2},
		{"no acceptable candidate is a judged failure naming the blocking results", []evalRow{r("a", "fail", true), r("b", "fail", true)}, true, stageevents.Failed, 2},
		{"no acceptable candidate but nothing blocking: still a warning, never an unnamed failure", []evalRow{r("a", "warn", false)}, true, stageevents.Warning, 1},
		{"an abstain is not a pass: a judge that errored says nothing", []evalRow{r("a", "pass", false), r("b", "abstain", false)}, false, stageevents.Warning, 2},
		{"only abstains: no judge gave a verdict, so unknown, never passed", []evalRow{r("a", "abstain", false), r("b", "abstain", false)}, false, stageevents.Unknown, 2},
		{"an unrecognised verdict is not a pass either", []evalRow{r("a", "pass", false), r("b", "mystery", false)}, false, stageevents.Warning, 2},
		{"a blocking result that did not fail cannot make a failed stage", []evalRow{r("a", "abstain", true), r("b", "pass", false)}, true, stageevents.Warning, 2},
		{"no judged results cannot be called passed", nil, false, stageevents.Unknown, 0},
	}
	for _, tc := range cases {
		status, out := summarizeEvals(tc.rows, tc.noAcceptable)
		if status != tc.want || len(out.EvalResultIDs) != tc.wantIDs {
			t.Errorf("%s: %s with %d ids, want %s with %d", tc.name, status, len(out.EvalResultIDs), tc.want, tc.wantIDs)
		}
		if status == stageevents.Failed && len(out.EvalResultIDs) == 0 {
			t.Errorf("%s: a failed evals stage must name its results", tc.name)
		}
	}
}

func TestSummarizeEvalsCountsAbstainsInTheDetail(t *testing.T) {
	_, out := summarizeEvals([]evalRow{{ID: "a", Verdict: "pass"}, {ID: "b", Verdict: "abstain"}, {ID: "c", Verdict: "abstain"}}, false)
	if !strings.Contains(out.Detail, "2 abstain") {
		t.Errorf("detail = %q, want the abstain count", out.Detail)
	}
	_, all := summarizeEvals([]evalRow{{ID: "a", Verdict: "abstain"}}, false)
	if !strings.Contains(all.Detail, "no judge gave a verdict") {
		t.Errorf("detail = %q, want it to say no judge gave a verdict", all.Detail)
	}
}

func TestStageFailureKinds(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want stageevents.FailureKind
	}{
		{"a worker that could not answer is transport", workerFailure("judge", errors.New("503")), stageevents.Transport},
		{"a cancelled worker call is transport", workerFailure("judge", context.Canceled), stageevents.Transport},
		{"a database error the run retries is internal, not transport", transient("read world", errors.New("pq: syntax error")), stageevents.Internal},
		{"a bug the run settles as transient is internal, not transport", &TransientError{Phase: "run", Reason: "nil pointer", Err: errors.New("nil pointer")} /* what settle makes of an unclassified error */, stageevents.Internal},
		{"unusable output is a contract failure", permanent("generate", &invalidOutput{Violations: []string{"x"}}), stageevents.Contract},
		{"an unclassified permanent failure is internal", permanent("context", errors.New("no state")), stageevents.Internal},
		{"a bare invalid output is a contract failure", &invalidOutput{Violations: []string{"x"}}, stageevents.Contract},
		{"a cancelled run is transport", context.Canceled, stageevents.Transport},
		{"a timed-out database call is transport", transient("claim", context.DeadlineExceeded), stageevents.Transport},
	}
	for _, tc := range cases {
		if got := stageevents.ClassifyError(stageFailure(tc.err)); got != tc.want {
			t.Errorf("%s: %s, want %s", tc.name, got, tc.want)
		}
	}
}
