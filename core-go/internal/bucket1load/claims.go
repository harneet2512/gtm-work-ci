package bucket1load

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/bucket1"
)

func loadActivities(ctx context.Context, db *sql.DB, run runRow, ep *bucket1.Episode) error {
	rows, err := db.QueryContext(ctx, `SELECT a.id::text, a.source_system, a.source_object_id, a.occurred_at,
 COALESCE(a.account_id::text, ''), COALESCE(a.body_text, ''),
 COALESCE((SELECT p.person_id::text FROM activity_participants p WHERE p.activity_id = a.id AND p.person_id IS NOT NULL
           AND p.role IN ('from', 'speaker', 'actor', 'organizer') ORDER BY p.role LIMIT 1), '')
 FROM activities a WHERE a.id = ANY($1::uuid[]) ORDER BY a.occurred_at, a.id`, uuidArray(run.triggerIDs))
	if err != nil {
		return fmt.Errorf("bucket1load: read trigger activities: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var a bucket1.Activity
		if err := rows.Scan(&a.ID, &a.Source, &a.ExternalID, &a.OccurredAt, &a.AccountID, &a.Text, &a.SpeakerID); err != nil {
			return fmt.Errorf("bucket1load: scan activity: %w", err)
		}
		a.OccurredAt = a.OccurredAt.UTC()
		ep.Activities = append(ep.Activities, a)
	}
	return rows.Err()
}

// claimField maps a claim field_path to the AccountState field it folds into (0003_claims_state.sql).
func claimField(path string) string {
	switch path {
	case "commitment":
		return "current_commitments"
	case "buying_group.member", "stakeholder_role":
		return "buying_group"
	case "delegation":
		return "champion_status"
	}
	return path
}

// claimKind: a claim quoting its source is a stated fact; a rule or CRM record claim is a record (its source is
// the record itself, no quote exists); an AI claim with no quote would be an inference.
func claimKind(standing string, quote string) string {
	switch {
	case quote != "":
		return "fact"
	case standing == "crm_explicit" || standing == "first_party_record" || standing == "human_approved":
		return "record"
	}
	return "inference"
}

const claimColumns = `c.id::text, COALESCE(c.source_activity_id::text, ''), c.account_id::text, c.field_path, c.value::text,
 COALESCE(c.evidence_quote, ''), COALESCE(c.speaker_person_id::text, ''), c.occurred_at, c.status, c.standing`

func scanClaim(rows *sql.Rows) (bucket1.Claim, error) {
	var c bucket1.Claim
	var path, value, standing string
	if err := rows.Scan(&c.ID, &c.ActivityID, &c.AccountID, &path, &value, &c.Quote, &c.SpeakerID, &c.OccurredAt, &c.Status, &standing); err != nil {
		return c, fmt.Errorf("bucket1load: scan claim: %w", err)
	}
	c.Field, c.OccurredAt = claimField(path), c.OccurredAt.UTC()
	c.Value = strings.Trim(value, `"`)
	c.Kind = claimKind(standing, c.Quote)
	return c, nil
}

func loadClaims(ctx context.Context, db *sql.DB, run runRow, ep *bucket1.Episode) error {
	rows, err := db.QueryContext(ctx, `SELECT `+claimColumns+` FROM claims c WHERE c.source_activity_id = ANY($1::uuid[]) ORDER BY c.occurred_at, c.id`, uuidArray(run.triggerIDs))
	if err != nil {
		return fmt.Errorf("bucket1load: read new claims: %w", err)
	}
	defer rows.Close()
	newIDs := map[string]int{}
	for rows.Next() {
		c, err := scanClaim(rows)
		if err != nil {
			return err
		}
		newIDs[c.ID] = len(ep.Claims)
		ep.Claims = append(ep.Claims, c)
		ep.NewClaimFields = appendOnce(ep.NewClaimFields, c.Field)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if err := linkSupersessions(ctx, db, ep, newIDs); err != nil {
		return err
	}
	return loadPriorClaims(ctx, db, run, ep, newIDs)
}

// linkSupersessions: a claim that superseded an older one of the same field names it, with the reducer's own rule as
// the reason (the newer claim of a field wins within the same standing).
func linkSupersessions(ctx context.Context, db *sql.DB, ep *bucket1.Episode, newIDs map[string]int) error {
	if len(newIDs) == 0 {
		return nil
	}
	ids := make([]string, 0, len(newIDs))
	for id := range newIDs {
		ids = append(ids, id)
	}
	rows, err := db.QueryContext(ctx, `SELECT id::text, superseded_by::text FROM claims WHERE superseded_by = ANY($1::uuid[])`, uuidArray(ids))
	if err != nil {
		return fmt.Errorf("bucket1load: read supersessions: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var old, by string
		if err := rows.Scan(&old, &by); err != nil {
			return fmt.Errorf("bucket1load: scan supersession: %w", err)
		}
		if i, ok := newIDs[by]; ok {
			ep.Claims[i].Supersedes = old
			ep.Claims[i].SupersessionReason = "reducer rule: the newer claim of the same field wins within the same standing"
		}
	}
	return rows.Err()
}

func loadPriorClaims(ctx context.Context, db *sql.DB, run runRow, ep *bucket1.Episode, newIDs map[string]int) error {
	rows, err := db.QueryContext(ctx, `SELECT `+claimColumns+` FROM claims c WHERE c.account_id = $1::uuid AND c.occurred_at <= $2
 AND NOT (c.id = ANY($3::uuid[])) ORDER BY c.occurred_at DESC, c.id LIMIT $4`, run.accountID, ep.At, uuidArray(keys(newIDs)), priorClaimLimit)
	if err != nil {
		return fmt.Errorf("bucket1load: read prior claims: %w", err)
	}
	defer rows.Close()
	ep.PriorStatusAfter = map[string]string{}
	for rows.Next() {
		c, err := scanClaim(rows)
		if err != nil {
			return err
		}
		ep.PriorClaims = append(ep.PriorClaims, c)
		ep.PriorStatusAfter[c.ID] = c.Status
	}
	return rows.Err()
}

func keys(m map[string]int) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func appendOnce(xs []string, x string) []string {
	for _, e := range xs {
		if e == x {
			return xs
		}
	}
	return append(xs, x)
}

// timeOf reads a JSON timestamp; the zero time on anything else.
func timeOf(raw any) time.Time {
	s, _ := raw.(string)
	t, _ := time.Parse(time.RFC3339, s)
	return t.UTC()
}

func jsonRefs(raw []byte) []bucket1.Ref {
	var in []struct {
		ActivityID string `json:"activity_id"`
		ClaimID    string `json:"claim_id"`
		Quote      string `json:"quote"`
	}
	if json.Unmarshal(raw, &in) != nil {
		return nil
	}
	out := make([]bucket1.Ref, 0, len(in))
	for _, r := range in {
		out = append(out, bucket1.Ref{ActivityID: r.ActivityID, ClaimID: r.ClaimID, Quote: r.Quote})
	}
	return out
}
