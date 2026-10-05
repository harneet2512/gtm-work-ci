package corectx

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/claimstore"
)

// excerptRunes is how much of an activity body an `activities` item carries.
const excerptRunes = 1200

type claimItem struct {
	ClaimID         string          `json:"claim_id"`
	ActivityID      string          `json:"activity_id"`
	FieldPath       string          `json:"field_path"`
	Value           json.RawMessage `json:"value"`
	Standing        string          `json:"standing"`
	Confidence      float64         `json:"confidence"`
	Status          string          `json:"status"`
	Quote           *string         `json:"quote,omitempty"`
	SubjectPersonID *string         `json:"subject_person_id,omitempty"`
	SpeakerPersonID *string         `json:"speaker_person_id,omitempty"`
	OccurredAt      time.Time       `json:"occurred_at"`
	Extractor       string          `json:"extractor"`
}

type activityParticipant struct {
	PersonID    *string `json:"person_id"`
	Role        string  `json:"role"`
	DisplayName string  `json:"display_name,omitempty"`
}

type activityItem struct {
	ActivityID   string                `json:"activity_id"`
	ActivityType string                `json:"activity_type"`
	SourceSystem string                `json:"source_system"`
	OccurredAt   time.Time             `json:"occurred_at"`
	Trigger      bool                  `json:"is_trigger"`
	Summary      *string               `json:"summary,omitempty"`
	Excerpt      *string               `json:"excerpt,omitempty"`
	Participants []activityParticipant `json:"participants"`
}

// activityItems returns the account's recent activities with the run's trigger activities first.
// Only activities with org visibility are served to the agent.
func activityItems(ctx context.Context, db claimstore.DB, sc runScope, p Params) ([]any, bool, error) {
	rows, err := db.QueryContext(ctx, `SELECT a.id::text, a.activity_type, a.source_system, a.occurred_at,
 (a.id = ANY(string_to_array($2, ',')::uuid[])) AS is_trigger, a.summary, left(a.body_text, $4)
 FROM activities a WHERE a.account_id = $1::uuid
   AND a.permissions ->> 'visibility' = 'org' AND a.occurred_at < $5
 ORDER BY is_trigger DESC, a.occurred_at DESC, a.id DESC LIMIT $3`,
		sc.accountID, uuidList(sc.triggerIDs), p.Limit+1, excerptRunes, sc.cutoff.UTC())
	if err != nil {
		return nil, false, fmt.Errorf("corectx: read activities: %w", err)
	}
	defer rows.Close()
	var items []*activityItem
	for rows.Next() {
		var a activityItem
		if err := rows.Scan(&a.ActivityID, &a.ActivityType, &a.SourceSystem, &a.OccurredAt, &a.Trigger, &a.Summary, &a.Excerpt); err != nil {
			return nil, false, fmt.Errorf("corectx: scan activity: %w", err)
		}
		a.OccurredAt, a.Participants = a.OccurredAt.UTC(), []activityParticipant{}
		items = append(items, &a)
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	if err := addParticipants(ctx, db, items); err != nil {
		return nil, false, err
	}
	all := make([]any, len(items))
	for i, a := range items {
		all[i] = *a
	}
	return cut(all, p.Limit)
}

func addParticipants(ctx context.Context, db claimstore.DB, items []*activityItem) error {
	if len(items) == 0 {
		return nil
	}
	ids := make([]string, len(items))
	byID := make(map[string]*activityItem, len(items))
	for i, a := range items {
		ids[i], byID[a.ActivityID] = a.ActivityID, a
	}
	rows, err := db.QueryContext(ctx, `SELECT activity_id::text, person_id::text, role, display_name
 FROM activity_participants WHERE activity_id = ANY(string_to_array($1, ',')::uuid[])
 ORDER BY activity_id, role, raw_identity`, uuidList(ids))
	if err != nil {
		return fmt.Errorf("corectx: read participants: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var actID string
		var part activityParticipant
		var name *string
		if err := rows.Scan(&actID, &part.PersonID, &part.Role, &name); err != nil {
			return fmt.Errorf("corectx: scan participant: %w", err)
		}
		if name != nil {
			part.DisplayName = *name
		}
		byID[actID].Participants = append(byID[actID].Participants, part)
	}
	return rows.Err()
}
