package codespace

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/harneet2512/gtm-work/core-go/internal/demorun"
)

// Platform is every effect that touches the machine: the single core process, the Neo4j projection and core's
// event-N-invisible assertion. RealPlatform implements it; tests script it.
type Platform interface {
	StopCore(ctx context.Context) error
	// StartCore starts core against the case's database and returns when it is healthy.
	StartCore(ctx context.Context, c Case) error
	// RebuildGraph wipes Neo4j and projects the case's database into it (the graph is a projection of Postgres).
	RebuildGraph(ctx context.Context, c Case) error
	Invisibility(ctx context.Context, manifestID string) (demorun.Invisibility, error)
}

// Ops are the operations the codespace demo needs beyond the local runner: activate a case, reset both cases to
// Event N-1, and seed both cases.
type Ops struct {
	Cases []Case
	Paths Paths
	Admin Admin
	P     Platform
	// Log receives one human line per step; nil discards.
	Log func(format string, args ...any)
	// Carry copies what the previous case learned into a later case's database (company knowledge is deployment-wide but every
	// case has its own database). It runs, idempotently, when a later case is activated and reports how many entries it wrote.
	Carry func(ctx context.Context, from, to Case) (int, error)
	// Slack, when set, clears the bot's own earlier messages from #ghost-demo on every reset (nil: Slack is off).
	Slack ChannelClearer
}

// ChannelClearer deletes what the bot posted in the demo channel and reports how many messages it removed.
type ChannelClearer interface {
	Clear(ctx context.Context) (int, error)
}

func (o Ops) say(format string, args ...any) {
	if o.Log != nil {
		o.Log(format, args...)
	}
}

// SeedState is the saved seed state of a case and whether it exists.
func (o Ops) SeedState(slot string) (demorun.DemoState, bool, error) {
	st, ok, err := demorun.LoadState(o.Paths.State(slot))
	if err != nil || !ok || st.ManifestID == "" {
		return demorun.DemoState{}, false, err
	}
	return st, true, nil
}

// Activate points the single core at one case: stop core, rebuild the case's own graph from its database unless it already
// holds it, start core on it, and check Event N is still invisible. It returns the
// invisibility status. A leaked world is an error and the case is NOT recorded as active.
func (o Ops) Activate(ctx context.Context, slot string) (string, error) {
	c, err := FindCase(o.Cases, slot)
	if err != nil {
		return "", err
	}
	st, ok, err := o.SeedState(slot)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", fmt.Errorf("codespace: %s (%s) is not seeded yet; the first-time setup has not finished", slot, c.Label)
	}
	if err := o.P.StopCore(ctx); err != nil {
		return "", err
	}
	if prev, ok := o.previous(slot); ok && o.Carry != nil {
		n, err := o.Carry(ctx, prev, c)
		if err != nil {
			return "", fmt.Errorf("codespace: carry knowledge from %s into %s: %w", prev.Label, c.Label, err)
		}
		if n > 0 {
			o.say("carried %d knowledge entries from %s into %s", n, prev.Label, c.Label)
		}
	}
	graph, err := ReadMarker(o.Paths.GraphMarker(slot))
	if err != nil {
		return "", err
	}
	if graph != slot {
		o.say("rebuilding the graph from %s", c.Label)
		if err := o.P.RebuildGraph(ctx, c); err != nil {
			_ = os.Remove(o.Paths.GraphMarker(slot)) // the graph may be half-built: never trust the old marker
			return "", err
		}
		if err := WriteMarker(o.Paths.GraphMarker(slot), slot); err != nil {
			return "", err
		}
	}
	o.say("starting core on %s", c.Label)
	if err := o.P.StartCore(ctx, c); err != nil {
		return "", err
	}
	inv, err := o.P.Invisibility(ctx, st.ManifestID)
	if err != nil {
		return "", fmt.Errorf("codespace: event-N-invisible check for %s: %w", c.Label, err)
	}
	if inv.Status == "leaked" {
		return inv.Status, fmt.Errorf("codespace: Event N is visible before Play in %s: %d leak(s)", c.Label, len(inv.Leaks))
	}
	if err := WriteMarker(o.Paths.ActiveFile(), slot); err != nil {
		return "", err
	}
	return inv.Status, nil
}

// ResetAll puts every case back at Event N-1 and leaves case 1 active. The existing replay Reset API only moves
// the replay cursor (the released rows stay and Play would answer 409 already_released), so it cannot undo Event N;
// each case's database is restored instead from the template taken right after the freeze, then the graph is
// rebuilt and the event-N-invisible assertion must read "withheld" for both. Cases are verified in reverse order so
// the demo starts on case 1.
func (o Ops) ResetAll(ctx context.Context, step func(string)) error {
	say := func(s string) {
		o.say("%s", s)
		if step != nil {
			step(s)
		}
	}
	for _, c := range o.Cases {
		if _, ok, err := o.SeedState(c.Slot); err != nil || !ok {
			return fmt.Errorf("codespace: %s (%s) was never seeded, so there is nothing to restore: %v", c.Slot, c.Label, errOr(err, "no seed state"))
		}
		if ok, err := o.Admin.Exists(ctx, c.Template()); err != nil || !ok {
			return fmt.Errorf("codespace: the frozen template of %s is missing (%s); re-run the first-time setup: %v", c.Label, c.Template(), errOr(err, "not found"))
		}
	}
	if o.Slack != nil {
		say("Clearing earlier messages from #ghost-demo")
		n, err := o.Slack.Clear(ctx)
		if err != nil {
			return fmt.Errorf("codespace: the earlier Cliff messages could not be removed from #ghost-demo (they would sit above the next run's): %w", err)
		}
		o.say("removed %d earlier messages", n)
	}
	say("Stopping core")
	if err := o.P.StopCore(ctx); err != nil {
		return err
	}
	for _, c := range o.Cases {
		say("Restoring " + c.Label + " to Event N-1")
		if err := o.Admin.Drop(ctx, c.Database); err != nil {
			return err
		}
		if err := o.Admin.Clone(ctx, c.Database, c.Template()); err != nil {
			return err
		}
	}
	for _, c := range o.Cases { // the restored databases are new worlds: every graph is rebuilt
		if err := os.Remove(o.Paths.GraphMarker(c.Slot)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	for i := len(o.Cases) - 1; i >= 0; i-- {
		c := o.Cases[i]
		say("Checking " + c.Label)
		status, err := o.Activate(ctx, c.Slot)
		if err != nil {
			return err
		}
		if status != "withheld" {
			return fmt.Errorf("codespace: after the restore %s reports event-N-invisible %q, want withheld", c.Label, status)
		}
	}
	say("Reset complete")
	return nil
}

func errOr(err error, fallback string) string {
	if err != nil {
		return err.Error()
	}
	return fallback
}

// previous is the case before slot in the chronology (case 1 for case 2); ok is false for the first case.
func (o Ops) previous(slot string) (Case, bool) {
	for i, c := range o.Cases {
		if c.Slot == slot && i > 0 {
			return o.Cases[i-1], true
		}
	}
	return Case{}, false
}
