// Package usagestore stores what the model worker spent per call (contracts/schemas/worker_usage.v1.json) in
// run_model_usage (migration 0034), per run and run step, for the HAR-145 OperationalMetrics read. These are METRICS
// (HAR-97 M1-M5), never evals: nothing here has a verdict, and no evaluator reads it.
//
// The workerclient reports usage to a sink on the call's context. A Collector is the sink: it only gathers records in
// memory, because some worker calls happen inside a database transaction that holds the run's row locks (the delta
// labeler runs inside the send transaction) and an insert from another connection would wait on them. The owner of the
// call flushes the collector after its transaction has ended, with Flush.
package usagestore

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"regexp"
	"sync"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/workerclient"
)

var uuidPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// flushTimeout bounds one Flush: the calls were already paid for, so a flush outlives a cancelled caller (the run that
// failed is exactly the one whose spend must be kept) but never hangs.
const flushTimeout = 10 * time.Second

// Collector is a workerclient.UsageSink that gathers records until Flush. It is safe for concurrent use.
type Collector struct {
	mu   sync.Mutex
	recs []workerclient.UsageRecord
}

// RecordUsage implements workerclient.UsageSink.
func (c *Collector) RecordUsage(r workerclient.UsageRecord) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.recs = append(c.recs, r)
}

// Records returns a copy of what has been collected and not yet flushed.
func (c *Collector) Records() []workerclient.UsageRecord {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]workerclient.UsageRecord(nil), c.recs...)
}

// take empties the collector and returns what it held.
func (c *Collector) take() []workerclient.UsageRecord {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := c.recs
	c.recs = nil
	return out
}

// Store writes usage rows.
type Store struct {
	db  *sql.DB
	log *slog.Logger
}

// New returns a Store on db. A nil logger discards logs.
func New(db *sql.DB, log *slog.Logger) (*Store, error) {
	if db == nil {
		return nil, errors.New("usagestore: database is required")
	}
	if log == nil {
		log = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return &Store{db: db, log: log}, nil
}

// Flush stores every record the collector holds under the run and run step, then empties it, so a record is never
// stored twice. It runs on a context detached from the caller's cancellation. Each record is its own statement: a
// refused record (an unknown stage or run, no usage source) is reported in the returned error but never costs the
// others their rows, and the collector is left empty either way, so a bad record cannot make the next flush fail too.
func (s *Store) Flush(ctx context.Context, runID, runStep string, c *Collector) error {
	recs := c.take()
	if len(recs) == 0 {
		return nil
	}
	if !uuidPattern.MatchString(runID) {
		return fmt.Errorf("usagestore: run id %q is not a uuid", runID)
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), flushTimeout)
	defer cancel()
	var errs []error
	for _, r := range recs {
		if err := insert(ctx, s.db, runID, runStep, r); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// execer is what insert needs of a database.
type execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// normalize makes a worker's usage fit what the table allows without losing the row. A worker is not trusted to be
// consistent: a replayed call spent nothing, whatever it sent; cached and reasoning tokens are clamped to their totals;
// and a record with no model call keeps no tokens and no cost. A figure the worker did not report stays nil.
func normalize(u workerclient.Usage) workerclient.Usage {
	if u.UsageSource == workerclient.UsageReplay {
		u.ModelCalls, u.InputTokens, u.OutputTokens, u.CachedInputTokens, u.ReasoningTokens, u.CostUSD = 0, 0, 0, nil, nil, nil
	}
	if u.ModelCalls == 0 {
		u.InputTokens, u.OutputTokens, u.CostUSD = 0, 0, nil
	}
	u.CachedInputTokens = clamp(u.CachedInputTokens, u.InputTokens)
	u.ReasoningTokens = clamp(u.ReasoningTokens, u.OutputTokens)
	return u
}

func clamp(p *int64, ceiling int64) *int64 {
	if p == nil {
		return nil
	}
	v := min(*p, ceiling)
	return &v
}

// insert writes one record.
func insert(ctx context.Context, db execer, runID, runStep string, r workerclient.UsageRecord) error {
	u := normalize(r.Usage)
	models, err := json.Marshal(nonNil(u.Models))
	if err != nil {
		return fmt.Errorf("usagestore: encode models: %w", err)
	}
	var step any
	if runStep != "" {
		step = runStep
	}
	_, err = db.ExecContext(ctx, `INSERT INTO run_model_usage (agent_run_id, run_step, stage, models, model_calls, input_tokens, output_tokens,
 cached_input_tokens, reasoning_tokens, tool_calls, retries, cost_usd, model_ms, wall_ms, usage_source)
 VALUES ($1::uuid, $2, $3, ARRAY(SELECT jsonb_array_elements_text($4::jsonb)), $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15)`,
		runID, step, r.Stage, string(models), u.ModelCalls, u.InputTokens, u.OutputTokens, u.CachedInputTokens, u.ReasoningTokens,
		u.ToolCalls, u.Retries, u.CostUSD, u.ModelMs, max(r.WallMs, 0), u.UsageSource)
	if err != nil {
		return fmt.Errorf("usagestore: store %s usage of run %s: %w", r.Stage, runID, err)
	}
	return nil
}

func nonNil(xs []string) []string {
	if xs == nil {
		return []string{}
	}
	return xs
}
