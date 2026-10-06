package codespace

import (
	"context"
	"fmt"

	"github.com/harneet2512/gtm-work/core-go/internal/demorun"
	"github.com/harneet2512/gtm-work/core-go/internal/slacksurface"
)

// corePlayer plays Event N of a case with the runner's wait loop (BI update, run published, Message 1 and 2 when the
// Slack bot is running) and returns the state with the run and episode ids.
type corePlayer struct {
	client demorun.CoreClient
	dsnFor func(Case) (string, error)
	cases  []Case
	slack  bool
	emit   func(string)
}

// Play implements Player.
func (p corePlayer) Play(ctx context.Context, st demorun.DemoState) (demorun.DemoState, error) {
	var dsn string
	for _, c := range p.cases {
		if c.OpportunityID == st.OpportunityID {
			d, err := p.dsnFor(c)
			if err != nil {
				return st, err
			}
			dsn = d
		}
	}
	if dsn == "" {
		return st, fmt.Errorf("no case database for opportunity %s", st.OpportunityID)
	}
	core := &demorun.LiveCore{CoreClient: p.client, DSN: dsn}
	defer core.Close()
	out, _, err := demorun.RunPlay(ctx, core, st, demorun.PlayOptions{SlackExpected: p.slack}, p.emit)
	return out, err
}

// CheckRecordable refuses a record run while the Slack bot is on: the recorded Message 1, 2 and 3 would be posted to the
// demo channel and stay there. The model calls do not need Slack, so the run is recorded with it off.
func (r Runtime) CheckRecordable() error {
	if !r.Cfg.NoSlack {
		return fmt.Errorf("codespace: the record run must run with Slack off, or its messages would be posted to the demo channel: set %s=1 (scripts/demo/record-local.ps1 does)", SlackOffEnv)
	}
	return nil
}

// Record is the one-time record run (after CheckRecordable).
func (r Runtime) Record(ctx context.Context) error {
	if err := r.CheckRecordable(); err != nil {
		return err
	}
	if err := r.NewRecorder().Run(ctx); err != nil {
		return err
	}
	return r.SealRecording(ctx)
}

// NewRecorder wires the one-time record run over the real core: the human works through Cliff's real Slack handlers over a
// fake transport (the same code the live demo runs, so the presenter's identical typing replays from the cache), and the run
// sheet is written to the demo home.
func (r Runtime) NewRecorder() Recorder {
	say := func(s string) { r.Ops.say("%s", s) }
	dsnFor := func(c Case) (string, error) { return DSNFor(r.Cfg.DSN(), c.Database) }
	rec := Recorder{
		Ops:    r.Ops,
		Player: corePlayer{client: r.Client, dsnFor: dsnFor, cases: r.Ops.Cases, slack: !r.Cfg.NoSlack, emit: say},
		Probe:  DBProbe{DSNFor: dsnFor},
		Script: DefaultScript(),
		Sheet:  func(s RunSheet) error { return WriteRunSheet(RunSheetPath(r.Cfg.Layout.Dir()), s) },
	}
	rec.Human = &SlackHuman{
		Core:   slacksurface.NewCoreHTTP(r.Client.Base, slacksurface.Secret(r.Client.Token), nil),
		WebURL: r.Cfg.WebURL(), Wait: rec.waitUntil,
		Say: func(format string, args ...any) { r.Ops.say(format, args...) },
	}
	return rec
}
