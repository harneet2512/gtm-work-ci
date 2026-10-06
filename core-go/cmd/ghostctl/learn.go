package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/config"
	"github.com/harneet2512/gtm-work/core-go/internal/evalreport"
	"github.com/harneet2512/gtm-work/core-go/internal/knowledge"
	"github.com/harneet2512/gtm-work/core-go/internal/learning"
	"github.com/harneet2512/gtm-work/core-go/internal/store"
)

// ghostctl learn is the operator entry point of the HAR-119 learning loop: candidate evaluator versions
// seeded by unexplained human deltas or corrected verdicts are backtested, advanced to shadow, promoted
// to active or retired — every transition evidence-gated and audited in eval_promotions. `learn report`
// is the HAR-121 eval-of-evals readout: one JSON document keyed by '<evaluator>:v<N>'.
const learnUsage = "usage: ghostctl learn status | ghostctl learn backtest --evaluator T --version N [--gold dir[,dir]] [--rules path] | " +
	"ghostctl learn advance|promote|retire --evaluator T --version N [--by name] [--reason text] [--rules path] [--at RFC3339 [--replay]] | " +
	"ghostctl learn report [--evaluator T [--version N] | --all] [--out path]"

const learnTimeout = 2 * time.Minute

// Default gold dirs of a backtest, relative to the repository root.
// defaultGoldDirs: the gold a backtest reads by default. fixtures/evals/cases (invented accounts) is retired and not
// listed; the loader reads <dir>/<case type>/<case>.json, so the CRMArena gold is its cases/ directory.
var defaultGoldDirs = []string{"fixtures/evals/crmarena/cases", "fixtures/evals/synthetic"}

const defaultLearnRules = "contracts/knowledge/lifecycle.v1.json"

func runLearn(args []string, out io.Writer) error { return runLearnTo(args, out, os.Stderr) }

// runLearnTo is runLearn with an injected diagnostics writer (the `learn report` target-host line), so
// stdout stays pure JSON and tests can assert where each stream goes.
func runLearnTo(args []string, out, errw io.Writer) error {
	if len(args) == 0 {
		return errors.New(learnUsage)
	}
	sub, rest := args[0], args[1:]
	if sub == "status" {
		return learnStatus(rest, out)
	}
	if sub == "report" {
		return learnReport(rest, out, errw)
	}
	if sub != "backtest" && sub != "advance" && sub != "promote" && sub != "retire" {
		return errors.New(learnUsage)
	}
	fs := flag.NewFlagSet("learn "+sub, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	evaluator := fs.String("evaluator", "", "eval axis (eval_type)")
	version := fs.Int("version", 0, "evaluator version")
	gold := fs.String("gold", "", "comma-separated gold dirs (default "+strings.Join(defaultGoldDirs, ",")+")")
	rulesPath := fs.String("rules", "", "knowledge lifecycle rules (default "+defaultLearnRules+")")
	by := fs.String("by", "ghostctl", "who the eval_promotions audit records")
	reason := fs.String("reason", "", "retirement note (required by retire)")
	atFlag := fs.String("at", "", "world time to stamp the transition with (RFC 3339): the replayed episode's time, never the wall clock")
	replayFlag := fs.Bool("replay", false, "this runs inside a replay: --at is required and the database clock is never used")
	if err := fs.Parse(rest); err != nil || fs.NArg() != 0 || *evaluator == "" || *version < 1 {
		return errors.New(learnUsage)
	}
	at, replay, err := parseReplayTime(*atFlag, *replayFlag)
	if err != nil {
		return err
	}
	if !learning.ValidAxis(*evaluator) {
		return fmt.Errorf("%q is not an eval axis (eval_type)", *evaluator)
	}
	if sub == "retire" && strings.TrimSpace(*reason) == "" {
		return errors.New("learn retire needs --reason")
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "target database host: %s\n", hostOf(cfg.DatabaseURL))
	ctx, cancel := context.WithTimeout(context.Background(), learnTimeout)
	defer cancel()
	db, err := store.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer db.Close()
	switch sub {
	case "backtest":
		dirs := defaultGoldDirs
		if *gold != "" {
			dirs = strings.Split(*gold, ",")
		}
		for i := range dirs {
			dirs[i] = resolveRepoFile(dirs[i], dirs[i])
		}
		var rules *knowledge.Rules
		// Fail closed like learn promote does: a backtest whose rules never loaded records no episode
		// evidence, so its candidate would sit gated forever with no signal why.
		r, err := knowledge.LoadRules(resolveRepoFile(*rulesPath, defaultLearnRules))
		if err != nil {
			return fmt.Errorf("load knowledge rules: %w", err)
		}
		rules = &r
		rep, err := learning.Backtest(ctx, db, learning.BacktestOpts{
			Evaluator: *evaluator, Version: *version, GoldDirs: dirs, Rules: rules, Now: at, Replay: replay})
		if err != nil {
			return err
		}
		raw, err := json.MarshalIndent(rep, "", "  ")
		if err != nil {
			return fmt.Errorf("encode backtest report: %w", err)
		}
		fmt.Fprintf(out, "%s\n", raw)
		if !rep.Passed {
			return fmt.Errorf("backtest %s did not pass", rep.ID)
		}
		return nil
	case "advance":
		t, err := learning.AdvanceAt(ctx, db, *evaluator, *version, *by, at, replay)
		return printTransition(out, t, err)
	case "promote":
		rules, err := knowledge.LoadRules(resolveRepoFile(*rulesPath, defaultLearnRules))
		if err != nil {
			return err
		}
		t, err := learning.PromoteAt(ctx, db, *evaluator, *version, rules, *by, at, replay)
		return printTransition(out, t, err)
	default: // retire
		t, err := learning.Retire(ctx, db, *evaluator, *version, *by, *reason)
		return printTransition(out, t, err)
	}
}

// parseReplayTime reads --at/--replay: a replay must carry the episode's world time (HAR-97 B9: promoted_at and
// the audit row are never the wall clock), and --at alone stamps a live run explicitly. Validated before the
// database is dialed.
func parseReplayTime(raw string, replay bool) (time.Time, bool, error) {
	if raw == "" {
		if replay {
			return time.Time{}, false, errors.New("learn: --replay needs --at (the replayed episode's world time)")
		}
		return time.Time{}, false, nil
	}
	at, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return time.Time{}, false, fmt.Errorf("learn: --at must be RFC 3339: %w", err)
	}
	return at.UTC(), replay, nil
}

