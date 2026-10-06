package demorun

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func (r *flowRig) withCase() (report, snapshot string) {
	r.t.Helper()
	report = filepath.Join(r.root, "report.json")
	body, _ := json.Marshal(sampleReport())
	if err := os.WriteFile(report, body, 0o644); err != nil {
		r.t.Fatal(err)
	}
	snapshot = filepath.Join(r.root, "snapshot")
	writeSnapshot(r.t, snapshot)
	return report, snapshot
}

func (r *flowRig) seedOpts() SeedOptions {
	report, snapshot := r.withCase()
	return SeedOptions{Opportunity: r.opp, Data: snapshot, Report: report}
}

func TestSeedFreezesProjectsRestartsCoreAndChecksInvisibility(t *testing.T) {
	r := newRig(t)
	if err := r.flow.Sup.Start(context.Background(), r.flow.CoreSpec); err != nil { // core is up before the seed
		t.Fatal(err)
	}
	_, before := r.flow.Sup.PIDs.State(SvcCore)
	if err := r.flow.Seed(context.Background(), r.seedOpts()); err != nil {
		t.Fatalf("Seed: %v\n%s", err, r.out.String())
	}
	if len(r.tools) != 2 || r.tools[0][0] != "freeze-demo-manifest" || r.tools[1][0] != "graph" || r.tools[1][1] != "rebuild" {
		t.Fatalf("tools = %v, want the freeze then a graph rebuild", r.tools)
	}
	args := strings.Join(r.tools[0], " ")
	for _, want := range []string{"--into-database", "--opportunity " + MedTechOpportunity, "--held-out 1e32b30c-3892-5e61-9dd5-411dc5392ac5",
		"--created-at 2026-10-04T00:00:00Z", "--replay-events-out", "--report", "--data"} {
		if !strings.Contains(filepathSlash(args), filepathSlash(want)) {
			t.Errorf("freeze args lack %q: %s", want, args)
		}
	}
	if strings.Contains(args, "--transition-rules") {
		t.Fatal("the detector is off unless the user set GHOST_TRANSITION_RULES")
	}
	st, ok, err := LoadState(r.flow.Cfg.Layout.StateFile())
	if err != nil || !ok || st.ManifestID != "man-1" || st.AccountID != "acct-1" || st.CaseName != "MedTech Advances" ||
		st.InvisibilityAtSeed != "withheld" || st.HeldOutEventID != "1e32b30c-3892-5e61-9dd5-411dc5392ac5" {
		t.Fatalf("state = %+v ok=%v err=%v", st, ok, err)
	}
	if s, after := r.flow.Sup.PIDs.State(SvcCore); s != StateRunning || after.PID == before.PID {
		t.Fatalf("core must be restarted after the freeze: %v %d -> %d", s, before.PID, after.PID)
	}
	for _, want := range []string{"pausing core", "PASS event-N-invisible: withheld", "seeded MedTech Advances"} {
		if !strings.Contains(r.out.String(), want) {
			t.Errorf("output lacks %q:\n%s", want, r.out.String())
		}
	}
}

func TestSeedPassesTheTransitionRulesOnlyWhenTheUserSetThem(t *testing.T) {
	r := newRig(t)
	r.flow.Cfg.DotEnv["GHOST_TRANSITION_RULES"] = "contracts/transitions/rules.v1.json"
	if err := r.flow.Seed(context.Background(), r.seedOpts()); err != nil {
		t.Fatal(err)
	}
	if args := strings.Join(r.tools[0], " "); !strings.Contains(args, "--transition-rules contracts/transitions/rules.v1.json") {
		t.Fatalf("the same rules must shape the frozen history: %s", args)
	}
}

