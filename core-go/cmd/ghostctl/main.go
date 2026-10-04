// Command ghostctl is the operator CLI for the Ghost core service.
//
//	ghostctl migrate up
//	ghostctl migrate status
//	ghostctl migrate down --yes      (also requires GHOST_ALLOW_DESTRUCTIVE=1)
//	ghostctl ingest <file-or-dir>    (SourceEvent JSON, lexical order, no HTTP)
//	ghostctl graph rebuild|drift|project|status|diff   (Neo4j context-graph projection)
//	ghostctl jobs                    (parked recompute jobs and quarantined activities)
//	ghostctl jobs unquarantine <activity-id>
//	ghostctl breaker [status|reset]  (provider circuit breaker of the running core, HAR-135)
//	ghostctl seed-org <org.json>     (employees, their identities and reporting lines)
//	ghostctl import-crmarena [--dry-run] (--until <RFC3339> | --full-timeline) <export-dir>   (CRMArena-Pro B2B snapshot, HAR-130)
//	ghostctl crmarena-split [--cutoff D] [--seed N] [--previous-min-quiet-days N] <export-dir> <out.json>
//	ghostctl replay-crmarena --split <split.json> [--deal ID] [--from T] [--to T] [--out f] <export-dir>
//	ghostctl crmarena-report [--out <report.json>]
//	ghostctl transitions-eval [--date D] [--out <report.json>]   (transition detector vs the transition gold, HAR-126)
//	ghostctl mine-demo-cases [--data D] [--split S] [--scoring C] [--transition-rules R] [--date D] [--out-dir O]   (HAR-129 section 1: rank real event sequences for the demo; embedded Postgres only)
//	ghostctl freeze-demo-manifest --opportunity ID --held-out EVENT_ID --out F [--report R | --why T] [--into-database]   (freeze one mined case; Event N is never ingested)
//	ghostctl abc-pack --export D --synthetic D --specs F --out F   (blind A/B/C uplift experiment, HAR-128 WP30: build the frozen world pack from the git-ignored data)
//	ghostctl abc-arms --pack F --learning F --out F [--worker-mode replay|record]   (the experiment through the real lifecycle, orchestrator and worker; driven by python -m bench.uplift)
//	ghostctl proof-har129 [--out O] [--run-id ID] [--requirements P]   (write artifacts/har129/<run_id>/: the 13 HAR-129 section H files and the section I confirmation matrix; every row NOT RUN until a real integrated run)
//
// It reads DATABASE_URL from the environment (or the repository .env file).
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/config"
	"github.com/harneet2512/gtm-work/core-go/internal/store"
)

const usage = "usage: ghostctl migrate <up|status|down --yes> | ghostctl ingest <file-or-dir> | ghostctl seed-org <org.json> | " +
	"ghostctl jobs [unquarantine <activity-id>] | ghostctl breaker [status|reset] | ghostctl import-crmarena [--dry-run] (--until T | --full-timeline) <dir> | ghostctl crmarena-split ... | ghostctl replay-crmarena ... | ghostctl crmarena-report | ghostctl transitions-eval | ghostctl mine-demo-cases | ghostctl freeze-demo-manifest | ghostctl abc-pack ... | ghostctl abc-arms ... | ghostctl proof-har129"

// commands are the subcommands other than migrate.
var commands = map[string]func([]string, io.Writer) error{
	"ingest": runIngest, "jobs": runJobs, "breaker": runBreaker, "graph": runGraph, "seed-org": runSeedOrg,
	"import-crmarena": runImportCRMArena, "crmarena-split": runCRMArenaSplit, "replay-crmarena": runReplayCRMArena, "crmarena-report": runCRMArenaReport,
	"transitions-eval": runTransitionsEval, "mine-demo-cases": runMineDemoCases, "freeze-demo-manifest": runFreezeDemoManifest,
	"proof-har129": runProofHar129, "abc-pack": runABCPack, "abc-arms": runABCArms,
}

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "ghostctl:", err)
		os.Exit(1)
	}
}

func run(args []string, out io.Writer) error {
	if len(args) > 0 {
		if cmd, ok := commands[args[0]]; ok {
			return cmd(args[1:], out)
		}
	}
	action, err := parseArgs(args, os.Getenv("GHOST_ALLOW_DESTRUCTIVE") == "1")
	if err != nil {
		return err
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "target database host: %s\n", hostOf(cfg.DatabaseURL))

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	db, err := store.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer db.Close()
	m, err := store.NewMigrator(db)
	if err != nil {
		return err
	}

	switch action {
	case "up":
		err = m.Up(ctx)
	case "down":
		err = m.DownTo(ctx, 0)
	}
	if err != nil {
		return err
	}
	v, err := m.Version(ctx)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "schema version %d\n", v)
	return nil
}

// parseArgs validates the command line. Dropping the schema needs both --yes and the
// GHOST_ALLOW_DESTRUCTIVE=1 environment switch.
func parseArgs(args []string, destructiveAllowed bool) (string, error) {
	if len(args) < 2 || args[0] != "migrate" {
		return "", errors.New(usage)
	}
	switch args[1] {
	case "up", "status":
		if len(args) != 2 {
			return "", errors.New(usage)
		}
		return args[1], nil
	case "down":
		if len(args) != 3 || args[2] != "--yes" {
			return "", errors.New("migrate down drops every table; re-run with --yes")
		}
		if !destructiveAllowed {
			return "", errors.New("migrate down also requires GHOST_ALLOW_DESTRUCTIVE=1")
		}
		return "down", nil
	default:
		return "", errors.New(usage)
	}
}

// hostOf returns the host of a database URL without credentials.
func hostOf(databaseURL string) string {
	u, err := url.Parse(databaseURL)
	if err != nil || u.Host == "" {
		return "(unparseable)"
	}
	return u.Host
}
