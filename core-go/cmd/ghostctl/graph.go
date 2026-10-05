package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/config"
	"github.com/harneet2512/gtm-work/core-go/internal/ctxgraph"
	"github.com/harneet2512/gtm-work/core-go/internal/ingest"
	"github.com/harneet2512/gtm-work/core-go/internal/store"
)

const graphUsage = "usage: ghostctl graph rebuild [--keep] | graph drift [--account <id>] | graph project | graph status | graph diff <event-id>"

const graphTimeout = 30 * time.Minute

// runGraph operates the Neo4j projection: rebuild it from Postgres, check it for drift, drain the
// outbox once, show the projection lag, or show the exact graph diff of a source event.
// It reads DATABASE_URL and NEO4J_URI / NEO4J_USER / NEO4J_PASSWORD / NEO4J_DATABASE.
func runGraph(args []string, out io.Writer) error {
	if len(args) == 0 {
		return errors.New(graphUsage)
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), graphTimeout)
	defer cancel()
	db, err := store.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer db.Close()
	fmt.Fprintf(out, "target database host: %s\n", hostOf(cfg.DatabaseURL))
	switch args[0] {
	case "status":
		return graphStatus(ctx, db, out)
	case "diff":
		if len(args) != 2 {
			return errors.New(graphUsage)
		}
		return printJSON(out, func() (any, error) { return ctxgraph.NewService(nil, db).EventDiff(ctx, args[1]) })
	}
	proj, closeGraph, err := openProjector(ctx, db)
	if err != nil {
		return err
	}
	defer closeGraph()
	switch args[0] {
	case "rebuild":
		if len(args) > 2 || (len(args) == 2 && args[1] != "--keep") {
			return errors.New(graphUsage)
		}
		return printJSON(out, func() (any, error) { return proj.Rebuild(ctx, len(args) == 1) })
	case "drift":
		account := ""
		if len(args) == 3 && args[1] == "--account" {
			account = args[2]
		} else if len(args) != 1 {
			return errors.New(graphUsage)
		}
		return graphDrift(ctx, proj, account, out)
	case "project":
		n, err := proj.Drain(ctx)
		fmt.Fprintf(out, "projected %d jobs\n", n)
		return err
	}
	return errors.New(graphUsage)
}

func openProjector(ctx context.Context, db *sql.DB) (*ctxgraph.Projector, func(), error) {
	cfg, ok, err := ctxgraph.ConfigFromEnv()
	if err != nil {
		return nil, nil, err
	}
	if !ok {
		return nil, nil, errors.New("NEO4J_URI is not set: the graph projection is not configured")
	}
	g, err := ctxgraph.Open(ctx, cfg)
	if err != nil {
		return nil, nil, err
	}
	p, err := ctxgraph.NewProjector(db, g, ctxgraph.Options{WorkerID: "ghostctl"})
	if err != nil {
		_ = g.Close(ctx)
		return nil, nil, err
	}
	return p, func() { _ = g.Close(context.Background()) }, nil
}

func graphStatus(ctx context.Context, db *sql.DB, out io.Writer) error {
	return printJSON(out, func() (any, error) { return ctxgraph.ReadLag(ctx, db, time.Now()) })
}

// graphDrift prints the report and fails (non-zero exit) when anything drifts.
func graphDrift(ctx context.Context, p *ctxgraph.Projector, account string, out io.Writer) error {
	rep, err := p.Drift(ctx, account)
	if err != nil {
		return err
	}
	if err := printJSON(out, func() (any, error) { return rep, nil }); err != nil {
		return err
	}
	if rep.Drift != 0 {
		return fmt.Errorf("graph drift: %d elements differ from Postgres (run: ghostctl graph rebuild)", rep.Drift)
	}
	return nil
}

// graphIngestHook enqueues a graph projection job for every attributed activity when the Neo4j
// projection is configured (NEO4J_URI set); it only writes the Postgres outbox, so ingest never needs Neo4j.
func graphIngestHook() ingest.AttributeHook {
	if _, ok, err := ctxgraph.ConfigFromEnv(); err != nil || !ok {
		return nil
	}
	return ctxgraph.IngestHook()
}

func printJSON(out io.Writer, f func() (any, error)) error {
	v, err := f()
	if err != nil {
		return err
	}
	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}
