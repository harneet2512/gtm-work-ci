// Package knowledgestore persists Knowledge objects (tables knowledge, knowledge_evidence,
// knowledge_status_history; migrations 0005, 0007, 0014) and applies the lifecycle of
// core-go/internal/knowledge when evidence arrives (HAR-118, ADR-0013).
package knowledgestore

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/knowledge"
)

// ErrNotFound is returned when no knowledge row has the id.
var ErrNotFound = errors.New("knowledge not found")

// Querier is satisfied by *sql.DB and *sql.Tx.
type Querier interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// ErrUnearnedEvidence is returned when Insert is given a status, counts or history that no recorded
// evidence backs: evidence enters only through RecordEvidence, so support_count always equals the
// decision episodes in knowledge_evidence (ADR-0013).
var ErrUnearnedEvidence = errors.New("knowledge must start as an unsupported candidate")

// ErrCreatedAtRequired is returned by InsertReplay when the knowledge has no CreatedAt. In a replay the clock is
// the event's, not the wall's: ListApplicableAsOf compares created_at with it, so the database default (now())
// would put the row in the wrong era.
var ErrCreatedAtRequired = errors.New("knowledge created_at must be given in replay time")

// InsertReplay is Insert for a writer that feeds a replay (HAR-117 invariant I3): the knowledge writer must supply
// CreatedAt in replay time and the wall-clock default is rejected.
func InsertReplay(ctx context.Context, tx *sql.Tx, k knowledge.Knowledge, reason string) (knowledge.Knowledge, error) {
	if k.CreatedAt.IsZero() {
		return knowledge.Knowledge{}, fmt.Errorf("%w: %s", ErrCreatedAtRequired, k.ID)
	}
	return Insert(ctx, tx, k, reason)
}

