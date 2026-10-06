package evalinput

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/claimstore"
	"github.com/harneet2512/gtm-work/core-go/internal/evals/deterministic"
	"github.com/harneet2512/gtm-work/core-go/internal/signalstore"
)

// Steps are the run's ordered steps (the plan-adherence eval reads them).
func Steps(ctx context.Context, db claimstore.DB, runID string) ([]deterministic.RunStep, error) {
	rows, err := db.QueryContext(ctx, `SELECT seq, step, status, started_at, finished_at FROM agent_run_steps
 WHERE agent_run_id = $1::uuid ORDER BY seq`, runID)
	if err != nil {
		return nil, fmt.Errorf("evalinput: read steps: %w", err)
	}
	defer rows.Close()
	out := []deterministic.RunStep{}
	for rows.Next() {
		var st deterministic.RunStep
		var started, finished sql.NullTime
		if err := rows.Scan(&st.Seq, &st.Step, &st.Status, &started, &finished); err != nil {
			return nil, fmt.Errorf("evalinput: read steps: %w", err)
		}
		st.StartedAt, st.FinishedAt = nullTimeUTC(started), nullTimeUTC(finished)
		out = append(out, st)
	}
	return out, rows.Err()
}

// People are the account's contacts plus every employee (recipient checks resolve both).
func People(ctx context.Context, db claimstore.DB, accountID string) ([]deterministic.Person, error) {
	rows, err := db.QueryContext(ctx, `SELECT id::text, display_name, kind, account_id::text, merged_into::text,
 primary_email IS NOT NULL, internal_only FROM people WHERE kind = 'employee' OR account_id = $1::uuid ORDER BY id`, accountID)
	if err != nil {
		return nil, fmt.Errorf("evalinput: read people: %w", err)
	}
	defer rows.Close()
	out := []deterministic.Person{}
	for rows.Next() {
		var p deterministic.Person
		var acct, merged sql.NullString
		if err := rows.Scan(&p.PersonID, &p.DisplayName, &p.Kind, &acct, &merged, &p.HasEmail, &p.InternalOnly); err != nil {
			return nil, fmt.Errorf("evalinput: read people: %w", err)
		}
		if acct.Valid {
			p.AccountID = &acct.String
		}
		if merged.Valid {
			p.MergedInto = &merged.String
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// Opportunities are the account's open pipeline (recipient-role checks read the owner).
func Opportunities(ctx context.Context, db claimstore.DB, accountID string) ([]deterministic.Opportunity, error) {
	rows, err := db.QueryContext(ctx, `SELECT id::text, account_id::text, owner_person_id::text FROM opportunities
 WHERE account_id = $1::uuid ORDER BY id`, accountID)
	if err != nil {
		return nil, fmt.Errorf("evalinput: read opportunities: %w", err)
	}
	defer rows.Close()
	out := []deterministic.Opportunity{}
	for rows.Next() {
		var o deterministic.Opportunity
		var owner sql.NullString
		if err := rows.Scan(&o.OpportunityID, &o.AccountID, &owner); err != nil {
			return nil, fmt.Errorf("evalinput: read opportunities: %w", err)
		}
		if owner.Valid {
			o.OwnerPersonID = &owner.String
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

// Activities are the cited evidence (trigger activities plus every evidence ref's activity).
func Activities(ctx context.Context, db claimstore.DB, accountID string, ids []string) ([]deterministic.Activity, error) {
	rows, err := db.QueryContext(ctx, `SELECT id::text, account_id::text, activity_type, occurred_at, COALESCE(body_text, summary, '')
 FROM activities WHERE id = ANY($1::uuid[]) AND account_id = $2::uuid ORDER BY occurred_at, id`, signalstore.UUIDArray(ids), accountID)
	if err != nil {
		return nil, fmt.Errorf("evalinput: read activities: %w", err)
	}
	defer rows.Close()
	out := []deterministic.Activity{}
	for rows.Next() {
		var a deterministic.Activity
		var acct sql.NullString
		if err := rows.Scan(&a.ActivityID, &acct, &a.ActivityType, &a.OccurredAt, &a.Text); err != nil {
			return nil, fmt.Errorf("evalinput: read activities: %w", err)
		}
		a.OccurredAt = a.OccurredAt.UTC()
		if acct.Valid {
			a.AccountID = &acct.String
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// PriorSends are the account's emails the rep sent in the duplicate window before the replay clock.
func PriorSends(ctx context.Context, db claimstore.DB, accountID string, at time.Time) ([]deterministic.PriorAction, error) {
	rows, err := db.QueryContext(ctx, `SELECT a.id::text, a.occurred_at, a.summary, a.body_text,
   COALESCE((SELECT array_agg(p.person_id::text) FROM activity_participants p WHERE p.activity_id = a.id
             AND p.role IN ('to', 'cc') AND p.person_id IS NOT NULL), '{}')
 FROM activities a WHERE a.account_id = $1::uuid AND a.activity_type = 'EmailSent'
   AND a.occurred_at <= $2 AND a.occurred_at > $3 ORDER BY a.occurred_at, a.id`,
		accountID, at.UTC(), at.UTC().Add(-priorSendWindow))
	if err != nil {
		return nil, fmt.Errorf("evalinput: read prior sends: %w", err)
	}
	defer rows.Close()
	out := []deterministic.PriorAction{}
	for rows.Next() {
		var p deterministic.PriorAction
		var subject, body sql.NullString
		var recipients string
		if err := rows.Scan(&p.RefID, &p.OccurredAt, &subject, &body, &recipients); err != nil {
			return nil, fmt.Errorf("evalinput: read prior sends: %w", err)
		}
		p.RefKind, p.Action, p.Status, p.OccurredAt = "activity", "send_email", "completed", p.OccurredAt.UTC()
		p.RecipientPersonIDs, p.Attachments = parseUUIDArray(recipients), []string{}
		if subject.Valid {
			p.Subject = &subject.String
		}
		if body.Valid {
			p.BodyText = &body.String
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func nullTimeUTC(t sql.NullTime) *time.Time {
	if !t.Valid {
		return nil
	}
	u := t.Time.UTC()
	return &u
}

// parseUUIDArray reads a Postgres uuid-array text literal ({a,b}).
func parseUUIDArray(lit string) []string {
	ids := []string{}
	for _, f := range splitList(trimBraces(lit)) {
		ids = append(ids, f)
	}
	return ids
}

func trimBraces(s string) string {
	if len(s) >= 2 && s[0] == '{' && s[len(s)-1] == '}' {
		return s[1 : len(s)-1]
	}
	return s
}

func splitList(s string) []string {
	if s == "" {
		return []string{}
	}
	return strings.Split(s, ",")
}
