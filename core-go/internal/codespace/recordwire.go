package codespace

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

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

// RecordPlayTimeout is how long the record run waits for one Play's strategy run. The first recording makes every model call for real (a
// thinking model, about 90 calls for generation and judging), which takes well over the 20 minutes of a replayed Play; the stored calls
// make the later passes fast.
const RecordPlayTimeout = 90 * time.Minute

// Play implements Player.
func (p corePlayer) Play(ctx context.Context, st demorun.DemoState) (demorun.DemoState, error) {
	var dsn string
	for _, c := range p.cases {
		// The seed state holds the manifest's internal opportunity uuid and the case's account name; a Case holds the Salesforce
		// opportunity id and its label (the account name). Matching only the ids never found the case of a real seed.
		if c.OpportunityID == st.OpportunityID || (st.CaseName != "" && c.Label == st.CaseName) {
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
	out, _, err := demorun.RunPlay(ctx, core, st, demorun.PlayOptions{SlackExpected: p.slack, Timeout: RecordPlayTimeout}, p.emit)
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
		Ops:                 r.Ops,
		Player:              corePlayer{client: r.Client, dsnFor: dsnFor, cases: r.Ops.Cases, slack: !r.Cfg.NoSlack, emit: say},
		Probe:               DBProbe{DSNFor: dsnFor, RulesPath: filepath.Join(r.Cfg.Layout.Root, "contracts", "knowledge", "lifecycle.v1.json")},
		Gates:               DBGateProbe{DSNFor: dsnFor},
		CacheCalls:          func() (int, error) { c, err := ReadCacheCounters(r.Cfg); return c.Calls(), err },
		Misses:              func() (int, error) { c, err := ReadCacheCounters(r.Cfg); return c.Misses, err },
		Script:              DefaultScript(),
		AllowNoContinuation: AllowNoContinuationFromEnv(os.Getenv),
		Sheet:               func(s RunSheet) error { return WriteRunSheet(RunSheetPath(r.Cfg.Layout.Dir()), s) },
	}
	rec.Human = &SlackHuman{
		Core:   slacksurface.NewCoreHTTP(r.Client.Base, slacksurface.Secret(r.Client.Token), nil),
		WebURL: r.Cfg.WebURL(), Wait: rec.waitUntil,
		Say: func(format string, args ...any) { r.Ops.say(format, args...) },
	}
	return rec
}

// Drive is the live walk of one case in front of the real Slack channel: Play releases Event N (the bot posts Message 1 and
// 2 as it does for the presenter), then the scripted human acts through the same handlers over the mirrored transport
// (Select, Edit, Save, Send, the correction), which updates Message 2 and posts Message 3 in the channel. Every model call
// must replay from the cache: the run fails on a strict miss.
func (r Runtime) Drive(ctx context.Context, slot string, real slacksurface.Poster) (demorun.DemoState, HumanPath, error) {
	c, err := caseOfSlot(r.Ops.Cases, slot)
	if err != nil {
		return demorun.DemoState{}, HumanPath{}, err
	}
	rec := r.NewRecorder()
	human, err := slackHumanOf(rec.Human)
	if err != nil {
		return demorun.DemoState{}, HumanPath{}, err
	}
	human.Real, human.Channel = real, r.Cfg.Merged("SLACK_CHANNEL_ID")
	st, ok, err := r.Ops.SeedState(slot)
	if err != nil || !ok {
		return st, HumanPath{}, fmt.Errorf("%s is not seeded: %v", slot, errOr(err, "no seed state"))
	}
	rec.Player = corePlayer{client: r.Client, dsnFor: func(c Case) (string, error) { return DSNFor(r.Cfg.DSN(), c.Database) }, cases: r.Ops.Cases, slack: true,
		emit: func(s string) { r.Ops.say("%s", s) }}
	return rec.driveCase(ctx, c, st)
}

// caseOfSlot is the case registered under the slot; an unknown slot is an error, never an empty Case.
func caseOfSlot(cases []Case, slot string) (Case, error) {
	for _, k := range cases {
		if k.Slot == slot {
			return k, nil
		}
	}
	return Case{}, fmt.Errorf("codespace: unknown case %q (cases: %s)", slot, slotNames(cases))
}

func slotNames(cases []Case) string {
	names := make([]string, 0, len(cases))
	for _, k := range cases {
		names = append(names, k.Slot)
	}
	return strings.Join(names, ", ")
}

// slackHumanOf is the Slack human the drive needs (it mirrors the real channel); any other Human cannot be driven live.
func slackHumanOf(h Human) (*SlackHuman, error) {
	sh, ok := h.(*SlackHuman)
	if !ok || sh == nil {
		return nil, fmt.Errorf("codespace: the live drive needs the Slack human, got %T", h)
	}
	return sh, nil
}

// driveCase plays Event N of the case, lets the human act on it, and waits for the episode's gates.
func (rec Recorder) driveCase(ctx context.Context, c Case, st demorun.DemoState) (demorun.DemoState, HumanPath, error) {
	played, err := rec.Player.Play(ctx, st)
	if err != nil {
		return played, HumanPath{}, err
	}
	path, err := rec.Human.Act(ctx, c, played, rec.Script)
	if err != nil {
		return played, path, err
	}
	return played, path, rec.awaitGates(ctx, c, played.EpisodeID)
}
