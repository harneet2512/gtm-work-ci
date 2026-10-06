package demorun

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// fakePlayCore scripts core: each Runs call returns the next snapshot, surface messages appear at set poll counts.
type fakePlayCore struct {
	inv        Invisibility
	invErr     error
	play       PlayOutcome
	playErr    error
	latestBI   [2]string
	runsByPoll [][]RunInfo
	polls      int
	m1At, m2At int // poll number from which the message is posted (0 = never)
	played     bool
	latestErr  error
}

func (f *fakePlayCore) Invisibility(context.Context, string) (Invisibility, error) {
	return f.inv, f.invErr
}
func (f *fakePlayCore) Play(context.Context, string) (PlayOutcome, error) {
	f.played = true
	return f.play, f.playErr
}
func (f *fakePlayCore) LatestBI(context.Context, string) (string, string, error) {
	return f.latestBI[0], f.latestBI[1], f.latestErr
}
func (f *fakePlayCore) PlayRuns(context.Context, string) ([]RunInfo, error) {
	f.polls++
	i := f.polls - 1
	if i >= len(f.runsByPoll) {
		i = len(f.runsByPoll) - 1
	}
	return f.runsByPoll[i], nil
}
func (f *fakePlayCore) SurfaceMessageTS(_ context.Context, subject, _, kind string) (string, error) {
	switch {
	case kind == "bi" && f.m1At > 0 && f.polls >= f.m1At:
		return "1700000000.000100", nil
	case kind == "chooser" && f.m2At > 0 && f.polls >= f.m2At:
		return "1700000009.000200", nil
	}
	return "", nil
}

type fakeClock struct{ now time.Time }

func (c *fakeClock) Now() time.Time { return c.now }
func (c *fakeClock) Sleep(_ context.Context, d time.Duration) error {
	c.now = c.now.Add(d)
	return nil
}

func playOpts(c *fakeClock) PlayOptions {
	return PlayOptions{Timeout: 10 * time.Minute, Poll: 3 * time.Second, SlackExpected: true, Now: c.Now, Sleep: c.Sleep}
}

func seeded() DemoState {
	return DemoState{ManifestID: "m-1", AccountID: "acct-1", CaseName: "MedTech Advances"}
}

func happyCore() *fakePlayCore {
	return &fakePlayCore{
		inv:  Invisibility{Status: "withheld"},
		play: PlayOutcome{AccountChangeID: "ac-1", AccountID: "acct-1", BIUpdateID: "bi-1", BISummary: "Budget approval moved"},
		runsByPoll: [][]RunInfo{
			{},
			{{ID: "run-1", Phase: "queued"}},
			{{ID: "run-1", Phase: "generating"}},
			{{ID: "run-1", Phase: "evaluating"}},
			{{ID: "run-1", Phase: "published", StrategySetID: "ss-1", EpisodeID: "ep-1"}},
			{{ID: "run-1", Phase: "published", StrategySetID: "ss-1", EpisodeID: "ep-1"}},
		},
		m1At: 2, m2At: 5,
	}
}

func TestRunPlayReportsBIUpdateM1RunPhasesAndM2InOrder(t *testing.T) {
	core, clk := happyCore(), &fakeClock{now: time.Unix(0, 0)}
	var lines []string
	st, res, err := RunPlay(context.Background(), core, seeded(), playOpts(clk), func(s string) { lines = append(lines, s) })
	if err != nil {
		t.Fatalf("RunPlay: %v", err)
	}
	all := strings.Join(lines, "\n")
	order := []string{"BI update written: bi-1", "run run-1: queued", "M1 posted", "1700000000.000100", "generating", "evaluating", "published", "M2 posted", "1700000009.000200"}
	pos := -1
	for _, want := range order {
		i := strings.Index(all[pos+1:], want)
		if i < 0 {
			t.Fatalf("missing or out of order %q in:\n%s", want, all)
		}
		pos += 1 + i
	}
	if !res.Published || res.M1TS == "" || res.M2TS == "" || res.RunID != "run-1" || res.EpisodeID != "ep-1" {
		t.Fatalf("result = %+v", res)
	}
	if st.RunID != "run-1" || st.BIUpdateID != "bi-1" || st.EpisodeID != "ep-1" || st.StrategySetID != "ss-1" || st.PlayedAt.IsZero() {
		t.Fatalf("state not updated: %+v", st)
	}
}

