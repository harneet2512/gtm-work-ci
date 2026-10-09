package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/demorun"
)

const demoUsage = "usage: ghostctl demo <up [--llm-mode live|replay] [--cassettes DIR] [--no-slack] [--no-web] | seed [--case NAME] [--data DIR] [--report FILE] [--opportunity ID] | " +
	"play [--no-wait] | verify | status | logs <service> [-n N] | down | reset --yes>"

// demoCases maps the friendly names of --case to opportunity ids from the mined report.
var demoCases = map[string]string{
	"medtech": demorun.MedTechOpportunity,
	"pioneer": "006Wt000007BF69IAG",
}

// The demo commands register in init: `demo seed` calls run (freeze, graph rebuild) in process, and run reads the
// commands map, so putting them in the map's initializer would be an initialization cycle.
func init() {
	commands["demo"] = runDemo
	commands["demo-host"] = runDemoHost
}

// runDemo is the one-command live demo runner (HAR-137 live smoke, HAR-129): the flags are parsed here, the logic
// lives in internal/demorun (see docs/demo-runner.md).
func runDemo(args []string, out io.Writer) error {
	if len(args) == 0 {
		return errors.New(demoUsage)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	switch args[0] {
	case "up":
		return demoUp(ctx, args[1:], out)
	case "seed":
		return demoSeed(ctx, args[1:], out)
	case "play":
		return demoPlay(ctx, args[1:], out)
	case "verify":
		return demoVerify(ctx, args[1:], out)
	case "down":
		return demoDown(ctx, out)
	case "status":
		return demoStatus(ctx, out)
	case "logs":
		return demoLogs(args[1:], out)
	case "reset":
		return demoReset(ctx, args[1:], out)
	}
	return errors.New(demoUsage)
}

// runDemoHost runs one store (postgres or neo4j) in this process until it is told to stop: the body of the
// detached host processes `demo up` starts.
func runDemoHost(args []string, out io.Writer) error {
	if len(args) != 1 {
		return errors.New("usage: ghostctl demo-host <postgres|neo4j>")
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return demorun.RunHost(ctx, args[0], os.Getenv, out)
}

// loadFlow resolves the repository, its .env and the generated secrets and wires the real collaborators into a
// demorun.Flow. The returned closer releases the database `play` may have opened.
func loadFlow(out io.Writer) (demorun.Flow, func(), error) {
	wd, err := os.Getwd()
	if err != nil {
		return demorun.Flow{}, nil, err
	}
	root, err := demorun.FindRoot(wd)
	if err != nil {
		return demorun.Flow{}, nil, err
	}
	layout := demorun.NewLayout(root)
	layout.StateDir = os.Getenv("GHOST_DEMO_DIR")
	dotEnv, envFile, err := loadDotEnv(root)
	if err != nil {
		return demorun.Flow{}, nil, err
	}
	secrets, err := demorun.EnsureSecrets(layout.SecretsFile())
	if err != nil {
		return demorun.Flow{}, nil, err
	}
	process := demorun.ProcessEnv()
	cfg := demorun.Config{Layout: layout, Ports: demorun.DefaultPorts(demorun.Merge(dotEnv, process)),
		DotEnv: dotEnv, Process: process, Secrets: secrets, LLMMode: "live"}
	sup := demorun.Supervisor{Layout: layout, PIDs: layout.PIDs(), Out: out}
	core := &demorun.LiveCore{CoreClient: demorun.CoreClient{Base: cfg.CoreURL(), Token: secrets["GHOST_API_TOKEN"]}, DSN: cfg.DSN()}
	return demorun.Flow{
		Cfg: cfg, Sup: sup, Out: out, EnvFile: envFile, Core: core, Stores: demorun.PGStores{DSN: cfg.DSN()},
		Prepare: func(ctx context.Context, noSlack, noWeb bool) (demorun.Tooling, error) {
			return demorun.Prepare(ctx, layout, sup, demorun.PrepareOptions{NoSlack: noSlack, NoWeb: noWeb, SkipBuild: demorun.SkipBuildEnabled(os.Getenv), Out: out})
		},
		RunTool: func(args []string) error { return run(args, out) },
		ApplyToolEnv: func(e demorun.Env) error {
			for k, v := range e {
				if err := os.Setenv(k, v); err != nil {
					return err
				}
			}
			return nil
		},
	}, core.Close, nil
}

// loadDotEnv reads GHOST_ENV_FILE, else <root>/.env, else the main checkout's .env (a worktree has none).
func loadDotEnv(root string) (demorun.Env, string, error) {
	candidates := []string{os.Getenv("GHOST_ENV_FILE"), filepath.Join(root, ".env"), filepath.Join(demorun.MainCheckout(root), ".env")}
	for _, p := range candidates {
		if p == "" {
			continue
		}
		env, found, err := demorun.LoadEnvFile(p)
		if err != nil {
			return nil, "", err
		}
		if found {
			return env, p, nil
		}
	}
	return demorun.Env{}, "", nil
}

func exeExt() string {
	if os.PathSeparator == '\\' {
		return ".exe"
	}
	return ""
}

func demoUp(ctx context.Context, args []string, out io.Writer) error {
	fs := flag.NewFlagSet("demo up", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	o := demorun.UpOptions{}
	fs.StringVar(&o.Mode, "llm-mode", "live", "worker LLM mode: live (OpenRouter), replay (cassettes, no network) or record")
	fs.BoolVar(&o.NoSlack, "no-slack", false, "do not start the Slack bot (no Slack token is read)")
	fs.BoolVar(&o.NoWeb, "no-web", false, "do not start the web app")
	fs.StringVar(&o.Cassettes, "cassettes", "", "with --llm-mode replay: directory of recorded CRMArena extraction cassettes (default GHOST_DEMO_CASSETTES, else data/crmarena_extraction/cassettes of this or the main checkout)")
	if err := fs.Parse(args); err != nil || fs.NArg() != 0 {
		return errors.New(demoUsage)
	}
	if o.Mode != "live" && o.Mode != "replay" && o.Mode != "record" { // before anything is created on disk
		return fmt.Errorf("ghostctl: --llm-mode must be live, replay or record, got %q", o.Mode)
	}
	f, closeFlow, err := loadFlow(out)
	if err != nil {
		return err
	}
	defer closeFlow()
	return f.Up(ctx, o)
}

func demoSeed(ctx context.Context, args []string, out io.Writer) error {
	fs := flag.NewFlagSet("demo seed", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	caseName := fs.String("case", "medtech", "demo case: medtech (MedTech Advances, the main case) or pioneer (Pioneer Envisions)")
	opp := fs.String("opportunity", "", "Salesforce opportunity id (overrides --case)")
	data := fs.String("data", "", "CRMArena snapshot directory (default: GHOST_DEMO_DATA, then data/crmarena_b2b of this or the main checkout)")
	report := fs.String("report", "", "demo-cases report (default: the newest bench/reports/demo-cases-*.json)")
	if err := fs.Parse(args); err != nil || fs.NArg() != 0 {
		return errors.New(demoUsage)
	}
	oppID := *opp
	if oppID == "" {
		id, ok := demoCases[*caseName]
		if !ok {
			return fmt.Errorf("ghostctl: unknown --case %q (medtech or pioneer)", *caseName)
		}
		oppID = id
	}
	f, closeFlow, err := loadFlow(out)
	if err != nil {
		return err
	}
	defer closeFlow()
	return f.Seed(ctx, demorun.SeedOptions{Opportunity: oppID, Data: *data, Report: *report})
}

func demoPlay(ctx context.Context, args []string, out io.Writer) error {
	fs := flag.NewFlagSet("demo play", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	noWait := fs.Bool("no-wait", false, "release Event N and return after the BI update")
	timeout := fs.Duration("timeout", 20*time.Minute, "how long to wait for the strategy run and Slack messages")
	if err := fs.Parse(args); err != nil || fs.NArg() != 0 {
		return errors.New(demoUsage)
	}
	f, closeFlow, err := loadFlow(out)
	if err != nil {
		return err
	}
	defer closeFlow()
	return f.Play(ctx, demorun.PlayCmdOptions{NoWait: *noWait, Timeout: *timeout})
}

func demoVerify(ctx context.Context, args []string, out io.Writer) error {
	if len(args) != 0 {
		return errors.New(demoUsage)
	}
	f, closeFlow, err := loadFlow(out)
	if err != nil {
		return err
	}
	defer closeFlow()
	return f.Verify(ctx)
}

func demoDown(ctx context.Context, out io.Writer) error {
	f, closeFlow, err := loadFlow(out)
	if err != nil {
		return err
	}
	defer closeFlow()
	if err := demorun.Down(ctx, f.Sup, f.AllSpecs()); err != nil {
		return err
	}
	fmt.Fprintln(out, "demo stack stopped (data kept in .demo; `demo reset --yes` wipes it)")
	return nil
}

func demoStatus(ctx context.Context, out io.Writer) error {
	f, closeFlow, err := loadFlow(io.Discard)
	if err != nil {
		return err
	}
	defer closeFlow()
	demorun.WriteStatus(out, f.Sup.Status(ctx, f.AllSpecs()))
	return nil
}

func demoLogs(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("demo logs", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	n := fs.Int("n", 60, "number of lines")
	// `demo logs core -n 20` and `demo logs -n 20 core` both work.
	var svc string
	rest := args
	if len(rest) > 0 && len(rest[0]) > 0 && rest[0][0] != '-' {
		svc, rest = rest[0], rest[1:]
	}
	if err := fs.Parse(rest); err != nil {
		return errors.New(demoUsage)
	}
	if svc == "" && fs.NArg() == 1 {
		svc = fs.Arg(0)
	}
	if svc == "" || (fs.NArg() > 1) {
		return errors.New("usage: ghostctl demo logs <neo4j|postgres|worker|core|slackbot|web> [-n N]")
	}
	f, closeFlow, err := loadFlow(io.Discard)
	if err != nil {
		return err
	}
	defer closeFlow()
	return f.Sup.Logs(svc, *n, out)
}

func demoReset(ctx context.Context, args []string, out io.Writer) error {
	fs := flag.NewFlagSet("demo reset", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	yes := fs.Bool("yes", false, "confirm: delete the demo database, graph, manifest and logs")
	if err := fs.Parse(args); err != nil || fs.NArg() != 0 {
		return errors.New(demoUsage)
	}
	f, closeFlow, err := loadFlow(out)
	if err != nil {
		return err
	}
	defer closeFlow()
	if err := demorun.Reset(ctx, f.Sup, f.AllSpecs(), *yes); err != nil {
		return err
	}
	fmt.Fprintln(out, "demo data wiped. `demo up` then `demo seed` starts from an empty database.")
	return nil
}
