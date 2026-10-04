package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/crmarena"
	"github.com/harneet2512/gtm-work/core-go/internal/demomine"
	"github.com/harneet2512/gtm-work/core-go/internal/store/embedded"
	"github.com/harneet2512/gtm-work/core-go/internal/transitions"
)

const mineDemoUsage = "usage: ghostctl mine-demo-cases [--data <snapshot dir>] [--split <split.json>] [--scoring <config.json>] " +
	"[--transition-rules <rules.json>] [--worker-url <replay worker URL>] [--date YYYY-MM-DD] [--out-dir <dir>]"

// Default locations, relative to the repository root.
const (
	fullSnapshotDir   = "data/crmarena_b2b"
	sampleSnapshotDir = "fixtures/crmarena_sample"
	defaultScoring    = "bench/config/demo_case_scoring.v1.json"
	defaultSplit      = "bench/data/deal_split.json"
	defaultReportDir  = "bench/reports"
	mineTimeout       = 6 * time.Hour
)

// runMineDemoCases is HAR-129 section 1's case mining: it replays the frozen CRMArena snapshot event by event
// through the real ingest -> state path on a private embedded Postgres (never DATABASE_URL), records what each
// event materially changed, scores every opportunity's candidate sequences with the committed weights and
// writes the ranked report. No LLM is called.
func runMineDemoCases(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("mine-demo-cases", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	dataDir := fs.String("data", "", "snapshot directory (default "+fullSnapshotDir+" if present, else "+sampleSnapshotDir+")")
	splitPath := fs.String("split", "", "frozen split; limits the scan to its current deals (default "+defaultSplit+" for "+fullSnapshotDir+")")
	scoring := fs.String("scoring", "", "scoring config (default "+defaultScoring+")")
	rulesPath := fs.String("transition-rules", "", "transition rule set; the detector is off without it")
	workerURL := fs.String("worker-url", "", "offline replay worker (bench/data/crmarena_replay_worker.py) serving recorded extraction cassettes; without it only rule extractors run. Never a live model")
	date := fs.String("date", time.Now().UTC().Format(time.DateOnly), "report date")
	outDir := fs.String("out-dir", "", "report directory (default "+defaultReportDir+")")
	if err := fs.Parse(args); err != nil || fs.NArg() != 0 {
		return errors.New(mineDemoUsage)
	}
	if _, err := time.Parse(time.DateOnly, *date); err != nil {
		return fmt.Errorf("ghostctl: --date %q is not YYYY-MM-DD", *date)
	}
	dir := pickSnapshot(*dataDir)
	cfg, err := demomine.LoadConfig(resolveRepoFile(*scoring, defaultScoring))
	if err != nil {
		return err
	}
	rules, err := loadRules(*rulesPath)
	if err != nil {
		return err
	}
	ext, err := replayExtractor(*workerURL)
	if err != nil {
		return err
	}
	split, label, err := scanScope(dir, *splitPath)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "snapshot %s; scan: %s; detector %s\nstarting a private embedded Postgres (DATABASE_URL is not used)\n", dir, label, onOff(rules != nil))
	ctx, cancel := context.WithTimeout(context.Background(), mineTimeout)
	defer cancel()
	env, err := embedded.Start(ctx)
	if err != nil {
		return err
	}
	defer env.Close()
	rep, err := demomine.Mine(ctx, env.DB, demomine.MineOptions{Dir: dir, Split: split, SplitLabel: label, Config: cfg, Rules: rules, Extractor: ext, Date: *date, Progress: out})
	if err != nil {
		return err
	}
	return writeReport(rep, resolveRepoFile(*outDir, defaultReportDir), out)
}

// pickSnapshot is the flag, else the full snapshot when present, else the committed sample.
func pickSnapshot(flagValue string) string {
	if flagValue != "" {
		return flagValue
	}
	if full := resolveRepoFile("", fullSnapshotDir); fileExists(filepath.Join(full, "Opportunity.json")) {
		return full
	}
	return resolveRepoFile("", sampleSnapshotDir)
}

// scanScope limits the scan to the current deals of the frozen split: the one given, or the repository's for
// the full snapshot. Any other snapshot (the sample) is scanned in full.
func scanScope(dir, splitFlag string) (*crmarena.Split, string, error) {
	path := splitFlag
	if path == "" && filepath.Base(filepath.Clean(dir)) == "crmarena_b2b" {
		if def := resolveRepoFile("", defaultSplit); fileExists(def) {
			path = def
		}
	}
	if path == "" {
		return nil, "every opportunity of the snapshot", nil
	}
	s, err := readSplit(path)
	if err != nil {
		return nil, "", err
	}
	return &s, fmt.Sprintf("the %d current deals of the frozen split (cutoff %s)", len(s.Current), s.Cutoff), nil
}

func loadRules(path string) (*transitions.RuleSet, error) {
	if path == "" {
		return nil, nil
	}
	rules, err := transitions.LoadRules(path)
	if err != nil {
		return nil, err
	}
	return &rules, nil
}

// replayExtractor connects to the offline replay worker, or returns nil when none is given.
func replayExtractor(url string) (*demomine.ReplayExtractor, error) {
	if url == "" {
		return nil, nil
	}
	return demomine.NewReplayExtractor(url)
}

func onOff(on bool) string {
	if on {
		return "on"
	}
	return "off"
}

func writeReport(rep demomine.Report, dir string, out io.Writer) error {
	raw, err := rep.JSON()
	if err != nil {
		return err
	}
	base := filepath.Join(dir, "demo-cases-"+rep.Date)
	if err := os.WriteFile(base+".json", raw, 0o644); err != nil {
		return fmt.Errorf("ghostctl: write report: %w", err)
	}
	if err := os.WriteFile(base+".md", rep.Markdown(), 0o644); err != nil {
		return fmt.Errorf("ghostctl: write report: %w", err)
	}
	fmt.Fprintf(out, "scanned %d opportunities (%d events replayed, %d material); %d candidate cases; report %s.{json,md}\n",
		rep.Counts.OpportunitiesScanned, rep.Counts.EventsReplayed, rep.Counts.MaterialEvents, rep.Counts.OpportunitiesWithCase, filepath.ToSlash(base))
	return nil
}

// readReport loads a mined report (JSON) for freeze-demo-manifest.
func readReport(path string) (*demomine.Report, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("ghostctl: read report: %w", err)
	}
	var rep demomine.Report
	if err := json.Unmarshal(raw, &rep); err != nil {
		return nil, fmt.Errorf("ghostctl: decode report %s: %w", path, err)
	}
	return &rep, nil
}