func TestSeedTwiceIsAReadBackNotASecondFreeze(t *testing.T) {
	r := newRig(t)
	opts := r.seedOpts()
	if err := r.flow.Seed(context.Background(), opts); err != nil {
		t.Fatal(err)
	}
	r.tools = nil
	r.out.Reset()
	if err := r.flow.Seed(context.Background(), opts); err != nil {
		t.Fatalf("second Seed: %v", err)
	}
	if len(r.tools) != 0 {
		t.Fatalf("a second seed must not freeze again: %v", r.tools)
	}
	if !strings.Contains(r.out.String(), "already seeded") || !strings.Contains(r.out.String(), "event-N-invisible") {
		t.Fatalf("should say so and re-check invisibility:\n%s", r.out.String())
	}
}

func TestSeedRefusesWithoutTheStoresAndWithANonEmptyDatabase(t *testing.T) {
	r := newRig(t)
	if err := r.flow.Sup.PIDs.Remove(SvcNeo4j); err != nil {
		t.Fatal(err)
	}
	if err := r.flow.Seed(context.Background(), r.seedOpts()); err == nil || !strings.Contains(err.Error(), "neo4j") || !strings.Contains(err.Error(), "demo up") {
		t.Fatalf("a missing store must say `demo up`, got %v", err)
	}
	r = newRig(t)
	r.stores.emptyErr = errors.New("the demo database is not empty")
	if err := r.flow.Sup.Start(context.Background(), r.flow.CoreSpec); err != nil {
		t.Fatal(err)
	}
	if err := r.flow.Seed(context.Background(), r.seedOpts()); err == nil || !strings.Contains(err.Error(), "not empty") {
		t.Fatalf("got %v", err)
	}
	if len(r.tools) != 0 || !r.flow.running(SvcCore) {
		t.Fatalf("a refused seed must not stop core or run a tool: tools=%v", r.tools)
	}
}

func TestSeedFailureNamesTheRecovery(t *testing.T) {
	r := newRig(t)
	r.toolFn = func(args []string) error {
		if args[0] == "freeze-demo-manifest" {
			return errors.New("the replay is stale")
		}
		return nil
	}
	err := r.flow.Seed(context.Background(), r.seedOpts())
	if err == nil || !strings.Contains(err.Error(), "stale") || !strings.Contains(err.Error(), "demo reset --yes") {
		t.Fatalf("a freeze failure must say how to recover, got %v", err)
	}
	if _, ok, _ := LoadState(r.flow.Cfg.Layout.StateFile()); ok {
		t.Fatal("a failed seed must not record state")
	}
}

func TestSeedRefusesALeakedWorld(t *testing.T) {
	r := newRig(t)
	r.core.inv = Invisibility{Status: "leaked", Leaks: []Leak{{Store: "neo4j", Kind: "graph_node", ID: "n1"}}}
	err := r.flow.Seed(context.Background(), r.seedOpts())
	if err == nil || !strings.Contains(err.Error(), "visible before Play") || !strings.Contains(err.Error(), "n1") {
		t.Fatalf("got %v", err)
	}
	if !strings.Contains(r.out.String(), "FAIL event-N-invisible") {
		t.Fatalf("the FAIL line must be printed:\n%s", r.out.String())
	}
}

func TestSeedInputErrorsAreSpecific(t *testing.T) {
	r := newRig(t)
	opts := r.seedOpts()
	bad := opts
	bad.Opportunity = "006NOPE"
	if err := r.flow.Seed(context.Background(), bad); err == nil || !strings.Contains(err.Error(), "006NOPE") {
		t.Fatalf("unranked opportunity: %v", err)
	}
	bad = opts
	bad.Data = filepath.Join(r.root, "no-snapshot")
	r.flow.Cfg.Process = Env{}
	if err := r.flow.Seed(context.Background(), bad); err == nil || !strings.Contains(err.Error(), "snapshot") {
		t.Fatalf("missing snapshot: %v", err)
	}
	bad = opts
	bad.Report = filepath.Join(r.root, "no-report.json")
	if err := r.flow.Seed(context.Background(), bad); err == nil {
		t.Fatal("a missing report must fail")
	}
	if len(r.tools) != 0 {
		t.Fatalf("input errors must be caught before any tool runs: %v", r.tools)
	}
}
