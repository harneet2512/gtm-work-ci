package demorun

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const fakePid = 2147483001 // never a real process: the pid store's Alive treats it as running

type fakeStores struct {
	emptyErr error
	facts    Facts
	factsErr error
	gotOpts  FetchOptions
}

func (f *fakeStores) RequireEmpty(context.Context) error { return f.emptyErr }
func (f *fakeStores) Facts(_ context.Context, _ DemoState, o FetchOptions) (Facts, error) {
	f.gotOpts = o
	return f.facts, f.factsErr
}

type flowRig struct {
	t      *testing.T
	flow   Flow
	out    *bytes.Buffer
	stores *fakeStores
	core   *fakePlayCore
	tools  [][]string
	toolFn func(args []string) error
	root   string
	opp    string
}

// newRig builds a Flow over a temp repository. Postgres and Neo4j are recorded as running (fake pids), core is the
// test binary re-run as a fake service, and the freeze and graph tools are scripted.
func newRig(t *testing.T) *flowRig {
	t.Helper()
	root := t.TempDir()
	l := NewLayout(root)
	l.StateDir = filepath.Join(root, "state")
	out := &bytes.Buffer{}
	pids := PIDStore{Dir: l.PIDDir(), Alive: func(pid int) bool { return pid == fakePid || ProcessAlive(pid) }}
	for _, svc := range []string{SvcPostgres, SvcNeo4j} {
		if err := pids.Write(Record{Service: svc, PID: fakePid, StartedAt: time.Now()}); err != nil {
			t.Fatal(err)
		}
	}
	coreSpec, port := helperSpec(t, SvcCore, "serve", nil)
	cfg := testConfig()
	cfg.Layout = l
	cfg.Ports.Core = port
	r := &flowRig{t: t, out: out, stores: &fakeStores{}, core: happyCore(), root: root, opp: MedTechOpportunity}
	r.flow = Flow{
		Cfg: cfg, Sup: Supervisor{Layout: l, PIDs: pids, Out: out}, Out: out, Core: r.core, Stores: r.stores,
		CoreSpec: coreSpec, Heartbeat: time.Hour,
		RunTool:      r.runTool,
		ApplyToolEnv: func(Env) error { return nil },
		PageExists:   func(context.Context, string) bool { return true },
		Now:          func() time.Time { return time.Date(2026, 10, 4, 15, 0, 0, 0, time.UTC) },
	}
	r.core.inv = Invisibility{Status: "withheld", Checked: []string{"postgres", "neo4j"}}
	t.Cleanup(func() { _ = r.flow.Sup.Stop(context.Background(), coreSpec) })
	return r
}

// runTool records the command and, for the freeze, writes what the real one writes: the manifest and the replay event.
func (r *flowRig) runTool(args []string) error {
	r.tools = append(r.tools, args)
	if r.toolFn != nil {
		if err := r.toolFn(args); err != nil {
			return err
		}
	}
	if args[0] != "freeze-demo-manifest" {
		return nil
	}
	flags := map[string]string{}
	for i := 1; i < len(args); i++ {
		if args[i] == "--into-database" { // the one boolean flag
			continue
		}
		if i+1 < len(args) {
			flags[args[i]] = args[i+1]
			i++
		}
	}
	if err := os.MkdirAll(filepath.Dir(flags["--out"]), 0o755); err != nil {
		return err
	}
	body, _ := json.Marshal(map[string]string{"id": "man-1", "account_id": "acct-1", "opportunity_id": "opp-1"})
	if err := os.WriteFile(flags["--out"], body, 0o644); err != nil {
		return err
	}
	return os.MkdirAll(flags["--replay-events-out"], 0o755)
}

// withCase writes a demo-cases report and a snapshot where the flow looks for them and returns the paths.
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

