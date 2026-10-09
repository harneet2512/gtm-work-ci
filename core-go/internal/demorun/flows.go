package demorun

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/store"
)

// Stores is the demo's access to Postgres: the two reads the commands need. PGStores is the real one.
type Stores interface {
	// RequireEmpty fails unless the database holds no source events, accounts or runs (what the freeze requires).
	RequireEmpty(ctx context.Context) error
	// Facts reads the authoritative rows `demo verify` judges.
	Facts(ctx context.Context, st DemoState, o FetchOptions) (Facts, error)
}

// PGStores implements Stores over the demo Postgres, opening a connection per call.
type PGStores struct{ DSN string }

// RequireEmpty implements Stores.
func (p PGStores) RequireEmpty(ctx context.Context) error {
	db, err := store.Open(ctx, p.DSN)
	if err != nil {
		return err
	}
	defer db.Close()
	var n int
	if err := db.QueryRowContext(ctx, `SELECT (SELECT count(*) FROM source_events) + (SELECT count(*) FROM accounts) + (SELECT count(*) FROM agent_runs)`).Scan(&n); err != nil {
		return fmt.Errorf("check that the demo database is empty: %w", err)
	}
	if n != 0 {
		return errors.New("the demo database is not empty but has no seed state; run `demo reset --yes`, `demo up`, then `demo seed`")
	}
	return nil
}

// Facts implements Stores.
func (p PGStores) Facts(ctx context.Context, st DemoState, o FetchOptions) (Facts, error) {
	db, err := store.Open(ctx, p.DSN)
	if err != nil {
		return Facts{}, fmt.Errorf("open the demo database (is `demo up` running?): %w", err)
	}
	defer db.Close()
	return FetchFacts(ctx, db, st, o)
}

// Flow is the body of the `demo up|seed|play|verify` commands with every outside effect injected, so each is
// unit-tested without starting a system. cmd/ghostctl wires the real ones.
type Flow struct {
	Cfg     Config
	Sup     Supervisor
	Out     io.Writer
	EnvFile string // the .env that was loaded ("" when none), for the first line of `up`

	Core   PlayCore
	Stores Stores
	// CoreSpec overrides the spec used to stop and restart core around the freeze (tests); zero derives it.
	CoreSpec Spec
	// Prepare builds what `up` runs (Prepare in production).
	Prepare func(ctx context.Context, noSlack, noWeb bool) (Tooling, error)
	// RunTool runs a ghostctl command in process (freeze-demo-manifest, graph rebuild).
	RunTool func(args []string) error
	// ApplyToolEnv puts the demo stores into the environment RunTool reads.
	ApplyToolEnv func(Env) error
	// ResolveSlack resolves the Slack tokens (ResolveSlackTokens in production).
	ResolveSlack func(processEnv, dotEnv Env) (Env, error)
	PageExists   func(ctx context.Context, url string) bool
	Now          func() time.Time
	Heartbeat    time.Duration // how often a long step prints a progress line (default 30 s)

	// ManifestFile and StateFile, when set, replace the layout's manifest.json and state.json: the codespace demo
	// keeps one pair per case (each case lives in its own database).
	ManifestFile string
	StateFile    string
	// AfterGraph runs after the freeze and the graph rebuild, while core is still down, before core restarts: the
	// codespace demo copies the frozen database to a template here (a copy needs no open sessions).
	AfterGraph func(ctx context.Context) error
	// FreezeWorkerURL, when set, is passed to the freeze as --worker-url: the offline replay worker that serves the
	// recorded extraction cassettes. The mined report was produced through it, so a freeze without it extracts no
	// claims from the emails and fails the report's history check ("history event N differs").
	FreezeWorkerURL string
}

func (f Flow) statePath() string {
	if f.StateFile != "" {
		return f.StateFile
	}
	return f.Cfg.Layout.StateFile()
}

func (f Flow) manifestPath() string {
	if f.ManifestFile != "" {
		return f.ManifestFile
	}
	return f.Cfg.Layout.ManifestFile()
}

func (f Flow) say(format string, args ...any) {
	if f.Out != nil {
		fmt.Fprintf(f.Out, format+"\n", args...)
	}
}

func (f Flow) now() time.Time {
	if f.Now != nil {
		return f.Now()
	}
	return time.Now()
}

