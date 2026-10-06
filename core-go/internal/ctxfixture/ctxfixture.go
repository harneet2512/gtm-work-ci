// Package ctxfixture builds the database world the context API tests read: the CRMArena sample
// (fixtures/crmarena_sample) ingested and recomputed through the real pipeline, then agent runs
// inserted by hand (HAR-106 owns writing runs, signals and diffs in production). It is test support
// only; nothing in cmd/ imports it.
package ctxfixture

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/harneet2512/gtm-work/core-go/internal/coalesce"
	"github.com/harneet2512/gtm-work/core-go/internal/crmarena"
	"github.com/harneet2512/gtm-work/core-go/internal/graph"
	"github.com/harneet2512/gtm-work/core-go/internal/ingest"
)

// SampleDir finds fixtures/crmarena_sample by walking up from the working directory.
func SampleDir() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		cand := filepath.Join(dir, "fixtures", "crmarena_sample")
		if st, err := os.Stat(filepath.Join(cand, "Account.json")); err == nil && !st.IsDir() {
			return cand, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", errors.New("ctxfixture: fixtures/crmarena_sample not found above the working directory")
		}
		dir = parent
	}
}

// World is the loaded database: two accounts that have state, each with one run.
type World struct {
	AccountA, AccountB string
	RunA, RunB         string
	TriggerA, TriggerB string // the trigger activity of each run
}

// Load ingests the sample, drains the recompute outbox and inserts one open run per account for the
// two accounts with the most activity. The database must be empty and migrated.
func Load(ctx context.Context, db *sql.DB, sampleDir string) (*World, error) {
	snap, err := crmarena.Load(sampleDir)
	if err != nil {
		return nil, err
	}
	res, err := crmarena.Build(snap)
	if err != nil {
		return nil, err
	}
	company := res.Reps.Company(filepath.Join(sampleDir, "User.json"))
	if _, err := graph.SeedCompany(ctx, db, company, res.Epoch()); err != nil {
		return nil, err
	}
	svc, err := ingest.NewService(db, ingest.Options{Extension: graph.NewExtension()})
	if err != nil {
		return nil, err
	}
	named := make([]ingest.NamedEvent, len(res.Events))
	for i, e := range res.Events {
		named[i] = ingest.NamedEvent{File: "crmarena", Index: i, Event: e.Source}
	}
	if _, err := ingest.IngestAll(ctx, svc, named); err != nil {
		return nil, err
	}
	co, err := coalesce.New(db, coalesce.Options{})
	if err != nil {
		return nil, err
	}
	if _, err := co.Drain(ctx); err != nil {
		return nil, err
	}
	return seedRuns(ctx, db)
}

func seedRuns(ctx context.Context, db *sql.DB) (*World, error) {
	rows, err := db.QueryContext(ctx, `SELECT s.account_id::text FROM account_state s
 JOIN activities a ON a.account_id = s.account_id GROUP BY s.account_id ORDER BY count(*) DESC, s.account_id LIMIT 2`)
	if err != nil {
		return nil, fmt.Errorf("ctxfixture: pick accounts: %w", err)
	}
	var accounts []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return nil, err
		}
		accounts = append(accounts, id)
	}
	_ = rows.Close()
	if len(accounts) < 2 {
		return nil, errors.New("ctxfixture: the sample produced fewer than two accounts with state")
	}
	w := &World{AccountA: accounts[0], AccountB: accounts[1]}
	if w.RunA, w.TriggerA, err = InsertRun(ctx, db, w.AccountA, "context_built"); err != nil {
		return nil, err
	}
	w.RunB, w.TriggerB, err = InsertRun(ctx, db, w.AccountB, "context_built")
	return w, err
}

// InsertRun inserts an eligible trigger evaluation and one dry_run agent run for the account, at
// its current state version, triggered by the account's newest activity (every activity at that instant, so
// no non-trigger activity ties with the trigger: such a tie makes a pull fail closed, ADR-0019). It returns the
// run id and the trigger activity id (the highest id among them). The account must have no other open run.
func InsertRun(ctx context.Context, db *sql.DB, accountID, status string) (runID, triggerID string, err error) {
	var version int
	var triggerIDs string
	if err = db.QueryRowContext(ctx, `WITH newest AS (SELECT max(occurred_at) AS at FROM activities WHERE account_id = $1::uuid)
SELECT s.version,
       (SELECT string_agg(a.id::text, ',' ORDER BY a.id DESC) FROM activities a, newest WHERE a.account_id = s.account_id AND a.occurred_at = newest.at)
  FROM account_state s WHERE s.account_id = $1::uuid`, accountID).Scan(&version, &triggerIDs); err != nil {
		return "", "", fmt.Errorf("ctxfixture: account %s has no state: %w", accountID, err)
	}
	var evalID string
	if err = db.QueryRowContext(ctx, `INSERT INTO trigger_evaluations (account_id, workflow, eligible, reason_codes, explanation)
 VALUES ($1::uuid, 'post_interaction_followup', true, ARRAY['eligible_customer_replied'], 'test fixture') RETURNING id::text`, accountID).Scan(&evalID); err != nil {
		return "", "", fmt.Errorf("ctxfixture: insert trigger evaluation: %w", err)
	}
	if err = db.QueryRowContext(ctx, `INSERT INTO agent_runs (account_id, workflow, run_mode, status, trigger_evaluation_id, trigger_activity_ids, state_version)
 VALUES ($1::uuid, 'post_interaction_followup', 'dry_run', $2, $3::uuid, string_to_array($4, ',')::uuid[], $5) RETURNING id::text`,
		accountID, status, evalID, triggerIDs, version).Scan(&runID); err != nil {
		return "", "", fmt.Errorf("ctxfixture: insert run: %w", err)
	}
	triggerID, _, _ = strings.Cut(triggerIDs, ",")
	return runID, triggerID, nil
}

var (
	once      sync.Once
	shared    *World
	sharedErr error
)

// Get loads the world once per test binary (the sample ingest takes seconds) and returns it.
func Get(t testing.TB, db *sql.DB) *World {
	t.Helper()
	once.Do(func() {
		dir, err := SampleDir()
		if err != nil {
			sharedErr = err
			return
		}
		shared, sharedErr = Load(context.Background(), db, dir)
	})
	if sharedErr != nil {
		t.Fatalf("load the CRMArena sample world: %v", sharedErr)
	}
	return shared
}

// FreshRun cancels the account's open runs and inserts a new one with the given status, for tests
// that change a run's status or fill its pull budget.
func FreshRun(t testing.TB, db *sql.DB, accountID, status string) string {
	t.Helper()
	ctx := context.Background()
	if _, err := db.ExecContext(ctx, `UPDATE agent_runs SET status = 'cancelled', updated_at = now()
 WHERE account_id = $1::uuid AND status IN ('pending', 'context_built', 'drafted', 'awaiting_human', 'approved', 'edited')`, accountID); err != nil {
		t.Fatalf("cancel open runs: %v", err)
	}
	id, _, err := InsertRun(ctx, db, accountID, status)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// SetStatus moves a run to another status.
func SetStatus(ctx context.Context, db *sql.DB, runID, status string) error {
	_, err := db.ExecContext(ctx, `UPDATE agent_runs SET status = $2, updated_at = now() WHERE id = $1::uuid`, runID, status)
	return err
}
