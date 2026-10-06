// Package claimstore is the SQL side of claims: writing and loading claim rows, applying
// adjudication status updates, the extraction cache and the person directory. Everything takes a
// DB so it runs inside the caller's transaction.
package claimstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/harneet2512/gtm-work/core-go/internal/claims"
)

// DB is the subset of *sql.DB and *sql.Tx that claimstore uses.
type DB interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// InsertClaims writes claims, skipping any that already exist (the same activity, field,
// extractor, value and subject: claims_dedupe_uniq), so re-running an extractor never duplicates.
// It returns how many rows were actually inserted.
func InsertClaims(ctx context.Context, db DB, cs []claims.Claim) (int, error) {
	const insert = `
INSERT INTO claims (account_id, opportunity_id, subject_person_id, field_path, value, standing, confidence,
                    source_activity_id, speaker_person_id, evidence_quote, occurred_at, extractor, status, expires_at)
VALUES ($1::uuid, NULLIF($2, '')::uuid, NULLIF($3, '')::uuid, $4, $5::jsonb, $6, $7, $8::uuid, NULLIF($9, '')::uuid,
        NULLIF($10, ''), $11, $12, $13, $14)
ON CONFLICT ON CONSTRAINT claims_dedupe_uniq DO NOTHING`
	inserted := 0
	for _, c := range cs {
		status := c.Status
		if status == "" {
			status = claims.StatusActive
		}
		res, err := db.ExecContext(ctx, insert, c.AccountID, c.OpportunityID, c.SubjectPersonID, string(c.FieldPath), string(c.Value),
			string(c.Standing), c.Confidence, c.SourceActivityID, c.SpeakerPersonID, c.EvidenceQuote, c.OccurredAt.UTC(), c.Extractor, string(status), c.ExpiresAt)
		if err != nil {
			return inserted, fmt.Errorf("claimstore: insert %s claim from activity %s: %w", c.FieldPath, c.SourceActivityID, err)
		}
		n, err := res.RowsAffected()
		if err != nil {
			return inserted, fmt.Errorf("claimstore: rows affected: %w", err)
		}
		inserted += int(n)
	}
	return inserted, nil
}

// LoadAccountClaims returns every claim of the account that a human has not rejected, in a stable
// order. Adjudication re-derives statuses, so outranked, superseded and expired rows are included.
func LoadAccountClaims(ctx context.Context, db DB, accountID string) ([]claims.Claim, error) {
	return queryClaims(ctx, db, `WHERE c.account_id = $1::uuid AND c.status <> 'rejected'`, accountID)
}

// queryClaims selects claims (alias c; the clause may join activities a) in the stable load order.
func queryClaims(ctx context.Context, db DB, where string, args ...any) ([]claims.Claim, error) {
	query := `
SELECT c.id::text, c.account_id::text, COALESCE(c.opportunity_id::text, ''), COALESCE(c.subject_person_id::text, ''), c.field_path,
       c.value::text, c.standing, c.confidence::float8, c.source_activity_id::text, COALESCE(c.speaker_person_id::text, ''),
       COALESCE(c.evidence_quote, ''), c.occurred_at, c.extractor, c.status, COALESCE(c.superseded_by::text, ''), c.expires_at
  FROM claims c ` + where + `
 ORDER BY c.occurred_at, c.field_path, c.value::text, c.id`
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("claimstore: load claims: %w", err)
	}
	defer rows.Close()
	var out []claims.Claim
	for rows.Next() {
		var c claims.Claim
		var value, field, standing, status string
		var expires sql.NullTime
		if err := rows.Scan(&c.ID, &c.AccountID, &c.OpportunityID, &c.SubjectPersonID, &field, &value, &standing, &c.Confidence,
			&c.SourceActivityID, &c.SpeakerPersonID, &c.EvidenceQuote, &c.OccurredAt, &c.Extractor, &status, &c.SupersededBy, &expires); err != nil {
			return nil, fmt.Errorf("claimstore: scan claim: %w", err)
		}
		c.FieldPath, c.Value, c.Standing, c.Status = claims.FieldPath(field), json.RawMessage(value), claims.Standing(standing), claims.Status(status)
		c.OccurredAt = c.OccurredAt.UTC()
		if expires.Valid {
			t := expires.Time.UTC()
			c.ExpiresAt = &t
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("claimstore: read claims: %w", err)
	}
	return out, nil
}

