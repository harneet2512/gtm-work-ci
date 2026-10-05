package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/config"
	"github.com/harneet2512/gtm-work/core-go/internal/crmarena"
	"github.com/harneet2512/gtm-work/core-go/internal/graph"
	"github.com/harneet2512/gtm-work/core-go/internal/ingest"
	"github.com/harneet2512/gtm-work/core-go/internal/store"
)

const (
	crmarenaSplitUsage = "usage: ghostctl crmarena-split [--cutoff YYYY-MM-DD] [--seed N] [--previous-min-quiet-days N] <export-dir> <out.json>"
	importTimeout      = 2 * time.Hour
	crmarenaDataset    = "CRMArena-Pro B2B Salesforce org (Salesforce AI Research)"
	crmarenaLicence    = "CC BY-NC 4.0 (non-commercial use only)"
)

// runImportCRMArena maps a CRMArena-Pro export (bench/data/crmarena_export.py) onto SourceEvents, seeds
// the seller's reps as employees and ingests the events in time order through the HAR-96 pipeline.
// --dry-run builds and validates the events without a database. A database that already holds the
// legacy fixture-world accounts is refused unless --allow-mixed says the mix is intended. The timeline
// must be chosen: --until <RFC3339 cutoff> ingests only events dated before it (the default for
// HAR-129 runs: the frozen split's cutoff), --full-timeline ingests everything, outcomes included.
func runImportCRMArena(args []string, out io.Writer) error {
	opts, err := parseImportFlags(args)
	if err != nil {
		return err
	}
	snap, err := crmarena.Load(opts.dir)
	if err != nil {
		return err
	}
	res, err := crmarena.Build(snap)
	if err != nil {
		return err
	}
	if res, err = supersede(res, opts.coveredPath, out); err != nil {
		return err
	}
	if opts.timelineLimited() {
		if res, err = withholdAfter(res, opts.until, out); err != nil {
			return err
		}
	}
	printBuild(out, res)
	if opts.dryRun {
		return nil
	}
	return ingestCRMArena(res, filepath.Join(opts.dir, "User.json"), opts.allowMixed, out)
}

func ingestCRMArena(res crmarena.Result, source string, allowMixed bool, out io.Writer) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "target database host: %s\n", hostOf(cfg.DatabaseURL))
	ctx, cancel := context.WithTimeout(context.Background(), importTimeout)
	defer cancel()
	db, err := store.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer db.Close()
	if !allowMixed {
		if err := refuseLegacyWorld(ctx, db); err != nil {
			return err
		}
	}
	company := res.Reps.Company(source)
	seed, err := graph.SeedCompany(ctx, db, company, res.Epoch())
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "seeded %d reps: %d created, %d updated\n", len(company.People), seed.Created, seed.Updated)
	svc, err := ingest.NewService(db, ingest.Options{Extension: graph.NewExtension(), Debounce: cfg.CoalesceDebounce, MaxWait: cfg.CoalesceMaxWait, AfterAttribute: graphIngestHook()})
	if err != nil {
		return err
	}
	named := make([]ingest.NamedEvent, len(res.Events))
	for i, e := range res.Events {
		named[i] = ingest.NamedEvent{File: "crmarena", Index: i, Event: e.Source}
	}
	sum, err := ingest.IngestAll(ctx, svc, named)
	fmt.Fprintf(out, "ingested %d events: %d new, %d duplicate\n", sum.New+sum.Duplicate, sum.New, sum.Duplicate)
	return err
}

// legacyAccountsQuery counts current CRM account mappings that are not Salesforce accounts: the
// fixture world's AC-4, BE-7, NS-2. Salesforce account ids start with the key prefix 001.
const legacyAccountsQuery = `SELECT count(*) FROM entity_source_mappings
 WHERE entity_type = 'account' AND source_system = 'crm' AND valid_to IS NULL AND source_key NOT LIKE 'account:001%'`

// refuseLegacyWorld stops an import that would mix CRMArena into the hand-authored fixture world:
// both populate the same accounts, deals and metrics, and the results would be neither.
func refuseLegacyWorld(ctx context.Context, db *sql.DB) error {
	var n int
	if err := db.QueryRowContext(ctx, legacyAccountsQuery).Scan(&n); err != nil {
		return fmt.Errorf("ghostctl: check for legacy fixture accounts: %w", err)
	}
	if n > 0 {
		return fmt.Errorf("ghostctl: the database already holds %d legacy fixture-world account(s); "+
			"import into an empty database, or pass --allow-mixed to mix the two worlds on purpose", n)
	}
	return nil
}