func TestRunPlayFollowsARunThatAlreadyExistsWhenPlayStarts(t *testing.T) {
	// A Play that was interrupted is resumed and its pipeline finishes on its own, so the run may predate this call.
	core, clk := happyCore(), &fakeClock{now: time.Unix(0, 0)}
	core.runsByPoll = [][]RunInfo{{{ID: "run-1", Phase: "published", StrategySetID: "ss-1", EpisodeID: "ep-1"}}}
	core.m1At, core.m2At = 1, 1
	_, res, err := RunPlay(context.Background(), core, seeded(), playOpts(clk), func(string) {})
	if err != nil || res.RunID != "run-1" || !res.Published {
		t.Fatalf("res=%+v err=%v", res, err)
	}
}

func TestRunPlayKeepsFollowingTheRememberedRunWhenSeveralExist(t *testing.T) {
	core, clk := happyCore(), &fakeClock{now: time.Unix(0, 0)}
	core.runsByPoll = [][]RunInfo{{{ID: "run-new", Phase: "failed", Reason: "x"}, {ID: "run-1", Phase: "published", StrategySetID: "ss-1", EpisodeID: "ep-1"}}}
	core.m1At, core.m2At = 1, 1
	st := seeded()
	st.RunID = "run-1"
	_, res, err := RunPlay(context.Background(), core, st, playOpts(clk), func(string) {})
	if err != nil || res.RunID != "run-1" {
		t.Fatalf("the remembered run must win over a newer one: res=%+v err=%v", res, err)
	}
}

func TestRunPlayStopsOnAFailedRunWithItsReason(t *testing.T) {
	core, clk := happyCore(), &fakeClock{now: time.Unix(0, 0)}
	core.runsByPoll = [][]RunInfo{{{ID: "run-1", Phase: "failed", Reason: "provider returned 402 insufficient credits"}}}
	_, res, err := RunPlay(context.Background(), core, seeded(), playOpts(clk), func(string) {})
	if err == nil || !strings.Contains(err.Error(), "failed") || !strings.Contains(err.Error(), "402") {
		t.Fatalf("want the failure and its reason, got %v", err)
	}
	if res.RunID != "run-1" {
		t.Fatalf("the failed run must still be named: %+v", res)
	}
}

func TestRunPlayAnnouncesAPausedRunOnceNotEveryPoll(t *testing.T) {
	core, clk := happyCore(), &fakeClock{now: time.Unix(0, 0)}
	core.runsByPoll = [][]RunInfo{
		{{ID: "run-1", Phase: "paused", Reason: "rate limited"}}, {{ID: "run-1", Phase: "paused", Reason: "rate limited"}},
		{{ID: "run-1", Phase: "paused", Reason: "rate limited"}}, {{ID: "run-1", Phase: "published", StrategySetID: "ss", EpisodeID: "ep"}},
	}
	core.m1At, core.m2At = 1, 4
	var lines []string
	if _, _, err := RunPlay(context.Background(), core, seeded(), playOpts(clk), func(s string) { lines = append(lines, s) }); err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(strings.Join(lines, "\n"), "paused"); n != 1 {
		t.Fatalf("paused should be announced once, was %d times:\n%s", n, strings.Join(lines, "\n"))
	}
	if !strings.Contains(strings.Join(lines, "\n"), "rate limited") {
		t.Fatal("the pause reason must be shown (it is the free-tier signal)")
	}
}

