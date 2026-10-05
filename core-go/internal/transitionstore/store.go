// Package transitionstore persists StateTransitions (tables state_transitions and
// state_transition_history, migration 0021) and runs the transition detector inside the recompute
// transaction (HAR-126, ADR-0012). Only the detector writes transitions; every other reader, the
// account agent included, uses List and Current, which are read-only.
package transitionstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/transitions"
)

// Querier is satisfied by *sql.DB and *sql.Tx.
type Querier interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// Unknown is the relationship state before any CONFIRMED transition.
const Unknown = "unknown"

// DefaultListLimit bounds List when the caller gives no limit.
const DefaultListLimit = 50

// Relationship is the account's newest CONFIRMED relationship state.
type Relationship struct {
	State        string
	TransitionID string // "" when unknown
	ConfirmedAt  *time.Time
}

const columns = `id, account_id, opportunity_id, from_state, to_state_candidate, status, to_jsonb(trigger_activity_ids),
	supporting_facts, missing_facts, contradicting_facts, confidence::float8, state_version, rule_set_version,
	first_observed_at, confirmed_at, rejected_at, closed_at, close_reason, last_updated_at`

func scan(row interface{ Scan(...any) error }) (transitions.Record, error) {
	var r transitions.Record
	var opp, to, reason sql.NullString
	var trigger, supporting, missing, contradicting []byte
	var confirmed, rejected, closed sql.NullTime
	if err := row.Scan(&r.ID, &r.AccountID, &opp, &r.FromState, &to, &r.Status, &trigger, &supporting, &missing, &contradicting,
		&r.Confidence, &r.StateVersion, &r.RuleSetVersion, &r.FirstObservedAt, &confirmed, &rejected, &closed, &reason, &r.LastUpdatedAt); err != nil {
		return transitions.Record{}, err
	}
	for _, d := range []struct {
		raw []byte
		to  any
	}{{trigger, &r.TriggerActivityIDs}, {supporting, &r.SupportingFacts}, {missing, &r.MissingFacts}, {contradicting, &r.ContradictingFacts}} {
		if err := json.Unmarshal(d.raw, d.to); err != nil {
			return transitions.Record{}, fmt.Errorf("transitionstore: decode transition %s: %w", r.ID, err)
		}
	}
	r.OpportunityID, r.ToStateCandidate, r.CloseReason = nullable(opp), nullable(to), nullable(reason)
	r.ConfirmedAt, r.RejectedAt, r.ClosedAt = nullTime(confirmed), nullTime(rejected), nullTime(closed)
	r.FirstObservedAt, r.LastUpdatedAt = r.FirstObservedAt.UTC(), r.LastUpdatedAt.UTC()
	return r, nil
}

func nullable(s sql.NullString) *string {
	if !s.Valid {
		return nil
	}
	return &s.String
}

func nullTime(t sql.NullTime) *time.Time {
	if !t.Valid {
		return nil
	}
	u := t.Time.UTC()
	return &u
}

