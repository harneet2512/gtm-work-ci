package codespace

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"path/filepath"
	"strconv"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/demorun"
)

// ControlTokenEnv carries the control service's token. It is made for each boot (never stored), so it is not the long-lived
// core API token: only the web server of this boot, which is given it, can call the hidden handoff.
const ControlTokenEnv = "GHOST_DEMO_CONTROL_TOKEN"

// minControlToken is the shortest token a serve process accepts from its environment (32 random bytes in hex are 64).
const minControlToken = 32

func newControlToken() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic("codespace: no randomness for the control token: " + err.Error())
	}
	return hex.EncodeToString(b)
}

// DefaultControlPort is where the loopback control service listens (GHOST_DEMO_PORT_CONTROL overrides).
const DefaultControlPort = 8099

// Options are the codespace-specific inputs of Assemble.
type Options struct {
	// ResolveSlack resolves SLACK_BOT_TOKEN and SLACK_APP_TOKEN (demorun.ResolveSlackTokens in production). A failure
	// does not stop the demo: the Slack bot is left out and the status names the missing secrets.
	ResolveSlack func(process, dotEnv demorun.Env) (demorun.Env, error)
	// BuildWeb makes the web production build when it is missing or stale; it receives npm's path.
	BuildWeb func(ctx context.Context, npm string) error
	// Data and Report are the seed inputs (snapshot directory, demo-cases report); empty means the runner's defaults.
	Data, Report string
	// Seed freezes one case with the runner's seed flow (wired to demorun.Flow.Seed by cmd/ghostctl).
	Seed    SeedFunc
	Migrate func(ctx context.Context, dsn string) error
}

// Runtime is a codespace wired to the real collaborators of a demorun.Flow.
type Runtime struct {
	Cfg      demorun.Config
	Ops      Ops
	Platform *RealPlatform
	// Baseline is the sealed Event N-1 state Start and Reset restore (built once by the setup, never by the demo path).
	Baseline Baseline
	Status   *StatusSource
	Control  *Control
	Boot     Boot
	// Client is core's HTTP client (the demo API token): the record run's scripted human uses it.
	Client demorun.CoreClient
	Seeder Seeder
	Port   int
	// ControlToken is this boot's token for the control service (ControlTokenEnv).
	ControlToken string
}

// ControlURL is where the web server finds the control service.
func (r Runtime) ControlURL() string { return fmt.Sprintf("http://127.0.0.1:%d", r.Port) }

// Assemble wires the codespace operations over a runner Flow: the web app is served from its production build and
// told where the control service is, the Slack bot is skipped when its tokens are not present, and every operation
// shares one supervisor, one core and one set of tools.
func Assemble(f demorun.Flow, o Options) Runtime {
	cfg := f.Cfg
	port := controlPort(cfg)
	rt := Runtime{Port: port}
	cfg.WebProd = true
	cfg.GraphInstances = len(CasesFromEnv(cfg.Process)) // one Neo4j per case: a later case's graph is built before it is needed
	cfg.LLMMode = "cache"                               // every model call is made once, stored, and replayed; GHOST_DEMO_LLM_MODE overrides (live, replay, record)
	if m := cfg.Process["GHOST_DEMO_LLM_MODE"]; m != "" {
		cfg.LLMMode = m
	}
	rt.ControlToken = cfg.Process[ControlTokenEnv] // the serve process is started with its boot's token
	if len(rt.ControlToken) < minControlToken {
		rt.ControlToken = newControlToken()
	}
	cfg.WebExtraEnv = demorun.Env{"GHOST_DEMO_CONTROL_URL": rt.ControlURL(), ControlTokenEnv: rt.ControlToken}
	if cfg.Merged("GHOST_MODEL") == "" { // the demo's runtime model unless .env or the environment names one
		cfg.Process = demorun.Merge(cfg.Process, demorun.Env{"GHOST_MODEL": DefaultModel})
	}
	if slackForcedOff(cfg) { // the record run: nothing may be posted to the demo channel
		cfg.NoSlack = true
	} else if tokens, err := o.ResolveSlack(cfg.Process, cfg.DotEnv); err == nil {
		cfg.Slack = tokens
	} else {
		cfg.NoSlack = true
	}
	f.Cfg = cfg
	rt.Cfg = cfg
	if lc, ok := f.Core.(*demorun.LiveCore); ok {
		rt.Client = lc.CoreClient
	}

	rt.Platform = &RealPlatform{Base: cfg, Services: f.Sup, Core: f.Core, RunTool: f.RunTool, ApplyToolEnv: f.ApplyToolEnv}
	rt.Baseline = NewBaseline(cfg, CasesFromEnv(cfg.Process))
	rt.Ops = Ops{Cases: CasesFromEnv(cfg.Process), Paths: Paths{Layout: cfg.Layout}, Admin: PGAdmin{DSN: cfg.DSN()}, P: rt.Platform, Base: rt.Baseline,
		Carry: KnowledgeCarrier{RulesPath: filepath.Join(cfg.Layout.Root, "contracts", "knowledge", "lifecycle.v1.json"),
			DSNFor: func(c Case) (string, error) { return DSNFor(cfg.DSN(), c.Database) }}.Carry,
		Log: func(format string, args ...any) {
			if f.Out != nil {
				fmt.Fprintf(f.Out, format+"\n", args...)
			}
		}}
	if !cfg.NoSlack {
		rt.Ops.Slack = Janitor{API: NewSlackChannelAPI(cfg.Slack["SLACK_BOT_TOKEN"], ""), Channel: cfg.Merged("SLACK_CHANNEL_ID")}
	}
	prepare := func(ctx context.Context) (demorun.Tooling, error) {
		t, err := f.Prepare(ctx, cfg.NoSlack, false)
		if err != nil {
			return t, err
		}
		if o.BuildWeb != nil {
			if err := o.BuildWeb(ctx, t.Npm); err != nil {
				return t, err
			}
		}
		return t, nil
	}
	rt.Boot = Boot{Cfg: cfg, Sup: f.Sup, Ops: rt.Ops, Mode: cfg.Merged(StartModeEnv), Prepare: prepare, Up: demorun.Up, Out: f.Out,
		ControlSpec: func(t demorun.Tooling) demorun.Spec { return rt.controlSpec(t) }}
	rt.Seeder = Seeder{Ops: rt.Ops, BaseDSN: cfg.DSN(), Migrate: o.Migrate, Seed: o.Seed,
		Options: demorun.SeedOptions{Data: o.Data, Report: o.Report}}
	rt.Status = rt.statusSource(f)
	rt.Control = &Control{Ops: rt.Ops, Status: rt.Status, Token: rt.ControlToken}
	rt.Status.Job = rt.Control.Job
	return rt
}

