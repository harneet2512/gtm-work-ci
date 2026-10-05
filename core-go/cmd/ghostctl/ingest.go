package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/config"
	"github.com/harneet2512/gtm-work/core-go/internal/graph"
	"github.com/harneet2512/gtm-work/core-go/internal/ingest"
	"github.com/harneet2512/gtm-work/core-go/internal/store"
)

const ingestUsage = "usage: ghostctl ingest <file-or-dir>"

const ingestTimeout = 30 * time.Minute

// runIngest ingests SourceEvent JSON files through the same Service the HTTP API uses (no
// HTTP involved). Files are read up front, so a bad path or file fails before any write; the
// events are then ingested in lexical file order and the run stops at the first failure.
func runIngest(args []string, out io.Writer) error {
	if len(args) != 1 {
		return errors.New(ingestUsage)
	}
	events, err := ingest.LoadEvents(args[0])
	if err != nil {
		return err
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "target database host: %s\n", hostOf(cfg.DatabaseURL))

	ctx, cancel := context.WithTimeout(context.Background(), ingestTimeout)
	defer cancel()
	db, err := store.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer db.Close()
	svc, err := ingest.NewService(db, ingest.Options{Extension: graph.NewExtension(), Debounce: cfg.CoalesceDebounce, MaxWait: cfg.CoalesceMaxWait, AfterAttribute: graphIngestHook()})
	if err != nil {
		return err
	}

	sum, err := ingest.IngestAll(ctx, svc, events)
	fmt.Fprintf(out, "ingested %d events: %d new, %d duplicate\n", sum.New+sum.Duplicate, sum.New, sum.Duplicate)
	return err
}
