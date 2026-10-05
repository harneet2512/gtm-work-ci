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

// Boot is what runs on every start (the Start shortcut), idempotently: the control service first (so the status exists
// from the first second), then the stores and the worker, then a reset of the whole chronology (Start always begins the
// demo again, whatever a previous session left), then core on case 1 with the graph consistent with it, then the Slack bot
// and the web app. The Reset shortcut runs the same sequence. Each service is health-checked by the runner's supervisor;
// a service that is already up is left alone, so running Boot twice is harmless (apart from the reset).
type Boot struct {
	Cfg demorun.Config
	Sup demorun.Supervisor
	Ops Ops
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

// Run starts everything on a reset chronology. The failure, if any, is recorded for the status endpoint and returned.
func (b Boot) Run(ctx context.Context) (err error) {
	defer func() { b.recordOutcome(err) }()
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
	var stores, surfaces []demorun.Spec
	for _, s := range b.Cfg.Specs(tools) {
		switch s.Name {
		case demorun.SvcPostgres, demorun.SvcWorker:
			stores = append(stores, s)
		case demorun.SvcCore:
			// started by the reset below, on case 1's database
		default:
			if strings.HasPrefix(s.Name, demorun.SvcNeo4j) { // one graph instance per case
				stores = append(stores, s)
				continue
			}
			surfaces = append(surfaces, s)
		}
	}
	if err := b.Up(ctx, b.Sup, stores); err != nil {
		return err
	}
	// The surfaces come down first: the bot's relay state and the web's pages belong to the world being discarded.
	if err := b.down(ctx, surfaces); err != nil {
		return err
	}
	b.say("resetting the demo to the start")
	if err := b.Ops.ResetAll(ctx, nil); err != nil {
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
