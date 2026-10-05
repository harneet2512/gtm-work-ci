package demomine

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
)

// Persist writes the frozen manifest to demo_manifests. The table's own constraints (held-out position is
// last history position plus one, no state on Event N, the opportunity belongs to the account) are the final
// check that what was frozen is what the contract allows.
func Persist(ctx context.Context, db *sql.DB, m Manifest) error {
	events, err := json.Marshal(m.Events)
	if err != nil {
		return fmt.Errorf("demomine: encode events: %w", err)
	}
	held, err := json.Marshal(m.HeldOutEvent)
	if err != nil {
		return fmt.Errorf("demomine: encode held-out event: %w", err)
	}
	expect, err := nullableJSON(m.SelectionExpectations)
	if err != nil {
		return err
	}
	mining, err := nullableJSON(m.Mining)
	if err != nil {
		return err
	}
	_, err = db.ExecContext(ctx, `
INSERT INTO demo_manifests (id, account_id, opportunity_id, data_cutoff, events, held_out_event, selection_expectations,
                            why_selected, mining, content_sha256, created_at)
VALUES ($1::uuid, $2::uuid, $3::uuid, $4::timestamptz, $5::jsonb, $6::jsonb, $7::jsonb, $8, $9::jsonb, $10, $11::timestamptz)`,
		m.ID, m.AccountID, m.OpportunityID, m.DataCutoff, string(events), string(held), expect, m.WhySelected, mining, m.ContentSHA256, m.CreatedAt)
	if err != nil {
		return fmt.Errorf("demomine: write demo_manifests row: %w", err)
	}
	return nil
}

// nullableJSON encodes a pointer as JSON, or SQL NULL when it is nil.
func nullableJSON[T any](v *T) (any, error) {
	if v == nil {
		return nil, nil
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("demomine: encode %T: %w", v, err)
	}
	return string(raw), nil
}
