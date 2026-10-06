package learning

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/knowledge"
	"github.com/harneet2512/gtm-work/core-go/internal/knowledgestore"
)

// Transition is one eval_promotions audit row.
type Transition struct {
	Evaluator, FromStatus, ToStatus string
	Version                         int
	Reason                          string
}

// Advance moves a candidate to shadow when its latest backtest passed the gate. The transition is
// recorded in eval_promotions; the SQL trigger enforces the candidate->shadow legality.
func Advance(ctx context.Context, db *sql.DB, evaluator string, version int, decidedBy string) (Transition, error) {
	return AdvanceAt(ctx, db, evaluator, version, decidedBy, time.Time{}, false)
}

// AdvanceAt is Advance with the audit row stamped at at, the replay's world time; a replay with a zero at
// is refused (knowledgestore.ErrCreatedAtRequired) instead of falling back to the database clock.
func AdvanceAt(ctx context.Context, db *sql.DB, evaluator string, version int, decidedBy string, at time.Time, replay bool) (Transition, error) {
	if err := requireWorldTime(replay, at, fmt.Sprintf("%s:v%d", evaluator, version)); err != nil {
		return Transition{}, err
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return Transition{}, err
	}
	defer func() { _ = tx.Rollback() }()
	runID, passed, err := latestBacktest(ctx, tx, evaluator, version)
	if err != nil {
		return Transition{}, err
	}
	if runID == "" {
		return Transition{}, gateReason(false, "%s:v%d has no backtest run; run ghostctl learn backtest first", evaluator, version)
	}
	if !passed {
		return Transition{}, gateReason(false, "%s:v%d's latest backtest did not pass", evaluator, version)
	}
	t, err := transitionAt(ctx, tx, evaluator, version, "candidate", "shadow", &runID, decidedBy,
		fmt.Sprintf("latest backtest %s passed the gate", runID), at)
	if err != nil {
		return Transition{}, err
	}
	return t, tx.Commit()
}

// Promote activates a shadow version: the knowledge the candidate seeded must have earned an applicable
// lifecycle status (provisional or better — evidence, not the seed alone, moved it there), it must not
// be disputed or stale, and the latest backtest must have passed. promote_evaluator_version retires the
// previous active version atomically; the deterministic engine then emits the bumped '<type>:v<N>'.
func Promote(ctx context.Context, db *sql.DB, evaluator string, version int, rules knowledge.Rules, decidedBy string) (Transition, error) {
	return PromoteAt(ctx, db, evaluator, version, rules, decidedBy, time.Time{}, false)
}

