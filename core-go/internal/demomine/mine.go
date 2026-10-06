package demomine

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"path/filepath"
	"sort"

	"github.com/harneet2512/gtm-work/core-go/internal/claims"
	"github.com/harneet2512/gtm-work/core-go/internal/crmarena"
	"github.com/harneet2512/gtm-work/core-go/internal/transitions"
)

// TopCases is how many ranked cases the report lists in full.
const TopCases = 10

// progressEvery is how many events pass between progress lines.
const progressEvery = 500

// MineOptions configure one mining run.
type MineOptions struct {
	// Dir is the snapshot directory (data/crmarena_b2b, or fixtures/crmarena_sample for tests).
	Dir string
	// Split limits the scan to its current deals; nil scans every deal that has events.
	Split *crmarena.Split
	// SplitLabel names where the split came from, for the report.
	SplitLabel string
	Config     Config
	// Rules turns the transition detector on; nil leaves it off.
	Rules *transitions.RuleSet
	// Extractor serves the LLM-extraction claims from recorded cassettes; nil means rule extractors only.
	Extractor *ReplayExtractor
	Date      string
	// Progress receives progress lines (nil discards).
	Progress io.Writer
}

// Mine replays the whole snapshot timeline, in its real order, into db (an empty, private database), records
// every event of every scanned opportunity, ranks the candidate sequences and returns the report. It makes
// no LLM call: the claims come from the CRM-structured events and the existing rules.
func Mine(ctx context.Context, db *sql.DB, o MineOptions) (Report, error) {
	snap, err := crmarena.Load(o.Dir)
	if err != nil {
		return Report{}, err
	}
	res, err := crmarena.Build(snap)
	if err != nil {
		return Report{}, err
	}
	w, err := NewWorld(ctx, db, res, filepath.Join(o.Dir, "User.json"), o.Rules, extractorOrNil(o.Extractor))
	if err != nil {
		return Report{}, err
	}
	scan := scannedDeals(snap, o.Split)
	byDeal := map[string][]EventRecord{}
	for i, e := range res.Events {
		ap, err := w.Apply(ctx, e)
		if err != nil {
			return Report{}, err
		}
		if deal := e.ReplayDeal(); deal != "" && scan[deal] {
			ap.Record.Position = len(byDeal[deal]) + 1
			byDeal[deal] = append(byDeal[deal], ap.Record)
		}
		if o.Progress != nil && (i+1)%progressEvery == 0 {
			fmt.Fprintf(o.Progress, "replayed %d of %d events\n", i+1, len(res.Events))
		}
	}
	seqs := sequences(snap, byDeal)
	cases := o.Config.Rank(seqs)
	rep := buildReport(o, snap, res, scan, seqs, cases)
	if o.Extractor != nil {
		st, err := o.Extractor.Stats(ctx)
		if err != nil {
			return Report{}, err
		}
		rep.Counts.Extractor = &st
	}
	return rep, nil
}

// scannedDeals is the set of opportunities to mine: the split's current deals, else every opportunity.
func scannedDeals(snap crmarena.Snapshot, split *crmarena.Split) map[string]bool {
	if split != nil {
		return split.CurrentSet()
	}
	out := map[string]bool{}
	for _, op := range snap.Opportunities {
		out[op.ID] = true
	}
	return out
}

// sequences builds one Sequence per scanned opportunity that has events, in opportunity id order.
func sequences(snap crmarena.Snapshot, byDeal map[string][]EventRecord) []Sequence {
	names := map[string]string{}
	for _, a := range snap.Accounts {
		names[a.ID] = a.Name
	}
	opps := map[string]crmarena.Opportunity{}
	for _, op := range snap.Opportunities {
		opps[op.ID] = op
	}
	ids := make([]string, 0, len(byDeal))
	for id := range byDeal {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	out := make([]Sequence, 0, len(ids))
	for _, id := range ids {
		op := opps[id]
		out = append(out, Sequence{AccountID: op.AccountID, AccountName: names[op.AccountID],
			OpportunityID: id, OpportunityName: op.Name, Events: byDeal[id]})
	}
	return out
}

// extractorOrNil avoids a typed-nil interface when no extractor is given.
func extractorOrNil(e *ReplayExtractor) claims.Extractor {
	if e == nil {
		return nil
	}
	return e
}
