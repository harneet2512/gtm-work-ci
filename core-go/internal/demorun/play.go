package demorun

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// PlayCore is the part of core RunPlay uses (CoreClient implements it; tests script it).
type PlayCore interface {
	Invisibility(ctx context.Context, manifestID string) (Invisibility, error)
	Play(ctx context.Context, manifestID string) (PlayOutcome, error)
	LatestBI(ctx context.Context, accountID string) (id, summary string, err error)
	// PlayRuns lists the runs the Play of this manifest opened (their trigger activity is the Play's), newest first.
	PlayRuns(ctx context.Context, manifestID string) ([]RunInfo, error)
	SurfaceMessageTS(ctx context.Context, subjectID, surface, kind string) (string, error)
}

// PlayOptions tune RunPlay. Now and Sleep are injectable so the wait loop is testable without real time.
type PlayOptions struct {
	Timeout       time.Duration // overall wait after Play; default 20 minutes (free-tier pacing)
	Poll          time.Duration // default 3 seconds
	SlackExpected bool          // the Slack bot is running, so M1 and M2 should be posted
	NoWait        bool          // return right after the BI update
	Now           func() time.Time
	Sleep         func(ctx context.Context, d time.Duration) error
}

func (o PlayOptions) withDefaults() PlayOptions {
	if o.Timeout <= 0 {
		o.Timeout = 20 * time.Minute
	}
	if o.Poll <= 0 {
		o.Poll = 3 * time.Second
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Sleep == nil {
		o.Sleep = func(ctx context.Context, d time.Duration) error {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(d):
				return nil
			}
		}
	}
	return o
}

// PlayResult is what the wait observed.
type PlayResult struct {
	BIUpdateID    string
	M1TS          string
	RunID         string
	Phase         string
	Reason        string
	StrategySetID string
	EpisodeID     string
	M2TS          string
	Published     bool
	SlackSkipped  bool
}

// LatestBI implements PlayCore on the real client: the newest business-intelligence update of the account.
func (c CoreClient) LatestBI(ctx context.Context, accountID string) (string, string, error) {
	var res struct {
		ID      string `json:"id"`
		Summary string `json:"summary"`
	}
	_, err := c.do(ctx, "GET", "/accounts/"+accountID+"/business-intelligence/latest", nil, &res)
	var ae *APIError
	if errors.As(err, &ae) && ae.Status == 404 {
		return "", "", nil
	}
	return res.ID, res.Summary, err
}

// RunPlay releases Event N and follows what it sets off: the BI update, Slack Message 1, the strategy run to
// published, Slack Message 2. Every observation is emitted as one line as soon as it is true. The event-N-invisible
// assertion is read first and a leaked world is refused before anything is released. It never invents a step: a
// step that cannot be observed (Slack off) is reported as SKIPPED.
func RunPlay(ctx context.Context, core PlayCore, st DemoState, o PlayOptions, emit func(string)) (DemoState, PlayResult, error) {
	o = o.withDefaults()
	start := o.Now()
	say := func(format string, args ...any) {
		d := o.Now().Sub(start).Round(time.Second)
		emit(fmt.Sprintf("[%02d:%02d] %s", int(d.Minutes()), int(d.Seconds())%60, fmt.Sprintf(format, args...)))
	}
	if err := st.RequireSeeded(); err != nil {
		return st, PlayResult{}, err
	}
	var res PlayResult
	inv, err := core.Invisibility(ctx, st.ManifestID)
	if err != nil {
		return st, res, fmt.Errorf("event-N-invisible check: %w", err)
	}
	if inv.Status == "leaked" {
		return st, res, fmt.Errorf("the world is leaked, so Play is refused: %s", describeLeaks(inv.Leaks))
	}
	say("event-N-invisible: %s (checked %s)", inv.Status, strings.Join(inv.Checked, ", "))
	say("releasing Event N of %q ...", st.CaseName)
	out, err := core.Play(ctx, st.ManifestID)
	if err != nil {
		return st, res, fmt.Errorf("play: %w", err)
	}
	if st.PlayedAt.IsZero() {
		st.PlayedAt = o.Now()
	}
	biID, biSummary := out.BIUpdateID, out.BISummary
	if out.AlreadyReleased {
		say("Event N was already released: nothing changes; reading the world back")
		if biID, biSummary, err = core.LatestBI(ctx, st.AccountID); err != nil {
			return st, res, fmt.Errorf("read the latest BI update: %w", err)
		}
	}
	if out.AccountChangeID != "" {
		st.AccountChangeID = out.AccountChangeID
	}
	res.BIUpdateID, st.BIUpdateID = biID, biID
	if biID == "" {
		say("the change is not material: no BI update was written, so there is no Message 1")
	} else {
		say("BI update written: %s - %s", biID, clip(biSummary, 140))
	}
	if o.NoWait {
		return st, res, nil
	}
	return follow(ctx, core, st, res, o, say)
}

