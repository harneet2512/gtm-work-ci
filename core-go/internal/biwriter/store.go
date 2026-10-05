package biwriter

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
)

// Stored says what Write did.
type Stored struct {
	ChangeID string
	// BIID is the update's id; "" when the change is not material (nothing to report).
	BIID string
	// Created is false when the held-out event already had its change: nothing was written (idempotent).
	Created bool
}

// Querier is the write side of *sql.Tx.
type Querier interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// Write persists the result in the caller's transaction: the AccountChange, and for a material change its
// BusinessIntelligenceUpdate. The held-out event gets one change (unique index account_changes_held_out_once)
// and one update (unique account_change_id): when the change already exists Write writes nothing and returns
// the stored ids. It validates the citation rule again before touching the database.
func Write(ctx context.Context, tx Querier, f Facts, r Result) (Stored, error) {
	if err := Validate(f, r); err != nil {
		return Stored{}, err
	}
	c := r.Change
	refs, err := marshalAll(c.PreviousStateRef, c.CurrentStateRef, c.GraphDiffRef, c.EvidenceRefs)
	if err != nil {
		return Stored{}, err
	}
	var id string
	err = tx.QueryRowContext(ctx, `
INSERT INTO account_changes (id, account_id, opportunity_id, held_out_event_id, trigger_activity_ids, previous_state_ref,
                             current_state_ref, state_diff_id, graph_diff_ref, material_change, evidence_refs, created_at)
VALUES ($1::uuid, $2::uuid, $3::uuid, $4::uuid, $5::uuid[], $6::jsonb, $7::jsonb, $8::uuid, $9::jsonb, $10, $11::jsonb, $12)
ON CONFLICT (held_out_event_id) WHERE held_out_event_id IS NOT NULL DO NOTHING
RETURNING id::text`,
		c.ID, c.AccountID, c.OpportunityID, c.HeldOutEventID, uuidArray(c.TriggerActivityIDs), refs[0], refs[1], c.StateDiffID,
		refs[2], c.MaterialChange, refs[3], c.CreatedAt).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return existing(ctx, tx, *c.HeldOutEventID)
	}
	if err != nil {
		return Stored{}, fmt.Errorf("biwriter: insert account change: %w", err)
	}
	out := Stored{ChangeID: id, Created: true}
	if r.BI == nil {
		return out, nil
	}
	if err := insertBI(ctx, tx, *r.BI); err != nil {
		return Stored{}, err
	}
	out.BIID = r.BI.ID
	return out, nil
}

func existing(ctx context.Context, tx Querier, heldOutEventID string) (Stored, error) {
	out := Stored{}
	var bi sql.NullString
	err := tx.QueryRowContext(ctx, `
SELECT c.id::text, (SELECT b.id::text FROM business_intelligence_updates b WHERE b.account_change_id = c.id)
  FROM account_changes c WHERE c.held_out_event_id = $1::uuid`, heldOutEventID).Scan(&out.ChangeID, &bi)
	if err != nil {
		return Stored{}, fmt.Errorf("biwriter: read the existing change of event %s: %w", heldOutEventID, err)
	}
	out.BIID = bi.String
	return out, nil
}

func insertBI(ctx context.Context, tx Querier, bi BI) error {
	parts, err := marshalAll(bi.Claims, bi.AccountMapRef, bi.Transition)
	if err != nil {
		return err
	}
	transition := any(parts[2])
	if bi.Transition == nil {
		transition = nil
	}
	_, err = tx.ExecContext(ctx, `
INSERT INTO business_intelligence_updates (id, account_id, opportunity_id, account_change_id, summary, claims, why_it_matters,
                                           knowledge_refs, account_map_ref, model, transition, created_at)
VALUES ($1::uuid, $2::uuid, $3::uuid, $4::uuid, $5, $6::jsonb, $7, $8::uuid[], $9::jsonb, $10, $11::jsonb, $12)`,
		bi.ID, bi.AccountID, bi.OpportunityID, bi.AccountChangeID, bi.Summary, parts[0], bi.WhyItMatters,
		uuidArray(bi.KnowledgeRefs), parts[1], bi.Model, transition, bi.CreatedAt)
	if err != nil {
		return fmt.Errorf("biwriter: insert business-intelligence update: %w", err)
	}
	return nil
}

func marshalAll(values ...any) ([]string, error) {
	out := make([]string, len(values))
	for i, v := range values {
		raw, err := json.Marshal(v)
		if err != nil {
			return nil, fmt.Errorf("biwriter: encode document: %w", err)
		}
		out[i] = string(raw)
	}
	return out, nil
}

// uuidArray renders ids as a Postgres array literal; the ids are cast to uuid by the statement.
func uuidArray(ids []string) string {
	out := "{"
	for i, id := range ids {
		if i > 0 {
			out += ","
		}
		out += `"` + id + `"`
	}
	return out + "}"
}

// Reader is the read side of *sql.DB and *sql.Tx.
type Reader interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

const changeSQL = `
SELECT jsonb_build_object('id', id, 'account_id', account_id, 'opportunity_id', opportunity_id,
  'held_out_event_id', held_out_event_id, 'trigger_activity_ids', to_jsonb(trigger_activity_ids),
  'previous_state_ref', previous_state_ref, 'current_state_ref', current_state_ref, 'state_diff_id', state_diff_id,
  'graph_diff_ref', graph_diff_ref, 'material_change', material_change, 'evidence_refs', evidence_refs,
  'created_at', created_at)::text,
 (SELECT jsonb_build_object('id', b.id, 'account_id', b.account_id, 'opportunity_id', b.opportunity_id,
    'account_change_id', b.account_change_id, 'summary', b.summary, 'claims', b.claims, 'why_it_matters', b.why_it_matters,
    'knowledge_refs', to_jsonb(b.knowledge_refs), 'account_map_ref', b.account_map_ref, 'transition', b.transition,
    'model', b.model, 'created_at', b.created_at)::text
    FROM business_intelligence_updates b WHERE b.account_change_id = c.id)
FROM account_changes c WHERE c.id = $1::uuid`

// ErrNotFound: no such account change.
var ErrNotFound = errors.New("biwriter: account change not found")

// Read returns the stored AccountChange and its update (nil when there is none) as the JSON their contract
// schemas describe, exactly as persisted.
func Read(ctx context.Context, db Reader, changeID string) (change, update []byte, err error) {
	var c string
	var b sql.NullString
	err = db.QueryRowContext(ctx, changeSQL, changeID).Scan(&c, &b)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil, ErrNotFound
	}
	if err != nil {
		return nil, nil, fmt.Errorf("biwriter: read account change %s: %w", changeID, err)
	}
	if b.Valid {
		update = []byte(b.String)
	}
	return []byte(c), update, nil
}
