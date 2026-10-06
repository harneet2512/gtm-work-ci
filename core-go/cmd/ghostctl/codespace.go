package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/codespace"
	"github.com/harneet2512/gtm-work/core-go/internal/demorun"
	"github.com/harneet2512/gtm-work/core-go/internal/store"
)

const codespaceUsage = "usage: ghostctl codespace <setup [--data DIR] [--report FILE] | up | serve | status | case <case1|case2> | reset --yes | baseline [seal|verify] [--deep]>\n" +
	"setup is the one-time setup (it builds everything once and seals the baseline), up the every-start boot (it restores the baseline, or resumes the live state with GHOST_DEMO_START=resume; it never rebuilds), reset restores the baseline, baseline verifies or re-seals it, serve the loopback control service the web server calls (readiness, and the hidden handoff behind Play)"

func init() { commands["codespace"] = runCodespace }

// runCodespace is the cloud demo's hidden automation (docs/demo/codespace.md): flag parsing and wiring only; the
// logic and its tests live in internal/codespace, on top of the runner in internal/demorun.
func runCodespace(args []string, out io.Writer) error {
	if len(args) == 0 {
		return errors.New(codespaceUsage)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	switch args[0] {
	case "setup", "up", "down", "record", "serve", "status", "case", "reset", "baseline":
	default:
		return errors.New(codespaceUsage)
	}
	var data, report string
	var yes, deep bool
	var mode string
	fs := flag.NewFlagSet("codespace "+args[0], flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.StringVar(&data, "data", "", "CRMArena snapshot directory (default data/crmarena_b2b)")
	fs.StringVar(&report, "report", "", "demo-cases report (default: the newest bench/reports/demo-cases-*.json)")
	fs.BoolVar(&yes, "yes", false, "confirm a reset")
	fs.BoolVar(&deep, "deep", false, "baseline verify: also read every byte")
	fs.StringVar(&mode, "mode", "", "up: restore or resume, overriding GHOST_DEMO_START (the Reset shortcut passes restore)")
	if err := fs.Parse(args[1:]); err != nil {
		return errors.New(codespaceUsage)
	}
	rt, closeFlow, err := loadCodespace(out, data, report)
	if err != nil {
		return err
	}
	defer closeFlow()
	switch args[0] {
	case "setup":
		return codespaceSetup(ctx, rt)
	case "up":
		m, err := codespace.ResolveStartMode(mode, rt.Boot.Mode)
		if err != nil {
			return err
		}
		rt.Boot.Mode = m
		return rt.Boot.Run(ctx)
	case "record":
		// The one-time record run (scripts/demo/record-local.ps1): both cases through the real pipeline with the scripted
		// human, every model call stored once by the cache, then the whole chronology reset to the start.
		return rt.Record(ctx)
	case "down":
		return codespace.StopAll(ctx, func(c context.Context) error { return demorun.Down(c, rt.Boot.Sup, rt.StopSpecs()) },
			demorun.ListProcesses, demorun.KillTree, rt.Cfg.GraphDirs(), func(f string, a ...any) { fmt.Fprintf(out, f+"\n", a...) })
	case "serve":
		return codespaceServe(ctx, rt, out)
	case "baseline":
		return codespaceBaseline(ctx, rt, fs.Args(), deep, out)
	case "status":
		return json.NewEncoder(out).Encode(rt.Status.Compute(ctx))
	case "case":
		if fs.NArg() != 1 {
			return errors.New(codespaceUsage)
		}
		status, err := rt.Ops.Activate(ctx, fs.Arg(0))
		if err == nil {
			fmt.Fprintf(out, "active; event-N-invisible: %s\n", status)
		}
		return err
	default: // reset
		if !yes || fs.NArg() != 0 {
			return errors.New("ghostctl: codespace reset puts both cases back at Event N-1; re-run with --yes")
		}
		return rt.Ops.ResetAll(ctx, nil)
	}
}

// codespaceBaseline verifies the sealed baseline (fast, or byte by byte with --deep) or re-seals it from the live copy, which
// must be stopped. Nothing here rebuilds.
func codespaceBaseline(ctx context.Context, rt codespace.Runtime, args []string, deep bool, out io.Writer) error {
	action := "verify"
	var rest []string
	for _, a := range args { // the flag package stops at the first word, so "verify --deep" arrives here
		if a == "--deep" || a == "-deep" {
			deep = true
			continue
		}
		rest = append(rest, a)
	}
	args = rest
	if len(args) > 1 || (len(args) == 1 && args[0] != "verify" && args[0] != "seal") {
		return errors.New(codespaceUsage)
	}
	if len(args) == 1 {
		action = args[0]
	}
	if action == "seal" {
		m, err := rt.Baseline.Seal(ctx)
		if err != nil {
			return err
		}
		fmt.Fprintf(out, "sealed %d components at %s\n", len(m.Components), m.BuiltAt.Format(time.RFC3339))
		return nil
	}
	verify := rt.Baseline.Verify
	if deep {
		verify = rt.Baseline.VerifyDeep
	}
	if err := verify(); err != nil {
		return err
	}
	m, _ := rt.Baseline.Manifest()
	fmt.Fprintf(out, "baseline ok: %d components, built %s\n", len(m.Components), m.BuiltAt.Format(time.RFC3339))
	return nil
}

// loadCodespace wires the codespace operations over the runner's flow (loadFlow): real supervisor, in-process
// freeze and graph tools, core's HTTP client, the embedded Postgres and the production web build.
func loadCodespace(out io.Writer, data, report string) (codespace.Runtime, func(), error) {
	useDemoHome()
	f, closeFlow, err := loadFlow(out)
	if err != nil {
		return codespace.Runtime{}, nil, err
	}
	// The mutable state lives in a working copy under the demo home; the sealed baseline sits beside it (docs/demo/local.md).
	f.Cfg.Layout.Live = filepath.Join(f.Cfg.Layout.Dir(), "live")
	paths := codespace.Paths{Layout: f.Cfg.Layout}
	seed := func(ctx context.Context, c codespace.Case, opts demorun.SeedOptions, after func(context.Context) error) error {
		cf := f // a copy: the case's own database, files and snapshot hook
		cf.Cfg.Database = c.Database
		cf.Cfg = cf.Cfg.ForGraph(c.Graph) // the freeze projects into this case's own Neo4j
		cf.Stores = demorun.PGStores{DSN: cf.Cfg.DSN()}
		cf.ManifestFile, cf.StateFile, cf.AfterGraph = paths.Manifest(c.Slot), paths.State(c.Slot), after
		cf.FreezeWorkerURL = os.Getenv("GHOST_DEMO_FREEZE_WORKER_URL")
		return cf.Seed(ctx, opts)
	}
	rt := codespace.Assemble(f, codespace.Options{
		ResolveSlack: func(p, d demorun.Env) (demorun.Env, error) {
			return demorun.ResolveSlackTokens(p, d)
		},
		BuildWeb: func(ctx context.Context, npm string) error { return ensureWebBuild(ctx, f.Cfg.Layout.Root, npm, out) },
		Data:     data, Report: report, Seed: seed, Migrate: migrateDatabase,
	})
	return rt, closeFlow, nil
}

// codespaceSetup is the one-time setup: build everything, start the stores, replay each case's history through Event N-1 once,
// stop the stores, then seal the result as the baseline that every Start and Reset restores. It is the only place the demo
// builds a graph or processes an event.
func codespaceSetup(ctx context.Context, rt codespace.Runtime) error {
	// Seal rewrites the manifest, so read the previous note first (see noteKnownMisses below).
	var prev *codespace.KnownMissNote
	if m, err := rt.Baseline.Manifest(); err == nil {
		prev = m.KnownMisses
	}
	return setupWith(ctx, rt, setupSteps{
		up:      func(c context.Context, specs []demorun.Spec) error { return demorun.Up(c, rt.Boot.Sup, specs) },
		down:    func(c context.Context, specs []demorun.Spec) error { return demorun.Down(c, rt.Boot.Sup, specs) },
		seedAll: rt.Seeder.SeedAll,
		settle: func(c context.Context) error {
			if err := rt.Platform.StartStores(c); err != nil {
				return err
			}
			return rt.Platform.StopStores(c)
		},
		seal: func(c context.Context) error {
			m, err := rt.Baseline.Seal(c)
			if err == nil {
				rt.Ops.Log("sealed the baseline: %d components, built %s", len(m.Components), m.BuiltAt.Format(time.RFC3339))
			}
			return err
		},
		knownMisses: replayKnownMisses,
		noteKnownMisses: func(allowed, served int, manifestPath string) error {
			sum := fileSHA256(manifestPath)
			if sum == "" {
				return fmt.Errorf("ghostctl: read the known-miss allowlist %s", manifestPath)
			}
			// A re-check of seeded cases freezes nothing, so the worker served nothing: keep the count of the freeze that
			// produced this baseline when the allowlist is the same file.
			return rt.Baseline.NoteKnownMisses(allowed, keepServed(prev, sum, served), sum)
		},
	})
}

// setupSteps are the effects of the first-time setup, so its order and its failures are testable without the stack.
type setupSteps struct {
	up, down func(context.Context, []demorun.Spec) error
	seedAll  func(context.Context) error
	// settle starts the stores once more and stops them. Neo4j is stopped by killing it, which leaves the graph written by the
	// setup in its transaction log; the first start recovers it and checkpoints, so settling first means the sealed baseline starts
	// without that recovery (about 10 to 25 seconds saved on every Start and Reset).
	settle func(context.Context) error
	// seal runs last, with every store stopped.
	seal func(context.Context) error
	// knownMisses reads the allowlisted extraction misses the replay worker allowed and served (before it stops);
	// noteKnownMisses records them in the sealed baseline's manifest. Both optional.
	knownMisses     func(ctx context.Context, workerURL string) (allowed, served int, err error)
	noteKnownMisses func(allowed, served int, manifestPath string) error
}

func setupWith(ctx context.Context, rt codespace.Runtime, steps setupSteps) error {
	tools, err := rt.Boot.Prepare(ctx)
	if err != nil {
		return err
	}
	// The mined report was produced through the offline replay worker (recorded extraction cassettes), so the freeze
	// must use it too; otherwise the emails yield no claims and the history check refuses the replay.
	cassettes := demorun.FindCassettes("", rt.Cfg)
	if cassettes == "" {
		return errors.New("ghostctl: the extraction cassettes were not found (data/crmarena_extraction/cassettes under the demo home, or GHOST_DEMO_CASSETTES); the freeze cannot reproduce the mined history without them")
	}
	replay := rt.Cfg
	replay.LLMMode, replay.ReplayCassettes = "replay", cassettes
	// The prompts the mining answered with no claims (the cassette set has no recording for them) stay answered that way; every
	// other miss refuses the seal. A setup with no allowlist at all allows none.
	replay.KnownMisses = demorun.FindKnownMisses(rt.Cfg)
	var stores []demorun.Spec
	for _, s := range replay.Specs(tools) {
		if strings.HasPrefix(s.Name, demorun.SvcNeo4j) || s.Name == demorun.SvcPostgres || s.Name == demorun.SvcWorker {
			stores = append(stores, s)
		}
	}
	if err := steps.up(ctx, stores); err != nil {
		return err
	}
	workerURL := fmt.Sprintf("http://127.0.0.1:%d", replay.Ports.Worker)
	// Up reuses a service that already answers its health check. A replay worker left over from an earlier setup runs the code it
	// was started with and would not enforce this allowlist (every known miss would come back as a 502), so check before seeding.
	if steps.knownMisses != nil && replay.KnownMisses != "" {
		if allowed, _, err := steps.knownMisses(ctx, workerURL); err != nil {
			return err
		} else if allowed == 0 {
			return fmt.Errorf("ghostctl: the replay worker on %s does not enforce the known-miss allowlist %s (a worker left over from an earlier setup?); stop it and run the setup again", workerURL, replay.KnownMisses)
		}
	}
	_ = os.Setenv("GHOST_DEMO_FREEZE_WORKER_URL", workerURL)
	if err := steps.seedAll(ctx); err != nil {
		return err
	}
	var allowed, served int // read while the worker still runs; recorded in the manifest once the baseline is sealed
	if steps.knownMisses != nil {
		if allowed, served, err = steps.knownMisses(ctx, fmt.Sprintf("http://127.0.0.1:%d", replay.Ports.Worker)); err != nil {
			return err
		}
	}
	core, err := rt.Platform.CoreSpec(rt.Ops.Cases[0])
	if err != nil {
		return err
	}
	if err := steps.down(ctx, append(append([]demorun.Spec{}, stores...), core)); err != nil {
		return err
	}
	if err := steps.settle(ctx); err != nil {
		return err
	}
	if err := steps.seal(ctx); err != nil {
		return err
	}
	if steps.noteKnownMisses != nil {
		return steps.noteKnownMisses(allowed, served, replay.KnownMisses)
	}
	return nil
}

// keepServed is the served count to record: this run's, unless it served nothing (a re-check of seeded cases freezes nothing) and the
// previous baseline was built under the same allowlist file, in which case the freeze's own count stays.
func keepServed(prev *codespace.KnownMissNote, manifestSHA256 string, served int) int {
	if prev != nil && served == 0 && prev.ManifestSHA256 == manifestSHA256 {
		return prev.Served
	}
	return served
}

// replayKnownMisses reads the replay worker's counters: the size of its known-miss allowlist and how many allowlisted prompts it served.
func replayKnownMisses(ctx context.Context, workerURL string) (allowed, served int, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, workerURL+"/replay-stats", nil)
	if err != nil {
		return 0, 0, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, 0, fmt.Errorf("ghostctl: read the replay worker's counters: %w", err)
	}
	defer resp.Body.Close()
	var st struct {
		Served  int `json:"known_miss_hits"`
		Allowed int `json:"known_misses"`
	}
	if resp.StatusCode != http.StatusOK || json.NewDecoder(resp.Body).Decode(&st) != nil {
		return 0, 0, errors.New("ghostctl: the worker is not the replay worker (no /replay-stats)")
	}
	return st.Allowed, st.Served, nil
}

// codespaceServe runs the control service until the process is told to stop.
func codespaceServe(ctx context.Context, rt codespace.Runtime, out io.Writer) error {
	srv := rt.Server()
	done := make(chan struct{})
	go func() {
		defer close(done)
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdown)
	}()
	fmt.Fprintf(out, "control service listening on %s\n", srv.Addr)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("ghostctl: control service: %w", err)
	}
	<-done
	return nil
}