// PromoteAt is Promote with promoted_at and the audit row stamped at at, the replay's world time (HAR-97
// B9: never now() in a replay); a replay with a zero at is refused.
func PromoteAt(ctx context.Context, db *sql.DB, evaluator string, version int, rules knowledge.Rules, decidedBy string, at time.Time, replay bool) (Transition, error) {
	if err := requireWorldTime(replay, at, fmt.Sprintf("%s:v%d", evaluator, version)); err != nil {
		return Transition{}, err
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return Transition{}, err
	}
	defer func() { _ = tx.Rollback() }()
	var status string
	var kid *string
	err = tx.QueryRowContext(ctx, `SELECT status, knowledge_id::text FROM evaluator_versions
 WHERE evaluator = $1 AND version = $2 FOR UPDATE`, evaluator, version).Scan(&status, &kid)
	if errors.Is(err, sql.ErrNoRows) {
		return Transition{}, fmt.Errorf("%w: %s:v%d", ErrNotFound, evaluator, version)
	}
	if err != nil {
		return Transition{}, fmt.Errorf("learning: load %s:v%d: %w", evaluator, version, err)
	}
	if status != "shadow" {
		return Transition{}, gateReason(false, "%s:v%d is %s, not shadow", evaluator, version, status)
	}
	runID, passed, err := latestBacktest(ctx, tx, evaluator, version)
	if err != nil {
		return Transition{}, err
	}
	if runID == "" || !passed {
		return Transition{}, gateReason(false, "%s:v%d has no passing backtest", evaluator, version)
	}
	if kid == nil {
		return Transition{}, gateReason(false, "%s:v%d links no knowledge; the lifecycle cannot gate it", evaluator, version)
	}
	k, err := knowledgestore.Get(ctx, tx, *kid)
	if err != nil {
		return Transition{}, fmt.Errorf("learning: load candidate knowledge %s: %w", *kid, err)
	}
	if !rules.Applicable(k.Status) {
		return Transition{}, gateReason(false,
			"%s:v%d's knowledge %s is %s; promotion needs an applicable status (provisional or better, earned by evidence)",
			evaluator, version, k.ID, k.Status)
	}
	// The SQL function performs the atomic retire-and-activate (it also sets promoted_at); the audit row
	// records the gate evidence that allowed it.
	if _, err := tx.ExecContext(ctx, `SELECT promote_evaluator_version($1, $2)`, evaluator, version); err != nil {
		return Transition{}, fmt.Errorf("learning: promote %s:v%d: %w", evaluator, version, err)
	}
	if !at.IsZero() {
		if _, err := tx.ExecContext(ctx, `UPDATE evaluator_versions SET promoted_at = $3::timestamptz
 WHERE evaluator = $1 AND version = $2`, evaluator, version, at.UTC()); err != nil {
			return Transition{}, fmt.Errorf("learning: stamp promoted_at of %s:v%d: %w", evaluator, version, err)
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO eval_promotions
 (evaluator, version, from_status, to_status, backtest_run_id, decided_by, reason, created_at)
 VALUES ($1, $2, 'shadow', 'active', $3::uuid, $4, $5, COALESCE($6::timestamptz, now()))`, evaluator, version, runID, decidedBy,
		fmt.Sprintf("knowledge %s is %s and latest backtest %s passed", k.ID, k.Status, runID), nullTime(at)); err != nil {
		return Transition{}, fmt.Errorf("learning: audit promote %s:v%d: %w", evaluator, version, err)
	}
	return Transition{Evaluator: evaluator, Version: version, FromStatus: "shadow", ToStatus: "active",
		Reason: fmt.Sprintf("knowledge %s is %s and latest backtest %s passed", k.ID, k.Status, runID)}, tx.Commit()
}

// Retire ends a candidate, shadow or active version (the trigger enforces the legal transitions).
func Retire(ctx context.Context, db *sql.DB, evaluator string, version int, decidedBy, reason string) (Transition, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return Transition{}, err
	}
	defer func() { _ = tx.Rollback() }()
	var status string
	if err := tx.QueryRowContext(ctx, `SELECT status FROM evaluator_versions
 WHERE evaluator = $1 AND version = $2 FOR UPDATE`, evaluator, version).Scan(&status); err != nil {
		return Transition{}, fmt.Errorf("%w: %s:v%d", ErrNotFound, evaluator, version)
	}
	if status == "retired" {
		return Transition{}, gateReason(false, "%s:v%d is already retired", evaluator, version)
	}
	t, err := transition(ctx, tx, evaluator, version, status, "retired", nil, decidedBy, reason)
	if err != nil {
		return Transition{}, err
	}
	return t, tx.Commit()
}

// latestBacktest is the newest eval_backtest_runs row of the version (id, passed; "" when none).
func latestBacktest(ctx context.Context, tx *sql.Tx, evaluator string, version int) (id string, passed bool, err error) {
	err = tx.QueryRowContext(ctx, `SELECT id::text, passed FROM eval_backtest_runs
 WHERE evaluator = $1 AND version = $2 ORDER BY created_at DESC, id DESC LIMIT 1`, evaluator, version).
		Scan(&id, &passed)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	return id, passed, err
}

// transition applies a legal status move and audits it.
func transition(ctx context.Context, tx *sql.Tx, evaluator string, version int, from, to string,
	runID *string, decidedBy, reason string) (Transition, error) {
	return transitionAt(ctx, tx, evaluator, version, from, to, runID, decidedBy, reason, time.Time{})
}

// transitionAt is transition with the audit row stamped at at, the decided episode's world time; a zero at
// keeps the database clock (live).
func transitionAt(ctx context.Context, tx *sql.Tx, evaluator string, version int, from, to string,
	runID *string, decidedBy, reason string, at time.Time) (Transition, error) {
	res, err := tx.ExecContext(ctx, `UPDATE evaluator_versions SET status = $3
 WHERE evaluator = $1 AND version = $2 AND status = $4`, evaluator, version, to, from)
	if err != nil {
		return Transition{}, fmt.Errorf("learning: %s:v%d %s->%s: %w", evaluator, version, from, to, err)
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return Transition{}, gateReason(false, "%s:v%d is not %s", evaluator, version, from)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO eval_promotions
 (evaluator, version, from_status, to_status, backtest_run_id, decided_by, reason, created_at)
 VALUES ($1, $2, $3, $4, $5::uuid, $6, $7, COALESCE($8::timestamptz, now()))`,
		evaluator, version, from, to, runID, decidedBy, reason, nullTime(at)); err != nil {
		return Transition{}, fmt.Errorf("learning: audit %s:v%d %s->%s: %w", evaluator, version, from, to, err)
	}
	return Transition{Evaluator: evaluator, Version: version, FromStatus: from, ToStatus: to, Reason: reason}, nil
}

// VersionStatus is one evaluator_versions row for the status report.
type VersionStatus struct {
	Evaluator   string  `json:"evaluator"`
	Version     int     `json:"version"`
	Status      string  `json:"status"`
	Kind        string  `json:"kind"`
	CreatedFrom string  `json:"created_from"`
	KnowledgeID *string `json:"knowledge_id,omitempty"`
	Metrics     *string `json:"metrics,omitempty"`
	CreatedAt   string  `json:"created_at"`
}

// Status lists every evaluator_versions row, oldest version first — the candidate/shadow/active set.
func Status(ctx context.Context, db *sql.DB) ([]VersionStatus, error) {
	rows, err := db.QueryContext(ctx, `SELECT evaluator::text, version, status, kind, created_from,
 knowledge_id::text, metrics::text, created_at::text FROM evaluator_versions ORDER BY evaluator, version`)
	if err != nil {
		return nil, fmt.Errorf("learning: list versions: %w", err)
	}
	defer rows.Close()
	var out []VersionStatus
	for rows.Next() {
		var v VersionStatus
		if err := rows.Scan(&v.Evaluator, &v.Version, &v.Status, &v.Kind, &v.CreatedFrom, &v.KnowledgeID,
			&v.Metrics, &v.CreatedAt); err != nil {
			return nil, fmt.Errorf("learning: scan version: %w", err)
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// nullTime maps the zero time to SQL NULL (the database clock) and anything else to itself in UTC.
func nullTime(at time.Time) any {
	if at.IsZero() {
		return nil
	}
	return at.UTC()
}
