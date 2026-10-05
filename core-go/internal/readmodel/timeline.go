package readmodel

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/claimstore"
)

// Participant is one activity participant (activity.v1.json participants[]).
type Participant struct {
	RawIdentity string  `json:"raw_identity"`
	DisplayName string  `json:"display_name,omitempty"`
	Role        string  `json:"role"`
	PersonID    *string `json:"person_id"`
}

// Activity is contracts/schemas/activity.v1.json.
type Activity struct {
	ID              string          `json:"id"`
	IdempotencyKey  string          `json:"idempotency_key"`
	ActivityType    string          `json:"activity_type"`
	SourceSystem    string          `json:"source_system"`
	SourceObjectID  string          `json:"source_object_id"`
	SourceEventID   string          `json:"source_event_id"`
	OccurredAt      time.Time       `json:"occurred_at"`
	IngestedAt      time.Time       `json:"ingested_at"`
	Participants    []Participant   `json:"participants"`
	AccountID       *string         `json:"account_id"`
	OpportunityID   *string         `json:"opportunity_id"`
	AccountHint     *string         `json:"account_hint"`
	OpportunityHint *string         `json:"opportunity_hint"`
	PayloadRef      string          `json:"payload_ref"`
	Summary         *string         `json:"summary"`
	Permissions     json.RawMessage `json:"permissions"`
	Provenance      json.RawMessage `json:"provenance"`
	CausedBy        *string         `json:"caused_by_activity_id"`
	CorrelationID   *string         `json:"correlation_id"`
}

// Timeline is one page of GET /accounts/{id}/timeline.
type Timeline struct {
	Items        []Activity `json:"items"`
	NextBefore   *time.Time `json:"next_before"`
	NextBeforeID *string    `json:"next_before_id"`
}

const activityColumns = `a.id::text, se.idempotency_key, a.activity_type, a.source_system, a.source_object_id,
 a.source_event_id::text, a.occurred_at, a.ingested_at, a.account_id::text, a.opportunity_id::text,
 a.account_hint, a.opportunity_hint, a.summary, a.permissions, a.provenance,
 a.caused_by_activity_id::text, a.correlation_id::text`

const activityFrom = ` FROM activities a JOIN source_events se ON se.id = a.source_event_id `