// LoadOpen returns the account's open transition (CANDIDATE or UNRESOLVED, not closed), row-locked when q
// is a transaction; nil when none.
func LoadOpen(ctx context.Context, q Querier, accountID string) (*transitions.Record, error) {
	r, err := scan(q.QueryRowContext(ctx, `SELECT `+columns+` FROM state_transitions
		WHERE account_id = $1::uuid AND status IN ('CANDIDATE', 'UNRESOLVED') AND closed_at IS NULL FOR UPDATE`, accountID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("transitionstore: load open transition of %s: %w", accountID, err)
	}
	return &r, nil
}

// Open is LoadOpen without the row lock: the account's open transition (CANDIDATE or UNRESOLVED, not closed) for
// a plain read, nil when none.
func Open(ctx context.Context, q Querier, accountID string) (*transitions.Record, error) {
	r, err := scan(q.QueryRowContext(ctx, `SELECT `+columns+` FROM state_transitions
		WHERE account_id = $1::uuid AND status IN ('CANDIDATE', 'UNRESOLVED') AND closed_at IS NULL`, accountID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("transitionstore: open transition of %s: %w", accountID, err)
	}
	return &r, nil
}

// Current returns the account's newest CONFIRMED relationship state, or Unknown.
func Current(ctx context.Context, q Querier, accountID string) (Relationship, error) {
	var id, state string
	var at time.Time
	err := q.QueryRowContext(ctx, `SELECT id::text, to_state_candidate, confirmed_at FROM state_transitions
		WHERE account_id = $1::uuid AND status = 'CONFIRMED' ORDER BY confirmed_at DESC, last_updated_at DESC, id LIMIT 1`, accountID).Scan(&id, &state, &at)
	if errors.Is(err, sql.ErrNoRows) {
		return Relationship{State: Unknown}, nil
	}
	if err != nil {
		return Relationship{}, fmt.Errorf("transitionstore: load relationship state of %s: %w", accountID, err)
	}
	at = at.UTC()
	return Relationship{State: state, TransitionID: id, ConfirmedAt: &at}, nil
}

// Save inserts rec (empty ID) or updates the open transition it carries the id of. It returns the stored record.
func Save(ctx context.Context, q Querier, rec transitions.Record) (transitions.Record, error) {
	facts, err := encodeFacts(rec)
	if err != nil {
		return transitions.Record{}, err
	}
	trigger := pgTextArray(rec.TriggerActivityIDs)
	if rec.ID == "" {
		row := q.QueryRowContext(ctx, `INSERT INTO state_transitions (account_id, opportunity_id, from_state, to_state_candidate, status,
			trigger_activity_ids, supporting_facts, missing_facts, contradicting_facts, confidence, state_version, rule_set_version,
			first_observed_at, confirmed_at, rejected_at, closed_at, close_reason, last_updated_at)
			VALUES ($1::uuid, $2::uuid, $3, $4, $5, $6::uuid[], $7::jsonb, $8::jsonb, $9::jsonb, $10, $11, $12, $13, $14, $15, $16, $17, $18)
			RETURNING `+columns, rec.AccountID, rec.OpportunityID, rec.FromState, rec.ToStateCandidate, rec.Status, trigger,
			facts[0], facts[1], facts[2], rec.Confidence, rec.StateVersion, rec.RuleSetVersion, rec.FirstObservedAt, rec.ConfirmedAt,
			rec.RejectedAt, rec.ClosedAt, rec.CloseReason, rec.LastUpdatedAt)
		stored, err := scan(row)
		return finish(stored, err, "insert", rec)
	}
	row := q.QueryRowContext(ctx, `UPDATE state_transitions SET to_state_candidate = $2, status = $3, trigger_activity_ids = $4::uuid[],
		supporting_facts = $5::jsonb, missing_facts = $6::jsonb, contradicting_facts = $7::jsonb, confidence = $8, state_version = $9,
		rule_set_version = $10, confirmed_at = $11, rejected_at = $12, closed_at = $13, close_reason = $14, last_updated_at = $15
		WHERE id = $1::uuid RETURNING `+columns, rec.ID, rec.ToStateCandidate, rec.Status, trigger, facts[0], facts[1], facts[2],
		rec.Confidence, rec.StateVersion, rec.RuleSetVersion, rec.ConfirmedAt, rec.RejectedAt, rec.ClosedAt, rec.CloseReason, rec.LastUpdatedAt)
	stored, err := scan(row)
	return finish(stored, err, "update", rec)
}

func finish(stored transitions.Record, err error, verb string, rec transitions.Record) (transitions.Record, error) {
	if err != nil {
		return transitions.Record{}, fmt.Errorf("transitionstore: %s %s transition of %s: %w", verb, rec.Status, rec.AccountID, err)
	}
	return stored, nil
}

func encodeFacts(rec transitions.Record) ([3]string, error) {
	var out [3]string
	for i, f := range [][]transitions.FactResult{rec.SupportingFacts, rec.MissingFacts, rec.ContradictingFacts} {
		if f == nil {
			f = []transitions.FactResult{}
		}
		raw, err := json.Marshal(f)
		if err != nil {
			return out, fmt.Errorf("transitionstore: encode facts: %w", err)
		}
		out[i] = string(raw)
	}
	return out, nil
}

// pgTextArray renders ids as a Postgres array literal; ids are validated uuids upstream and cast by the query.
func pgTextArray(ids []string) string {
	return "{" + strings.Join(ids, ",") + "}"
}

// List returns the account's transitions newest first (last_updated_at), terminal ones included, optionally
// of one status. It is the read model behind GET /accounts/{id}/transitions; nothing here writes.
func List(ctx context.Context, q Querier, accountID, status string, limit int) ([]transitions.Record, error) {
	if limit <= 0 {
		limit = DefaultListLimit
	}
	rows, err := q.QueryContext(ctx, `SELECT `+columns+` FROM state_transitions
		WHERE account_id = $1::uuid AND ($2 = '' OR status = $2) ORDER BY last_updated_at DESC, id LIMIT $3`, accountID, status, limit)
	if err != nil {
		return nil, fmt.Errorf("transitionstore: list transitions of %s: %w", accountID, err)
	}
	defer rows.Close()
	var out []transitions.Record
	for rows.Next() {
		r, err := scan(rows)
		if err != nil {
			return nil, fmt.Errorf("transitionstore: scan transition: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ForActivity returns the account's newest transition that the activity touched (its trigger_activity_ids
// name it: the recompute of that activity produced or last changed it), or nil when it touched none. It is
// how a change report says which relationship-state transition one event moved; nothing here writes.
func ForActivity(ctx context.Context, q Querier, accountID, activityID string) (*transitions.Record, error) {
	row := q.QueryRowContext(ctx, `SELECT `+columns+` FROM state_transitions
		WHERE account_id = $1::uuid AND trigger_activity_ids @> ARRAY[$2]::uuid[]
		ORDER BY last_updated_at DESC, id LIMIT 1`, accountID, activityID)
	r, err := scan(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("transitionstore: transition of activity %s: %w", activityID, err)
	}
	return &r, nil
}

// History returns the snapshots of one transition, oldest first (state_transition_history).
func History(ctx context.Context, q Querier, transitionID string) ([]transitions.Record, error) {
	rows, err := q.QueryContext(ctx, `SELECT snapshot FROM state_transition_history WHERE transition_id = $1::uuid ORDER BY id`, transitionID)
	if err != nil {
		return nil, fmt.Errorf("transitionstore: history of %s: %w", transitionID, err)
	}
	defer rows.Close()
	var out []transitions.Record
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			return nil, fmt.Errorf("transitionstore: scan history: %w", err)
		}
		var r transitions.Record
		if err := json.Unmarshal(raw, &r); err != nil {
			return nil, fmt.Errorf("transitionstore: decode history snapshot: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
