package play

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// eventTrail is what the real pipeline left behind for one sequence event: the stored source event and
// activity, the first diff that folded it, that diff's trigger evaluation, and the change, decision episode
// and graph diff that exist because of it. The advance path records this trail as the episode's bookkeeping —
// it never rewrites what the pipeline wrote.
type eventTrail struct {
	SourceEventID     string
	ActivityID        string
	StateDiffID       string
	Eligible          bool
	Coalesced         bool
	ReasonCodes       []string
	AccountChangeID   *string
	DecisionEpisodeID *string
	GraphDiffID       *int64
}

// NextEpisode is POST /replay/manifests/{id}/episodes/next: release exactly the next event. A history event
// is already in the world — releasing it records the trail the freeze's pipeline run left. The held-out
// event goes through Play itself (dataset, pin, invisibility, ingest, drain, graph wait, biwriter).
// Idempotent: advancing over an already-released position returns its bookkeeping and ingests nothing.
func (s *Service) NextEpisode(ctx context.Context, manifestID string) ([]byte, error) {
	seq, err := LoadSequence(ctx, s.db, manifestID)
	if err != nil {
		return nil, err
	}
	released, err := releasedCursor(ctx, s.db, manifestID)
	if err != nil {
		return nil, err
	}
	next := released + 1
	if next > seq.N() {
		return nil, ErrReplayComplete
	}
	var row episodeRow
	if next == seq.N() {
		row, err = s.advanceHeldOut(ctx, seq)
	} else {
		row, err = s.advanceHistory(ctx, seq, next)
	}
	if err != nil {
		return nil, err
	}
	return s.advanceResult(ctx, seq, row)
}

// advanceHistory releases a history event: under the manifest lock it records the stored trail of the event
// and moves the cursor. An existing row at the position means the episode was released before (then reset):
// it is returned unchanged — no second ingest, no second evaluation.
func (s *Service) advanceHistory(ctx context.Context, seq Sequence, position int) (episodeRow, error) {
	unlock, err := s.lock(ctx, seq.ManifestID)
	if err != nil {
		return episodeRow{}, err
	}
	defer unlock()
	if row, found, err := loadEpisode(ctx, s.db, seq.ManifestID, position); err != nil {
		return episodeRow{}, err
	} else if found {
		return row, bumpCursor(ctx, s.db, seq.ManifestID, position, s.clk.Now().UTC())
	}
	ev, _ := seq.At(position)
	trail, err := s.trail(ctx, seq, ev)
	if err != nil {
		return episodeRow{}, err
	}
	row, err := s.episodeAt(ctx, seq, ev, trail)
	if err != nil {
		return episodeRow{}, err
	}
	return row, s.commitEpisode(ctx, seq.ManifestID, row)
}

// advanceHeldOut releases event N through Play and records what it did. A Play already done — by this path
// or by POST /replay/play directly — is adopted: its bookkeeping comes from the stored trail, so the episode
// reports the same facts without a second release.
func (s *Service) advanceHeldOut(ctx context.Context, seq Sequence) (episodeRow, error) {
	position := seq.N()
	if row, found, err := loadEpisode(ctx, s.db, seq.ManifestID, position); err != nil {
		return episodeRow{}, err
	} else if found {
		return row, bumpCursor(ctx, s.db, seq.ManifestID, position, s.clk.Now().UTC())
	}
	// Play carries the manifest lock itself (the lock cannot be held across this call). It is the
	// serialization point for the release; the manifest lock below only serializes the bookkeeping phase.
	if _, err := s.Play(ctx, Request{ManifestID: seq.ManifestID}); err != nil && !errors.Is(err, ErrAlreadyReleased) {
		return episodeRow{}, err
	}
	var src, act, change sql.NullString
	err := s.db.QueryRowContext(ctx, `SELECT source_event_id::text, activity_id::text, account_change_id::text
	  FROM demo_plays WHERE manifest_id = $1::uuid`, seq.ManifestID).Scan(&src, &act, &change)
	if err != nil {
		return episodeRow{}, fmt.Errorf("play: read the play record of %s: %w", seq.ManifestID, err)
	}
	if !src.Valid || !act.Valid {
		return episodeRow{}, fmt.Errorf("%w: Play of %s recorded no released event", ErrEpisodeTrail, seq.ManifestID)
	}
	unlock, err := s.lock(ctx, seq.ManifestID)
	if err != nil {
		return episodeRow{}, err
	}
	defer unlock()
	// A concurrent advance may have recorded the bookkeeping while this one ran Play.
	if row, found, err := loadEpisode(ctx, s.db, seq.ManifestID, position); err != nil {
		return episodeRow{}, err
	} else if found {
		return row, bumpCursor(ctx, s.db, seq.ManifestID, position, s.clk.Now().UTC())
	}
	trail, err := s.trailOf(ctx, seq.AccountID, src.String, act.String, seq.Events[position-1].EventID)
	if err != nil {
		return episodeRow{}, err
	}
	trail.AccountChangeID = strOrNil(change)
	ev, _ := seq.At(position)
	row, err := s.episodeAt(ctx, seq, ev, trail)
	if err != nil {
		return episodeRow{}, err
	}
	return row, s.commitEpisode(ctx, seq.ManifestID, row)
}

