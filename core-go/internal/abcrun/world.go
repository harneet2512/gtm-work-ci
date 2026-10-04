package abcrun

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/claims"
	"github.com/harneet2512/gtm-work/core-go/internal/clock"
	"github.com/harneet2512/gtm-work/core-go/internal/coalesce"
	"github.com/harneet2512/gtm-work/core-go/internal/graph"
	"github.com/harneet2512/gtm-work/core-go/internal/ingest"
)

// WorkerID names the recompute claimer so a reproduced run claims the same way.
const WorkerID = "abcrun"

// Builder turns a situation's events into a database world and an open agent run, through the real ingest and
// recompute path. Only the extractor is replaced (TruthExtractor), only for the claims; stage and roles come from the
// deterministic CRM rules exactly as in production.
type Builder struct {
	db      *sql.DB
	svc     *ingest.Service
	co      *coalesce.Service
	extract *swapExtractor
	clk     *clock.Fixed
}

// NewBuilder wires ingest and recompute on a fixed replay clock.
func NewBuilder(db *sql.DB, replayNow time.Time) (*Builder, error) {
	b := &Builder{db: db, extract: &swapExtractor{}, clk: clock.NewFixed(replayNow)}
	var err error
	if b.svc, err = ingest.NewService(db, ingest.Options{Clock: b.clk, Debounce: time.Second, MaxWait: time.Second, Extension: graph.NewExtension()}); err != nil {
		return nil, err
	}
	b.co, err = coalesce.New(db, coalesce.Options{Clock: clock.NewFixed(replayNow.Add(time.Hour)), WorkerID: WorkerID, Extractor: b.extract})
	return b, err
}

// Ingest is the ingest service the pull API needs.
func (b *Builder) Ingest() *ingest.Service { return b.svc }

// SeedCompany seeds the vendor's employees once, before any situation.
func (b *Builder) SeedCompany(ctx context.Context, c graph.Company, at time.Time) error {
	if err := SeedIDs(ctx, b.db, "company"); err != nil {
		return err
	}
	_, err := graph.SeedCompany(ctx, b.db, c, at)
	return err
}

// Ref is what a built situation hands the runner.
type Ref struct {
	AccountID     string
	OpportunityID string
	TriggerID     string // the trigger activity
	StateVersion  int
}

// Build ingests the situation's events in order and folds them into account state. The runs are opened per arm (OpenRun).
func (b *Builder) Build(ctx context.Context, s Situation) (Ref, error) {
	b.extract.set(TruthExtractor{Claims: s.Truth})
	var trigger string
	for i, ev := range s.Events {
		res, err := b.svc.Ingest(ctx, ev)
		if err != nil {
			return Ref{}, fmt.Errorf("abcrun: situation %s event %d (%s/%s/%s): %w", s.ID, i, ev.SourceSystem, ev.SourceObjectID, ev.SourceEventKey, err)
		}
		if ev.SourceObjectID == s.Trigger.SourceObjectID && ev.SourceEventKey == s.Trigger.SourceEventKey {
			trigger = res.ActivityID
		}
	}
	if trigger == "" {
		return Ref{}, fmt.Errorf("abcrun: situation %s: the trigger event was not ingested", s.ID)
	}
	if _, err := b.co.Drain(ctx); err != nil {
		return Ref{}, fmt.Errorf("abcrun: situation %s: recompute: %w", s.ID, err)
	}
	return b.readRef(ctx, s, trigger)
}

// RunSteps are the five steps of a dry-run agent run.
var runSteps = []string{"build_context", "draft", "crm_intent", "await_human", "execute"}

func (b *Builder) readRef(ctx context.Context, s Situation, trigger string) (Ref, error) {
	ref := Ref{TriggerID: trigger}
	var opp sql.NullString
	err := b.db.QueryRowContext(ctx, `SELECT a.account_id::text, a.opportunity_id::text FROM activities a WHERE a.id = $1::uuid`, trigger).Scan(&ref.AccountID, &opp)
	if err != nil {
		return ref, fmt.Errorf("abcrun: situation %s: read the trigger activity: %w", s.ID, err)
	}
	ref.OpportunityID = opp.String
	if err := b.db.QueryRowContext(ctx, `SELECT version FROM account_state WHERE account_id = $1::uuid`, ref.AccountID).Scan(&ref.StateVersion); err != nil {
		return ref, fmt.Errorf("abcrun: situation %s has no account state after recompute: %w", s.ID, err)
	}
	return ref, nil
}

// OpenRun inserts the run for a built situation: an eligible trigger evaluation and a dry-run AgentRun with its steps.
// Called once per arm that needs its own run (B, C); the account must have no other open run.
func (b *Builder) OpenRun(ctx context.Context, ref Ref) (string, error) {
	var evalID, runID string
	if err := b.db.QueryRowContext(ctx, `INSERT INTO trigger_evaluations (account_id, workflow, eligible, reason_codes, explanation)
 VALUES ($1::uuid, 'post_interaction_followup', true, ARRAY['eligible_customer_replied'], 'a customer email opened the run')
 RETURNING id::text`, ref.AccountID).Scan(&evalID); err != nil {
		return "", fmt.Errorf("abcrun: insert trigger evaluation: %w", err)
	}
	var opp any
	if ref.OpportunityID != "" {
		opp = ref.OpportunityID
	}
	if err := b.db.QueryRowContext(ctx, `INSERT INTO agent_runs (account_id, opportunity_id, workflow, run_mode, status, trigger_evaluation_id, trigger_activity_ids, state_version)
 VALUES ($1::uuid, $2::uuid, 'post_interaction_followup', 'dry_run', 'pending', $3::uuid, ARRAY[$4::uuid], $5) RETURNING id::text`,
		ref.AccountID, opp, evalID, ref.TriggerID, ref.StateVersion).Scan(&runID); err != nil {
		return "", fmt.Errorf("abcrun: insert run: %w", err)
	}
	for i, step := range runSteps {
		if _, err := b.db.ExecContext(ctx, `INSERT INTO agent_run_steps (agent_run_id, seq, step, run_mode, status) VALUES ($1::uuid, $2, $3, 'dry_run', 'pending')`,
			runID, i+1, step); err != nil {
			return "", fmt.Errorf("abcrun: insert step %s: %w", step, err)
		}
	}
	return runID, nil
}

// CancelRun frees the account for the next arm's run.
func (b *Builder) CancelRun(ctx context.Context, runID string) error {
	_, err := b.db.ExecContext(ctx, `UPDATE agent_runs SET status = 'cancelled', updated_at = now() WHERE id = $1::uuid`, runID)
	return err
}

// swapExtractor lets one recompute service serve every situation: the truth table changes per situation.
type swapExtractor struct{ cur TruthExtractor }

func (s *swapExtractor) set(t TruthExtractor) { s.cur = t }

func (s *swapExtractor) Extract(ctx context.Context, req claims.ExtractRequest) (claims.ExtractResponse, error) {
	return s.cur.Extract(ctx, req)
}
