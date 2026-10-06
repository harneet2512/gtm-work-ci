package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/transitioneval"
	"github.com/harneet2512/gtm-work/core-go/internal/transitions"
)

const transitionsEvalUsage = "usage: ghostctl transitions-eval [--rules rules.v1.json] [--gold scenarios.json] [--blind blind.json] [--date YYYY-MM-DD] [--out report.json]"

// Default locations, relative to the repository root.
const (
	defaultRules = "contracts/transitions/rules.v1.json"
	defaultGold  = "fixtures/gold/transitions/scenarios.json"
	defaultBlind = "fixtures/gold/transitions/blind.json"
)

// runTransitionsEval replays the transition gold through the detector and prints (and optionally writes) the
// precision/recall report. It needs no database: the report depends only on the gold and the rule set.
func runTransitionsEval(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("transitions-eval", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	rulesPath := fs.String("rules", "", "rule set (default "+defaultRules+")")
	goldPath := fs.String("gold", "", "authored gold (default "+defaultGold+")")
	blindPath := fs.String("blind", "", "blind gold (default "+defaultBlind+" when it exists)")
	date := fs.String("date", time.Now().UTC().Format("2006-01-02"), "report date")
	outPath := fs.String("out", "", "write the JSON report here")
	if err := fs.Parse(args); err != nil || fs.NArg() != 0 {
		return errors.New(transitionsEvalUsage)
	}
	rules, err := transitions.LoadRules(resolveRepoFile(*rulesPath, defaultRules))
	if err != nil {
		return err
	}
	files := map[string]string{transitioneval.OriginAuthored: resolveRepoFile(*goldPath, defaultGold)}
	if blind := resolveRepoFile(*blindPath, defaultBlind); fileExists(blind) {
		files[transitioneval.OriginBlind] = blind
	} else if *blindPath != "" {
		return fmt.Errorf("blind gold %s not found", blind)
	}
	gold, err := transitioneval.LoadGoldFiles(files)
	if err != nil {
		return err
	}
	var names []string
	for _, origin := range []string{transitioneval.OriginAuthored, transitioneval.OriginBlind} {
		if p, ok := files[origin]; ok {
			names = append(names, relToRepo(p))
		}
	}
	rep, err := transitioneval.Build(rules, gold, names, *date, "go run ./cmd/ghostctl transitions-eval --date "+*date+" --out <report.json>  (from core-go)")
	if err != nil {
		return err
	}
	raw, err := json.MarshalIndent(rep, "", "  ")
	if err != nil {
		return fmt.Errorf("encode report: %w", err)
	}
	raw = append(raw, '\n')
	if *outPath != "" {
		if err := os.WriteFile(*outPath, raw, 0o644); err != nil {
			return fmt.Errorf("write report: %w", err)
		}
	}
	printSummary(out, rep)
	return nil
}

func printSummary(out io.Writer, rep transitioneval.Report) {
	line := func(name string, m transitioneval.Metrics) {
		fmt.Fprintf(out, "%-22s steps=%-3d status_acc=%s detection P=%s R=%s  premature=%s  uncertainty=%s  facts P=%s R=%s\n", name, m.Steps,
			pct(m.StatusAccuracy.Value), pct(m.Detection.Precision), pct(m.Detection.Recall), ratioText(m.PrematurePromotion),
			ratioText(m.UncertaintyMarking), pct(m.SupportingFacts.Precision), pct(m.SupportingFacts.Recall))
	}
	fmt.Fprintf(out, "transition gold, rule set %s\n", rep.RuleSet)
	line("headline (uncontested)", rep.Headline)
	line("  authored", rep.Authored)
	line("  blind", rep.Blind)
	line("contested", rep.Contested)
}

func pct(v *float64) string {
	if v == nil {
		return "n/a"
	}
	return fmt.Sprintf("%.1f%%", *v*100)
}

func ratioText(r transitioneval.Ratio) string {
	return fmt.Sprintf("%d/%d", r.Num, r.Den)
}

// resolveRepoFile returns path when given, else the default found by walking up from the working directory.
func resolveRepoFile(path, def string) string {
	if path != "" {
		return path
	}
	dir, err := os.Getwd()
	if err != nil {
		return def
	}
	for {
		p := filepath.Join(dir, def)
		if fileExists(p) {
			return p
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return def
		}
		dir = parent
	}
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// relToRepo names a gold file by its repository-relative path when it is one of the defaults.
func relToRepo(p string) string {
	for _, def := range []string{defaultGold, defaultBlind} {
		if filepath.Base(p) == filepath.Base(def) {
			return def
		}
	}
	return filepath.ToSlash(p)
}