// episodeAt turns a trail into the episode's bookkeeping: the fold's ids are the pipeline's provenance,
// and state_version is the version the world-timed read at the episode bound reports — which for a
// coalesced fold is the version before the shared diff landed, never a version past the bound.
func (s *Service) episodeAt(ctx context.Context, seq Sequence, ev SequenceEvent, trail eventTrail) (episodeRow, error) {
	row := trail.row(seq, ev, s.clk.Now().UTC())
	st, err := s.stateAt(ctx, seq.AccountID, ev.Position, episodeBound(seq, ev.Position))
	if err != nil {
		return episodeRow{}, err
	}
	if st != nil {
		row.StateVersion = &st.Version
	}
	return row, nil
}

// commitEpisode records an episode's bookkeeping and moves the released cursor in one transaction: a
// half-committed release is impossible.
func (s *Service) commitEpisode(ctx context.Context, manifestID string, row episodeRow) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("play: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := writeEpisode(ctx, tx, manifestID, row); err != nil {
		return err
	}
	if err := bumpCursor(ctx, tx, manifestID, row.Position, row.ReleasedAt); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("play: commit the release of episode %d: %w", row.Position, err)
	}
	return nil
}

// trail finds the stored source event and activity of a sequence event, then reads what its recompute left.
func (s *Service) trail(ctx context.Context, seq Sequence, ev SequenceEvent) (eventTrail, error) {
	var src, act string
	err := s.db.QueryRowContext(ctx, `SELECT se.id::text, a.id::text
	  FROM source_events se JOIN activities a ON a.source_event_id = se.id
	 WHERE (se.id::text = $1 OR (se.source_system = $2 AND se.source_object_id = $3 AND se.source_event_key = $4))
	   AND a.account_id = $5::uuid
	 ORDER BY (se.id::text = $1) DESC, a.occurred_at LIMIT 1`,
		ev.EventID, ev.Source.System, ev.Source.ObjectID, ev.Source.EventKey, seq.AccountID).Scan(&src, &act)
	if errors.Is(err, sql.ErrNoRows) {
		return eventTrail{}, fmt.Errorf("%w: no source event or activity of position %d (%s)", ErrEpisodeTrail, ev.Position, ev.EventID)
	}
	if err != nil {
		return eventTrail{}, fmt.Errorf("play: find the stored event of position %d: %w", ev.Position, err)
	}
	return s.trailOf(ctx, seq.AccountID, src, act, ev.EventID)
}