func (f Flow) coreSpec() Spec {
	if f.CoreSpec.Name != "" {
		return f.CoreSpec
	}
	for _, s := range f.Cfg.Specs(Tooling{Core: filepath.Join(f.Cfg.Layout.BinDir(), "core"+exeSuffix())}) {
		if s.Name == SvcCore {
			return s
		}
	}
	return Spec{}
}

// AllSpecs lists every service (for status, down and reset, which need names and checks but not executables).
func (f Flow) AllSpecs() []Spec { return f.Cfg.Specs(Tooling{}) }

func (f Flow) running(service string) bool {
	st, _ := f.Sup.PIDs.State(service)
	return st == StateRunning
}

// UpOptions are the flags of `demo up`.
type UpOptions struct {
	Mode      string // live | replay | record
	NoSlack   bool
	NoWeb     bool
	Cassettes string
}

// Up validates, prepares and starts the stack.
func (f Flow) Up(ctx context.Context, o UpOptions) error {
	if o.Mode != "live" && o.Mode != "replay" && o.Mode != "record" {
		return fmt.Errorf("--llm-mode must be live, replay or record, got %q", o.Mode)
	}
	cfg := f.Cfg
	cfg.LLMMode, cfg.NoSlack, cfg.NoWeb = o.Mode, o.NoSlack, o.NoWeb
	if o.Mode == "replay" {
		cfg.ReplayCassettes = FindCassettes(o.Cassettes, cfg)
		if cfg.ReplayCassettes != "" {
			f.say("replay mode: serving recorded extraction cassettes from %s (no model is called; calls with no cassette fail)", cfg.ReplayCassettes)
		} else {
			f.say("replay mode: no extraction cassette directory found, so the stock worker replays worker-py/cassettes (only the hand-written fixture prompts hit)")
		}
	}
	if f.EnvFile != "" {
		f.say("environment: %s (values are never printed)", f.EnvFile)
	} else {
		f.say("environment: no .env found; using the process environment only")
	}
	if !o.NoSlack {
		if cfg.Merged("SLACK_CHANNEL_ID") == "" {
			return errors.New("SLACK_CHANNEL_ID (the #gtm-ai-demo channel id, C...) is not set in .env or the environment; set it or run `demo up --no-slack`")
		}
		resolve := f.ResolveSlack
		if resolve == nil {
			resolve = func(p, d Env) (Env, error) { return ResolveSlackTokens(p, d) }
		}
		tokens, err := resolve(cfg.Process, cfg.DotEnv)
		if err != nil {
			return err
		}
		cfg.Slack = tokens
		if cfg.Merged("SLACK_ALLOWED_USER_IDS") == "" {
			f.say("WARNING: SLACK_ALLOWED_USER_IDS is not set, so any member of the channel can press Choose/Edit/Send (SLACK_ALLOW_ALL_USERS=1 for this demo). Set it in .env to restrict.")
		}
	}
	if o.Mode == "live" && cfg.Merged("OPENROUTER_API_KEY") == "" {
		return errors.New("--llm-mode live needs OPENROUTER_API_KEY in .env or the environment (or run --llm-mode replay)")
	}
	tools, err := f.Prepare(ctx, o.NoSlack, o.NoWeb)
	if err != nil {
		return err
	}
	specs := cfg.Specs(tools)
	if err := Up(ctx, f.Sup, specs); err != nil {
		f.say("demo up stopped at the failed service above; earlier services are still running. Fix the cause and run `demo up` again, or `demo down`.")
		return err
	}
	f.say("")
	WriteStatus(f.Out, f.Sup.Status(ctx, specs))
	f.say("\nThe demo stack is up (worker mode: %s). Next: `demo seed`, then `demo play`.", o.Mode)
	return nil
}

// FindCassettes resolves the extraction cassette directory for replay mode: the flag, GHOST_DEMO_CASSETTES, then
// data/crmarena_extraction/cassettes of this checkout or the main one. "" when none exists.
func FindCassettes(flagValue string, cfg Config) string {
	root := cfg.Layout.Root
	for _, c := range []string{flagValue, cfg.Process["GHOST_DEMO_CASSETTES"],
		filepath.Join(root, "data", "crmarena_extraction", "cassettes"),
		filepath.Join(MainCheckout(root), "data", "crmarena_extraction", "cassettes")} {
		if c == "" {
			continue
		}
		if info, err := os.Stat(c); err == nil && info.IsDir() {
			return c
		}
	}
	return ""
}