// migrateDatabase applies the schema to a fresh case database (the Postgres host migrates only its own).
func migrateDatabase(ctx context.Context, dsn string) error {
	db, err := store.Open(ctx, dsn)
	if err != nil {
		return err
	}
	defer db.Close()
	m, err := store.NewMigrator(db)
	if err != nil {
		return err
	}
	return m.Up(ctx)
}

// ensureWebBuild makes the web production build when it is missing or was made from other web sources (the git tree
// id of web/ is the stamp), so a codespace that pulled new web code rebuilds once and a restart does not.
func ensureWebBuild(ctx context.Context, root, npm string, out io.Writer) error {
	web := filepath.Join(root, "web")
	current := ""
	cmd := exec.CommandContext(ctx, "git", "rev-parse", "HEAD:web")
	cmd.Dir = root
	if b, err := cmd.Output(); err == nil {
		current = strings.TrimSpace(string(b))
	}
	_, statErr := os.Stat(filepath.Join(web, ".next", "BUILD_ID"))
	prev, _ := os.ReadFile(codespace.WebBuildStampFile(root))
	if !codespace.WebBuildNeeded(statErr == nil, strings.TrimSpace(string(prev)), current) {
		fmt.Fprintln(out, "web build is current")
		return nil
	}
	fmt.Fprintln(out, "building the web app (next build) ...")
	build := exec.CommandContext(ctx, npm, "run", "build")
	build.Dir = web
	build.Env = append(os.Environ(), "NEXT_TELEMETRY_DISABLED=1")
	if b, err := build.CombinedOutput(); err != nil {
		tail := string(b)
		if len(tail) > 2000 {
			tail = tail[len(tail)-2000:]
		}
		return fmt.Errorf("ghostctl: npm run build: %w\n%s", err, tail)
	}
	return os.WriteFile(codespace.WebBuildStampFile(root), []byte(current+"\n"), 0o644)
}

// useDemoHome points the runner at GHOST_DEMO_HOME (default D:\ghost-demo on Windows) and keeps the pinned Neo4j
// tarball under it, before the runner reads its environment: everything the demo writes lives outside the repository.
func useDemoHome() {
	env := map[string]string{"GHOST_DEMO_HOME": os.Getenv("GHOST_DEMO_HOME"), "GHOST_DEMO_DIR": os.Getenv("GHOST_DEMO_DIR")}
	home := codespace.DemoHome(env, runtime.GOOS)
	if home == "" {
		return
	}
	_ = os.Setenv("GHOST_DEMO_DIR", home)
	for name, rel := range map[string]string{"GHOST_DEMO_DATA": "crmarena_b2b", "GHOST_DEMO_CASSETTES": filepath.Join("crmarena_extraction", "cassettes")} {
		if os.Getenv(name) == "" {
			_ = os.Setenv(name, filepath.Join(home, "data", rel))
		}
	}
	if os.Getenv("GHOST_NEO4J_CACHE") == "" {
		_ = os.Setenv("GHOST_NEO4J_CACHE", codespace.NeoCacheDir(home))
	}
}