// Insert stores a new candidate (validated by the matcher's rules first) and records its initial status
// with the reason. It returns the stored object. A zero CreatedAt takes the database clock, which is right for a
// live system and wrong for a replay: replay writers use InsertReplay.
func Insert(ctx context.Context, tx *sql.Tx, k knowledge.Knowledge, reason string) (knowledge.Knowledge, error) {
	if err := knowledge.Validate(k); err != nil {
		return knowledge.Knowledge{}, err
	}
	if k.Status == "" {
		k.Status = knowledge.StatusCandidate
	}
	if k.Status != knowledge.StatusCandidate || k.Counts != (knowledge.Counts{}) || len(k.SupportingDecisionEpisodeIDs) > 0 ||
		len(k.Counterexamples) > 0 || len(k.StatusHistory) > 0 || k.LastValidatedAt != nil {
		return knowledge.Knowledge{}, fmt.Errorf("%w: %s", ErrUnearnedEvidence, k.ID)
	}
	if err := setChangeContext(ctx, tx, reason, nil); err != nil {
		return knowledge.Knowledge{}, err
	}
	cols, err := encode(k)
	if err != nil {
		return knowledge.Knowledge{}, err
	}
	var created *time.Time // nil: the database clock
	if !k.CreatedAt.IsZero() {
		t := k.CreatedAt.UTC()
		created = &t
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO knowledge (id, key, title, situation_signature, applicability_conditions,
		guidance, guidance_do, guidance_dont, status, support_count, counts, counterexamples, exceptions,
		evidence_classes, used_by_evaluators, provenance, created_at, last_validated_at)
		VALUES ($1, coalesce($2, 'K' || nextval('knowledge_key_seq')), $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13,
		ARRAY(SELECT jsonb_array_elements_text($14::jsonb)), ARRAY(SELECT jsonb_array_elements_text($15::jsonb)),
		$16, coalesce($17, now()), $18)`,
		k.ID, k.Key, k.Title, cols.signature, cols.applicability, k.Guidance.Summary, cols.do, cols.dont, k.Status,
		k.Counts.Decisions, cols.counts, cols.counterexamples, cols.exceptions, cols.evidenceClasses,
		cols.usedBy, cols.provenance, created, k.LastValidatedAt)
	if err != nil {
		return knowledge.Knowledge{}, fmt.Errorf("insert knowledge %s: %w", k.ID, err)
	}
	return Get(ctx, tx, k.ID)
}

// Get loads one knowledge object with its supporting episodes and status history.
func Get(ctx context.Context, q Querier, id string) (knowledge.Knowledge, error) {
	return get(ctx, q, id, false)
}

// ListApplicable loads every knowledge object whose status the rules allow in decision guidance.
func ListApplicable(ctx context.Context, q Querier, rules knowledge.Rules) ([]knowledge.Knowledge, error) {
	statuses, err := json.Marshal(rules.ApplicableStatuses)
	if err != nil {
		return nil, err
	}
	rows, err := q.QueryContext(ctx, `SELECT id FROM knowledge
		WHERE status IN (SELECT jsonb_array_elements_text($1::jsonb)) ORDER BY key`, string(statuses))
	if err != nil {
		return nil, fmt.Errorf("list applicable knowledge: %w", err)
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := closeRows(rows); err != nil {
		return nil, err
	}
	out := make([]knowledge.Knowledge, 0, len(ids))
	for _, id := range ids {
		k, err := Get(ctx, q, id)
		if err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	return out, nil
}

func get(ctx context.Context, q Querier, id string, lock bool) (knowledge.Knowledge, error) {
	query := `SELECT id, key, title, situation_signature, applicability_conditions, guidance, guidance_do,
		guidance_dont, status, counts, counterexamples, exceptions, to_jsonb(evidence_classes),
		to_jsonb(used_by_evaluators), provenance, created_at, last_validated_at FROM knowledge WHERE id = $1`
	if lock {
		query += ` FOR UPDATE`
	}
	var k knowledge.Knowledge
	var raw rawColumns
	err := q.QueryRowContext(ctx, query, id).Scan(&k.ID, &k.Key, &k.Title, &raw.signature, &raw.applicability,
		&k.Guidance.Summary, &raw.do, &raw.dont, &k.Status, &raw.counts, &raw.counterexamples, &raw.exceptions,
		&raw.evidenceClasses, &raw.usedBy, &raw.provenance, &k.CreatedAt, &k.LastValidatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return knowledge.Knowledge{}, fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	if err != nil {
		return knowledge.Knowledge{}, fmt.Errorf("load knowledge %s: %w", id, err)
	}
	if err := raw.decodeInto(&k); err != nil {
		return knowledge.Knowledge{}, fmt.Errorf("decode knowledge %s: %w", id, err)
	}
	if k.SupportingDecisionEpisodeIDs, err = supportingEpisodes(ctx, q, id); err != nil {
		return knowledge.Knowledge{}, err
	}
	if k.StatusHistory, err = history(ctx, q, id); err != nil {
		return knowledge.Knowledge{}, err
	}
	return k, nil
}

func supportingEpisodes(ctx context.Context, q Querier, id string) ([]string, error) {
	rows, err := q.QueryContext(ctx, `SELECT ref_id::text FROM knowledge_evidence
		WHERE knowledge_id = $1 AND kind = 'decision_episode' ORDER BY created_at, id`, id)
	if err != nil {
		return nil, fmt.Errorf("load supporting episodes: %w", err)
	}
	ids := []string{}
	for rows.Next() {
		var ref string
		if err := rows.Scan(&ref); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, ref)
	}
	return ids, closeRows(rows)
}

func history(ctx context.Context, q Querier, id string) ([]knowledge.HistoryEntry, error) {
	rows, err := q.QueryContext(ctx, `SELECT from_status, to_status, reason, evidence_kind, evidence_ref_id::text, changed_at
		FROM knowledge_status_history WHERE knowledge_id = $1 ORDER BY id`, id)
	if err != nil {
		return nil, fmt.Errorf("load knowledge history: %w", err)
	}
	var out []knowledge.HistoryEntry
	for rows.Next() {
		var h knowledge.HistoryEntry
		if err := rows.Scan(&h.FromStatus, &h.ToStatus, &h.Reason, &h.EvidenceKind, &h.EvidenceRefID, &h.ChangedAt); err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, h)
	}
	return out, closeRows(rows)
}

func closeRows(rows *sql.Rows) error {
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	return rows.Close()
}
