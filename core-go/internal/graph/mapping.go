package graph

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// Mapping methods (entity_source_mappings.method).
const (
	MethodExact = "exact"
	MethodRule  = "rule"
	MethodHuman = "human"
	MethodSeed  = "seed"
)

// Mapping is one source identity resolved onto a canonical entity. A closed row (ValidTo set)
// is history: a remap closes it and opens a new one.
type Mapping struct {
	ID         string
	EntityType EntityType
	EntityID   string
	SourceKey
	Confidence         float64
	Method             string
	EvidenceActivityID string
	// Evidence records why the identity was attached: the matched cue, the rule, the source file.
	Evidence  json.RawMessage
	ValidFrom time.Time
	ValidTo   *time.Time
}

// MappingOverride replaces parts of a mapping's provenance on a remap; zero fields keep the old value.
type MappingOverride struct {
	Method             string
	Confidence         *float64
	EvidenceActivityID string
	Evidence           json.RawMessage
}

const mappingColumns = `id::text, entity_type, entity_id::text, source_system, source_key, confidence::float8, method,
 coalesce(evidence_activity_id::text, ''), evidence, valid_from, valid_to`

type rowScanner interface{ Scan(dest ...any) error }

func scanMapping(r rowScanner) (Mapping, error) {
	var m Mapping
	var to sql.NullTime
	var evidence []byte
	var entityType string
	err := r.Scan(&m.ID, &entityType, &m.EntityID, &m.System, &m.Key, &m.Confidence, &m.Method, &m.EvidenceActivityID, &evidence, &m.ValidFrom, &to)
	m.EntityType, m.Evidence = EntityType(entityType), evidence
	if to.Valid {
		t := to.Time.UTC()
		m.ValidTo = &t
	}
	m.ValidFrom = m.ValidFrom.UTC()
	return m, err
}

// CurrentMapping returns the open mapping of a source identity, if any.
func CurrentMapping(ctx context.Context, q DBTX, k SourceKey) (Mapping, bool, error) {
	m, err := scanMapping(q.QueryRowContext(ctx,
		`SELECT `+mappingColumns+` FROM entity_source_mappings WHERE source_system = $1 AND source_key = $2 AND valid_to IS NULL`, k.System, k.Key))
	if errors.Is(err, sql.ErrNoRows) {
		return Mapping{}, false, nil
	}
	if err != nil {
		return Mapping{}, false, fmt.Errorf("graph: current mapping %s/%s: %w", k.System, k.Key, err)
	}
	return m, true, nil
}

// InsertMapping opens a mapping. It fails if the identity already has a current one.
func InsertMapping(ctx context.Context, q Txn, m Mapping) (Mapping, error) {
	if err := lockIdentity(ctx, q, m.SourceKey); err != nil {
		return Mapping{}, err
	}
	return insertMapping(ctx, q, m)
}

func lockIdentity(ctx context.Context, q Txn, k SourceKey) error {
	return lock(ctx, q, "mapping:"+k.System, k.Key)
}

func insertMapping(ctx context.Context, q Txn, m Mapping) (Mapping, error) {
	out, err := scanMapping(q.QueryRowContext(ctx, `
INSERT INTO entity_source_mappings (entity_type, entity_id, source_system, source_key, confidence, method, evidence_activity_id, evidence, valid_from)
VALUES ($1, $2::uuid, $3, $4, $5, $6, $7::uuid, $8::jsonb, COALESCE($9::timestamptz, now()))
RETURNING `+mappingColumns, string(m.EntityType), m.EntityID, m.System, m.Key, m.Confidence, m.Method,
		optional(m.EvidenceActivityID), optionalJSON(m.Evidence), nullTime(m.ValidFrom)))
	if err != nil {
		return Mapping{}, fmt.Errorf("graph: insert mapping %s/%s: %w", m.System, m.Key, err)
	}
	return out, nil
}

// EnsureMapping opens the mapping unless the identity already has a current one. It returns the
// current mapping and whether this call created it; when the identity is already mapped to a
// different entity the existing mapping is returned untouched (use Remap to move it).
func EnsureMapping(ctx context.Context, q Txn, m Mapping) (Mapping, bool, error) {
	if err := lockIdentity(ctx, q, m.SourceKey); err != nil {
		return Mapping{}, false, err
	}
	cur, ok, err := CurrentMapping(ctx, q, m.SourceKey)
	if err != nil || ok {
		return cur, false, err
	}
	created, err := insertMapping(ctx, q, m)
	return created, err == nil, err
}

// CloseMapping ends a mapping at `at` without replacing it (the identity becomes unmapped).
func CloseMapping(ctx context.Context, q DBTX, id string, at time.Time) error {
	if _, err := q.ExecContext(ctx, `UPDATE entity_source_mappings SET valid_to = `+closedAtSQL(2)+` WHERE id = $1::uuid AND valid_to IS NULL`, id, at); err != nil {
		return fmt.Errorf("graph: close mapping %s: %w", id, err)
	}
	return nil
}

// Remap points an identity at another entity: the current mapping is closed at `at` (or just
// after its own start if `at` is not later) and a new one is opened that keeps the old
// method, confidence and evidence unless overridden. Remapping onto the entity it already
// maps to changes nothing.
func Remap(ctx context.Context, q Txn, k SourceKey, newEntityID string, at time.Time, o *MappingOverride) (Mapping, error) {
	if err := lockIdentity(ctx, q, k); err != nil {
		return Mapping{}, err
	}
	cur, ok, err := CurrentMapping(ctx, q, k)
	if err != nil {
		return Mapping{}, err
	}
	if !ok {
		return Mapping{}, fmt.Errorf("graph: remap %s/%s: no current mapping", k.System, k.Key)
	}
	if cur.EntityID == newEntityID {
		return cur, nil
	}
	var closedAt time.Time
	err = q.QueryRowContext(ctx, `UPDATE entity_source_mappings SET valid_to = `+closedAtSQL(2)+` WHERE id = $1::uuid RETURNING valid_to`, cur.ID, at).Scan(&closedAt)
	if err != nil {
		return Mapping{}, fmt.Errorf("graph: close mapping %s/%s: %w", k.System, k.Key, err)
	}
	next := Mapping{EntityType: cur.EntityType, EntityID: newEntityID, SourceKey: k, Confidence: cur.Confidence,
		Method: cur.Method, EvidenceActivityID: cur.EvidenceActivityID, Evidence: cur.Evidence, ValidFrom: closedAt.UTC()}
	if o != nil {
		if o.Method != "" {
			next.Method = o.Method
		}
		if o.Confidence != nil {
			next.Confidence = *o.Confidence
		}
		if o.EvidenceActivityID != "" {
			next.EvidenceActivityID = o.EvidenceActivityID
		}
		if len(o.Evidence) > 0 {
			next.Evidence = o.Evidence
		}
	}
	return insertMapping(ctx, q, next)
}

// nullTime maps the zero time to SQL NULL (the column default then applies).
func nullTime(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t
}