func printBuild(out io.Writer, res crmarena.Result) {
	if len(res.Events) == 0 {
		fmt.Fprintln(out, "built 0 events")
		return
	}
	bySystem := map[string]int{}
	for _, e := range res.Events {
		bySystem[e.Source.SourceSystem]++
	}
	systems := make([]string, 0, len(bySystem))
	for s := range bySystem {
		systems = append(systems, s)
	}
	sort.Strings(systems)
	fmt.Fprintf(out, "built %d events from %s to %s; %d reps\n", len(res.Events),
		res.Epoch().Format(time.DateOnly), res.Events[len(res.Events)-1].OccurredAt().Format(time.DateOnly), res.Reps.Len())
	for _, s := range systems {
		fmt.Fprintf(out, "  %-8s %6d\n", s, bySystem[s])
	}
	st := res.Stats
	fmt.Fprintf(out, "derived account domains %d (without %d); replies linked %d, unlinked %d; deals created at first activity %d\n",
		st.DerivedDomains, st.AccountsWithoutDomain, st.RepliesLinked, st.RepliesUnlinked, st.DealsCreatedAtFirstActivity)
}

// runCRMArenaSplit freezes the previous/current deal split (bench/data/deal_split.json).
func runCRMArenaSplit(args []string, out io.Writer) error {
	opts, rest, err := splitFlags(args)
	if err != nil {
		return err
	}
	if len(rest) != 2 {
		return errors.New(crmarenaSplitUsage)
	}
	snap, err := crmarena.Load(rest[0])
	if err != nil {
		return err
	}
	windows, err := crmarena.Windows(snap)
	if err != nil {
		return err
	}
	split, err := crmarena.NewSplitWith(windows, crmarena.SplitConfig{Cutoff: opts.cutoff, Seed: opts.seed, PreviousMinQuietDays: opts.minQuiet})
	if err != nil {
		return err
	}
	split.Source = crmarena.SplitProvenance{Dataset: crmarenaDataset, Licence: crmarenaLicence, Manifest: fileSHA256(filepath.Join(rest[0], "manifest.json"))}
	raw, err := json.MarshalIndent(split, "", "  ")
	if err != nil {
		return fmt.Errorf("ghostctl: encode split: %w", err)
	}
	if err := os.WriteFile(rest[1], append(raw, '\n'), 0o644); err != nil {
		return fmt.Errorf("ghostctl: write split: %w", err)
	}
	c := split.Counts
	fmt.Fprintf(out, "cutoff %s (previous needs %d quiet days): %d previous, %d ambiguous, %d current (%d replayable), %d future, %d without activity; %d accounts with both\n",
		split.Cutoff, split.PreviousMinQuietDays, c.Previous, c.Ambiguous, c.Current, c.CurrentReplayable, c.Future, c.NoActivity, c.AccountsWithBoth)
	return nil
}

type splitOptions struct {
	cutoff   string
	seed     int64
	minQuiet int
}

func splitFlags(args []string) (splitOptions, []string, error) {
	o := splitOptions{minQuiet: crmarena.DefaultPreviousMinQuietDays}
	var rest []string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--cutoff", "--seed", "--previous-min-quiet-days":
			if i+1 == len(args) {
				return o, nil, errors.New(crmarenaSplitUsage)
			}
			if err := splitFlagValue(&o, args[i], args[i+1]); err != nil {
				return o, nil, err
			}
			i++
		default:
			rest = append(rest, args[i])
		}
	}
	return o, rest, nil
}

// splitFlagValue applies one flag with a value to the options.
func splitFlagValue(o *splitOptions, flag, value string) error {
	if flag == "--cutoff" {
		o.cutoff = value
		return nil
	}
	n, err := strconv.ParseInt(value, 10, 64)
	if err != nil || (flag == "--previous-min-quiet-days" && n < 0) {
		return fmt.Errorf("ghostctl: %s %q is not a valid integer", flag, value)
	}
	if flag == "--seed" {
		o.seed = n
	} else {
		o.minQuiet = int(n)
	}
	return nil
}

// fileSHA256 is the hex digest of a file, or "" when it cannot be read.
func fileSHA256(path string) string {
	raw, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
