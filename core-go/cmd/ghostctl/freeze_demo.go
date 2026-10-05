package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/config"
	"github.com/harneet2512/gtm-work/core-go/internal/demomine"
	"github.com/harneet2512/gtm-work/core-go/internal/schemacheck"
	"github.com/harneet2512/gtm-work/core-go/internal/store"
	"github.com/harneet2512/gtm-work/core-go/internal/store/embedded"
)

const freezeDemoUsage = "usage: ghostctl freeze-demo-manifest --opportunity <salesforce id> --held-out <event id> --out <manifest.json> " +
	"--created-at <RFC3339> (--into-database | --allow-throwaway) [--report <demo-cases.json> | --why <text>] [--replay-events-out <dir>] [--data <dir>] [--transition-rules <rules.json>] [--worker-url <replay worker URL>]"

const freezeTimeout = 2 * time.Hour

// runFreezeDemoManifest freezes one mined case: it materializes the world through Event N-1 (Event N is never
// ingested), writes the manifest as JSON validated against demo_manifest.v1.json with its content_sha256, and
// writes the demo_manifests row. By default that happens in a private embedded Postgres, which is discarded
// when the command ends and is therefore NON-CANONICAL (its database ids name nothing that survives); it needs
// --allow-throwaway. --into-database materializes into DATABASE_URL instead, which must be empty (checked:
// source_events, accounts and agent_runs have no rows), so the demo database holds the world through N-1 and the
// manifest that names it. One frozen case per database: to freeze a second case, point DATABASE_URL at another
// fresh database and run the command again. --created-at is required: the hash covers it, and it must not be
// the wall clock. content_sha256 itself does not depend on database-assigned ids, so two freezes of the same case
// agree on it.
func runFreezeDemoManifest(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("freeze-demo-manifest", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	opp := fs.String("opportunity", "", "Salesforce opportunity id of the case")
	held := fs.String("held-out", "", "event id (uuid) of Event N, as listed in the report")
	outPath := fs.String("out", "", "manifest JSON file to write")
	report := fs.String("report", "", "mined report (demo-cases-<date>.json) the case comes from")
	why := fs.String("why", "", "why this case was selected (needed when the case is not in the report)")
	dataDir := fs.String("data", "", "snapshot directory (default as mine-demo-cases)")
	rulesPath := fs.String("transition-rules", "", "transition rule set; the detector is off without it")
	workerURL := fs.String("worker-url", "", "offline replay worker, as for mine-demo-cases (use the same as the report)")
	into := fs.Bool("into-database", false, "materialize into DATABASE_URL (must be empty: no source_events, accounts or agent_runs) instead of a private embedded Postgres; one case per database")
	throwaway := fs.Bool("allow-throwaway", false, "freeze in a private embedded Postgres that is discarded: the output is NON-CANONICAL (its database ids name nothing)")
	createdAt := fs.String("created-at", "", "created_at (RFC 3339, required: the hash covers it and must not depend on the wall clock)")
	replayOut := fs.String("replay-events-out", "", "directory to write Event N's as-known SourceEvent to (<event id>.json): the replay dataset core's Play reads (GHOST_REPLAY_EVENTS)")
	if err := fs.Parse(args); err != nil || fs.NArg() != 0 || *opp == "" || *held == "" || *outPath == "" || *createdAt == "" {
		return errors.New(freezeDemoUsage)
	}
	if !*into && !*throwaway {
		return errors.New("ghostctl: freeze-demo-manifest needs --into-database (canonical) or --allow-throwaway (a discarded database, NON-CANONICAL output); " + freezeDemoUsage)
	}
	now, err := time.Parse(time.RFC3339, *createdAt)
	if err != nil {
		return fmt.Errorf("ghostctl: --created-at %q is not RFC 3339: %w", *createdAt, err)
	}
	now = now.UTC()
	rules, err := loadRules(*rulesPath)
	if err != nil {
		return err
	}
	opts := demomine.FreezeOptions{Dir: pickSnapshot(*dataDir), OpportunityID: *opp, HeldOutID: *held, Rules: rules, Why: *why, Now: now}
	if opts.Extractor, err = replayExtractor(*workerURL); err != nil {
		return err
	}
	if *report != "" {
		if opts.Report, err = readReport(*report); err != nil {
			return err
		}
	}
	validator, err := schemacheck.New() // fail before any work if the contracts cannot be found
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), freezeTimeout)
	defer cancel()
	db, where, closeDB, err := openFreezeDB(ctx, *into, out)
	if err != nil {
		return err
	}
	defer closeDB()
	fr, err := demomine.Freeze(ctx, db, opts)
	if err != nil {
		return err
	}
	if err := validator.Validate("demo_manifest", fr.JSON); err != nil {
		return fmt.Errorf("ghostctl: the frozen manifest does not validate against demo_manifest.v1.json: %w", err)
	}
	if err := demomine.VerifyHash(fr.JSON); err != nil {
		return err
	}
	if err := demomine.Persist(ctx, db, fr.Manifest); err != nil {
		return err
	}
	if err := os.WriteFile(*outPath, fr.JSON, 0o644); err != nil {
		return fmt.Errorf("ghostctl: write manifest: %w", err)
	}
	if *replayOut != "" {
		if err := writeReplayEvent(*replayOut, fr); err != nil {
			return err
		}
	}
	m := fr.Manifest
	if !*into {
		fmt.Fprintln(out, "NON-CANONICAL: the database ids in this manifest name a throwaway database that is now gone; use --into-database for the demo")
	}
	fmt.Fprintf(out, "froze manifest %s: %d history events, held-out event %s (position %d), content_sha256 %s\ndemo_manifests row written to %s; %d events materialized, the held-out event not among them; JSON at %s\n",
		m.ID, len(m.Events), m.HeldOutEvent.EventID, m.HeldOutEvent.ReplayPosition, m.ContentSHA256, where, fr.Ingested, *outPath)
	return nil
}

// writeReplayEvent writes Event N's as-known SourceEvent as <dir>/<event id>.json: exactly the document the
// manifest pins by payload digest, and the only place Event N lives until Play releases it.
func writeReplayEvent(dir string, fr demomine.Frozen) error {
	raw, err := json.Marshal(fr.HeldSource)
	if err != nil {
		return fmt.Errorf("ghostctl: encode the replay event: %w", err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("ghostctl: create the replay events directory: %w", err)
	}
	path := filepath.Join(dir, fr.Manifest.HeldOutEvent.EventID+".json")
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		return fmt.Errorf("ghostctl: write the replay event: %w", err)
	}
	return nil
}

// openFreezeDB opens the target: a private embedded Postgres, or DATABASE_URL when asked.
func openFreezeDB(ctx context.Context, useConfigured bool, out io.Writer) (*sql.DB, string, func(), error) {
	if !useConfigured {
		env, err := embedded.Start(ctx)
		if err != nil {
			return nil, "", nil, err
		}
		return env.DB, "a private embedded Postgres (discarded at exit)", func() { _ = env.Close() }, nil
	}
	cfg, err := config.Load()
	if err != nil {
		return nil, "", nil, err
	}
	fmt.Fprintf(out, "target database host: %s\n", hostOf(cfg.DatabaseURL))
	db, err := store.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return nil, "", nil, err
	}
	return db, "the database at " + hostOf(cfg.DatabaseURL), func() { _ = db.Close() }, nil
}
