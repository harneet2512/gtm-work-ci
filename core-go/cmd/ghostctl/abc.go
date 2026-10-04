package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/abcrun"
	"github.com/harneet2512/gtm-work/core-go/internal/clock"
	"github.com/harneet2512/gtm-work/core-go/internal/knowledge"
	"github.com/harneet2512/gtm-work/core-go/internal/orchestrator"
	"github.com/harneet2512/gtm-work/core-go/internal/runtoken"
	"github.com/harneet2512/gtm-work/core-go/internal/store/embedded"
	"github.com/harneet2512/gtm-work/core-go/internal/workerclient"
)

const (
	abcPackUsage = "usage: ghostctl abc-pack --export <CRMArena export dir> --synthetic <synthetic layer dir> --specs <situations.json> --out <pack.json>"
	abcArmsUsage = "usage: ghostctl abc-arms --pack <pack.json> --learning <learning.json> --out <arms.json> [--worker-mode replay|record] " +
		"[--cassettes <dir>] [--env-file <.env>] [--model <GHOST_MODEL>] [--worker-app <module:factory>] [--only id,id] [--pulls N] [--repo <root>]"
	abcTimeout = 12 * time.Hour
)

// runABCPack builds the frozen world pack of the A/B/C uplift experiment from the git-ignored data (HAR-128 WP30).
func runABCPack(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("abc-pack", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	export := fs.String("export", "", "CRMArena-Pro export directory")
	synthetic := fs.String("synthetic", "", "synthetic layer directory")
	specsPath := fs.String("specs", "", "situations chosen by bench/uplift (JSON list)")
	outPath := fs.String("out", "", "pack file to write")
	if err := fs.Parse(args); err != nil || fs.NArg() != 0 || *export == "" || *synthetic == "" || *specsPath == "" || *outPath == "" {
		return errors.New(abcPackUsage)
	}
	raw, err := os.ReadFile(*specsPath)
	if err != nil {
		return err
	}
	var specs []abcrun.Spec
	if err := json.Unmarshal(raw, &specs); err != nil {
		return fmt.Errorf("ghostctl: decode --specs: %w", err)
	}
	pack, err := abcrun.BuildPack(abcrun.BuildInput{ExportDir: *export, SyntheticDir: *synthetic, Specs: specs})
	if err != nil {
		return err
	}
	if err := writeJSONFile(*outPath, pack); err != nil {
		return err
	}
	fmt.Fprintf(out, "wrote a pack of %d situations to %s\n", len(pack.Situations), *outPath)
	return nil
}

// runABCArms runs the arms of the A/B/C uplift experiment through the real lifecycle, orchestrator and worker.
func runABCArms(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("abc-arms", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	packPath := fs.String("pack", "", "frozen world pack")
	learningPath := fs.String("learning", "", "frozen previous-deal learning evidence")
	outPath := fs.String("out", "", "arms file to write")
	mode := fs.String("worker-mode", "replay", "worker LLM mode: replay (cassettes, no network) or record (live model, writes cassettes)")
	cassettes := fs.String("cassettes", "", "cassette directory (CASSETTE_DIR)")
	envFile := fs.String("env-file", "", "dotenv file whose variables go to the worker process only (never printed)")
	workerApp := fs.String("worker-app", "", "uvicorn factory for the worker (default ghost_worker.app:create_app; a rehearsal passes a scripted stand-in)")
	model := fs.String("model", "", "GHOST_MODEL for the worker; replay needs the recorded model family because it is part of every cassette key")
	only := fs.String("only", "", "comma-separated situation ids to run (a call-budget limit)")
	pulls := fs.Int("pulls", 4, "context pulls the planner may make")
	dryRun := fs.Bool("dry-run", false, "build every world and report what each arm would read and how many generations a run makes; no model is called")
	repo := fs.String("repo", "", "repository root (default: found from the working directory)")
	if err := fs.Parse(args); err != nil || fs.NArg() != 0 || *packPath == "" || *learningPath == "" || *outPath == "" {
		return errors.New(abcArmsUsage)
	}
	root := *repo
	if root == "" {
		root = repoRootFrom(resolveRepoFile("", "contracts/knowledge/lifecycle.v1.json"))
	}
	pack, err := abcrun.LoadPack(*packPath)
	if err != nil {
		return err
	}
	learning, err := abcrun.LoadLearning(*learningPath)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), abcTimeout)
	defer cancel()
	res, err := runABC(ctx, abcSetup{pack: pack, learning: learning, learningPath: *learningPath, mode: *mode, cassettes: *cassettes,
		envFile: *envFile, model: *model, workerApp: *workerApp, only: splitList(*only), pulls: *pulls, root: root, dryRun: *dryRun, logf: func(f string, a ...any) { fmt.Fprintf(out, f+"\n", a...) }})
	if err != nil {
		return err
	}
	if err := writeJSONFile(*outPath, res); err != nil {
		return err
	}
	if *dryRun {
		gens := 0
		for _, r := range res.Plan {
			gens += r.Generations
		}
		fmt.Fprintf(out, "dry run: %d situations, %d strategy generations planned, no model called; plan in %s\n", len(res.Plan), gens, *outPath)
		return nil
	}
	fmt.Fprintf(out, "wrote %d situations (%d generations, %d context pulls) to %s\n", len(res.Situations), res.Calls.GenerationRuns, res.Calls.ContextPulls, *outPath)
	return nil
}

