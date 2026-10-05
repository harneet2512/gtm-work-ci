// Package pipeline is WP8 (HAR-106): the coalesce hook that turns each recompute into a StateDiff, the
// signals it emits, a trigger evaluation (eligible or not, with a reason) and, when eligible, an AgentRun
// with its typed steps. Everything runs inside the recompute transaction, so the state, the diff, the
// signals, the decision and the run commit together or not at all, and a retried job re-writes nothing.
//
//	recompute -> StateDiff -> Signals -> TriggerEvaluation -> AgentRun (dry_run by default)
//
// One burst of related activity is one coalesced job (WP6), hence one recompute, one diff, one
// evaluation and at most one run: the unique index on trigger_evaluations(state_diff_id, workflow)
// and the single open run per account (agent_runs_one_open_uniq) hold that line in the database.
package pipeline

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/claims"
	"github.com/harneet2512/gtm-work/core-go/internal/clock"
	"github.com/harneet2512/gtm-work/core-go/internal/reducer"
	"github.com/harneet2512/gtm-work/core-go/internal/runs"
	"github.com/harneet2512/gtm-work/core-go/internal/signals"
	"github.com/harneet2512/gtm-work/core-go/internal/signalstore"
	"github.com/harneet2512/gtm-work/core-go/internal/statediff"
	"github.com/harneet2512/gtm-work/core-go/internal/trigger"
)

// DefaultOwnDomain is our email domain; every other domain is a customer's (matches coalesce).
const DefaultOwnDomain = "vendor.example"

// Options configures a Pipeline. The zero value selects a dry-run pipeline on the wall clock.
type Options struct {
	// RunMode is the mode of runs the pipeline creates: runs.DryRun (default) or runs.Live.
	RunMode string
	// Cooldown is the minimum gap between two eligible evaluations of an account (trigger.DefaultCooldown).
	Cooldown  time.Duration
	OwnDomain string
	Clock     clock.Clock
}

// Pipeline implements coalesce.Hook.
type Pipeline struct {
	mode      string
	cooldown  time.Duration
	ownDomain string
	clk       clock.Clock
}

// New validates the options.
func New(opts Options) (*Pipeline, error) {
	p := &Pipeline{mode: opts.RunMode, cooldown: opts.Cooldown, ownDomain: opts.OwnDomain, clk: opts.Clock}
	if p.mode == "" {
		p.mode = runs.DryRun
	}
	if p.mode != runs.DryRun && p.mode != runs.Live {
		return nil, fmt.Errorf("pipeline: run mode %q is not dry_run or live", p.mode)
	}
	if opts.Cooldown < 0 {
		return nil, errors.New("pipeline: cooldown must not be negative")
	}
	if p.ownDomain == "" {
		p.ownDomain = DefaultOwnDomain
	}
	if p.clk == nil {
		p.clk = clock.Real{}
	}
	return p, nil
}

// AfterRecompute implements coalesce.Hook. It must run in the recompute transaction.
func (p *Pipeline) AfterRecompute(ctx context.Context, tx *sql.Tx, prev *reducer.AccountState, next reducer.AccountState,
	activityIDs []string, conflicts []claims.Conflict) error {
	now := p.clk.Now()
	diff := statediff.Compute(prev, next, activityIDs)
	diffID, inserted, err := signalstore.InsertDiff(ctx, tx, diff, now)
	if err != nil {
		return err
	}
	if !inserted { // this version was already processed: write nothing twice
		return nil
	}
	facts, denied, err := loadFacts(ctx, tx, activityIDs, p.ownDomain)
	if err != nil {
		return err
	}
	emitted := signals.Evaluate(signals.Input{Prev: prev, Next: next, Diff: diff, Activities: facts, Conflicts: conflicts})
	scope := signalstore.Scope{AccountID: next.AccountID, OpportunityID: opportunity(next), StateDiffID: diffID, CreatedAt: now}
	signalIDs, err := signalstore.InsertSignals(ctx, tx, scope, emitted)
	if err != nil {
		return err
	}
	in, err := p.triggerInput(ctx, tx, next, diff, emitted, facts, denied, now)
	if err != nil {
		return err
	}
	decision := trigger.Evaluate(in)
	stored, err := signalstore.InsertEvaluation(ctx, tx, next.AccountID, diffID, decision, signalIDs, now)
	if err != nil || !decision.Eligible || !stored.Inserted {
		return err
	}
	_, _, err = runs.Create(ctx, tx, runs.New{
		AccountID: next.AccountID, OpportunityID: opportunity(next), Mode: p.mode, EvaluationID: stored.ID,
		TriggerActivityIDs: activityIDs, StateVersion: next.Version,
	})
	return err
}

func (p *Pipeline) triggerInput(ctx context.Context, tx *sql.Tx, next reducer.AccountState, diff statediff.Diff,
	emitted []signals.Signal, facts []signals.ActivityFact, denied bool, now time.Time) (trigger.Input, error) {
	open, err := signalstore.OpenRunExists(ctx, tx, next.AccountID)
	if err != nil {
		return trigger.Input{}, err
	}
	last, err := signalstore.LastEligibleAt(ctx, tx, next.AccountID)
	if err != nil {
		return trigger.Input{}, err
	}
	champion, _ := next.Fields.Champion.Value.(string)
	return trigger.Input{
		AccountID: next.AccountID, Diff: diff, Signals: emitted, Activities: facts, OpenRun: open, PermissionDenied: denied,
		ChampionKnown: next.Fields.Champion.Known && champion != claims.Unknown, LastEligibleAt: last, Now: now, Cooldown: p.cooldown,
	}, nil
}

func opportunity(st reducer.AccountState) string {
	if st.OpportunityID == nil {
		return ""
	}
	return *st.OpportunityID
}