// queryActivities runs an activities query (columns: activityColumns) and attaches participants.
func queryActivities(ctx context.Context, db claimstore.DB, where string, args ...any) ([]Activity, error) {
	rows, err := db.QueryContext(ctx, `SELECT `+activityColumns+activityFrom+where, args...)
	if err != nil {
		return nil, fmt.Errorf("readmodel: query activities: %w", err)
	}
	defer rows.Close()
	out := []Activity{}
	for rows.Next() {
		var a Activity
		var perms, prov []byte
		if err := rows.Scan(&a.ID, &a.IdempotencyKey, &a.ActivityType, &a.SourceSystem, &a.SourceObjectID, &a.SourceEventID,
			&a.OccurredAt, &a.IngestedAt, &a.AccountID, &a.OpportunityID, &a.AccountHint, &a.OpportunityHint, &a.Summary,
			&perms, &prov, &a.CausedBy, &a.CorrelationID); err != nil {
			return nil, fmt.Errorf("readmodel: scan activity: %w", err)
		}
		a.OccurredAt, a.IngestedAt = utc(a.OccurredAt), utc(a.IngestedAt)
		a.Permissions, a.Provenance = perms, prov
		a.PayloadRef = "source_events/" + a.SourceEventID
		a.Participants = []Participant{}
		if a.Summary != nil {
			clipped := clipRunes(*a.Summary, maxSummary)
			a.Summary = &clipped
		}
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, attachParticipants(ctx, db, out)
}

func attachParticipants(ctx context.Context, db claimstore.DB, acts []Activity) error {
	if len(acts) == 0 {
		return nil
	}
	ids := make([]string, len(acts))
	index := make(map[string]int, len(acts))
	for i, a := range acts {
		ids[i], index[a.ID] = a.ID, i
	}
	list, err := uuidList(ids)
	if err != nil {
		return err
	}
	rows, err := db.QueryContext(ctx, `SELECT activity_id::text, raw_identity, role, display_name, person_id::text
 FROM activity_participants WHERE activity_id = ANY(string_to_array($1, ',')::uuid[])
 ORDER BY activity_id, role, raw_identity`, list)
	if err != nil {
		return fmt.Errorf("readmodel: load participants: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var actID string
		var p Participant
		var name *string
		if err := rows.Scan(&actID, &p.RawIdentity, &p.Role, &name, &p.PersonID); err != nil {
			return fmt.Errorf("readmodel: scan participant: %w", err)
		}
		if name != nil {
			p.DisplayName = *name
		}
		i := index[actID]
		acts[i].Participants = append(acts[i].Participants, p)
	}
	return rows.Err()
}

// Timeline returns the account's activities, newest first, strictly before `before` when given.
// A page never ends inside a run of activities that share one occurred_at, so paging with
// NextBefore skips nothing; a page whose `limit` rows all tie is returned whole.
func (r *Reader) Timeline(ctx context.Context, accountID string, limit int, before *time.Time) (Timeline, error) {
	return r.TimelinePage(ctx, accountID, limit, before, "")
}

// TimelinePage is Timeline with the tie-break cursor of ADR-0019: with beforeID the cursor is the pair
// (before, beforeID) and the page holds the rows ordered strictly after it by (occurred_at, id) descending,
// so no number of activities sharing one instant can drop a row. beforeID needs before and must be a uuid
// (ErrInvalid). Whenever a next page exists the result carries both halves of its cursor.
func (r *Reader) TimelinePage(ctx context.Context, accountID string, limit int, before *time.Time, beforeID string) (Timeline, error) {
	if err := requireUUID("account", accountID); err != nil {
		return Timeline{}, err
	}
	n, err := normalizeLimit(limit)
	if err != nil {
		return Timeline{}, err
	}
	if beforeID != "" && (before == nil || !ValidUUID(beforeID)) {
		return Timeline{}, fmt.Errorf("before_id needs before and must be a uuid: %w", ErrInvalid)
	}
	if err := accountExists(ctx, r.db, accountID); err != nil {
		return Timeline{}, err
	}
	if beforeID != "" {
		return r.keysetPage(ctx, accountID, n, before.UTC(), beforeID)
	}
	cursor := any(nil)
	if before != nil {
		cursor = before.UTC()
	}
	acts, err := queryActivities(ctx, r.db, `WHERE a.account_id = $1::uuid AND ($2::timestamptz IS NULL OR a.occurred_at < $2)
 ORDER BY a.occurred_at DESC, a.id DESC LIMIT $3`, accountID, cursor, n+1)
	if err != nil {
		return Timeline{}, err
	}
	page, next, wholeTie := paginate(acts, n)
	if wholeTie {
		// Every row of the page shares one instant: return the whole run of ties (up to
		// maxTieGroup), since the cursor cannot resume inside it.
		page, err = queryActivities(ctx, r.db, `WHERE a.account_id = $1::uuid AND a.occurred_at = $2
 ORDER BY a.id DESC LIMIT $3`, accountID, next.UTC(), maxTieGroup)
		if err != nil {
			return Timeline{}, err
		}
	}
	return withCursor(page, next), nil
}

// keysetPage serves the rows strictly after (at, id) in (occurred_at, id) descending order. The cursor row must
// exist in the account and occurred exactly at `at` (it is the last row of the previous page): a bare id
// would otherwise widen the strict `before`, admitting rows tied at `at` that no earlier page showed.
func (r *Reader) keysetPage(ctx context.Context, accountID string, n int, at time.Time, id string) (Timeline, error) {
	var cursorAt time.Time
	err := r.db.QueryRowContext(ctx, `SELECT occurred_at FROM activities WHERE id = $1::uuid AND account_id = $2::uuid`, id, accountID).Scan(&cursorAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Timeline{}, fmt.Errorf("before_id is not an activity of the account: %w", ErrInvalid)
	}
	if err != nil {
		return Timeline{}, fmt.Errorf("readmodel: look up timeline cursor: %w", err)
	}
	if !cursorAt.Equal(at) {
		return Timeline{}, fmt.Errorf("before_id must name an activity that occurred at before: %w", ErrInvalid)
	}
	acts, err := queryActivities(ctx, r.db, `WHERE a.account_id = $1::uuid AND (a.occurred_at < $2 OR (a.occurred_at = $2 AND a.id < $3::uuid))
 ORDER BY a.occurred_at DESC, a.id DESC LIMIT $4`, accountID, at, id, n+1)
	if err != nil {
		return Timeline{}, err
	}
	if len(acts) <= n {
		return Timeline{Items: acts}, nil
	}
	page := acts[:n]
	last := page[n-1].OccurredAt
	return withCursor(page, &last), nil
}

// withCursor completes a page: when a next page exists (next != nil) the cursor is next_before plus the
// id of the page's last row, which orders the rows that share next_before's instant.
func withCursor(page []Activity, next *time.Time) Timeline {
	t := Timeline{Items: page, NextBefore: next}
	if next != nil && len(page) > 0 {
		last := page[len(page)-1].ID
		t.NextBeforeID = &last
	}
	return t
}

// paginate cuts acts (fetched with one extra row) to a page that does not split a timestamp tie.
// wholeTie is true when every kept row shares one instant and more rows follow.
func paginate(acts []Activity, n int) (page []Activity, next *time.Time, wholeTie bool) {
	if len(acts) <= n {
		return acts, nil, false
	}
	cut := n
	if acts[n].OccurredAt.Equal(acts[n-1].OccurredAt) {
		for cut > 0 && acts[cut-1].OccurredAt.Equal(acts[n].OccurredAt) {
			cut--
		}
		if cut == 0 {
			last := acts[0].OccurredAt
			return acts[:n], &last, true
		}
	}
	last := acts[cut-1].OccurredAt
	return acts[:cut], &last, false
}
