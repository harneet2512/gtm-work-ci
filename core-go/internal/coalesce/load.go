package coalesce

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/harneet2512/gtm-work/core-go/internal/claims"
	"github.com/harneet2512/gtm-work/core-go/internal/claimstore"
	"github.com/harneet2512/gtm-work/core-go/internal/reducer"
)

// loadActivityInputs reads the given activities with their retained source payload and participants.
func loadActivityInputs(ctx context.Context, db claimstore.DB, ids []string) ([]claims.ActivityInput, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	acts, err := queryActivityInputs(ctx, db, ids)
	if err != nil {
		return nil, err
	}
	return acts, attachInputParticipants(ctx, db, ids, acts)
}

func queryActivityInputs(ctx context.Context, db claimstore.DB, ids []string) ([]claims.ActivityInput, error) {
	const query = `
SELECT a.id::text, COALESCE(a.account_id::text, ''), COALESCE(a.opportunity_id::text, ''), a.activity_type, a.source_system,
       a.occurred_at, COALESCE(a.body_text, ''), s.payload::text, s.id::text, a.source_object_id, s.idempotency_key,
       a.ingested_at, COALESCE(a.summary, ''), a.permissions::text, a.provenance::text
  FROM activities a JOIN source_events s ON s.id = a.source_event_id
 WHERE a.id = ANY($1::uuid[])
 ORDER BY a.occurred_at, a.id`
	rows, err := db.QueryContext(ctx, query, ids)
	if err != nil {
		return nil, fmt.Errorf("coalesce: load activities: %w", err)
	}
	defer rows.Close()
	var out []claims.ActivityInput
	for rows.Next() {
		var a claims.ActivityInput
		var payload, perms, prov string
		if err := rows.Scan(&a.ID, &a.AccountID, &a.OpportunityID, &a.Type, &a.SourceSystem, &a.OccurredAt, &a.Body, &payload,
			&a.SourceEventID, &a.SourceObjectID, &a.IdempotencyKey, &a.IngestedAt, &a.Summary, &perms, &prov); err != nil {
			return nil, fmt.Errorf("coalesce: scan activity: %w", err)
		}
		a.OccurredAt, a.IngestedAt = a.OccurredAt.UTC(), a.IngestedAt.UTC()
		a.Payload, a.Permissions, a.Provenance = json.RawMessage(payload), json.RawMessage(perms), json.RawMessage(prov)
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("coalesce: read activities: %w", err)
	}
	return out, nil
}

