package codespace

import (
	"context"
	"fmt"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/demorun"
)

// Platform is every effect that touches the machine: the single core process, the stores, the Neo4j projection and core's
// event-N-invisible assertion. RealPlatform implements it; tests script it.
type Platform interface {
	StopCore(ctx context.Context) error
	// StartCore starts core against the case's database and returns when it is healthy.
	StartCore(ctx context.Context, c Case) error
	// RebuildGraph wipes Neo4j and projects the case's database into it (the graph is a projection of Postgres). Only the
	// one-time setup (Seeder) calls it, never the demo path.
	RebuildGraph(ctx context.Context, c Case) error
	// StopStores and StartStores stop and start Postgres and every Neo4j so the live copy can be replaced; StartStores
	// returns when they are healthy.
	StopStores(ctx context.Context) error
	StartStores(ctx context.Context) error
	Invisibility(ctx context.Context, manifestID string) (demorun.Invisibility, error)
}

// StateBaseline is the sealed Event N-1 state: Verify is the fast check, Restore replaces the live copy by it.
type StateBaseline interface {
	Verify() error
	Restore(ctx context.Context) (RestoreStats, error)
}

// Ops are the operations the codespace demo needs beyond the local runner: activate a case, reset both cases to
// Event N-1 from the sealed baseline, resume the live state, and seed both cases (setup only).
type Ops struct {
	Cases []Case
	Paths Paths
	// Admin manages the case databases for the one-time setup only; Reset and Start never use it.
	Admin Admin
	P     Platform
	// Base is the sealed baseline Reset and Start restore from (Baseline in production).
	Base StateBaseline
	// Log receives one human line per step; nil discards.
	Log func(format string, args ...any)
	// Carry copies what the previous case learned into a later case's database (company knowledge is deployment-wide but every
	// case has its own database). It runs, idempotently, when a later case is activated at the handoff and reports how many
	// entries it wrote. It is knowledge carried live; it never runs on a restore.
	Carry func(ctx context.Context, from, to Case) (int, error)
	// Slack, when set, clears the bot's own earlier messages from #gtm-ai-demo on every reset (nil: Slack is off).
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

// Activate points the single core at one case for the handoff: stop core, carry what the earlier case learned into this
// case's database, start core on the case's own graph and check Event N is still invisible. It returns the invisibility
// status. A leaked world is an error and the case is NOT recorded as active. It never builds or rebuilds a graph: each case's
// graph came from the sealed baseline, and a missing one is an error that points to the one-time setup.
func (o Ops) Activate(ctx context.Context, slot string) (string, error) {
	return o.activate(ctx, slot, true)
}

func (o Ops) activate(ctx context.Context, slot string, carry bool) (string, error) {
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
	graph, err := ReadMarker(o.Paths.GraphMarker(slot))
	if err != nil {
		return "", err
	}
	if graph != slot {
		return "", fmt.Errorf("codespace: the graph of %s was never built; %s", c.Label, setupHint)
	}
	if err := o.P.StopCore(ctx); err != nil {
		return "", err
	}
	if prev, ok := o.previous(slot); ok && carry && o.Carry != nil {
		n, err := o.Carry(ctx, prev, c)
		if err != nil {
			return "", fmt.Errorf("codespace: carry knowledge from %s into %s: %w", prev.Label, c.Label, err)
		}
		if n > 0 {
			o.say("carried %d knowledge entries from %s into %s", n, prev.Label, c.Label)
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

// Resume starts core on the case that was active in the live copy, without restoring anything: the live state is kept as the
// last session left it. The baseline must still exist and match (a demo without one could not be reset later), and nothing is
// rebuilt.
func (o Ops) Resume(ctx context.Context) error {
	if err := o.checkBaseline(); err != nil {
		return err
	}
	slot, err := ReadMarker(o.Paths.ActiveFile())
	if err != nil {
		return err
	}
	if slot == "" {
		slot = o.Cases[0].Slot
	}
	o.say("resuming the live state")
	_, err = o.activate(ctx, slot, false)
	return err
}

func (o Ops) checkBaseline() error {
	if o.Base == nil {
		return fmt.Errorf("%w: no baseline is configured; %s", ErrNoBaseline, setupHint)
	}
	return o.Base.Verify()
}

// ResetAll puts every case back at Event N-1 and leaves case 1 active, by restoring the sealed baseline: the services stop, the
// live copy is replaced by the baseline (a file copy and a directory swap), the stores restart and core is started on each
// case in turn to assert that its Event N is withheld. Nothing is rebuilt, migrated, carried or computed, and no model is called;
// the model-call cache is outside both copies and is never touched. The baseline is verified first, so a missing or corrupt one
// fails before anything is stopped. Cases are checked in reverse order so the demo starts on case 1.
func (o Ops) ResetAll(ctx context.Context, step func(string)) error {
	say := func(s string) {
		o.say("%s", s)
		if step != nil {
			step(s)
		}
	}
	if err := o.checkBaseline(); err != nil {
		return err
	}
	for _, c := range o.Cases {
		if _, ok, err := o.SeedState(c.Slot); err != nil || !ok {
			return fmt.Errorf("codespace: %s (%s) was never seeded, so there is nothing to restore: %v", c.Slot, c.Label, errOr(err, "no seed state"))
		}
	}
	if o.Slack != nil {
		say("Clearing earlier messages from #gtm-ai-demo")
		n, err := o.Slack.Clear(ctx)
		if err != nil {
			return fmt.Errorf("codespace: the earlier Cliff messages could not be removed from #gtm-ai-demo (they would sit above the next run's): %w", err)
		}
		o.say("removed %d earlier messages", n)
	}
	say("Stopping core and the stores")
	if err := o.P.StopCore(ctx); err != nil {
		return err
	}
	if err := o.P.StopStores(ctx); err != nil {
		return err
	}
	say("Restoring the sealed baseline")
	stats, err := o.Base.Restore(ctx)
	if err != nil {
		return err
	}
	o.say("restored %d files (%d MB) in %s (copy %s, swap %s, delete %s, summed over the components)", stats.Files, stats.Bytes>>20,
		stats.Duration.Round(time.Millisecond), stats.Copy.Round(time.Millisecond), stats.Swap.Round(time.Millisecond), stats.Delete.Round(time.Millisecond))
	say("Starting the stores")
	if err := o.P.StartStores(ctx); err != nil {
		return err
	}
	for i := len(o.Cases) - 1; i >= 0; i-- {
		c := o.Cases[i]
		say("Checking " + c.Label)
		status, err := o.activate(ctx, c.Slot, false) // the baseline already holds everything: carrying is for the live handoff
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