// SlackOffEnv switches the Slack bot off on purpose (the record run sets it): no token is read and no message is posted.
const SlackOffEnv = "GHOST_DEMO_NO_SLACK"

func slackForcedOff(cfg demorun.Config) bool { return cfg.Process[SlackOffEnv] == "1" }

func controlPort(cfg demorun.Config) int {
	if n, err := strconv.Atoi(cfg.Process["GHOST_DEMO_PORT_CONTROL"]); err == nil && n >= 1 && n <= 65535 {
		return n
	}
	return DefaultControlPort
}

// controlSpec is the supervised `ghostctl codespace serve` process: the host copy of ghostctl that survives rebuilds.
func (r Runtime) controlSpec(t demorun.Tooling) demorun.Spec {
	return demorun.Spec{Name: "control", Dir: r.Cfg.Layout.Root, Path: t.Host, Args: []string{"codespace", "serve"}, Env: demorun.Env{ControlTokenEnv: r.ControlToken},
		Port: r.Port, Health: demorun.HTTPCheck(r.ControlURL()+"/healthz", nil), URL: r.ControlURL(),
		Wait: demorun.WaitOptions{Timeout: time.Minute, Interval: 500 * time.Millisecond}}
}

// StopSpecs are every supervised service including the control service, in start order (demorun.Down stops them in
// reverse), for the Stop shortcut.
func (r Runtime) StopSpecs() []demorun.Spec {
	return append(r.Cfg.Specs(demorun.Tooling{}), r.controlSpec(demorun.Tooling{}))
}

// requiredSecrets are the Codespaces secrets the demo cannot run without. The model key is only needed in live mode.
func requiredSecrets(cfg demorun.Config) []string {
	names := []string{"SLACK_BOT_TOKEN", "SLACK_APP_TOKEN", "SLACK_CHANNEL_ID"}
	if slackForcedOff(cfg) {
		names = nil
	}
	if cfg.LLMMode == "live" || cfg.LLMMode == "cache" { // cache records on a miss, which needs the key
		names = append([]string{"OPENROUTER_API_KEY"}, names...)
	}
	return names
}

func (r Runtime) statusSource(f demorun.Flow) *StatusSource {
	cfg := r.Cfg
	var required []string
	for i := range r.Ops.Cases {
		required = append(required, cfg.GraphSlot(i).Service)
	}
	required = append(required, demorun.SvcPostgres, demorun.SvcWorker, demorun.SvcCore, demorun.SvcWeb)
	if !cfg.NoSlack {
		required = append(required, demorun.SvcSlackbot)
	}
	specs := cfg.Specs(demorun.Tooling{})
	return &StatusSource{
		Ops:      r.Ops,
		Rows:     func(ctx context.Context) []demorun.StatusRow { return f.Sup.Status(ctx, specs) },
		Required: required,
		Secrets:  requiredSecrets(cfg),
		Present:  func(name string) bool { return cfg.Merged(name) != "" || cfg.Slack[name] != "" },
		Invisibility: func(ctx context.Context, manifestID string) (string, error) {
			c, cancel := context.WithTimeout(ctx, 8*time.Second)
			defer cancel()
			inv, err := f.Core.Invisibility(c, manifestID)
			return inv.Status, err
		},
		BootError: r.Ops.Paths.ReadBootError,
		LLM:       func() *LLMStatus { return ReadLLMStatus(cfg, time.Now()) },
	}
}

// ListenAddress is the loopback address the control service binds. It is never exposed beyond the machine.
func (r Runtime) ListenAddress() string { return fmt.Sprintf("127.0.0.1:%d", r.Port) }

// Server is the control service's HTTP server (no timeouts that would cut a long Reset: the handlers answer at once
// and the work runs in the background).
func (r Runtime) Server() *http.Server {
	return &http.Server{Addr: r.ListenAddress(), Handler: r.Control.Handler(),
		ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: jobTimeout + time.Minute}
}

// WebBuildNeeded says whether the production build must be (re)made: it is missing, or was made from a different
// version of the web sources than the checkout holds now (current is the git tree id of web/, "" when unknown, in
// which case an existing build is trusted).
func WebBuildNeeded(buildExists bool, stamp, current string) bool {
	if !buildExists {
		return true
	}
	return current != "" && stamp != current
}

// WebBuildStampFile is where the sources version of the last build is remembered.
func WebBuildStampFile(root string) string {
	return filepath.Join(root, "web", ".next", ".ghost-build-stamp")
}