func TestPlayRequiresASeedAndThenReportsTheFlowWithURLsAndChecklist(t *testing.T) {
	r := newRig(t)
	if err := r.flow.Play(context.Background(), PlayCmdOptions{Timeout: time.Minute}); err == nil || !strings.Contains(err.Error(), "demo seed") {
		t.Fatalf("play before seed: %v", err)
	}
	if err := SaveState(r.flow.Cfg.Layout.StateFile(), DemoState{ManifestID: "man-1", AccountID: "acct-1", CaseName: "MedTech Advances", InvisibilityAtSeed: "withheld"}); err != nil {
		t.Fatal(err)
	}
	r.flow.Cfg.Process = Env{}
	r.core.inv = Invisibility{Status: "withheld"}
	clock := &fakeClock{now: time.Unix(0, 0)}
	_ = clock
	if err := r.flow.Play(context.Background(), PlayCmdOptions{Timeout: time.Minute, NoWait: true}); err != nil {
		t.Fatalf("Play: %v\n%s", err, r.out.String())
	}
	for _, want := range []string{"BI update written", "OPEN IN THE BROWSER", "/accounts/acct-1", "WHAT TO CLICK IN SLACK", "demo verify"} {
		if !strings.Contains(r.out.String(), want) {
			t.Errorf("output lacks %q:\n%s", want, r.out.String())
		}
	}
	st, _, _ := LoadState(r.flow.Cfg.Layout.StateFile())
	if st.PlayedAt.IsZero() || st.BIUpdateID != "bi-1" {
		t.Fatalf("the played state must be saved: %+v", st)
	}
}

func TestPlayFailureStillSavesWhatItLearnedAndPrintsNoChecklist(t *testing.T) {
	r := newRig(t)
	if err := SaveState(r.flow.Cfg.Layout.StateFile(), DemoState{ManifestID: "man-1", AccountID: "acct-1", CaseName: "X"}); err != nil {
		t.Fatal(err)
	}
	r.core.playErr = &APIError{Status: 503, Code: "graph_unavailable", Message: "neo4j down"}
	err := r.flow.Play(context.Background(), PlayCmdOptions{Timeout: time.Minute})
	if err == nil || strings.Contains(r.out.String(), "WHAT TO CLICK") {
		t.Fatalf("a failed play prints the error, not the checklist: err=%v\n%s", err, r.out.String())
	}
}

func TestVerifyPrintsStepsAndFailsOnAFailedStep(t *testing.T) {
	r := newRig(t)
	if err := r.flow.Verify(context.Background()); err == nil || !strings.Contains(err.Error(), "demo seed") {
		t.Fatalf("verify before seed: %v", err)
	}
	if err := SaveState(r.flow.Cfg.Layout.StateFile(), DemoState{ManifestID: "man-1", AccountID: "acct-1", CaseName: "MedTech Advances", PlayedAt: time.Now(), InvisibilityAtSeed: "withheld", SlackWasOn: true}); err != nil {
		t.Fatal(err)
	}
	r.stores.facts = fullFacts()
	r.core.inv = Invisibility{Status: "released"}
	if err := r.flow.Verify(context.Background()); err != nil {
		t.Fatalf("a complete walkthrough verifies: %v\n%s", err, r.out.String())
	}
	if !r.stores.gotOpts.SlackOn || !strings.HasSuffix(filepathSlash(r.stores.gotOpts.AuditPath), "logs/slack-audit.jsonl") {
		t.Fatalf("verify must pass Slack-on from the remembered state and the audit path: %+v", r.stores.gotOpts)
	}
	r.stores.facts.Decision.SendDecision = "pending"
	if err := r.flow.Verify(context.Background()); err == nil || !strings.Contains(err.Error(), "FAILED") {
		t.Fatalf("a FAIL step must fail the command, got %v", err)
	}
	r.stores.factsErr = errors.New("database down")
	if err := r.flow.Verify(context.Background()); err == nil || !strings.Contains(err.Error(), "database down") {
		t.Fatalf("a read error must surface, got %v", err)
	}
}