// KnownMissesFile is the checked-in allowlist, relative to the repository root. Changing what the setup may answer with no claims means
// editing this file in a reviewed change: no environment variable, flag or diagnostic folder can widen it.
var KnownMissesFile = filepath.Join("bench", "data", "known_extraction_misses.json")

// FindKnownMisses resolves the known-miss allowlist the replay worker enforces (HAR-124): only the checked-in manifest of this checkout
// (or of the main one). The miss dumps beside the cassettes (--dump-misses) are diagnostic output and are never read as an allowlist.
// "" when the file does not exist, in which case every miss is an error.
func FindKnownMisses(cfg Config) string {
	root := cfg.Layout.Root
	for _, c := range []string{filepath.Join(root, KnownMissesFile), filepath.Join(MainCheckout(root), KnownMissesFile)} {
		if info, err := os.Stat(c); err == nil && !info.IsDir() {
			return c
		}
	}
	return ""
}

// withHeartbeat runs fn and prints a line every Heartbeat so a long replay does not look hung.
func (f Flow) withHeartbeat(ctx context.Context, what string, fn func() error) error {
	every := f.Heartbeat
	if every <= 0 {
		every = 30 * time.Second
	}
	done, finished := make(chan struct{}), make(chan struct{})
	start := time.Now()
	go func() {
		defer close(finished)
		t := time.NewTicker(every)
		defer t.Stop()
		for {
			select {
			case <-done:
				return
			case <-ctx.Done():
				return
			case <-t.C:
				f.say("  ... %s (%s elapsed)", what, time.Since(start).Round(time.Second))
			}
		}
	}()
	// The heartbeat goroutine must be gone before this returns: it writes to Out, which the caller reads next.
	defer func() { close(done); <-finished }()
	return fn()
}

// PlayCmdOptions are the flags of `demo play`.
type PlayCmdOptions struct {
	NoWait  bool
	Timeout time.Duration
}

// Play releases Event N, waits for what it sets off, then prints the web URLs and the click checklist.
func (f Flow) Play(ctx context.Context, o PlayCmdOptions) error {
	st, ok, err := LoadState(f.statePath())
	if err != nil {
		return err
	}
	if !ok {
		return errors.New("nothing is seeded yet; run `demo seed` first")
	}
	slackOn := f.running(SvcSlackbot)
	st.SlackWasOn = slackOn
	opts := PlayOptions{Timeout: o.Timeout, NoWait: o.NoWait, SlackExpected: slackOn}
	st, res, playErr := RunPlay(ctx, f.Core, st, opts, func(line string) { f.say("%s", line) })
	if saveErr := SaveState(f.statePath(), st); saveErr != nil && playErr == nil {
		playErr = saveErr
	}
	if playErr != nil {
		return playErr
	}
	exists := f.PageExists
	if exists == nil {
		exists = PageExists
	}
	f.say("\nOPEN IN THE BROWSER")
	for _, line := range WebURLs(ctx, f.Cfg.WebURL(), st.AccountID, st.ManifestID, res.RunID, exists) {
		f.say("%s", line)
	}
	f.say("")
	f.say("%s", Checklist(f.Cfg.Merged("SLACK_CHANNEL_ID")))
	return nil
}

// Verify reads the authoritative rows back and prints PASS/FAIL/SKIP per step; it fails when any step FAILs.
func (f Flow) Verify(ctx context.Context) error {
	st, ok, err := LoadState(f.statePath())
	if err != nil {
		return err
	}
	if !ok {
		return errors.New("nothing is seeded yet; run `demo seed` first")
	}
	slackOn := f.running(SvcSlackbot) || st.SlackWasOn
	facts, err := f.Stores.Facts(ctx, st, FetchOptions{SlackOn: slackOn, AuditPath: filepath.Join(f.Cfg.Layout.LogDir(), "slack-audit.jsonl")})
	if err != nil {
		return err
	}
	if inv, err := f.Core.Invisibility(ctx, st.ManifestID); err == nil {
		facts.InvisibilityNow = inv.Status
	}
	f.say("verifying %s (manifest %s, account %s)\n", st.CaseName, st.ManifestID, st.AccountID)
	if failed := WriteVerify(f.Out, Evaluate(facts)); failed > 0 {
		return fmt.Errorf("verify: %d step(s) FAILED", failed)
	}
	return nil
}
