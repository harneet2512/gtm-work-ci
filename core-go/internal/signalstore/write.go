// Package signalstore persists StateDiffs and Signals (migrations 0003, 0004, 0016) and answers the
// knowledge matcher's question: the account's Situation as of T, including the signals open at T.
// Writers are idempotent: a retried recompute, or the same clock tick twice, persists nothing new.
package signalstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/claimstore"
	"github.com/harneet2512/gtm-work/core-go/internal/signals"
	"github.com/harneet2512/gtm-work/core-go/internal/statediff"
)

// maxDedupeKey is the signals.dedupe_key column limit (signals_dedupe_key_len).
const maxDedupeKey = 300

// UUIDArray renders items as a Postgres array literal for a $n::uuid[] or $n::text[] parameter. Every
// element is quoted and escaped, so commas, braces, quotes or the word NULL in an element stay data.
func UUIDArray(items []string) string {
	quoted := make([]string, len(items))
	for i, it := range items {
		quoted[i] = `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(it) + `"`
	}
	return "{" + strings.Join(quoted, ",") + "}"
}

// InsertDiff stores the diff. inserted is false when this account's to_version already has a diff (a
// retried recompute); id is then the existing diff's id, so callers stay idempotent.
func InsertDiff(ctx context.Context, db claimstore.DB, d statediff.Diff, createdAt time.Time) (id string, inserted bool, err error) {
	changes, err := json.Marshal(d.Changes)
	if err != nil {
		return "", false, fmt.Errorf("signalstore: encode diff changes: %w", err)
	}
	err = db.QueryRowContext(ctx, `
INSERT INTO state_diffs (account_id, from_version, to_version, is_material, changes, activity_ids, created_at)
VALUES ($1::uuid, $2, $3, $4, $5::jsonb, $6::uuid[], $7)
ON CONFLICT (account_id, to_version) DO NOTHING RETURNING id::text`,
		d.AccountID, d.FromVersion, d.ToVersion, d.IsMaterial, string(changes), UUIDArray(d.ActivityIDs), createdAt.UTC()).Scan(&id)
	if err == nil {
		return id, true, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", false, fmt.Errorf("signalstore: insert diff v%d of %s: %w", d.ToVersion, d.AccountID, err)
	}
	if err := db.QueryRowContext(ctx, `SELECT id::text FROM state_diffs WHERE account_id = $1::uuid AND to_version = $2`,
		d.AccountID, d.ToVersion).Scan(&id); err != nil {
		return "", false, fmt.Errorf("signalstore: read existing diff v%d of %s: %w", d.ToVersion, d.AccountID, err)
	}
	return id, false, nil
}

// Scope is where a batch of signals belongs.
type Scope struct {
	AccountID     string
	OpportunityID string // "" for account-level
	StateDiffID   string // "" when the signals do not come from a diff (a clock tick)
	CreatedAt     time.Time
}

// InsertSignals stores the signals and returns the ids of every one of them, newly written or already
// present under the same (account, dedupe_key).
func InsertSignals(ctx context.Context, db claimstore.DB, sc Scope, in []signals.Signal) ([]string, error) {
	ids := make([]string, 0, len(in))
	for _, s := range in {
		id, err := insertSignal(ctx, db, sc, s)
		if err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, nil
}

func insertSignal(ctx context.Context, db claimstore.DB, sc Scope, s signals.Signal) (string, error) {
	if s.DedupeKey == "" || len(s.DedupeKey) > maxDedupeKey {
		return "", fmt.Errorf("signalstore: signal %s has a dedupe key of length %d (want 1..%d)", s.Type, len(s.DedupeKey), maxDedupeKey)
	}
	details, err := json.Marshal(nonNilMap(s.Details))
	if err != nil {
		return "", fmt.Errorf("signalstore: encode details of %s: %w", s.Type, err)
	}
	refs, err := json.Marshal(nonNilRefs(s))
	if err != nil {
		return "", fmt.Errorf("signalstore: encode evidence of %s: %w", s.Type, err)
	}
	var expires any
	if s.ExpiresAt != nil {
		expires = s.ExpiresAt.UTC()
	}
	opportunity := sc.OpportunityID
	if s.OpportunityID != "" { // a signal about a particular deal (one that just closed), not the batch's scope
		opportunity = s.OpportunityID
	}
	var id string
	err = db.QueryRowContext(ctx, `
INSERT INTO signals (account_id, opportunity_id, signal_type, state_diff_id, subject_person_id, subject_claim_id,
                     rule, details, evidence_refs, occurred_at, expires_at, dedupe_key, created_at)
VALUES ($1::uuid, $2::uuid, $3, $4::uuid, $5::uuid, $6::uuid, $7, $8::jsonb, $9::jsonb, $10, $11, $12, $13)
ON CONFLICT (account_id, dedupe_key) WHERE dedupe_key IS NOT NULL DO NOTHING RETURNING id::text`,
		sc.AccountID, nullIfEmpty(opportunity), s.Type, nullIfEmpty(sc.StateDiffID), nullIfEmpty(s.SubjectPersonID),
		nullIfEmpty(s.SubjectClaimID), s.Rule, string(details), string(refs), s.OccurredAt.UTC(), expires, s.DedupeKey,
		sc.CreatedAt.UTC()).Scan(&id)
	if err == nil {
		return id, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("signalstore: insert signal %s (%s): %w", s.Type, s.DedupeKey, err)
	}
	if err := db.QueryRowContext(ctx, `SELECT id::text FROM signals WHERE account_id = $1::uuid AND dedupe_key = $2`,
		sc.AccountID, s.DedupeKey).Scan(&id); err != nil {
		return "", fmt.Errorf("signalstore: read existing signal %s: %w", s.DedupeKey, err)
	}
	return id, nil
}

func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func nonNilMap(m map[string]any) map[string]any {
	if m == nil {
		return map[string]any{}
	}
	return m
}

func nonNilRefs(s signals.Signal) any {
	if s.EvidenceRefs == nil {
		return []any{}
	}
	return s.EvidenceRefs
}