type abcSetup struct {
	pack         abcrun.Pack
	learning     abcrun.Learning
	learningPath string
	mode         string
	cassettes    string
	envFile      string
	model        string
	workerApp    string
	only         []string
	pulls        int
	dryRun       bool
	root         string
	logf         func(string, ...any)
}

func runABC(ctx context.Context, s abcSetup) (abcrun.Output, error) {
	rules, err := knowledge.LoadRules(filepath.Join(s.root, "contracts", "knowledge", "lifecycle.v1.json"))
	if err != nil {
		return abcrun.Output{}, err
	}
	routing, err := orchestrator.LoadRouting(filepath.Join(s.root, "contracts", "transitions", "routing.v1.json"))
	if err != nil {
		return abcrun.Output{}, err
	}
	extra, names, err := readEnvFile(s.envFile)
	if err != nil {
		return abcrun.Output{}, err
	}
	if s.model != "" {
		extra = append(extra, "GHOST_MODEL="+s.model)
	}
	s.logf("starting a private embedded Postgres (DATABASE_URL is not used); worker mode %s; worker variables from the env file: %s", s.mode, strings.Join(names, ", "))
	env, err := embedded.Start(ctx)
	if err != nil {
		return abcrun.Output{}, err
	}
	defer env.Close()
	if err := abcrun.InstallDeterministicIDs(ctx, env.DB); err != nil {
		return abcrun.Output{}, err
	}
	replayNow := s.learning.Cutoff
	builder, err := abcrun.NewBuilder(env.DB, replayNow)
	if err != nil {
		return abcrun.Output{}, err
	}
	if err := builder.SeedCompany(ctx, s.pack.Company, replayNow.Add(-365*24*time.Hour)); err != nil {
		return abcrun.Output{}, err
	}
	signer, err := runtoken.NewSigner([]byte("abc-experiment-fixed-key-0123456789"), orchestrator.RequiredTokenTTL(true)+time.Minute)
	if err != nil {
		return abcrun.Output{}, err
	}
	clk := clock.NewFixed(replayNow)
	if s.dryRun {
		runner, err := abcrun.NewRunner(abcrun.Options{DB: env.DB, Pack: s.pack, Learning: s.learning, Rules: rules, Routing: routing,
			Worker: &abcrun.GenerationWorker{}, Signer: signer, Clock: clk, Builder: builder, Only: s.only, Pulls: s.pulls, Logf: s.logf})
		if err != nil {
			return abcrun.Output{}, err
		}
		rows, store, err := runner.Plan(ctx)
		return abcrun.Output{Version: abcrun.ArmsVersion, EmptyBeforeLearning: true, Store: store, Plan: rows}, err
	}
	core, err := abcrun.StartCore(env.DB, builder.Ingest(), signer, clk)
	if err != nil {
		return abcrun.Output{}, err
	}
	defer core.Close()
	proc, err := abcrun.StartWorker(ctx, abcrun.WorkerSpec{Python: os.Getenv("GHOST_WORKER_PYTHON"), RepoRoot: s.root, App: s.workerApp, Mode: s.mode,
		CassetteDir: s.cassettes, CoreURL: core.URL, ExtraEnv: extra, Logf: func(f string, a ...any) { s.logf(f, a...) }})
	if err != nil {
		return abcrun.Output{}, err
	}
	defer proc.Stop()
	client, err := workerclient.New(proc.URL)
	if err != nil {
		return abcrun.Output{}, err
	}
	worker := &abcrun.GenerationWorker{Inner: client}
	runner, err := abcrun.NewRunner(abcrun.Options{DB: env.DB, Pack: s.pack, Learning: s.learning, Rules: rules, Routing: routing, Worker: worker,
		Signer: signer, Clock: clk, Builder: builder, Only: s.only, Pulls: s.pulls, Logf: s.logf,
		Guard: pythonGuard(s.root, s.learningPath)})
	if err != nil {
		return abcrun.Output{}, err
	}
	return runner.Run(ctx)
}

