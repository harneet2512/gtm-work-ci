// Package controlplane serves the read side of the HAR-145 Ghost Eval Control Plane: the EpisodeSummary and EpisodeTrace of
// a decision episode, the EvalRun list, family summary and run comparison, the episode's KnowledgeMutations and its
// OperationalMetrics (contracts/openapi/core.yaml, tag control-plane). It only reads, and only joins rows that already
// exist: it never runs or recomputes an eval, so there is no eval logic here to drift from the evaluators. EVAL, METRIC
// and TRACE stay separate: results are EvalResults (eval_runs rows), cost and latency are metrics, and the trace is the
// causal chain of rows.
package controlplane

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/harneet2512/gtm-work/core-go/internal/claimstore"
	"github.com/harneet2512/gtm-work/core-go/internal/readmodel"
)

// Reader reads the store. It is safe for concurrent use.
type Reader struct {
	db *sql.DB
}

// New returns a Reader on db.
func New(db *sql.DB) (*Reader, error) {
	if db == nil {
		return nil, errors.New("controlplane: database is required")
	}
	return &Reader{db: db}, nil
}

// snapshot runs fn in one read-only repeatable-read transaction, so a trace or a comparison is one consistent view.
func (r *Reader) snapshot(ctx context.Context, fn func(claimstore.DB) error) error {
	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return fmt.Errorf("controlplane: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	return fn(tx)
}

// notFound is readmodel.ErrNotFound, which the API maps to 404 not_found.
func notFound(what string) error {
	return fmt.Errorf("controlplane: %s: %w", what, readmodel.ErrNotFound)
}

// requireID makes a malformed path id a not-found, like every other id read.
func requireID(kind, id string) error {
	if !readmodel.ValidUUID(id) {
		return notFound(kind + " id is not a uuid")
	}
	return nil
}

// loadResults reads the persisted EvalResults (eval_runs rows) of the runs, oldest first, keyed by run id.
func loadResults(ctx context.Context, db claimstore.DB, runIDs []string) (map[string][]result, error) {
	out := map[string][]result{}
	if len(runIDs) == 0 {
		return out, nil
	}
	rows, err := db.QueryContext(ctx, `SELECT agent_run_id::text, id::text, evaluator::text, verdict, blocking, created_at, phase = 'send'
 FROM eval_runs WHERE agent_run_id = ANY(string_to_array($1, ',')::uuid[]) ORDER BY created_at, draft_index, id`, strings.Join(runIDs, ","))
	if err != nil {
		return nil, fmt.Errorf("controlplane: load eval results: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var run string
		var r result
		if err := rows.Scan(&run, &r.ID, &r.EvalType, &r.Verdict, &r.Blocking, &r.CreatedAt, &r.SendTime); err != nil {
			return nil, fmt.Errorf("controlplane: scan eval result: %w", err)
		}
		r.CreatedAt = r.CreatedAt.UTC()
		out[run] = append(out[run], r)
	}
	return out, rows.Err()
}
