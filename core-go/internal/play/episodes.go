package play

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"time"
)

// episodeRow is the bookkeeping of one released episode (demo_episodes, migration 0026): what releasing the
// event at that position left behind. Written once, at release time; never updated.
type episodeRow struct {
	Position          int
	AccountID         string
	EventID           string
	SourceEventID     string
	ActivityID        string
	StateDiffID       *string
	StateVersion      *int
	Material          bool
	NoActionReason    *string
	AccountChangeID   *string
	DecisionEpisodeID *string
	GraphDiffID       *int64
	Coalesced         bool
	ReleasedAt        time.Time
}

const episodeColumns = `position, account_id::text, event_id::text, source_event_id::text, activity_id::text,
	state_diff_id::text, state_version, material, no_action_reason, account_change_id::text,
	decision_episode_id::text, graph_diff_id, coalesced, released_at`

// scanner is what sql.Row and sql.Rows share, so one scan list serves both load paths.
type scanner interface {
	Scan(dest ...any) error
}

func scanInto(e *episodeRow, row scanner) error {
	return row.Scan(&e.Position, &e.AccountID, &e.EventID, &e.SourceEventID, &e.ActivityID, &e.StateDiffID,
		&e.StateVersion, &e.Material, &e.NoActionReason, &e.AccountChangeID, &e.DecisionEpisodeID,
		&e.GraphDiffID, &e.Coalesced, &e.ReleasedAt)
}

func scanEpisode(row *sql.Row) (episodeRow, bool, error) {
	var e episodeRow
	err := scanInto(&e, row)
	if errors.Is(err, sql.ErrNoRows) {
		return episodeRow{}, false, nil
	}
	if err != nil {
		return episodeRow{}, false, fmt.Errorf("play: read an episode row: %w", err)
	}
	return e, true, nil
}

// loadEpisode reads the bookkeeping row of one position; found is false when it was never released.
func loadEpisode(ctx context.Context, db rowQuerier, manifestID string, position int) (episodeRow, bool, error) {
	return scanEpisode(db.QueryRowContext(ctx,
		`SELECT `+episodeColumns+` FROM demo_episodes WHERE manifest_id = $1::uuid AND position = $2`,
		manifestID, position))
}

