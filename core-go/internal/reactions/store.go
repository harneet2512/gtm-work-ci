package reactions

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

// errDupReaction / errDupOutcome are the SAVEPOINT-swallowed unique hits that make a re-scan a
// no-op (customer_reactions_once / business_outcomes_once, and customer_reactions_ignored_once —
// one 'ignored' per episode, partial index so it cannot be an ON CONFLICT target by name).
var (
	errDupReaction = errors.New("reaction already recorded")
	errDupOutcome  = errors.New("outcome already recorded")
)

// insertReaction writes one customer_reactions row linked to the episode and its run, returning the
// new id; a stored (activity, type) pair or a second 'ignored' on the episode returns errDupReaction.
func insertReaction(ctx context.Context, tx *sql.Tx, ep *episode, a *activity, typ string, now time.Time) (string, error) {
	refs, err := evidenceRefs(ep, a)
	if err != nil {
		return "", err
	}
	var id string
	err = tx.QueryRowContext(ctx, `INSERT INTO customer_reactions
		(id, account_id, agent_run_id, activity_id, reaction_type, polarity, evidence_refs,
		 decision_episode_id, created_at)
		VALUES (gen_random_uuid(), $1, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT DO NOTHING
		RETURNING id::text`,
		ep.AccountID, ep.RunID, a.ID, typ, polarityOf[typ], refs, ep.ID, now.UTC()).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", errDupReaction
	}
	if err != nil {
		return "", fmt.Errorf("reactions: insert %s: %w", typ, err)
	}
	return id, nil
}

// insertOutcome writes one business_outcomes row linked to the episode, its run and its opportunity,
// returning the new id; a stored (activity, outcome_type) pair returns errDupOutcome.
func insertOutcome(ctx context.Context, tx *sql.Tx, ep *episode, a *activity, o outcome) (string, error) {
	refs, err := evidenceRefs(ep, a)
	if err != nil {
		return "", err
	}
	value, err := json.Marshal(o.value)
	if err != nil {
		return "", fmt.Errorf("reactions: outcome value: %w", err)
	}
	var id string
	err = tx.QueryRowContext(ctx, `INSERT INTO business_outcomes
		(id, account_id, agent_run_id, opportunity_id, outcome_type, value, activity_id, occurred_at,
		 decision_episode_id, evidence_refs)
		VALUES (gen_random_uuid(), $1, $2, $3, $4, $5, $6, $7, $8, $9)
		ON CONFLICT ON CONSTRAINT business_outcomes_once DO NOTHING
		RETURNING id::text`,
		ep.AccountID, ep.RunID, a.OpportunityID, o.typ, value, a.ID, a.Occurred.UTC(), ep.ID, refs).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", errDupOutcome
	}
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return "", errDupOutcome // any unique hit on this insert is a dup; NULL activity_ids never conflict
		}
		return "", fmt.Errorf("reactions: insert outcome %s: %w", o.typ, err)
	}
	return id, nil
}

// evidenceRefs builds the evidence_refs array every reaction/outcome carries: the activity that
// proves it, a short quote when the activity has text, the speaker when a recipient authored it.
func evidenceRefs(ep *episode, a *activity) (json.RawMessage, error) {
	ref := map[string]any{"activity_id": a.ID, "occurred_at": a.Occurred.UTC().Format(time.RFC3339Nano)}
	if q := quoteOf(a); q != "" {
		ref["quote"] = q
	}
	if sp := speakerOf(ep, a); sp != "" {
		ref["speaker_person_id"] = sp
	}
	out, err := json.Marshal([]map[string]any{ref})
	if err != nil {
		return nil, fmt.Errorf("reactions: evidence refs: %w", err)
	}
	return out, nil
}

// quoteOf is the first 300 runes of the activity's own words (evidenceRef.quote allows 2000; a
// longer excerpt does not make better evidence).
func quoteOf(a *activity) string {
	s := strings.TrimSpace(a.Body)
	if s == "" {
		s = strings.TrimSpace(a.Summary)
	}
	r := []rune(s)
	if len(r) > 300 {
		s = string(r[:299]) + "…"
	}
	return s
}

// speakerOf is the person id of the author-side participant that made this the customer's voice;
// an unmatched external address leaves it empty (no person row exists to name).
func speakerOf(ep *episode, a *activity) string {
	for _, p := range a.participants {
		if authorRoles[p.Role] && p.PersonID != "" {
			return p.PersonID
		}
	}
	return ""
}
