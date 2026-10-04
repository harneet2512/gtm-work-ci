package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/crmarena"
	"github.com/harneet2512/gtm-work/core-go/internal/normalize"
)

const (
	importCRMArenaUsage = "usage: ghostctl import-crmarena [--dry-run] [--allow-mixed] [--synthetic-covered <covered_deals.json>] (--until <RFC3339 cutoff> | --full-timeline) <export-dir>"
	replayCRMArenaUsage = "usage: ghostctl replay-crmarena [--split <split.json>] [--deal <id>] [--from <RFC3339>] [--to <RFC3339>] [--out <file>] [--skip-store-check] [--synthetic-covered <covered_deals.json>] <export-dir>"
)

// importOptions are the parsed flags of import-crmarena.
type importOptions struct {
	dir          string
	dryRun       bool
	allowMixed   bool
	fullTimeline bool
	until        time.Time
	coveredPath  string // --synthetic-covered: deals whose stage path the synthetic layer supplies (WP32)
}

// timelineLimited reports whether only the events before the cutoff are ingested.
func (o importOptions) timelineLimited() bool { return !o.until.IsZero() }

// parseImportFlags requires an explicit choice of timeline. Ingesting the whole timeline puts every
// current deal's later events, final stage and quote status in the store, where AccountState would show
// an agent the outcome, so it takes --full-timeline and the default for HAR-129 runs is --until the cutoff.
func parseImportFlags(args []string) (importOptions, error) {
	var o importOptions
	var rest []string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--dry-run":
			o.dryRun = true
		case "--allow-mixed":
			o.allowMixed = true
		case "--full-timeline":
			o.fullTimeline = true
		case "--synthetic-covered":
			if i+1 == len(args) {
				return o, errors.New(importCRMArenaUsage)
			}
			o.coveredPath = args[i+1]
			i++
		case "--until":
			if i+1 == len(args) {
				return o, errors.New(importCRMArenaUsage)
			}
			at, err := time.Parse(time.RFC3339, args[i+1])
			if err != nil {
				return o, fmt.Errorf("ghostctl: --until %q is not an RFC 3339 time (e.g. 2023-11-01T00:00:00Z): %w", args[i+1], err)
			}
			o.until = at.UTC()
			i++
		default:
			rest = append(rest, args[i])
		}
	}
	if len(rest) != 1 {
		return o, errors.New(importCRMArenaUsage)
	}
	o.dir = rest[0]
	switch {
	case o.fullTimeline && !o.until.IsZero():
		return o, errors.New("ghostctl: --until and --full-timeline are mutually exclusive")
	case !o.fullTimeline && o.until.IsZero():
		return o, errors.New("ghostctl: import-crmarena needs --until <RFC3339 cutoff> (the frozen split's cutoff for HAR-129 runs) or " +
			"--full-timeline: the full timeline stores every current deal's final stage and quote status, i.e. the outcome")
	}
	return o, nil
}

// withholdAfter keeps the events dated before until, with later date-valued payload fields removed.
func withholdAfter(res crmarena.Result, until time.Time, out io.Writer) (crmarena.Result, error) {
	kept, red, err := crmarena.AsOf(res.Events, until)
	if err != nil {
		return res, err
	}
	fmt.Fprintf(out, "timeline cut at %s: %d of %d events kept, %d withheld; %d date fields at or after the cutoff removed from %d events\n",
		until.Format(time.RFC3339), len(kept), len(res.Events), len(res.Events)-len(kept), red.Fields, red.Events)
	res.Events = kept
	return res, nil
}

// replayOptions are the parsed flags of replay-crmarena.
type replayOptions struct {
	dir, splitPath, deal, outPath string
	coveredPath                   string // --synthetic-covered (WP32)
	from, to                      time.Time
	skipStoreCheck                bool
}

func parseReplayFlags(args []string) (replayOptions, error) {
	var o replayOptions
	var rest []string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--skip-store-check":
			o.skipStoreCheck = true
		case "--split", "--deal", "--out", "--from", "--to", "--synthetic-covered":
			if i+1 == len(args) {
				return o, errors.New(replayCRMArenaUsage)
			}
			if err := o.set(args[i], args[i+1]); err != nil {
				return o, err
			}
			i++
		default:
			rest = append(rest, args[i])
		}
	}
	if len(rest) != 1 || (o.deal != "" && o.splitPath == "") {
		return o, errors.New(replayCRMArenaUsage)
	}
	o.dir = rest[0]
	return o, nil
}