func attachInputParticipants(ctx context.Context, db claimstore.DB, ids []string, acts []claims.ActivityInput) error {
	index := make(map[string]int, len(acts))
	for i, a := range acts {
		index[a.ID] = i
	}
	rows, err := db.QueryContext(ctx, `
SELECT activity_id::text, raw_identity, role, COALESCE(display_name, ''), COALESCE(person_id::text, '')
  FROM activity_participants WHERE activity_id = ANY($1::uuid[]) ORDER BY activity_id, role, raw_identity`, ids)
	if err != nil {
		return fmt.Errorf("coalesce: load participants: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var p claims.Participant
		if err := rows.Scan(&id, &p.RawIdentity, &p.Role, &p.DisplayName, &p.PersonID); err != nil {
			return fmt.Errorf("coalesce: scan participant: %w", err)
		}
		if i, ok := index[id]; ok {
			acts[i].Participants = append(acts[i].Participants, p)
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("coalesce: read rows: %w", err)
	}
	return nil
}

// loadKnownPeople lists, for speaker attribution and side, the account's contacts (internal=false)
// and our employees who took part in its activities (internal=true), each with an email.
func loadKnownPeople(ctx context.Context, db claimstore.DB, accountID string) ([]claims.KnownPerson, error) {
	rows, err := db.QueryContext(ctx, `
SELECT p.id::text, p.primary_email, p.display_name, COALESCE(p.title, ''), p.kind = 'employee' FROM people p
 WHERE p.merged_into IS NULL AND p.primary_email IS NOT NULL
   AND (p.account_id = $1::uuid
        OR (p.kind = 'employee' AND EXISTS (
              SELECT 1 FROM activity_participants ap JOIN activities a ON a.id = ap.activity_id
               WHERE ap.person_id = p.id AND a.account_id = $1::uuid)))
 ORDER BY p.display_name, p.id`, accountID)
	if err != nil {
		return nil, fmt.Errorf("coalesce: load known people: %w", err)
	}
	defer rows.Close()
	var out []claims.KnownPerson
	for rows.Next() {
		var k claims.KnownPerson
		var internal bool
		if err := rows.Scan(&k.PersonID, &k.RawIdentity, &k.DisplayName, &k.Title, &internal); err != nil {
			return nil, fmt.Errorf("coalesce: scan known person: %w", err)
		}
		k.Internal = &internal
		out = append(out, k)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("coalesce: read rows: %w", err)
	}
	return out, nil
}

// accountFacts is what the reducer needs beyond claims.
type accountFacts struct {
	name       string
	activities []reducer.Activity
	people     map[string]reducer.Person
}

// loadAccountFacts reads the account's activities (with their opportunity and participants) and every
// person referenced by those activities, the claims or the account.
func loadAccountFacts(ctx context.Context, db claimstore.DB, accountID string, cs []claims.Claim) (accountFacts, error) {
	var f accountFacts
	if err := db.QueryRowContext(ctx, `SELECT name FROM accounts WHERE id = $1::uuid`, accountID).Scan(&f.name); err != nil {
		return f, fmt.Errorf("coalesce: load account %s: %w", accountID, err)
	}
	var err error
	if f.activities, err = loadAccountActivities(ctx, db, accountID); err != nil {
		return f, err
	}
	wanted, err := attachAccountParticipants(ctx, db, accountID, f.activities)
	if err != nil {
		return f, err
	}
	for _, c := range cs {
		for _, id := range []string{c.SubjectPersonID, c.SpeakerPersonID} {
			if id != "" {
				wanted[id] = true
			}
		}
	}
	f.people, err = loadPeople(ctx, db, accountID, wanted)
	return f, err
}

// loadAccountActivities returns the account's activities by time, each with the opportunity it is tied to.
func loadAccountActivities(ctx context.Context, db claimstore.DB, accountID string) ([]reducer.Activity, error) {
	rows, err := db.QueryContext(ctx, `
SELECT id::text, activity_type, occurred_at, COALESCE(opportunity_id::text, '') FROM activities WHERE account_id = $1::uuid ORDER BY occurred_at, id`, accountID)
	if err != nil {
		return nil, fmt.Errorf("coalesce: load account activities: %w", err)
	}
	defer rows.Close()
	var out []reducer.Activity
	for rows.Next() {
		var a reducer.Activity
		if err := rows.Scan(&a.ID, &a.Type, &a.OccurredAt, &a.OpportunityID); err != nil {
			return nil, fmt.Errorf("coalesce: scan account activity: %w", err)
		}
		a.OccurredAt = a.OccurredAt.UTC()
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("coalesce: read account activities: %w", err)
	}
	return out, nil
}

// attachAccountParticipants fills the participants of acts and returns the person ids seen.
func attachAccountParticipants(ctx context.Context, db claimstore.DB, accountID string, acts []reducer.Activity) (map[string]bool, error) {
	index := make(map[string]int, len(acts))
	for i, a := range acts {
		index[a.ID] = i
	}
	rows, err := db.QueryContext(ctx, `
SELECT p.activity_id::text, COALESCE(p.person_id::text, ''), p.raw_identity, p.role
  FROM activity_participants p JOIN activities a ON a.id = p.activity_id WHERE a.account_id = $1::uuid ORDER BY p.activity_id, p.role, p.raw_identity`, accountID)
	if err != nil {
		return nil, fmt.Errorf("coalesce: load account participants: %w", err)
	}
	defer rows.Close()
	wanted := map[string]bool{}
	for rows.Next() {
		var id string
		var p reducer.Participant
		if err := rows.Scan(&id, &p.PersonID, &p.RawIdentity, &p.Role); err != nil {
			return nil, fmt.Errorf("coalesce: scan account participant: %w", err)
		}
		if i, ok := index[id]; ok {
			acts[i].Participants = append(acts[i].Participants, p)
		}
		if p.PersonID != "" {
			wanted[p.PersonID] = true
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("coalesce: read rows: %w", err)
	}
	return wanted, nil
}

func loadPeople(ctx context.Context, db claimstore.DB, accountID string, wanted map[string]bool) (map[string]reducer.Person, error) {
	ids := make([]string, 0, len(wanted))
	for id := range wanted {
		ids = append(ids, id)
	}
	rows, err := db.QueryContext(ctx, `
SELECT id::text, kind, display_name, COALESCE(title, '') FROM people
 WHERE merged_into IS NULL AND (account_id = $1::uuid OR id = ANY($2::uuid[]))`, accountID, ids)
	if err != nil {
		return nil, fmt.Errorf("coalesce: load people: %w", err)
	}
	defer rows.Close()
	people := map[string]reducer.Person{}
	for rows.Next() {
		var p reducer.Person
		if err := rows.Scan(&p.ID, &p.Kind, &p.DisplayName, &p.Title); err != nil {
			return nil, fmt.Errorf("coalesce: scan person: %w", err)
		}
		people[p.ID] = p
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("coalesce: read rows: %w", err)
	}
	return people, nil
}
