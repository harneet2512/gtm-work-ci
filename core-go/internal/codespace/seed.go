package codespace

import (
	"context"
	"fmt"
	"os"

	"github.com/harneet2512/gtm-work/core-go/internal/demorun"
)

// SeedFunc freezes one case into its (empty, migrated) database with the runner's seed flow and calls afterGraph
// once the history and the graph exist but core is still down. cmd/ghostctl wires it to demorun.Flow.Seed.
type SeedFunc func(ctx context.Context, c Case, opts demorun.SeedOptions, afterGraph func(context.Context) error) error

// Seeder is the first-time setup: freeze both cases to Event N-1, each in its own database, snapshot each frozen
// database as a template, and verify Event N is invisible. It is idempotent: a case that has its seed state, its
// database and its template is left alone (the runner's seed re-reads it and re-checks invisibility), and a case
// that is incomplete (a failed first run) is wiped and frozen again, because the freeze refuses a non-empty database.
type Seeder struct {
	Ops
	// BaseDSN is any database of the demo cluster; the case databases are siblings of it.
	BaseDSN string
	// Migrate applies the schema to a fresh database (the host migrates only its own).
	Migrate func(ctx context.Context, dsn string) error
	Seed    SeedFunc
	// Options carries the shared inputs (snapshot directory, report); Opportunity is set per case.
	Options demorun.SeedOptions
}

// SeedAll seeds every case, then activates case 1 so the system is ready for the first Play.
func (s Seeder) SeedAll(ctx context.Context) error {
	for _, c := range s.Cases {
		if err := c.Validate(); err != nil {
			return err
		}
		if err := s.seedCase(ctx, c); err != nil {
			return fmt.Errorf("seed %s (%s): %w", c.Slot, c.Label, err)
		}
	}
	status, err := s.Activate(ctx, s.Cases[0].Slot)
	if err != nil {
		return err
	}
	s.say("setup complete: %d cases frozen at Event N-1; %s active (event-N-invisible: %s)", len(s.Cases), s.Cases[0].Label, status)
	return nil
}

func (s Seeder) complete(ctx context.Context, c Case) (bool, error) {
	if _, ok, err := s.SeedState(c.Slot); err != nil || !ok {
		return false, err
	}
	for _, db := range []string{c.Database, c.Template()} {
		ok, err := s.Admin.Exists(ctx, db)
		if err != nil || !ok {
			return false, err
		}
	}
	return true, nil
}

func (s Seeder) seedCase(ctx context.Context, c Case) error {
	done, err := s.complete(ctx, c)
	if err != nil {
		return err
	}
	if done {
		s.say("%s is already seeded; re-checking it", c.Label)
	} else if err := s.wipe(ctx, c); err != nil {
		return err
	}
	// The freeze writes the manifest and the seed state into the case directory and does not create it.
	if err := os.MkdirAll(s.Paths.CaseDir(c.Slot), 0o755); err != nil {
		return fmt.Errorf("codespace: create the directory of %s: %w", c.Slot, err)
	}
	opts := s.Options
	opts.Opportunity = c.OpportunityID
	return s.Seed(ctx, c, opts, func(ctx context.Context) error { return s.snapshot(ctx, c) })
}

// wipe leaves the case with an empty, migrated database and no seed state.
func (s Seeder) wipe(ctx context.Context, c Case) error {
	s.say("preparing an empty database for %s", c.Label)
	if err := s.P.StopCore(ctx); err != nil { // core holds sessions on the database being replaced
		return err
	}
	for _, db := range []string{c.Database, c.Template()} {
		if err := s.Admin.Drop(ctx, db); err != nil {
			return err
		}
	}
	if err := os.RemoveAll(s.Paths.CaseDir(c.Slot)); err != nil {
		return fmt.Errorf("codespace: remove the stale state of %s: %w", c.Slot, err)
	}
	if err := s.Admin.Create(ctx, c.Database); err != nil {
		return err
	}
	dsn, err := DSNFor(s.BaseDSN, c.Database)
	if err != nil {
		return err
	}
	if err := s.Migrate(ctx, dsn); err != nil {
		return fmt.Errorf("migrate %s: %w", c.Database, err)
	}
	return nil
}

// snapshot copies the frozen database to its template and records that the graph holds this case.
func (s Seeder) snapshot(ctx context.Context, c Case) error {
	s.say("snapshotting %s at Event N-1 (the Reset button restores from this)", c.Label)
	if err := s.Admin.Drop(ctx, c.Template()); err != nil {
		return err
	}
	if err := s.Admin.Clone(ctx, c.Template(), c.Database); err != nil {
		return err
	}
	return WriteMarker(s.Paths.GraphMarker(c.Slot), c.Slot)
}