// ApplyUpdates writes the status (and superseded_by) adjudication assigned to claims. Claims that
// are not listed keep their row untouched.
func ApplyUpdates(ctx context.Context, db DB, updates map[string]claims.StatusUpdate) error {
	if len(updates) == 0 {
		return nil
	}
	ids, statuses, supers := make([]string, 0, len(updates)), make([]string, 0, len(updates)), make([]string, 0, len(updates))
	for id, u := range updates {
		ids, statuses, supers = append(ids, id), append(statuses, string(u.Status)), append(supers, u.SupersededBy)
	}
	const update = `
UPDATE claims c SET status = u.status, superseded_by = NULLIF(u.sup, '')::uuid
  FROM unnest($1::uuid[], $2::text[], $3::text[]) AS u(id, status, sup)
 WHERE c.id = u.id AND c.status <> 'rejected'`
	if _, err := db.ExecContext(ctx, update, ids, statuses, supers); err != nil {
		return fmt.Errorf("claimstore: apply %d claim status updates: %w", len(updates), err)
	}
	return nil
}

// Cache is the extraction_cache table as a claims.Cache.
type Cache struct{ DB DB }

// Get returns the cached worker answer for (activity, extractor version).
func (c Cache) Get(ctx context.Context, activityID, version string) (claims.ExtractResponse, bool, error) {
	var model string
	var output []byte
	err := c.DB.QueryRowContext(ctx, `SELECT model, output FROM extraction_cache WHERE activity_id = $1::uuid AND extractor_version = $2`,
		activityID, version).Scan(&model, &output)
	if errors.Is(err, sql.ErrNoRows) {
		return claims.ExtractResponse{}, false, nil
	}
	if err != nil {
		return claims.ExtractResponse{}, false, fmt.Errorf("claimstore: read extraction cache: %w", err)
	}
	var resp claims.ExtractResponse
	if err := json.Unmarshal(output, &resp); err != nil {
		return claims.ExtractResponse{}, false, claims.Permanent(fmt.Errorf("claimstore: decode cached extraction of %s: %w", activityID, err))
	}
	resp.Model = model
	return resp, true, nil
}

// Put stores a worker answer; a concurrent writer of the same key wins and ours is dropped.
func (c Cache) Put(ctx context.Context, activityID, version string, resp claims.ExtractResponse) error {
	output, err := json.Marshal(resp)
	if err != nil {
		return fmt.Errorf("claimstore: encode extraction: %w", err)
	}
	_, err = c.DB.ExecContext(ctx, `
INSERT INTO extraction_cache (activity_id, extractor_version, model, output) VALUES ($1::uuid, $2, $3, $4::jsonb)
ON CONFLICT (activity_id, extractor_version) DO NOTHING`, activityID, version, resp.Model, string(output))
	if err != nil {
		return fmt.Errorf("claimstore: write extraction cache: %w", err)
	}
	return nil
}

// Directory resolves people by email through the current entity mapping, then the person record.
type Directory struct{ DB DB }

// PersonIDByEmail implements claims.Directory.
func (d Directory) PersonIDByEmail(ctx context.Context, email string) (string, bool, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	if email == "" {
		return "", false, nil
	}
	var id string
	err := d.DB.QueryRowContext(ctx, `
SELECT entity_id::text FROM entity_source_mappings
 WHERE entity_type = 'person' AND source_system = 'email' AND source_key = $1 AND valid_to IS NULL LIMIT 1`, email).Scan(&id)
	if err == nil {
		return id, true, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", false, fmt.Errorf("claimstore: look up %s: %w", email, err)
	}
	err = d.DB.QueryRowContext(ctx, `SELECT id::text FROM people WHERE primary_email = $1 AND merged_into IS NULL`, email).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("claimstore: look up %s: %w", email, err)
	}
	return id, true, nil
}
