// Command demosmoke runs and checks the HAR-137 Slack smoke test against the fixture core.
//
//	demosmoke no-human --out DIR            drive the full flow through a fake Slack and the fake core, write DIR
//	demosmoke real-core --out DIR           the same flow against the real core API on embedded Postgres (HAR-139)
//	demosmoke verify --dir DIR [flags]      assert the run in DIR (also used on a live, human-clicked run)
//
// verify exits 0 only when every check passes; it prints one line per check and never a secret value.
// scripts/slack/demo_smoke.py wraps both for the live session and for CI.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"

	"github.com/harneet2512/gtm-work/core-go/internal/demosmoke"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	os.Exit(run(ctx, os.Args[1:], os.Getenv, os.Stdout, os.Stderr))
}

func run(ctx context.Context, args []string, getenv func(string) string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: demosmoke no-human --out DIR | demosmoke real-core --out DIR | demosmoke verify --dir DIR [--allow-partial] [--secret-env A,B]")
		return 2
	}
	switch args[0] {
	case "no-human":
		return noHuman(ctx, args[1:], stdout, stderr)
	case "real-core":
		return realCore(ctx, args[1:], stdout, stderr)
	case "verify":
		return verify(args[1:], getenv, stdout, stderr)
	}
	fmt.Fprintf(stderr, "demosmoke: unknown command %q\n", args[0])
	return 2
}

func noHuman(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("no-human", flag.ContinueOnError)
	fs.SetOutput(stderr)
	out := fs.String("out", "", "directory to write the run's artefacts to")
	if fs.Parse(args) != nil || *out == "" {
		fmt.Fprintln(stderr, "demosmoke no-human: --out DIR is required")
		return 2
	}
	if err := demosmoke.RunNoHuman(ctx, *out); err != nil {
		fmt.Fprintln(stderr, "demosmoke:", err)
		return 1
	}
	fmt.Fprintln(stdout, "no-human flow completed; artefacts in", *out)
	return 0
}

func realCore(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("real-core", flag.ContinueOnError)
	fs.SetOutput(stderr)
	out := fs.String("out", "", "directory to write the run's artefacts to")
	if fs.Parse(args) != nil || *out == "" {
		fmt.Fprintln(stderr, "demosmoke real-core: --out DIR is required")
		return 2
	}
	if err := demosmoke.RunRealCore(ctx, *out); err != nil {
		fmt.Fprintln(stderr, "demosmoke:", err)
		return 1
	}
	fmt.Fprintln(stdout, "real-core flow completed; artefacts in", *out)
	return 0
}

func verify(args []string, getenv func(string) string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("verify", flag.ContinueOnError)
	fs.SetOutput(stderr)
	dir := fs.String("dir", "", "run directory (fakecore-log.json, slack-audit.jsonl and the other logs)")
	partial := fs.Bool("allow-partial", false, "accept a run that stopped before all three messages")
	secrets := fs.String("secret-env", "", "comma-separated names of environment variables whose values must not appear in any file")
	if fs.Parse(args) != nil || *dir == "" {
		fmt.Fprintln(stderr, "demosmoke verify: --dir DIR is required")
		return 2
	}
	var names []string
	for _, n := range strings.Split(*secrets, ",") {
		if n = strings.TrimSpace(n); n != "" {
			names = append(names, n)
		}
	}
	checks, err := demosmoke.Verify(demosmoke.VerifyOptions{Dir: *dir, AllowPartial: *partial, SecretEnv: names, Getenv: getenv})
	if err != nil {
		fmt.Fprintln(stderr, "demosmoke:", err)
		return 1
	}
	for _, c := range checks {
		verdict := "PASS"
		if !c.OK {
			verdict = "FAIL"
		}
		fmt.Fprintf(stdout, "%s  %s  (%s)\n", verdict, c.Name, c.Detail)
	}
	if bad := demosmoke.Failed(checks); len(bad) > 0 {
		fmt.Fprintf(stdout, "%d of %d checks failed\n", len(bad), len(checks))
		return 1
	}
	fmt.Fprintf(stdout, "all %d checks passed\n", len(checks))
	return 0
}