func (o *replayOptions) set(flag, value string) error {
	var err error
	switch flag {
	case "--split":
		o.splitPath = value
	case "--deal":
		o.deal = value
	case "--out":
		o.outPath = value
	case "--synthetic-covered":
		o.coveredPath = value
	case "--from":
		o.from, err = time.Parse(time.RFC3339, value)
	case "--to":
		o.to, err = time.Parse(time.RFC3339, value)
	}
	if err != nil {
		return fmt.Errorf("ghostctl: %s %q is not an RFC 3339 time: %w", flag, value, err)
	}
	return nil
}

// runReplayCRMArena writes the events after the cutoff, in ingest order, as a JSON array that
// `ghostctl ingest` accepts: the stream an agent is replayed on, change by change, after
// `import-crmarena --until <cutoff>`. With --split only the current deals' events are emitted (one
// deal with --deal) and --from defaults to the split's cutoff; without it --from is required and every
// event of the window is emitted.
func runReplayCRMArena(args []string, out io.Writer) error {
	o, err := parseReplayFlags(args)
	if err != nil {
		return err
	}
	events, err := replayEvents(o)
	if err != nil {
		return err
	}
	sources := make([]normalize.SourceEvent, len(events))
	for i, e := range events {
		sources[i] = e.Source
	}
	raw, err := json.MarshalIndent(sources, "", "  ")
	if err != nil {
		return fmt.Errorf("ghostctl: encode replay: %w", err)
	}
	raw = append(raw, '\n')
	if o.outPath == "" {
		_, err = out.Write(raw)
		return err
	}
	if err := os.WriteFile(o.outPath, raw, 0o644); err != nil {
		return fmt.Errorf("ghostctl: write replay: %w", err)
	}
	fmt.Fprintf(out, "wrote %d events to %s\n", len(events), o.outPath)
	return nil
}

func replayEvents(o replayOptions) ([]crmarena.Event, error) {
	snap, err := crmarena.Load(o.dir)
	if err != nil {
		return nil, err
	}
	res, err := crmarena.Build(snap)
	if err != nil {
		return nil, err
	}
	if res, err = supersede(res, o.coveredPath, os.Stderr); err != nil {
		return nil, err
	}
	if o.splitPath == "" {
		if o.from.IsZero() {
			return nil, errors.New("ghostctl: replay-crmarena needs --from, or --split to take the cutoff from")
		}
		if err := checkNoGap(res.Events, o.from, o.skipStoreCheck, os.Stderr); err != nil {
			return nil, err
		}
		return crmarena.Between(res.Events, o.from, o.to), nil
	}
	split, err := readSplit(o.splitPath)
	if err != nil {
		return nil, err
	}
	from := o.from
	if from.IsZero() {
		if from, err = split.CutoffTime(); err != nil {
			return nil, err
		}
	}
	if err := checkNoGap(res.Events, from, o.skipStoreCheck, os.Stderr); err != nil {
		return nil, err
	}
	current := split.CurrentSet()
	if o.deal != "" && !current[o.deal] {
		return nil, fmt.Errorf("ghostctl: deal %s is not a current deal of %s", o.deal, o.splitPath)
	}
	wanted := current
	if o.deal != "" {
		wanted = map[string]bool{o.deal: true}
	}
	var mine []crmarena.Event
	for _, e := range res.Events {
		if e.ReplayedWith(wanted) {
			mine = append(mine, e)
		}
	}
	return crmarena.Between(mine, from, o.to), nil
}

func readSplit(path string) (crmarena.Split, error) {
	var s crmarena.Split
	raw, err := os.ReadFile(path)
	if err != nil {
		return s, fmt.Errorf("ghostctl: read split: %w", err)
	}
	if err := json.Unmarshal(raw, &s); err != nil {
		return s, fmt.Errorf("ghostctl: decode split %s: %w", path, err)
	}
	return s, nil
}
