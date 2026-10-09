package demorun

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// SeedOptions are the flags of `demo seed`.
type SeedOptions struct {
	Opportunity string
	Data        string
	Report      string
}

// Seed freezes the demo case into the empty database, projects it into Neo4j and checks that Event N is invisible.
func (f Flow) Seed(ctx context.Context, o SeedOptions) error {
	for _, svc := range []string{SvcPostgres, SvcNeo4j} {
		if !f.running(svc) {
			return fmt.Errorf("%s is not running; run `demo up` first", svc)
		}
	}
	prev, seeded, err := LoadState(f.statePath())
	if err != nil {
		return err
	}
	if seeded && prev.ManifestID != "" {
		f.say("already seeded: %s (manifest %s). `demo reset --yes` starts over.", prev.CaseName, prev.ManifestID)
		return f.seedFinish(ctx, prev, false)
	}
	return f.seedFresh(ctx, o)
}

func (f Flow) seedFresh(ctx context.Context, o SeedOptions) error {
	l := f.Cfg.Layout
	main := MainCheckout(l.Root)
	repPath, err := FindReport(o.Report, l.Root, main)
	if err != nil {
		return err
	}
	rep, err := LoadReport(repPath)
	if err != nil {
		return err
	}
	c, err := PickCase(rep, o.Opportunity)
	if err != nil {
		return err
	}
	snapshot, note, err := ResolveSnapshot(o.Data, []string{f.Cfg.Process["GHOST_DEMO_DATA"],
		filepath.Join(l.Root, "data", "crmarena_b2b"), filepath.Join(main, "data", "crmarena_b2b")}, rep.Snapshot.ManifestSHA256)
	if err != nil {
		return err
	}
	createdAt, err := CreatedAt(rep.Date)
	if err != nil {
		return err
	}
	f.say("case: %s, %s (rank %d of the report dated %s)\nheld-out Event N: %s\nsnapshot: %s", c.AccountName, c.OpportunityName, c.Rank, rep.Date, c.HeldOutEventID, snapshot)
	if note != "" {
		f.say("NOTE: %s", note)
	}
	if err := f.Stores.RequireEmpty(ctx); err != nil {
		return err
	}
	if f.running(SvcCore) {
		f.say("pausing core while the history is written (its coalescer would race the freeze)")
		if err := f.Sup.Stop(ctx, f.coreSpec()); err != nil {
			return err
		}
	}
	if err := f.ApplyToolEnv(f.Cfg.ToolEnv()); err != nil {
		return err
	}
	freezeArgs := []string{"freeze-demo-manifest", "--into-database", "--opportunity", c.OpportunityID, "--held-out", c.HeldOutEventID,
		"--report", repPath, "--data", snapshot, "--out", f.manifestPath(), "--created-at", createdAt, "--replay-events-out", l.ReplayEventsDir()}
	if f.FreezeWorkerURL != "" {
		freezeArgs = append(freezeArgs, "--worker-url", f.FreezeWorkerURL)
	}
	if rules := f.Cfg.TransitionRules(); rules != "" {
		// The same rules must shape the frozen history and the live core (the default is the repository's rule set).
		if !filepath.IsAbs(rules) {
			rules = filepath.Join(l.Root, rules)
		}
		freezeArgs = append(freezeArgs, "--transition-rules", rules)
	}
	f.say("freezing the demo case into the empty database: replaying the snapshot through Event N-1 (this takes several minutes; Event N is never ingested)")
	if err := f.withHeartbeat(ctx, "freeze still running", func() error { return f.RunTool(freezeArgs) }); err != nil {
		return fmt.Errorf("%w\n(the freeze refuses a non-empty database, so after a failure run `demo reset --yes` and `demo up` before seeding again)", err)
	}
	f.say("projecting the history into Neo4j (graph rebuild) ...")
	if err := f.withHeartbeat(ctx, "graph rebuild still running", func() error { return f.RunTool([]string{"graph", "rebuild"}) }); err != nil {
		return err
	}
	st, err := ReadManifestIDs(f.manifestPath())
	if err != nil {
		return err
	}
	st.CaseName, st.HeldOutEventID, st.SeededAt = c.AccountName, c.HeldOutEventID, f.now().UTC()
	if err := SaveState(f.statePath(), st); err != nil {
		return err
	}
	if f.AfterGraph != nil {
		if err := f.AfterGraph(ctx); err != nil {
			return err
		}
	}
	return f.seedFinish(ctx, st, true)
}

// seedFinish makes sure core is up and reports the event-N-invisible assertion.
func (f Flow) seedFinish(ctx context.Context, st DemoState, justSeeded bool) error {
	if err := f.Sup.Start(ctx, f.coreSpec()); err != nil {
		return err
	}
	inv, err := f.Core.Invisibility(ctx, st.ManifestID)
	if err != nil {
		return fmt.Errorf("event-N-invisible check: %w", err)
	}
	verdict := "FAIL"
	if inv.Status == "withheld" || inv.Status == "released" {
		verdict = "PASS"
	}
	f.say("%s event-N-invisible: %s (checked %v, %d leak(s)) for manifest %s", verdict, inv.Status, inv.Checked, len(inv.Leaks), st.ManifestID)
	if inv.Status == "leaked" {
		return fmt.Errorf("Event N is visible before Play: %s", describeLeaks(inv.Leaks))
	}
	// The freeze leaves the replay cursor at 0, so the presenter's first Play (episodes/next) would release history event 1. Park it at
	// N-1 and check that the next releasable event is Event N, before anything is sealed. Once Event N has been released the cursor is
	// past N-1 and is left alone (a re-check of a played case).
	if parker, ok := f.Core.(cursorParker); ok && inv.Status == "withheld" {
		cur, err := parker.ParkCursorAtEventN(ctx, st.ManifestID, inv.HeldOutEventID)
		if err != nil {
			return fmt.Errorf("replay cursor of %s: %w", st.CaseName, err)
		}
		f.say("PASS replay-cursor: %d of %d history events released, the next Play releases Event N (%s)", cur.Released, cur.Total-1, cur.NextID)
	}
	if justSeeded {
		st.InvisibilityAtSeed = inv.Status
		if err := SaveState(f.statePath(), st); err != nil {
			return err
		}
	}
	f.say("seeded %s: account %s, opportunity %s. Next: `demo play`.", st.CaseName, st.AccountID, st.OpportunityID)
	return nil
}

// ReadManifestIDs reads the ids the frozen manifest assigned.
func ReadManifestIDs(path string) (DemoState, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return DemoState{}, fmt.Errorf("read the frozen manifest: %w", err)
	}
	var m struct {
		ID            string `json:"id"`
		AccountID     string `json:"account_id"`
		OpportunityID string `json:"opportunity_id"`
	}
	if err := json.Unmarshal(b, &m); err != nil || m.ID == "" || m.AccountID == "" {
		return DemoState{}, errors.New("the frozen manifest has no id or account_id")
	}
	return DemoState{ManifestID: m.ID, AccountID: m.AccountID, OpportunityID: m.OpportunityID}, nil
}
