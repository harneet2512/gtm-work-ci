package learning

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var testGoldDir = []string{"testdata/gold"}

func TestLoadGoldCases(t *testing.T) {
	cases, err := loadGoldCases(testGoldDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(cases) != 4 {
		t.Fatalf("loaded %d cases, want 4", len(cases))
	}
	ids := map[string]bool{}
	for _, c := range cases {
		ids[c.ID] = true
	}
	for _, want := range []string{"learn-agree-fail", "learn-agree-pass", "learn-false-block", "learn-false-pass"} {
		if !ids[want] {
			t.Fatalf("case %s not loaded; got %v", want, ids)
		}
	}
}

func TestExpectedVerdict(t *testing.T) {
	c := goldCase{Expected: []struct {
		EvalType string `json:"eval_type"`
		Verdict  string `json:"verdict"`
	}{{EvalType: "cta_calibration", Verdict: "fail"}}}
	if got := c.expectedVerdict("cta_calibration"); got != "fail" {
		t.Fatalf("verdict = %q", got)
	}
	if got := c.expectedVerdict("grounding"); got != "" {
		t.Fatalf("an axis the case does not assert = %q, want empty", got)
	}
}

func TestGoldMetrics(t *testing.T) {
	cases, err := loadGoldCases(testGoldDir)
	if err != nil {
		t.Fatal(err)
	}
	spec := Spec{LiteralChanges: []LiteralChange{{Kind: "channel_changed", After: "slack"}}}
	g, err := goldMetrics("channel_appropriateness", spec, cases, testGoldDir)
	if err != nil {
		t.Fatal(err)
	}
	if g.Cases != 4 || g.GoldFail != 2 || g.Scored != 4 {
		t.Fatalf("gold metrics = %+v", g)
	}
	if g.Agree != 2 || g.FalsePass != 1 || g.FalseBlock != 1 {
		t.Fatalf("agreement tallies = %+v", g)
	}
	if g.Agreement == nil || *g.Agreement != 0.5 {
		t.Fatalf("agreement = %v, want 0.5", g.Agreement)
	}
}

func TestGoldMetricsSkipsNonAgnosticSpecsAndUnassertedAxes(t *testing.T) {
	cases, err := loadGoldCases(testGoldDir)
	if err != nil {
		t.Fatal(err)
	}
	personBound := Spec{LiteralChanges: []LiteralChange{{Kind: "recipient_added",
		After: map[string]any{"person_id": "0b0e0000-0000-4000-8000-0000000000a1", "role": "to"}}}}
	g, err := goldMetrics("channel_appropriateness", personBound, cases, testGoldDir)
	if err != nil {
		t.Fatal(err)
	}
	if g.Cases != 4 || g.Scored != 0 || g.Agreement != nil {
		t.Fatalf("a person-bound spec counts coverage but scores nothing: %+v", g)
	}
	g, err = goldMetrics("trajectory", Spec{}, cases, testGoldDir)
	if err != nil {
		t.Fatal(err)
	}
	if g.Cases != 0 {
		t.Fatalf("no case asserts trajectory: %+v", g)
	}
}

func TestReportOfGate(t *testing.T) {
	at := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	for name, tc := range map[string]struct {
		evaluator string
		ep        EpisodeMetrics
		gold      GoldMetrics
		gate      Gate
		passed    bool
		reason    string
	}{
		"not enough support": {"channel_appropriateness",
			EpisodeMetrics{Supporting: 0}, GoldMetrics{Cases: 4}, DefaultGate, false, "below the gate 1"},
		"not enough gold": {"channel_appropriateness",
			EpisodeMetrics{Supporting: 3}, GoldMetrics{Cases: 0}, DefaultGate, false, "gold coverage 0"},
		"passes": {"channel_appropriateness",
			EpisodeMetrics{Supporting: 2}, GoldMetrics{Cases: 4}, DefaultGate, true, "clear the gate"},
		"corrected verdicts support a human_delta candidate without gold": {"human_delta",
			EpisodeMetrics{Supporting: 0, Corrected: 2}, GoldMetrics{Cases: 0}, DefaultGate, true, "clear the gate"},
		"a judged axis still needs its gold": {"cta_calibration",
			EpisodeMetrics{Supporting: 5}, GoldMetrics{Cases: 0}, DefaultGate, false, "gold coverage 0"},
		"trajectory never needs gold either": {"trajectory",
			EpisodeMetrics{Supporting: 1}, GoldMetrics{Cases: 0}, DefaultGate, true, "clear the gate"},
	} {
		t.Run(name, func(t *testing.T) {
			rep := reportOf(candidateRow{Evaluator: tc.evaluator}, 2, tc.ep, tc.gold, tc.gate, at)
			if rep.Passed != tc.passed || !strings.Contains(rep.Reason, tc.reason) {
				t.Fatalf("report = %+v, want passed=%v reason~%q", rep, tc.passed, tc.reason)
			}
		})
	}
}

// A retired gold directory (invented accounts) is never read: its cases are skipped and the backtest says there is no
// gold rather than passing on them.
func TestLoadGoldCasesSkipsARetiredDirectory(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "type"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "type", "c.json"), []byte(`{"id":"c","should_pass":true,"candidate_action":{},"expected":[]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	cases, err := loadGoldCases([]string{dir})
	if err != nil || len(cases) != 1 {
		t.Fatalf("before retirement: %d cases, %v", len(cases), err)
	}
	if err := os.WriteFile(filepath.Join(dir, "RETIRED.json"), []byte(`{"retired":"invented","reason":"invented companies"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	cases, err = loadGoldCases([]string{dir, testGoldDir[0]})
	if err != nil || len(cases) != 4 {
		t.Fatalf("after retirement: %d cases (want the 4 of the live dir), %v", len(cases), err)
	}
}

func TestReportOfSaysNoGoldWhenTheOnlyGoldIsRetired(t *testing.T) {
	at := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)
	gold := GoldMetrics{Cases: 0, Retired: []string{"fixtures/evals/cases"}}
	rep := reportOf(candidateRow{Evaluator: "stakeholder_selection"}, 1, EpisodeMetrics{Supporting: 3}, gold, DefaultGate, at)
	if rep.Passed || !strings.HasPrefix(rep.Reason, "no gold") || !strings.Contains(rep.Reason, "fixtures/evals/cases") {
		t.Fatalf("passed %v, reason %q", rep.Passed, rep.Reason)
	}
}

func TestReportOfDoesNotBlameRetirementWhenLiveGoldExistsButDoesNotAssertTheAxis(t *testing.T) {
	at := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)
	gold := GoldMetrics{Cases: 0, Dirs: []string{"live"}}
	rep := reportOf(candidateRow{Evaluator: "stakeholder_selection"}, 1, EpisodeMetrics{Supporting: 3}, gold, DefaultGate, at)
	if rep.Passed || strings.HasPrefix(rep.Reason, "no gold") {
		t.Fatalf("passed %v, reason %q", rep.Passed, rep.Reason)
	}
}