// pythonGuard is the hard block before every arm: bench/uplift's guard (experiment.assert_experiment_store plus the
// hidden-rule marker scan) run as a subprocess over the knowledge the arm will read. Any refusal aborts the run.
func pythonGuard(root, learningPath string) abcrun.Guard {
	return func(ctx context.Context, store []knowledge.Knowledge, empty bool) error {
		tmp, err := os.CreateTemp("", "abc-store-*.json")
		if err != nil {
			return err
		}
		defer os.Remove(tmp.Name())
		if err := json.NewEncoder(tmp).Encode(map[string]any{"empty_before_learning": empty, "items": store}); err != nil {
			_ = tmp.Close()
			return err
		}
		if err := tmp.Close(); err != nil {
			return err
		}
		py := os.Getenv("GHOST_WORKER_PYTHON")
		if py == "" {
			py = "python"
		}
		var stderr bytes.Buffer
		cmd := exec.CommandContext(ctx, py, "-m", "bench.uplift.guard", tmp.Name(), learningPath)
		cmd.Dir, cmd.Stderr = root, &stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("the experiment guard refused the knowledge store: %s", strings.TrimSpace(stderr.String()))
		}
		return nil
	}
}

// readEnvFile parses KEY=VALUE lines. The values go to the worker process only; the names are all that is ever shown.
func readEnvFile(path string) (pairs []string, names []string, err error) {
	if path == "" {
		return nil, nil, nil
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, fmt.Errorf("ghostctl: open --env-file: %w", err)
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(strings.TrimPrefix(sc.Text(), utf8BOM))
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		k = strings.TrimSpace(strings.TrimPrefix(k, "export "))
		if !ok || k == "" {
			continue
		}
		v = strings.Trim(strings.TrimSpace(v), `"'`)
		pairs, names = append(pairs, k+"="+v), append(names, k)
	}
	return pairs, names, sc.Err()
}

func splitList(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func writeJSONFile(path string, v any) error {
	raw, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, append(raw, '\n'), 0o644)
}

// repoRootFrom is the repository root given the path of a file known to sit at contracts/knowledge/lifecycle.v1.json.
func repoRootFrom(lifecycle string) string {
	return filepath.Dir(filepath.Dir(filepath.Dir(lifecycle)))
}

const utf8BOM = "\xef\xbb\xbf"
