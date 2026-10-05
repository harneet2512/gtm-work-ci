package play

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
)

// newID returns a random version 4 UUID.
func newID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(fmt.Sprintf("play: no randomness: %v", err))
	}
	b[6], b[8] = b[6]&0x0f|0x40, b[8]&0x3f|0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}

type stateRef struct {
	AccountID     string  `json:"account_id"`
	OpportunityID *string `json:"opportunity_id"`
	Version       int     `json:"version"`
}

// replayWorld is replay_world.v1.json.
type replayWorld struct {
	ManifestID string `json:"manifest_id"`
	DataCutoff string `json:"data_cutoff"`
	Cursor     struct {
		Position  int     `json:"replay_position"`
		LastEvent *string `json:"last_event_id"`
		HeldOutID *string `json:"held_out_event_id"`
	} `json:"event_cursor"`
	Entities struct {
		Accounts      []string `json:"account_ids"`
		People        []string `json:"person_ids"`
		Opportunities []string `json:"opportunity_ids"`
	} `json:"entities"`
	StateRefs []stateRef `json:"current_state_refs"`
	Boundary  struct {
		ClosedBefore string   `json:"closed_before"`
		Previous     []string `json:"previous_opportunity_ids"`
	} `json:"previous_deal_knowledge_boundary"`
	Manifest struct {
		ID     string `json:"manifest_id"`
		SHA256 string `json:"content_sha256"`
	} `json:"provenance_manifest_ref"`
	ComputedAt string `json:"computed_at"`
}

const timeLayout = "2006-01-02T15:04:05Z"

// world computes the ReplayWorld of the manifest from the manifest and the stores at the cursor: before Play the
// cursor is the last history event (N-1) and the held-out event is withheld; once released it is N. It is a
// read model, never stored.
//
// The previous-deal boundary is derived from the stores (the account's other deals that are closed) because the
// manifest carries no previous-deal list; the CRMArena split owns the authoritative one.
func (s *Service) world(ctx context.Context, m Manifest, released bool) (json.RawMessage, error) {
	w := replayWorld{ManifestID: m.ID, ComputedAt: s.clk.Now().UTC().Format(timeLayout)}
	last, held, cutoff := m.LastEventID, &m.Held.EventID, m.DataCutoff
	w.Cursor.Position = m.LastPosition
	if released {
		last, held, cutoff = m.Held.EventID, nil, m.Held.OccurredAt
		w.Cursor.Position = m.Held.Position
	}
	w.DataCutoff = cutoff.UTC().Format(timeLayout)
	w.Cursor.HeldOutID = held
	if last != "" { // a manifest without history events has no last event
		w.Cursor.LastEvent = &last
	}
	w.Manifest.ID, w.Manifest.SHA256 = m.ID, m.ContentSHA256
	w.Boundary.ClosedBefore = m.DataCutoff.UTC().Format(timeLayout)

	var err error
	w.Entities.Accounts = []string{m.AccountID}
	if w.Entities.People, err = s.ids(ctx, `
SELECT id::text FROM people WHERE account_id = $1::uuid AND merged_into IS NULL
UNION
SELECT DISTINCT ap.person_id::text FROM activity_participants ap JOIN activities a ON a.id = ap.activity_id
 WHERE a.account_id = $1::uuid AND ap.person_id IS NOT NULL ORDER BY 1`, m.AccountID); err != nil {
		return nil, err
	}
	if w.Entities.Opportunities, err = s.ids(ctx, `SELECT id::text FROM opportunities WHERE account_id = $1::uuid ORDER BY 1`, m.AccountID); err != nil {
		return nil, err
	}
	if w.Boundary.Previous, err = s.ids(ctx, `
SELECT o.id::text FROM opportunities o JOIN opportunity_state s ON s.opportunity_id = o.id
 WHERE o.account_id = $1::uuid AND o.id <> $2::uuid AND NOT s.is_open ORDER BY 1`, m.AccountID, m.OpportunityID); err != nil {
		return nil, err
	}
	if w.StateRefs, err = s.stateRefs(ctx, m.AccountID); err != nil {
		return nil, err
	}
	return json.Marshal(w)
}

func (s *Service) ids(ctx context.Context, query string, args ...any) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("play: read the replay world: %w", err)
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// stateRefs are the current account roll-up and every deal state of the account.
func (s *Service) stateRefs(ctx context.Context, accountID string) ([]stateRef, error) {
	refs := []stateRef{{AccountID: accountID}}
	if err := s.db.QueryRowContext(ctx, `SELECT COALESCE((SELECT version FROM account_state WHERE account_id = $1::uuid), 0)`, accountID).
		Scan(&refs[0].Version); err != nil {
		return nil, fmt.Errorf("play: read the account state version: %w", err)
	}
	rows, err := s.db.QueryContext(ctx, `SELECT opportunity_id::text, version FROM opportunity_state WHERE account_id = $1::uuid ORDER BY opportunity_id`, accountID)
	if err != nil {
		return nil, fmt.Errorf("play: read the deal state versions: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		ref := stateRef{AccountID: accountID}
		var opp string
		if err := rows.Scan(&opp, &ref.Version); err != nil {
			return nil, err
		}
		ref.OpportunityID = &opp
		refs = append(refs, ref)
	}
	return refs, rows.Err()
}
