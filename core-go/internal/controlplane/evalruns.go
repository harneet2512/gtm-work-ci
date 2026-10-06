package controlplane

import (
	"context"
	"fmt"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/claimstore"
	"github.com/harneet2512/gtm-work/core-go/internal/readmodel"
)

// EvalRun is eval_run.v1.json: one agent run's persisted EvalResults, counted. Its id is the agent run id.
type EvalRun struct {
	ID                string       `json:"id"`
	AccountID         string       `json:"account_id"`
	AccountName       string       `json:"account_name"`
	DecisionEpisodeID *string      `json:"decision_episode_id"`
	RunStatus         string       `json:"run_status"`
	EvaluatedAt       time.Time    `json:"evaluated_at"`
	ResultCount       int          `json:"result_count"`
	Counts            Counts       `json:"counts"`
	Areas             []AreaCounts `json:"areas"`
	PreviousEvalRunID *string      `json:"previous_eval_run_id"`
}

// EvalRunPage is GET /eval-runs.
type EvalRunPage struct {
	Items      []EvalRun `json:"items"`
	NextCursor *string   `json:"next_cursor"`
}

// EvalRunFilter selects the EvalRuns of GET /eval-runs; zero values mean no filter, the default limit, the first page.
type EvalRunFilter struct {
	AccountID string
	Limit     int
	Cursor    string
}

const evalRunCursorKind = "eval_runs"

// evalRunRow is one run with results, before its counts are attached.
type evalRunRow struct {
	run       EvalRun
	createdAt time.Time // the run's creation time: the order of runs and of "previous"
}

// evalRunQuery lists runs that have at least one EvalResult, with the previous run of the same account (by run
// creation time, then id): a delta across accounts would mean nothing. $1 limits the account (” = all), $2 limits to
// one run (” = all), $3/$4 are the keyset cursor (created_at, id) the page must be strictly older than.
const evalRunQuery = `
WITH per_run AS (
  SELECT er.agent_run_id AS id, ar.account_id, ar.created_at AS run_created_at, max(er.created_at) AS evaluated_at
    FROM eval_runs er JOIN agent_runs ar ON ar.id = er.agent_run_id
   WHERE ($1 = '' OR ar.account_id = $1::uuid)
   GROUP BY er.agent_run_id, ar.account_id, ar.created_at),
ranked AS (
  SELECT p.*, lag(p.id) OVER (PARTITION BY p.account_id ORDER BY p.run_created_at, p.id) AS previous_id FROM per_run p)
SELECT r.id::text, r.account_id::text, a.name, de.id::text, ar.status, r.evaluated_at, r.run_created_at, r.previous_id::text
  FROM ranked r
  JOIN agent_runs ar ON ar.id = r.id
  JOIN accounts a ON a.id = r.account_id
  LEFT JOIN decision_episodes de ON de.agent_run_id = r.id
 WHERE ($2 = '' OR r.id = $2::uuid)
   AND ($3 = '' OR (r.run_created_at, r.id) < ($4::timestamptz, $3::uuid))
 ORDER BY r.run_created_at DESC, r.id DESC
 LIMIT $5`

func queryEvalRuns(ctx context.Context, db claimstore.DB, accountID, onlyID, afterID string, after time.Time, limit int) ([]evalRunRow, error) {
	rows, err := db.QueryContext(ctx, evalRunQuery, accountID, onlyID, afterID, after, limit)
	if err != nil {
		return nil, fmt.Errorf("controlplane: list eval runs: %w", err)
	}
	defer rows.Close()
	var out []evalRunRow
	for rows.Next() {
		var row evalRunRow
		var episode, previous *string
		if err := rows.Scan(&row.run.ID, &row.run.AccountID, &row.run.AccountName, &episode, &row.run.RunStatus,
			&row.run.EvaluatedAt, &row.createdAt, &previous); err != nil {
			return nil, fmt.Errorf("controlplane: scan eval run: %w", err)
		}
		row.run.DecisionEpisodeID, row.run.PreviousEvalRunID = episode, previous
		row.run.EvaluatedAt, row.createdAt = row.run.EvaluatedAt.UTC(), row.createdAt.UTC()
		out = append(out, row)
	}
	return out, rows.Err()
}

// attachCounts fills each run's counts and areas from its results and its previous run's.
func attachCounts(ctx context.Context, db claimstore.DB, rows []evalRunRow) ([]EvalRun, error) {
	var ids []string
	for _, r := range rows {
		ids = append(ids, r.run.ID)
		if r.run.PreviousEvalRunID != nil {
			ids = append(ids, *r.run.PreviousEvalRunID)
		}
	}
	results, err := loadResults(ctx, db, ids)
	if err != nil {
		return nil, err
	}
	out := make([]EvalRun, 0, len(rows))
	for _, r := range rows {
		run := r.run
		cur := results[run.ID]
		var prev []result
		if run.PreviousEvalRunID != nil {
			prev = results[*run.PreviousEvalRunID]
		}
		run.Counts, run.ResultCount = tally(cur), len(cur)
		run.Areas = areaCounts(cur, prev, run.PreviousEvalRunID != nil)
		out = append(out, run)
	}
	return out, nil
}

