package codespace

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/harneet2512/gtm-work/core-go/internal/demorun"
)

// BootErrorFile holds why the last start-up failed ("" file when it did not), so the operator section can say so without
// anyone opening a terminal. It names services and causes, never a value.
func (p Paths) BootErrorFile() string { return filepath.Join(p.Layout.Dir(), "boot-error.txt") }

// ReadBootError is the recorded start-up failure, "" when none.
func (p Paths) ReadBootError() string {
	b, err := os.ReadFile(p.BootErrorFile())
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// StartModeEnv selects how Start begins: restore (the default) puts both cases back at Event N-1 from the sealed baseline;
// resume keeps the live state the last session left. Neither mode rebuilds anything.
const StartModeEnv = "GHOST_DEMO_START"

// Start modes.
const (
	StartRestore = "restore"
	StartResume  = "resume"
)

// ParseStartMode reads GHOST_DEMO_START: empty means restore.
func ParseStartMode(v string) (string, error) {
	switch m := strings.ToLower(strings.TrimSpace(v)); m {
	case "", StartRestore:
		return StartRestore, nil
	case StartResume:
		return StartResume, nil
	default:
		return "", fmt.Errorf("codespace: %s must be %q or %q, got %q", StartModeEnv, StartRestore, StartResume, v)
	}
}

// ResolveStartMode picks the mode of one start: an explicit flag (the Reset shortcut passes restore) wins over GHOST_DEMO_START,
// so Reset restores whatever .env or the environment says.
func ResolveStartMode(flag, env string) (string, error) {
	if strings.TrimSpace(flag) != "" {
		return ParseStartMode(flag)
	}
	return ParseStartMode(env)
}

// Boot is what runs on every start (the Start shortcut), idempotently: the sealed baseline is checked first (a missing or
// mismatching one fails here, before anything is stopped, and never falls back to a rebuild), then the control service (so
// the status exists from the first second) and the worker, then, in restore mode, the sealed baseline replaces the live copy
// (a file copy, seconds, no graph build and no model call) and in resume mode the live copy is kept, then core on the
// active case, then the Slack bot and the web app. The Reset shortcut runs the same sequence in restore mode. Each service
// is health-checked by the runner's supervisor; a service that is already up is left alone.
type Boot struct {
	// SlackErr is why the Slack tokens could not be resolved when Slack is expected (not forced off). Start fails on it:
	// a demo that silently runs without Slack opens a page whose Slack half never answers. It names variables, never values.
	SlackErr error
	Cfg      demorun.Config
	Sup      demorun.Supervisor
	Ops      Ops
	// Mode is the raw GHOST_DEMO_START value ("" means restore).
	Mode string
	// Prepare builds binaries, the worker's Python environment and the web production build.
	Prepare func(ctx context.Context) (demorun.Tooling, error)
	// ControlSpec is the supervised `ghostctl codespace serve` process.
	ControlSpec func(t demorun.Tooling) demorun.Spec
	// Up starts specs in order and stops at the first failure (demorun.Up in production).
	Up func(ctx context.Context, sup demorun.Supervisor, specs []demorun.Spec) error
	// Down stops specs in reverse order (demorun.Down in production); nil means demorun.Down.
	Down func(ctx context.Context, sup demorun.Supervisor, specs []demorun.Spec) error
	Out  io.Writer
}

func (b Boot) say(format string, args ...any) {
	if b.Out != nil {
		fmt.Fprintf(b.Out, format+"\n", args...)
	}
}

func (b Boot) down(ctx context.Context, specs []demorun.Spec) error {
	if b.Down != nil {
		return b.Down(ctx, b.Sup, specs)
	}
	return demorun.Down(ctx, b.Sup, specs)
}

// Run starts everything. The failure, if any, is recorded for the status endpoint and returned.
func (b Boot) Run(ctx context.Context) (err error) {
	defer func() { b.recordOutcome(err) }()
	if b.SlackErr != nil {
		return fmt.Errorf("Slack is expected but its tokens are missing, so Start will not open the demo: %w (to run without Slack on purpose set %s=1)", b.SlackErr, SlackOffEnv)
	}
	mode, err := ParseStartMode(b.Mode)
	if err != nil {
		return err
	}
	// A missing or mismatching baseline stops here, before a binary is built or a service stopped, and is never repaired by a rebuild.
	if err := b.Ops.checkBaseline(); err != nil {
		return err
	}
	tools, err := b.Prepare(ctx)
	if err != nil {
		return err
	}
	// Every boot makes a new control token (the web server below is given it), so a control service left over from an earlier
	// boot, which holds the old one, is stopped and started again.
	control := []demorun.Spec{b.ControlSpec(tools)}
	if err := b.down(ctx, control); err != nil {
		return err
	}
	if err := b.Up(ctx, b.Sup, control); err != nil {
		return err
	}
	var worker, stores, surfaces []demorun.Spec
	for _, s := range b.Cfg.Specs(tools) {
		switch {
		case s.Name == demorun.SvcWorker:
			worker = append(worker, s)
		case s.Name == demorun.SvcCore:
			// started below, on the active case's database
		case s.Name == demorun.SvcPostgres || strings.HasPrefix(s.Name, demorun.SvcNeo4j): // one graph instance per case
			stores = append(stores, s)
		default:
			surfaces = append(surfaces, s)
		}
	}
	if err := b.Up(ctx, b.Sup, worker); err != nil {
		return err
	}
	// The surfaces come down first: the bot's relay state and the web's pages belong to the world being replaced.
	if err := b.down(ctx, surfaces); err != nil {
		return err
	}
	if mode == StartResume {
		b.say("resuming the live state (no restore)")
		if err := b.Up(ctx, b.Sup, stores); err != nil {
			return err
		}
		err = b.Ops.Resume(ctx)
	} else {
		b.say("restoring the sealed baseline")
		err = b.Ops.ResetAll(ctx, nil) // stops and restarts the stores itself
	}
	if err != nil {
		return err
	}
	if err := b.Up(ctx, b.Sup, surfaces); err != nil {
		return err
	}
	b.say("%s", ReadyMessage)
	return nil
}

func (b Boot) recordOutcome(err error) {
	path := b.Ops.Paths.BootErrorFile()
	if err == nil {
		_ = os.Remove(path)
		return
	}
	msg := err.Error()
	var se *demorun.ServiceError
	if errors.As(err, &se) {
		msg = fmt.Sprintf("%s failed to %s: %v", se.Service, se.Phase, se.Err)
	}
	if mkErr := os.MkdirAll(filepath.Dir(path), 0o755); mkErr == nil {
		_ = os.WriteFile(path, []byte(msg+"\n"), 0o644)
	}
}
