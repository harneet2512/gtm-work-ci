package codespace

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/harneet2512/gtm-work/core-go/internal/demorun"
)

// fakeSeed stands in for demorun.Flow.Seed: it writes the seed state (as the real flow does) and runs afterGraph.
type fakeSeed struct {
	r     *rig
	calls []string
	fail  error
}

func (f *fakeSeed) fn(ctx context.Context, c Case, opts demorun.SeedOptions, after func(context.Context) error) error {
	f.calls = append(f.calls, c.Slot+":"+opts.Opportunity)
	if info, err := os.Stat(f.r.ops.Paths.CaseDir(c.Slot)); err != nil || !info.IsDir() {
		return fmt.Errorf("the case directory must exist before the freeze writes its manifest: %v", err)
	}
	if f.fail != nil {
		return f.fail
	}
	if _, ok := f.r.admin.dbs[c.Database]; !ok {
		return fmt.Errorf("the freeze needs the database %s to exist and be empty", c.Database)
	}
	f.r.admin.dbs[c.Database] = 1 // the frozen history
	st := demorun.DemoState{ManifestID: "man-" + c.Slot, AccountID: "acct-" + c.Slot, OpportunityID: opts.Opportunity}
	if err := demorun.SaveState(f.r.ops.Paths.State(c.Slot), st); err != nil {
		return err
	}
	return after(ctx)
}

func (r *rig) seeder(fs *fakeSeed) (Seeder, *[]string) {
	var migrated []string
	return Seeder{Ops: r.ops, BaseDSN: "postgres://ghost:ghost@127.0.0.1:15432/ghost_demo?sslmode=disable",
		Migrate: func(_ context.Context, dsn string) error { migrated = append(migrated, dsn); return nil },
		Seed:    fs.fn, Options: demorun.SeedOptions{Data: "/data", Report: "/report.json"}}, &migrated
}

func TestSeedAllFreezesBothCasesEachInItsOwnDatabaseSnapshotsThemAndActivatesCaseOne(t *testing.T) {
	r := newRig(t)
	fs := &fakeSeed{r: r}
	s, migrated := r.seeder(fs)
	if err := s.SeedAll(context.Background()); err != nil {
		t.Fatalf("SeedAll: %v", err)
	}
	if got := strings.Join(fs.calls, ","); got != "case1:006Wt000007BHzBIAW,case2:006Wt000007BDAnIAO" {
		t.Fatalf("seeded = %s", got)
	}
	if len(*migrated) != 2 || !strings.Contains((*migrated)[0], "/ghost_case1?") || !strings.Contains((*migrated)[1], "/ghost_case2?") {
		t.Fatalf("each fresh database must be migrated: %v", *migrated)
	}
	for _, c := range r.ops.Cases {
		if g, ok := r.admin.dbs[c.Template()]; !ok || g != 1 {
			t.Errorf("%s: template = %d,%v; want a copy of the frozen database", c.Slot, g, ok)
		}
	}
	if active, _ := ReadMarker(r.ops.Paths.ActiveFile()); active != SlotCase1 {
		t.Fatalf("active = %q, want case1", active)
	}
	// Each freeze projected its case into that case's own Neo4j, so activating case 1 at the end rebuilds nothing.
	if contains(r.plat.events, "rebuild:case1") || !contains(r.plat.events, "start-core:case1") {
		t.Fatalf("events = %v", r.plat.events)
	}
	for _, slot := range []string{SlotCase1, SlotCase2} {
		if g, _ := ReadMarker(r.ops.Paths.GraphMarker(slot)); g != slot {
			t.Fatalf("%s's graph is recorded as built: %q", slot, g)
		}
	}
}

func TestSeedAllIsIdempotentAndHealsAnIncompleteCase(t *testing.T) {
	r := newRig(t)
	fs := &fakeSeed{r: r}
	s, migrated := r.seeder(fs)
	if err := s.SeedAll(context.Background()); err != nil {
		t.Fatal(err)
	}
	*migrated = nil
	r.admin.calls = nil
	if err := s.SeedAll(context.Background()); err != nil {
		t.Fatalf("second SeedAll: %v", err)
	}
	if len(*migrated) != 0 || contains(r.admin.calls, "create:ghost_case1") {
		t.Fatalf("a complete case must not be recreated or migrated again: %v %v", *migrated, r.admin.calls)
	}
	// A failed first run left case 2's database without a template: it is wiped and frozen again.
	delete(r.admin.dbs, "ghost_case2_frozen")
	r.admin.dbs["ghost_case2"] = 9
	*migrated = nil
	if err := s.SeedAll(context.Background()); err != nil {
		t.Fatalf("heal: %v", err)
	}
	if len(*migrated) != 1 || !strings.Contains((*migrated)[0], "ghost_case2") || r.admin.dbs["ghost_case2"] != 1 {
		t.Fatalf("incomplete case not rebuilt from scratch: %v %v", *migrated, r.admin.dbs)
	}
}

func TestSeedAllNamesTheFailedCaseAndRejectsAnInvalidCase(t *testing.T) {
	r := newRig(t)
	fs := &fakeSeed{r: r, fail: errors.New("freeze refused")}
	s, _ := r.seeder(fs)
	err := s.SeedAll(context.Background())
	if err == nil || !strings.Contains(err.Error(), "case1") || !strings.Contains(err.Error(), "freeze refused") {
		t.Fatalf("err = %v", err)
	}
	s.Cases = []Case{{Slot: "../x", Database: "d", OpportunityID: "006"}}
	if err := s.SeedAll(context.Background()); err == nil {
		t.Fatal("an invalid case must be refused before any work")
	}
	r.admin.fail["create:ghost_case1"] = errors.New("cannot create")
	s2, _ := r.seeder(&fakeSeed{r: r})
	if err := s2.SeedAll(context.Background()); err == nil || !strings.Contains(err.Error(), "cannot create") {
		t.Fatalf("err = %v", err)
	}
}

func TestSeedAllReportsAMigrationFailure(t *testing.T) {
	r := newRig(t)
	s, _ := r.seeder(&fakeSeed{r: r})
	s.Migrate = func(context.Context, string) error { return errors.New("bad migration") }
	if err := s.SeedAll(context.Background()); err == nil || !strings.Contains(err.Error(), "migrate ghost_case1") {
		t.Fatalf("err = %v", err)
	}
}
