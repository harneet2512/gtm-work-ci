package readmodel

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/claimstore"
)

// StateDiff is contracts/schemas/state_diff.v1.json.
type StateDiff struct {
	ID          string          `json:"id"`
	AccountID   string          `json:"account_id"`
	FromVersion int             `json:"from_version"`
	ToVersion   int             `json:"to_version"`
	IsMaterial  bool            `json:"is_material"`
	Changes     json.RawMessage `json:"changes"`
	ActivityIDs json.RawMessage `json:"activity_ids"`
	CreatedAt   time.Time       `json:"created_at"`
}

// Signal is contracts/schemas/signal.v1.json.
type Signal struct {
	ID              string          `json:"id"`
	AccountID       string          `json:"account_id"`
	OpportunityID   *string         `json:"opportunity_id"`
	SignalType      string          `json:"signal_type"`
	StateDiffID     *string         `json:"state_diff_id"`
	SubjectPersonID *string         `json:"subject_person_id"`
	SubjectClaimID  *string         `json:"subject_claim_id"`
	OccurredAt      time.Time       `json:"occurred_at"`
	ExpiresAt       *time.Time      `json:"expires_at"`
	Rule            string          `json:"rule"`
	Details         json.RawMessage `json:"details"`
	EvidenceRefs    json.RawMessage `json:"evidence_refs"`
	CreatedAt       time.Time       `json:"created_at"`
}

const diffColumns = `id::text, account_id::text, from_version, to_version, is_material, changes,
 to_jsonb(activity_ids), created_at`

func scanDiff(row interface{ Scan(...any) error }) (StateDiff, error) {
	var d StateDiff
	var changes, ids []byte
	if err := row.Scan(&d.ID, &d.AccountID, &d.FromVersion, &d.ToVersion, &d.IsMaterial, &changes, &ids, &d.CreatedAt); err != nil {
		return d, err
	}
	d.Changes, d.ActivityIDs, d.CreatedAt = changes, ids, utc(d.CreatedAt)
	return d, nil
}

// Diffs lists the account's state diffs, newest first. An empty table gives an empty slice.
func (r *Reader) Diffs(ctx context.Context, accountID string, limit int, materialOnly bool) ([]StateDiff, error) {
	if err := requireUUID("account", accountID); err != nil {
		return nil, err
	}
	n, err := normalizeLimit(limit)
	if err != nil {
		return nil, err
	}
	if err := accountExists(ctx, r.db, accountID); err != nil {
		return nil, err
	}
	return QueryDiffs(ctx, r.db, accountID, n, materialOnly, "")
}

// QueryDiffs reads up to n diffs of an account, newest first. field, when not empty, keeps only
// the diffs that changed that AccountState field. accountID must already be validated.
func QueryDiffs(ctx context.Context, db claimstore.DB, accountID string, n int, materialOnly bool, field string) ([]StateDiff, error) {
	return QueryDiffsBefore(ctx, db, accountID, n, materialOnly, field, nil, 0)
}

// QueryDiffsBefore is QueryDiffs restricted, when before is set, to the diffs whose new state version the
// world held strictly before that time (ADR-0019: state_history.as_of < before). A diff holds only changes
// between two versions, so a visible target version means nothing in it comes from a later activity.
// maxVersion, when positive, also keeps only diffs up to that version (a run pinned to a state version must
// not see the diffs of a later version that shares its as_of).
func QueryDiffsBefore(ctx context.Context, db claimstore.DB, accountID string, n int, materialOnly bool, field string, before *time.Time, maxVersion int) ([]StateDiff, error) {
	var cutoff any
	if before != nil {
		cutoff = before.UTC()
	}
	filter := "[]"
	if field != "" {
		raw, err := json.Marshal([]map[string]string{{"field": field}})
		if err != nil {
			return nil, fmt.Errorf("readmodel: encode field filter: %w", err)
		}
		filter = string(raw)
	}
	rows, err := db.QueryContext(ctx, `SELECT `+diffColumns+` FROM state_diffs
 WHERE account_id = $1::uuid AND (NOT $2::boolean OR is_material) AND changes @> $3::jsonb
   AND ($6 = 0 OR to_version <= $6)
   AND ($5::timestamptz IS NULL OR EXISTS (SELECT 1 FROM state_history h
        WHERE h.account_id = state_diffs.account_id AND h.version = state_diffs.to_version AND h.as_of < $5))
 ORDER BY to_version DESC LIMIT $4`, accountID, materialOnly, filter, n, cutoff, maxVersion)
	if err != nil {
		return nil, fmt.Errorf("readmodel: list diffs: %w", err)
	}
	defer rows.Close()
	out := []StateDiff{}
	for rows.Next() {
		d, err := scanDiff(rows)
		if err != nil {
			return nil, fmt.Errorf("readmodel: scan diff: %w", err)
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

const signalColumns = `id::text, account_id::text, opportunity_id::text, signal_type, state_diff_id::text,
 subject_person_id::text, subject_claim_id::text, occurred_at, expires_at, rule, details, evidence_refs, created_at`

func scanSignal(row interface{ Scan(...any) error }) (Signal, error) {
	var s Signal
	var details, evidence []byte
	if err := row.Scan(&s.ID, &s.AccountID, &s.OpportunityID, &s.SignalType, &s.StateDiffID, &s.SubjectPersonID,
		&s.SubjectClaimID, &s.OccurredAt, &s.ExpiresAt, &s.Rule, &details, &evidence, &s.CreatedAt); err != nil {
		return s, err
	}
	s.Details, s.EvidenceRefs, s.CreatedAt = details, evidence, utc(s.CreatedAt)
	s.OccurredAt = utc(s.OccurredAt)
	if s.ExpiresAt != nil {
		e := utc(*s.ExpiresAt)
		s.ExpiresAt = &e
	}
	return s, nil
}

// Signals lists the account's signals, newest first. An empty table gives an empty slice.
func (r *Reader) Signals(ctx context.Context, accountID string, limit int) ([]Signal, error) {
	if err := requireUUID("account", accountID); err != nil {
		return nil, err
	}
	n, err := normalizeLimit(limit)
	if err != nil {
		return nil, err
	}
	if err := accountExists(ctx, r.db, accountID); err != nil {
		return nil, err
	}
	rows, err := r.db.QueryContext(ctx, `SELECT `+signalColumns+` FROM signals
 WHERE account_id = $1::uuid ORDER BY created_at DESC, id DESC LIMIT $2`, accountID, n)
	if err != nil {
		return nil, fmt.Errorf("readmodel: list signals: %w", err)
	}
	defer rows.Close()
	out := []Signal{}
	for rows.Next() {
		s, err := scanSignal(rows)
		if err != nil {
			return nil, fmt.Errorf("readmodel: scan signal: %w", err)
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// signalsByID loads the signals with the given ids (chronological), for a trace.
func signalsByID(ctx context.Context, db claimstore.DB, accountID string, ids []string) ([]Signal, error) {
	out := []Signal{}
	if len(ids) == 0 {
		return out, nil
	}
	list, err := uuidList(ids)
	if err != nil {
		return nil, err
	}
	rows, err := db.QueryContext(ctx, `SELECT `+signalColumns+` FROM signals
 WHERE account_id = $2::uuid AND id = ANY(string_to_array($1, ',')::uuid[]) ORDER BY created_at, id`, list, accountID)
	if err != nil {
		return nil, fmt.Errorf("readmodel: load signals: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		s, err := scanSignal(rows)
		if err != nil {
			return nil, fmt.Errorf("readmodel: scan signal: %w", err)
		}
		out = append(out, s)
	}
	return out, rows.Err()
}