func follow(ctx context.Context, core PlayCore, st DemoState, res PlayResult, o PlayOptions, say func(string, ...any)) (DemoState, PlayResult, error) {
	deadline := o.Now().Add(o.Timeout)
	res.SlackSkipped = !o.SlackExpected
	if res.SlackSkipped {
		say("Slack bot is not running: M1 and M2 are SKIPPED (not observed)")
	}
	var lastPhase, lastReason string
	for {
		if err := ctx.Err(); err != nil {
			return st, res, err
		}
		if o.SlackExpected && res.BIUpdateID != "" && res.M1TS == "" {
			if ts, err := core.SurfaceMessageTS(ctx, res.BIUpdateID, "slack", "bi"); err == nil && ts != "" {
				res.M1TS = ts
				say("M1 posted in Slack (ts %s)", ts)
			}
		}
		run, found, err := pickRun(ctx, core, st)
		if err != nil {
			return st, res, err
		}
		if found {
			res.RunID, st.RunID = run.ID, run.ID
			if run.Phase != lastPhase || (run.Reason != lastReason && run.Phase == "paused") {
				switch {
				case run.Phase == "paused" && lastPhase == "paused":
				case run.Reason != "":
					say("run %s: %s (%s)", run.ID, orUnknown(run.Phase), run.Reason)
				default:
					say("run %s: %s", run.ID, orUnknown(run.Phase))
				}
				lastPhase, lastReason = run.Phase, run.Reason
			}
			res.Phase, res.Reason = run.Phase, run.Reason
			if run.Phase == "failed" {
				return st, res, fmt.Errorf("the strategy run %s failed: %s", run.ID, orUnknown(run.Reason))
			}
			if run.Phase == "published" {
				res.Published = true
				res.StrategySetID, res.EpisodeID = run.StrategySetID, run.EpisodeID
				st.StrategySetID, st.EpisodeID = run.StrategySetID, run.EpisodeID
				if o.SlackExpected && res.M2TS == "" && run.EpisodeID != "" {
					if ts, err := core.SurfaceMessageTS(ctx, run.EpisodeID, "slack", "chooser"); err == nil && ts != "" {
						res.M2TS = ts
						say("M2 posted in Slack (ts %s)", ts)
					}
				}
			}
		}
		if res.Published && (res.SlackSkipped || (res.M2TS != "" && (res.BIUpdateID == "" || res.M1TS != ""))) {
			say("done: the strategy run is published%s", slackSummary(res))
			return st, res, nil
		}
		if !o.Now().Before(deadline) {
			return st, res, fmt.Errorf("timed out after %s waiting for the demo flow (last run phase %q, M1 %s, M2 %s); `demo logs core` and `demo logs worker` show why",
				o.Timeout, orUnknown(lastPhase), posted(res.M1TS), posted(res.M2TS))
		}
		if err := o.Sleep(ctx, o.Poll); err != nil {
			return st, res, err
		}
	}
}

// pickRun finds the run this Play opened. Core identifies it by its trigger activity (the demo_plays row), not by
// time: run timestamps follow the replay clock, and the run may already exist when `demo play` starts (an
// interrupted Play is resumed, and its pipeline finishes on its own).
func pickRun(ctx context.Context, core PlayCore, st DemoState) (RunInfo, bool, error) {
	runs, err := core.PlayRuns(ctx, st.ManifestID)
	if err != nil {
		return RunInfo{}, false, fmt.Errorf("list the runs of this Play: %w", err)
	}
	for _, r := range runs {
		if st.RunID != "" && r.ID == st.RunID {
			return r, true, nil
		}
	}
	if len(runs) > 0 && st.RunID == "" {
		return runs[0], true, nil
	}
	return RunInfo{}, false, nil
}

func describeLeaks(leaks []Leak) string {
	parts := make([]string, 0, len(leaks))
	for _, l := range leaks {
		parts = append(parts, fmt.Sprintf("%s %s %s", l.Store, l.Kind, l.ID))
	}
	if len(parts) == 0 {
		return "(no detail)"
	}
	return strings.Join(parts, "; ")
}

func slackSummary(r PlayResult) string {
	if r.SlackSkipped {
		return " (Slack steps skipped)"
	}
	return fmt.Sprintf(" and Slack shows M1 and M2 (ts %s, %s)", orDash(r.M1TS), orDash(r.M2TS))
}

func posted(ts string) string {
	if ts == "" {
		return "not posted"
	}
	return "posted"
}

func orUnknown(s string) string {
	if s == "" {
		return "unknown"
	}
	return s
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func clip(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "..."
}