func printTransition(out io.Writer, t learning.Transition, err error) error {
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "%s:v%d %s -> %s (%s)\n", t.Evaluator, t.Version, t.FromStatus, t.ToStatus, t.Reason)
	return nil
}

// learnReport emits the WP23/HAR-121 eval-of-evals report: one JSON document, one Report per evaluator
// version keyed '<evaluator>:v<N>', computed from the persisted supervision rows — no model calls.
// --evaluator alone reports every version of that axis; --all reports every registered version plus
// every observed eval_runs tag (the shipped v1s have no evaluator_versions row). stdout carries the
// JSON document only — the target-host line goes to errw (stderr in production) so `learn report --all | jq` stays clean.
func learnReport(args []string, out, errw io.Writer) error {
	fs := flag.NewFlagSet("learn report", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	evaluator := fs.String("evaluator", "", "eval axis (eval_type); omit with --all")
	version := fs.Int("version", 0, "evaluator version (requires --evaluator)")
	all := fs.Bool("all", false, "report every evaluator version")
	outPath := fs.String("out", "", "write the JSON document to this file instead of stdout")
	if err := fs.Parse(args); err != nil || fs.NArg() != 0 {
		return errors.New(learnUsage)
	}
	switch {
	case *all && (*evaluator != "" || *version != 0):
		return errors.New("learn report: --all cannot combine with --evaluator/--version")
	case !*all && *evaluator == "":
		return errors.New("learn report: pass --evaluator T [--version N] or --all")
	case *version != 0 && *evaluator == "":
		return errors.New("learn report: --version needs --evaluator")
	case *version < 0:
		return errors.New(learnUsage)
	}
	if *evaluator != "" && !learning.ValidAxis(*evaluator) {
		return fmt.Errorf("%q is not an eval axis (eval_type)", *evaluator)
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	fmt.Fprintf(errw, "target database host: %s\n", hostOf(cfg.DatabaseURL))
	ctx, cancel := context.WithTimeout(context.Background(), learnTimeout)
	defer cancel()
	db, err := store.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer db.Close()
	set, err := evalreport.Generate(ctx, db, evalreport.Options{Evaluator: *evaluator, Version: *version})
	if err != nil {
		return err
	}
	raw, err := evalreport.Encode(set)
	if err != nil {
		return fmt.Errorf("encode eval report: %w", err)
	}
	if *outPath != "" {
		if err := os.WriteFile(*outPath, raw, 0o644); err != nil {
			return fmt.Errorf("write eval report to %s: %w", *outPath, err)
		}
		fmt.Fprintf(out, "wrote %d report(s) to %s\n", len(set.Reports), *outPath)
		return nil
	}
	_, err = out.Write(raw)
	return err
}

func learnStatus(args []string, out io.Writer) error {
	if len(args) != 0 {
		return errors.New(learnUsage)
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "target database host: %s\n", hostOf(cfg.DatabaseURL))
	ctx, cancel := context.WithTimeout(context.Background(), learnTimeout)
	defer cancel()
	db, err := store.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer db.Close()
	rows, err := learning.Status(ctx, db)
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		fmt.Fprintln(out, "no evaluator versions registered")
		return nil
	}
	for _, v := range rows {
		kid := "-"
		if v.KnowledgeID != nil {
			kid = *v.KnowledgeID
		}
		fmt.Fprintf(out, "%-28s v%-3d %-9s %-14s from=%-12s knowledge=%s\n",
			v.Evaluator, v.Version, v.Status, v.Kind, v.CreatedFrom, kid)
	}
	return nil
}
