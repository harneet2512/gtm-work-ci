package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/config"
	"github.com/harneet2512/gtm-work/core-go/internal/knowledge"
	"github.com/harneet2512/gtm-work/core-go/internal/learning"
	"github.com/harneet2512/gtm-work/core-go/internal/store"
)

// ghostctl learn is the operator entry point of the HAR-119 learning loop: candidate evaluator versions
// seeded by unexplained human deltas or corrected verdicts are backtested, advanced to shadow, promoted
// to active or retired — every transition evidence-gated and audited in eval_promotions.
const learnUsage = "usage: ghostctl learn status | ghostctl learn backtest --evaluator T --version N [--gold dir[,dir]] [--rules path] | " +
	"ghostctl learn advance|promote|retire --evaluator T --version N [--by name] [--reason text] [--rules path]"

const learnTimeout = 2 * time.Minute

// Default gold dirs of a backtest, relative to the repository root.
var defaultGoldDirs = []string{"fixtures/evals/cases", "fixtures/evals/crmarena", "fixtures/evals/synthetic"}

const defaultLearnRules = "contracts/knowledge/lifecycle.v1.json"

func runLearn(args []string, out io.Writer) error {
	if len(args) == 0 {
		return errors.New(learnUsage)
	}
	sub, rest := args[0], args[1:]
	if sub == "status" {
		return learnStatus(rest, out)
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
	if err := fs.Parse(rest); err != nil || fs.NArg() != 0 || *evaluator == "" || *version < 1 {
		return errors.New(learnUsage)
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
			Evaluator: *evaluator, Version: *version, GoldDirs: dirs, Rules: rules})
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
		t, err := learning.Advance(ctx, db, *evaluator, *version, *by)
		return printTransition(out, t, err)
	case "promote":
		rules, err := knowledge.LoadRules(resolveRepoFile(*rulesPath, defaultLearnRules))
		if err != nil {
			return err
		}
		t, err := learning.Promote(ctx, db, *evaluator, *version, rules, *by)
		return printTransition(out, t, err)
	default: // retire
		t, err := learning.Retire(ctx, db, *evaluator, *version, *by, *reason)
		return printTransition(out, t, err)
	}
}

func printTransition(out io.Writer, t learning.Transition, err error) error {
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "%s:v%d %s -> %s (%s)\n", t.Evaluator, t.Version, t.FromStatus, t.ToStatus, t.Reason)
	return nil
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