// trailOf reads the pipeline's record of one stored event's activity: the first diff that folded it, the
// diff's trigger evaluation, the run's decision episode, the account change and whether the projection
// recorded a graph diff naming the event.
func (s *Service) trailOf(ctx context.Context, accountID, sourceEventID, activityID, eventID string) (eventTrail, error) {
	t := eventTrail{SourceEventID: sourceEventID, ActivityID: activityID}
	var folded int
	err := s.db.QueryRowContext(ctx, `SELECT id::text, cardinality(activity_ids) FROM state_diffs
	  WHERE account_id = $1::uuid AND activity_ids @> ARRAY[$2]::uuid[] ORDER BY to_version LIMIT 1`,
		accountID, activityID).Scan(&t.StateDiffID, &folded)
	if errors.Is(err, sql.ErrNoRows) {
		return eventTrail{}, fmt.Errorf("%w: no state diff folded the activity %s", ErrEpisodeTrail, activityID)
	}
	if err != nil {
		return eventTrail{}, fmt.Errorf("play: find the diff of activity %s: %w", activityID, err)
	}
	// A diff that folded more than the event's own activity is a shared fold: its verdict describes the
	// fold, and its to_version may postdate this event's bound.
	t.Coalesced = folded > 1
	var evalID string
	var codes []byte
	err = s.db.QueryRowContext(ctx, `SELECT id::text, eligible, to_jsonb(reason_codes)::text FROM trigger_evaluations
	  WHERE state_diff_id = $1::uuid ORDER BY evaluated_at DESC, id LIMIT 1`,
		t.StateDiffID).Scan(&evalID, &t.Eligible, &codes)
	if errors.Is(err, sql.ErrNoRows) {
		return eventTrail{}, fmt.Errorf("%w: no trigger evaluation of diff %s", ErrEpisodeTrail, t.StateDiffID)
	}
	if err != nil {
		return eventTrail{}, fmt.Errorf("play: find the trigger evaluation of diff %s: %w", t.StateDiffID, err)
	}
	if err := json.Unmarshal(codes, &t.ReasonCodes); err != nil {
		return eventTrail{}, fmt.Errorf("play: decode the reason codes of evaluation %s: %w", evalID, err)
	}
	var decision sql.NullString
	if err := s.db.QueryRowContext(ctx, `SELECT de.id::text FROM decision_episodes de
	  JOIN agent_runs r ON r.id = de.agent_run_id
	  WHERE r.trigger_evaluation_id = $1::uuid OR de.state_diff_id = $2::uuid
	  ORDER BY de.created_at LIMIT 1`, evalID, t.StateDiffID).Scan(&decision); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return eventTrail{}, fmt.Errorf("play: find the decision episode of evaluation %s: %w", evalID, err)
	}
	t.DecisionEpisodeID = strOrNil(decision)
	var change sql.NullString
	if err := s.db.QueryRowContext(ctx, `SELECT id::text FROM account_changes
	  WHERE account_id = $1::uuid AND (trigger_activity_ids @> ARRAY[$2]::uuid[] OR held_out_event_id = $3::uuid)
	  ORDER BY created_at LIMIT 1`, accountID, activityID, eventID).Scan(&change); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return eventTrail{}, fmt.Errorf("play: find the account change of activity %s: %w", activityID, err)
	}
	t.AccountChangeID = strOrNil(change)
	var graphDiff sql.NullInt64
	if err := s.db.QueryRowContext(ctx, `SELECT id FROM graph_projection_diffs
	  WHERE $1::uuid = ANY(source_event_ids) ORDER BY id LIMIT 1`, sourceEventID).Scan(&graphDiff); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return eventTrail{}, fmt.Errorf("play: find the graph diffs of event %s: %w", sourceEventID, err)
	}
	if graphDiff.Valid {
		t.GraphDiffID = &graphDiff.Int64
	}
	return t, nil
}

// row is the bookkeeping the trail becomes: material is the trigger's verdict; a non-material episode
// records the evaluation's reason ("No action required").
func (t eventTrail) row(seq Sequence, ev SequenceEvent, at time.Time) episodeRow {
	r := episodeRow{
		Position:          ev.Position,
		AccountID:         seq.AccountID,
		EventID:           ev.EventID,
		SourceEventID:     t.SourceEventID,
		ActivityID:        t.ActivityID,
		StateDiffID:       &t.StateDiffID,
		Material:          t.Eligible,
		AccountChangeID:   t.AccountChangeID,
		DecisionEpisodeID: t.DecisionEpisodeID,
		GraphDiffID:       t.GraphDiffID,
		Coalesced:         t.Coalesced,
		ReleasedAt:        at,
	}
	if !t.Eligible && len(t.ReasonCodes) > 0 {
		reason := strings.Join(t.ReasonCodes, ",")
		r.NoActionReason = &reason
	}
	return r
}

func strOrNil(s sql.NullString) *string {
	if !s.Valid {
		return nil
	}
	return &s.String
}

// advanceResult is episode_replay.v1.json $defs/advanceResult.
func (s *Service) advanceResult(ctx context.Context, seq Sequence, row episodeRow) ([]byte, error) {
	ev, _ := seq.At(row.Position)
	released := eventJSON(ev, &row)
	var version *int
	var digest *string
	if st, err := s.stateAt(ctx, seq.AccountID, row.Position, episodeBound(seq, row.Position)); err != nil {
		return nil, err
	} else if st != nil {
		version = &st.Version
		digest = &st.Digest
	}
	return json.Marshal(map[string]any{
		"manifest_id":         seq.ManifestID,
		"account_id":          seq.AccountID,
		"episode":             row.Position,
		"total":               seq.N(),
		"released":            released,
		"material":            row.Material,
		"no_action_reason":    row.NoActionReason,
		"state_version":       version,
		"state_digest":        digest,
		"graph_diff_id":       row.GraphDiffID,
		"account_change_id":   row.AccountChangeID,
		"decision_episode_id": row.DecisionEpisodeID,
		"coalesced":           row.Coalesced,
		"advanced_at":         s.clk.Now().UTC(),
	})
}
