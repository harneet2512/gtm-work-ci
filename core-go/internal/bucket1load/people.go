package bucket1load

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/harneet2512/gtm-work/core-go/internal/bucket1"
)

// loadPeople reads the account's people and employees, its opportunities and how each trigger activity was
// resolved (its participants, with the raw identity kept as the source mapping).
func loadPeople(ctx context.Context, db *sql.DB, run runRow, ep *bucket1.Episode) error {
	rows, err := db.QueryContext(ctx, `SELECT id::text, COALESCE(account_id::text, ''), COALESCE(primary_email, ''), kind = 'employee'
 FROM people WHERE account_id = $1::uuid OR kind = 'employee'
 OR id IN (SELECT person_id FROM activity_participants WHERE activity_id = ANY($2::uuid[]) AND person_id IS NOT NULL)`,
		run.accountID, uuidArray(run.triggerIDs))
	if err != nil {
		return fmt.Errorf("bucket1load: read people: %w", err)
	}
	defer rows.Close()
	domains := map[string]bool{}
	for rows.Next() {
		var p bucket1.Person
		if err := rows.Scan(&p.ID, &p.AccountID, &p.Email, &p.Internal); err != nil {
			return fmt.Errorf("bucket1load: scan person: %w", err)
		}
		if at := strings.LastIndex(p.Email, "@"); p.Internal && at >= 0 {
			domains[strings.ToLower(p.Email[at+1:])] = true
		}
		ep.People = append(ep.People, p)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for d := range domains {
		ep.InternalDomains = append(ep.InternalDomains, d)
	}
	if err := loadOpportunities(ctx, db, run, ep); err != nil {
		return err
	}
	return loadResolutions(ctx, db, ep)
}

func loadOpportunities(ctx context.Context, db *sql.DB, run runRow, ep *bucket1.Episode) error {
	rows, err := db.QueryContext(ctx, `SELECT id::text, account_id::text FROM opportunities WHERE account_id = $1::uuid`, run.accountID)
	if err != nil {
		return fmt.Errorf("bucket1load: read opportunities: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var o bucket1.Opportunity
		if err := rows.Scan(&o.ID, &o.AccountID); err != nil {
			return fmt.Errorf("bucket1load: scan opportunity: %w", err)
		}
		ep.Opportunities = append(ep.Opportunities, o)
	}
	return rows.Err()
}

// loadResolutions: one resolution per participant of a trigger activity; a participant core could not resolve
// (no person_id) is an abstention. Candidates are the persons the same raw identity resolved to.
func loadResolutions(ctx context.Context, db *sql.DB, ep *bucket1.Episode) error {
	for _, a := range ep.Activities {
		var opp string
		if err := db.QueryRowContext(ctx, `SELECT COALESCE(opportunity_id::text, '') FROM activities WHERE id = $1::uuid`, a.ID).Scan(&opp); err != nil {
			return fmt.Errorf("bucket1load: read opportunity of activity %s: %w", a.ID, err)
		}
		rows, err := db.QueryContext(ctx, `SELECT raw_identity, COALESCE(person_id::text, ''), p.kind = 'employee'
 FROM activity_participants ap LEFT JOIN people p ON p.id = ap.person_id WHERE ap.activity_id = $1::uuid`, a.ID)
		if err != nil {
			return fmt.Errorf("bucket1load: read participants of %s: %w", a.ID, err)
		}
		for rows.Next() {
			var raw, person string
			var internal sql.NullBool
			if err := rows.Scan(&raw, &person, &internal); err != nil {
				_ = rows.Close()
				return fmt.Errorf("bucket1load: scan participant: %w", err)
			}
			r := bucket1.Resolution{ActivityID: a.ID, PersonID: person, AccountID: a.AccountID, OpportunityID: opp,
				Internal: internal.Valid && internal.Bool, Mapping: "participant " + raw}
			if person != "" {
				r.Candidates = []string{person}
			}
			ep.Resolutions = append(ep.Resolutions, r)
		}
		err = rows.Err()
		_ = rows.Close()
		if err != nil {
			return err
		}
	}
	return nil
}