func TestRunPlayWithoutSlackSkipsMessagesAndSaysSo(t *testing.T) {
	core, clk := happyCore(), &fakeClock{now: time.Unix(0, 0)}
	core.m1At, core.m2At = 0, 0
	opts := playOpts(clk)
	opts.SlackExpected = false
	var lines []string
	_, res, err := RunPlay(context.Background(), core, seeded(), opts, func(s string) { lines = append(lines, s) })
	if err != nil || !res.Published || !res.SlackSkipped {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	all := strings.Join(lines, "\n")
	if !strings.Contains(all, "SKIPPED") || strings.Contains(all, "M1 posted") {
		t.Fatalf("Slack steps must be reported as skipped, never as posted:\n%s", all)
	}
}

func TestRunPlayTimeoutNamesTheLastPhase(t *testing.T) {
	core, clk := happyCore(), &fakeClock{now: time.Unix(0, 0)}
	core.runsByPoll = [][]RunInfo{{{ID: "run-1", Phase: "generating"}}}
	opts := playOpts(clk)
	opts.Timeout = 20 * time.Second
	_, _, err := RunPlay(context.Background(), core, seeded(), opts, func(string) {})
	if err == nil || !strings.Contains(err.Error(), "timed out") || !strings.Contains(err.Error(), "generating") {
		t.Fatalf("want a timeout naming the last phase, got %v", err)
	}
}

func TestRunPlayRefusesALeakedWorldAndNeverCallsPlay(t *testing.T) {
	core, clk := happyCore(), &fakeClock{now: time.Unix(0, 0)}
	core.inv = Invisibility{Status: "leaked", Leaks: []Leak{{Store: "postgres", Kind: "activity", ID: "act-9"}}}
	_, _, err := RunPlay(context.Background(), core, seeded(), playOpts(clk), func(string) {})
	if err == nil || !strings.Contains(err.Error(), "leaked") || !strings.Contains(err.Error(), "act-9") {
		t.Fatalf("want a refusal naming the leak, got %v", err)
	}
	if core.played {
		t.Fatal("Play must not be called on a leaked world")
	}
}

func TestRunPlayIsIdempotentWhenAlreadyReleased(t *testing.T) {
	core, clk := happyCore(), &fakeClock{now: time.Unix(0, 0)}
	core.inv = Invisibility{Status: "released"}
	core.play = PlayOutcome{AlreadyReleased: true}
	core.latestBI = [2]string{"bi-1", "Budget approval moved"}
	st := seeded()
	st.RunID = "run-1"
	var lines []string
	st2, res, err := RunPlay(context.Background(), core, st, playOpts(clk), func(s string) { lines = append(lines, s) })
	if err != nil || !res.Published {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if !strings.Contains(strings.Join(lines, "\n"), "already released") {
		t.Fatalf("should say Event N was already released:\n%s", strings.Join(lines, "\n"))
	}
	if st2.BIUpdateID != "bi-1" {
		t.Fatalf("the BI update must be read back, got %q", st2.BIUpdateID)
	}
}

func TestRunPlayNonMaterialChangeHasNoM1(t *testing.T) {
	core, clk := happyCore(), &fakeClock{now: time.Unix(0, 0)}
	core.play = PlayOutcome{AccountChangeID: "ac-1", AccountID: "acct-1"}
	core.runsByPoll = [][]RunInfo{{}}
	opts := playOpts(clk)
	opts.Timeout = 6 * time.Second
	var lines []string
	_, _, _ = RunPlay(context.Background(), core, seeded(), opts, func(s string) { lines = append(lines, s) })
	if !strings.Contains(strings.Join(lines, "\n"), "not material") {
		t.Fatalf("should explain there is no BI update:\n%s", strings.Join(lines, "\n"))
	}
}

func TestRunPlayPropagatesPlayErrors(t *testing.T) {
	core, clk := happyCore(), &fakeClock{now: time.Unix(0, 0)}
	core.playErr = &APIError{Status: 503, Code: "graph_unavailable", Message: "neo4j down"}
	_, _, err := RunPlay(context.Background(), core, seeded(), playOpts(clk), func(string) {})
	var ae *APIError
	if !errors.As(err, &ae) || ae.Code != "graph_unavailable" {
		t.Fatalf("want the APIError, got %v", err)
	}
}

func TestRunPlayNoWaitReturnsAfterTheBIUpdate(t *testing.T) {
	core, clk := happyCore(), &fakeClock{now: time.Unix(0, 0)}
	opts := playOpts(clk)
	opts.NoWait = true
	st, res, err := RunPlay(context.Background(), core, seeded(), opts, func(string) {})
	if err != nil || res.Published || st.BIUpdateID != "bi-1" || core.polls != 0 {
		t.Fatalf("NoWait must not poll: res=%+v polls=%d err=%v", res, core.polls, err)
	}
}