// ListEvalRuns returns one page of EvalRuns, newest run first. ErrInvalid: a malformed account id, a limit out of range
// or a bad cursor.
func (r *Reader) ListEvalRuns(ctx context.Context, f EvalRunFilter) (EvalRunPage, error) {
	n, err := readmodel.NormalizeLimit(f.Limit)
	if err != nil {
		return EvalRunPage{}, err
	}
	if f.AccountID != "" && !readmodel.ValidUUID(f.AccountID) {
		return EvalRunPage{}, fmt.Errorf("account_id is not a uuid: %w", readmodel.ErrInvalid)
	}
	var at time.Time
	var afterID string
	if f.Cursor != "" {
		key, err := readmodel.DecodeCursor(evalRunCursorKind, f.Cursor, 2)
		if err != nil {
			return EvalRunPage{}, err
		}
		if at, err = time.Parse(time.RFC3339Nano, key[0]); err != nil || !readmodel.ValidUUID(key[1]) {
			return EvalRunPage{}, fmt.Errorf("cursor does not belong to this list: %w", readmodel.ErrInvalid)
		}
		afterID = key[1]
	}
	var page EvalRunPage
	err = r.snapshot(ctx, func(db claimstore.DB) error {
		rows, err := queryEvalRuns(ctx, db, f.AccountID, "", afterID, at, n+1)
		if err != nil {
			return err
		}
		more := len(rows) > n
		if more {
			rows = rows[:n]
		}
		if page.Items, err = attachCounts(ctx, db, rows); err != nil {
			return err
		}
		if more {
			last := rows[n-1]
			next := readmodel.EncodeCursor(evalRunCursorKind, last.createdAt.Format(time.RFC3339Nano), last.run.ID)
			page.NextCursor = &next
		}
		return nil
	})
	if page.Items == nil {
		page.Items = []EvalRun{}
	}
	return page, err
}

// oneEvalRun is the single-run read inside a snapshot: ErrNotFound when the run does not exist or has no EvalResults.
func oneEvalRun(ctx context.Context, db claimstore.DB, id string) (EvalRun, error) {
	if err := requireID("eval run", id); err != nil {
		return EvalRun{}, err
	}
	var account string
	if err := db.QueryRowContext(ctx, `SELECT coalesce((SELECT account_id::text FROM agent_runs WHERE id = $1::uuid), '')`, id).Scan(&account); err != nil {
		return EvalRun{}, fmt.Errorf("controlplane: account of run: %w", err)
	}
	if account == "" {
		return EvalRun{}, notFound("eval run")
	}
	rows, err := queryEvalRuns(ctx, db, account, id, "", time.Time{}, 1)
	if err != nil {
		return EvalRun{}, err
	}
	if len(rows) == 0 {
		return EvalRun{}, notFound("eval run has no results")
	}
	runs, err := attachCounts(ctx, db, rows)
	if err != nil {
		return EvalRun{}, err
	}
	return runs[0], nil
}

// EvalRun returns one EvalRun. ErrNotFound: no such run, or the run has no EvalResults.
func (r *Reader) EvalRun(ctx context.Context, id string) (EvalRun, error) {
	var run EvalRun
	err := r.snapshot(ctx, func(db claimstore.DB) error {
		var err error
		run, err = oneEvalRun(ctx, db, id)
		return err
	})
	return run, err
}

// FamilySummary is eval_family_summary.v1.json.
type FamilySummary struct {
	EvalRunID         string       `json:"eval_run_id"`
	PreviousEvalRunID *string      `json:"previous_eval_run_id"`
	Areas             []FamilyArea `json:"areas"`
}

// EvalFamilies returns the run's results grouped into areas, families and eval types, with deltas against the account's
// previous EvalRun. ErrNotFound: no such run, or the run has no EvalResults.
func (r *Reader) EvalFamilies(ctx context.Context, id string) (FamilySummary, error) {
	var out FamilySummary
	err := r.snapshot(ctx, func(db claimstore.DB) error {
		run, err := oneEvalRun(ctx, db, id)
		if err != nil {
			return err
		}
		ids := []string{run.ID}
		if run.PreviousEvalRunID != nil {
			ids = append(ids, *run.PreviousEvalRunID)
		}
		results, err := loadResults(ctx, db, ids)
		if err != nil {
			return err
		}
		var prev []result
		if run.PreviousEvalRunID != nil {
			prev = results[*run.PreviousEvalRunID]
		}
		out = FamilySummary{EvalRunID: run.ID, PreviousEvalRunID: run.PreviousEvalRunID,
			Areas: familySummary(results[run.ID], prev, run.PreviousEvalRunID != nil)}
		return nil
	})
	return out, err
}