func TestUpValidatesBeforePreparingAnything(t *testing.T) {
	r := newRig(t)
	prepared := false
	r.flow.Prepare = func(context.Context, bool, bool) (Tooling, error) {
		prepared = true
		return Tooling{}, errors.New("stop here")
	}
	cases := []struct {
		name string
		mut  func(*Flow)
		o    UpOptions
		want string
	}{
		{"bad mode", func(*Flow) {}, UpOptions{Mode: "bogus"}, "llm-mode"},
		{"no channel", func(f *Flow) { delete(f.Cfg.DotEnv, "SLACK_CHANNEL_ID") }, UpOptions{Mode: "replay", NoWeb: true}, "SLACK_CHANNEL_ID"},
		{"no model key", func(f *Flow) { delete(f.Cfg.DotEnv, "OPENROUTER_API_KEY") }, UpOptions{Mode: "live", NoSlack: true}, "OPENROUTER_API_KEY"},
		{"slack tokens", func(f *Flow) {
			f.ResolveSlack = func(Env, Env) (Env, error) { return nil, errors.New("missing SLACK_BOT_TOKEN") }
		}, UpOptions{Mode: "replay"}, "SLACK_BOT_TOKEN"},
	}
	for _, c := range cases {
		f := r.flow
		f.Cfg.DotEnv = Merge(r.flow.Cfg.DotEnv)
		c.mut(&f)
		if err := f.Up(context.Background(), c.o); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: want %q, got %v", c.name, c.want, err)
		}
	}
	if prepared {
		t.Fatal("validation errors must come before Prepare builds anything")
	}
	// A Prepare failure propagates (the real services are exercised by the integration smoke).
	f := r.flow
	f.ResolveSlack = func(Env, Env) (Env, error) { return Env{"SLACK_BOT_TOKEN": "x", "SLACK_APP_TOKEN": "y"}, nil }
	if err := f.Up(context.Background(), UpOptions{Mode: "replay", NoWeb: true}); err == nil || !strings.Contains(err.Error(), "stop here") {
		t.Fatalf("Prepare error: %v", err)
	}
	if !strings.Contains(r.out.String(), "WARNING: SLACK_ALLOWED_USER_IDS is not set") {
		t.Fatalf("the allow-all fallback must be announced:\n%s", r.out.String())
	}
	if !strings.Contains(r.out.String(), "environment:") {
		t.Fatalf("the .env line is missing:\n%s", r.out.String())
	}
}

func TestFindCassettesPrefersTheFlagThenTheEnvironmentThenTheCheckouts(t *testing.T) {
	root := t.TempDir()
	cfg := Config{Layout: NewLayout(root), Process: Env{}}
	if got := FindCassettes("", cfg); got != "" {
		t.Fatalf("nothing exists: %q", got)
	}
	repoCass := filepath.Join(root, "data", "crmarena_extraction", "cassettes")
	if err := os.MkdirAll(repoCass, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := FindCassettes("", cfg); got != repoCass {
		t.Fatalf("the checkout's directory: %q", got)
	}
	envCass := t.TempDir()
	cfg.Process["GHOST_DEMO_CASSETTES"] = envCass
	if got := FindCassettes("", cfg); got != envCass {
		t.Fatalf("GHOST_DEMO_CASSETTES beats the checkout: %q", got)
	}
	flagCass := t.TempDir()
	if got := FindCassettes(flagCass, cfg); got != flagCass {
		t.Fatalf("the flag wins: %q", got)
	}
	if got := FindCassettes(filepath.Join(root, "not-a-dir"), Config{Layout: NewLayout(t.TempDir()), Process: Env{}}); got != "" {
		t.Fatalf("a flag that is not a directory is ignored: %q", got)
	}
}

func TestReadManifestIDsRejectsAnIncompleteManifest(t *testing.T) {
	dir := t.TempDir()
	good := filepath.Join(dir, "m.json")
	if err := os.WriteFile(good, []byte(`{"id":"i","account_id":"a","opportunity_id":"o"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if st, err := ReadManifestIDs(good); err != nil || st.ManifestID != "i" || st.AccountID != "a" || st.OpportunityID != "o" {
		t.Fatalf("%+v %v", st, err)
	}
	bad := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(bad, []byte(`{"id":""}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadManifestIDs(bad); err == nil {
		t.Fatal("a manifest without ids must be rejected")
	}
	if _, err := ReadManifestIDs(filepath.Join(dir, "none.json")); err == nil {
		t.Fatal("a missing manifest must be rejected")
	}
}

func TestHeartbeatPrintsWhileALongStepRuns(t *testing.T) {
	r := newRig(t)
	r.flow.Heartbeat = 10 * time.Millisecond
	err := r.flow.withHeartbeat(context.Background(), "freeze still running", func() error { time.Sleep(80 * time.Millisecond); return nil })
	if err != nil || !strings.Contains(r.out.String(), "freeze still running") {
		t.Fatalf("err=%v out=%q", err, r.out.String())
	}
}

func TestAllSpecsListsEverySurface(t *testing.T) {
	r := newRig(t)
	if got := specNames(r.flow.AllSpecs()); got != "neo4j,postgres,worker,core,slackbot,web" {
		t.Fatalf("AllSpecs = %s", got)
	}
}