// loadEpisodes reads every released episode of a manifest, chronological.
func loadEpisodes(ctx context.Context, db *sql.DB, manifestID string) ([]episodeRow, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT `+episodeColumns+` FROM demo_episodes WHERE manifest_id = $1::uuid ORDER BY position`, manifestID)
	if err != nil {
		return nil, fmt.Errorf("play: read the episodes of %s: %w", manifestID, err)
	}
	defer rows.Close()
	var out []episodeRow
	for rows.Next() {
		var e episodeRow
		if err := scanInto(&e, rows); err != nil {
			return nil, fmt.Errorf("play: scan an episode of %s: %w", manifestID, err)
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// releasedCursor is the replay's released position (demo_replays.released); 0 before the first advance.
func releasedCursor(ctx context.Context, db rowQuerier, manifestID string) (int, error) {
	var released int
	err := db.QueryRowContext(ctx, `SELECT released FROM demo_replays WHERE manifest_id = $1::uuid`, manifestID).
		Scan(&released)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("play: read the replay cursor of %s: %w", manifestID, err)
	}
	return released, nil
}

// maxReleasedPosition is the highest position with a bookkeeping row: the furthest point a reset can return
// the cursor to without inventing releases.
func maxReleasedPosition(ctx context.Context, db rowQuerier, manifestID string) (int, error) {
	var max int
	err := db.QueryRowContext(ctx,
		`SELECT coalesce(max(position), 0) FROM demo_episodes WHERE manifest_id = $1::uuid`, manifestID).
		Scan(&max)
	if err != nil {
		return 0, fmt.Errorf("play: read the released positions of %s: %w", manifestID, err)
	}
	return max, nil
}

type execQuerier interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// writeEpisode records an episode's bookkeeping. Idempotent: a second release of the same position is a
// no-op — the row already tells what the release did.
func writeEpisode(ctx context.Context, ex execQuerier, manifestID string, e episodeRow) error {
	_, err := ex.ExecContext(ctx, `INSERT INTO demo_episodes (manifest_id, position, account_id, event_id,
		source_event_id, activity_id, state_diff_id, state_version, material, no_action_reason,
		account_change_id, decision_episode_id, graph_diff_id, coalesced, released_at)
	 VALUES ($1::uuid, $2, $3::uuid, $4::uuid, $5::uuid, $6::uuid, $7::uuid, $8, $9, $10, $11::uuid, $12::uuid, $13, $14, $15)
	 ON CONFLICT (manifest_id, position) DO NOTHING`,
		manifestID, e.Position, e.AccountID, e.EventID, e.SourceEventID, e.ActivityID, e.StateDiffID,
		e.StateVersion, e.Material, e.NoActionReason, e.AccountChangeID, e.DecisionEpisodeID, e.GraphDiffID,
		e.Coalesced, e.ReleasedAt)
	if err != nil {
		return fmt.Errorf("play: record episode %d of %s: %w", e.Position, manifestID, err)
	}
	return nil
}

// bumpCursor advances the released cursor to position — guarded: only from position-1, so a racing advance
// that lost the release race cannot move it backwards or sideways.
func bumpCursor(ctx context.Context, ex execQuerier, manifestID string, position int, at time.Time) error {
	res, err := ex.ExecContext(ctx, `INSERT INTO demo_replays (manifest_id, released, updated_at) VALUES ($1::uuid, $2, $3)
	 ON CONFLICT (manifest_id) DO UPDATE SET released = $2, updated_at = $3
	   WHERE demo_replays.released = $2 - 1`, manifestID, position, at)
	if err != nil {
		return fmt.Errorf("play: move the replay cursor of %s to %d: %w", manifestID, position, err)
	}
	if n, err := res.RowsAffected(); err == nil && n == 0 {
		return fmt.Errorf("%w: the replay cursor of %s is not at %d", ErrPlayInProgress, manifestID, position-1)
	}
	return nil
}

// setCursor moves the released cursor to an arbitrary released position (reset). Unlike bumpCursor it is not
// guarded: the caller holds the manifest lock and has checked the target was once released.
func setCursor(ctx context.Context, ex execQuerier, manifestID string, released int, at time.Time) error {
	_, err := ex.ExecContext(ctx, `INSERT INTO demo_replays (manifest_id, released, updated_at) VALUES ($1::uuid, $2, $3)
	 ON CONFLICT (manifest_id) DO UPDATE SET released = $2, updated_at = $3`, manifestID, released, at)
	if err != nil {
		return fmt.Errorf("play: reset the replay cursor of %s to %d: %w", manifestID, released, err)
	}
	return nil
}

// provenanceOriginPattern is common.v1.json recordOrigin; provenancePattern is recordProvenance.
var (
	provenanceOriginSet = map[string]bool{"live": true, "dataset": true, "synthetic": true}
	provenancePattern   = regexp.MustCompile(`^[a-z][a-z0-9-]*:[A-Za-z0-9._-]{1,64}$`)
)

// episodeEventJSON is episode_replay.v1.json $defs/episodeEvent: one sequence event plus, when it was
// released, what the release left behind. An unreleased event is withheld: released=false and every
// consequence null — it carries no state, change or materiality.
type episodeEventJSON struct {
	Position          int       `json:"position"`
	EventID           string    `json:"event_id"`
	OccurredAt        time.Time `json:"occurred_at"`
	SourceSystem      string    `json:"source_system"`
	ProvenanceOrigin  *string   `json:"provenance_origin"`
	Provenance        *string   `json:"provenance"`
	Released          bool      `json:"released"`
	HeldOut           bool      `json:"held_out"`
	Material          *bool     `json:"material"`
	AccountChangeID   *string   `json:"account_change_id"`
	DecisionEpisodeID *string   `json:"decision_episode_id"`
	StateVersion      *int      `json:"state_version"`
	GraphDiffID       *int64    `json:"graph_diff_id"`
	NoActionReason    *string   `json:"no_action_reason"`
	Coalesced         *bool     `json:"coalesced"`
}

// eventJSON renders a sequence event; row is its bookkeeping when released (nil = withheld).
func eventJSON(ev SequenceEvent, row *episodeRow) episodeEventJSON {
	e := episodeEventJSON{
		Position:     ev.Position,
		EventID:      ev.EventID,
		OccurredAt:   ev.OccurredAt.UTC(),
		SourceSystem: ev.Source.System,
		HeldOut:      ev.HeldOut,
	}
	if provenanceOriginSet[ev.Origin] {
		e.ProvenanceOrigin = &ev.Origin
	}
	if provenancePattern.MatchString(ev.Provenance) {
		e.Provenance = &ev.Provenance
	}
	if row == nil {
		return e
	}
	e.Released = true
	e.Material = &row.Material
	e.AccountChangeID = row.AccountChangeID
	e.DecisionEpisodeID = row.DecisionEpisodeID
	e.StateVersion = row.StateVersion
	e.GraphDiffID = row.GraphDiffID
	e.NoActionReason = row.NoActionReason
	e.Coalesced = &row.Coalesced
	return e
}
