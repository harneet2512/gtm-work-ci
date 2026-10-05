package stageevents

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/harneet2512/gtm-work/core-go/internal/surfacemsg"
)

// SurfaceMessages is the create-only message-ref API Slack posts through (api.SurfaceMessageService);
// *surfacemsg.Store implements it.
type SurfaceMessages interface {
	Get(ctx context.Context, subject, surface, kind string) (surfacemsg.Ref, error)
	Reserve(ctx context.Context, subject, surface, kind, channel string) (surfacemsg.Ref, bool, error)
	RecordTS(ctx context.Context, subject, surface, kind, ts string) (surfacemsg.Ref, error)
}

// ObservedSurfaceMessages wraps the message refs so the Cliff stage follows what Slack really did: a reservation
// is a message that may or may not have been posted (the stage is running, the ref's ts is null) and a recorded ts
// is a posted message. The inner store's answer always wins: a recording problem is logged and dropped, because
// the Slack post already happened and its ref is the truth.
type ObservedSurfaceMessages struct {
	Inner SurfaceMessages
	Rec   *Recorder
}

// Get passes through.
func (o ObservedSurfaceMessages) Get(ctx context.Context, subject, surface, kind string) (surfacemsg.Ref, error) {
	return o.Inner.Get(ctx, subject, surface, kind)
}

// Reserve reserves the slot and, when this call created it, records the message as reserved.
func (o ObservedSurfaceMessages) Reserve(ctx context.Context, subject, surface, kind, channel string) (surfacemsg.Ref, bool, error) {
	ref, created, err := o.Inner.Reserve(ctx, subject, surface, kind, channel)
	if err == nil && created {
		o.observe(ctx, ref)
	}
	return ref, created, err
}

// RecordTS records the post's ts and the message as posted.
func (o ObservedSurfaceMessages) RecordTS(ctx context.Context, subject, surface, kind, ts string) (surfacemsg.Ref, error) {
	ref, err := o.Inner.RecordTS(ctx, subject, surface, kind, ts)
	if err == nil {
		o.observe(ctx, ref)
	}
	return ref, err
}

func (o ObservedSurfaceMessages) observe(ctx context.Context, ref surfacemsg.Ref) {
	if err := o.Rec.ObserveSurfaceMessage(ctx, ref); err != nil {
		slog.WarnContext(ctx, "could not record the Cliff stage of a surface message", "subject_id", ref.SubjectID, "kind", ref.Kind, "error", err)
	}
}

// ObserveSurfaceMessage folds one Slack message ref into the Cliff stage of the Play (or, for a run that did not
// come from a Play, of the run) that owns it. A message that no run or Play owns, or that is not on Slack, is not
// a Cliff message and changes nothing. The stage is running while any of its messages is reserved and not yet
// posted, and completed once every message that exists is posted. Completed is final: a message reserved later
// (the chooser, the judgment) is added to slack_refs and the detail but never reopens an ended stage, so an
// overall `complete` is never taken back. A reservation whose post is never recorded does not stay running
// forever: the reader reports a running stage past its deadline as unknown (see staleAfter).
func (r *Recorder) ObserveSurfaceMessage(ctx context.Context, ref surfacemsg.Ref) error {
	if r == nil || ref.Surface != "slack" {
		return nil
	}
	ctx, cancel := writeContext(ctx)
	defer cancel()
	scope, found, err := r.cliffScope(ctx, ref)
	if err != nil || !found {
		return err
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("stageevents: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	owner := scope.ManifestID
	if scope.RunID != "" {
		owner = scope.RunID
	}
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, "ghost.cliff:"+owner); err != nil {
		return fmt.Errorf("stageevents: lock the cliff stage: %w", err)
	}
	refs, err := cliffRefs(ctx, tx, scope)
	if err != nil {
		return err
	}
	refs = mergeSlackRef(refs, ref)
	if err := r.writeCliff(ctx, tx, scope, refs, ref); err != nil {
		return err
	}
	return tx.Commit()
}

// cliffScope finds who owns the message: the Play of its subject when there is one, else the run.
func (r *Recorder) cliffScope(ctx context.Context, ref surfacemsg.Ref) (Scope, bool, error) {
	var q string
	if ref.Kind == surfacemsg.KindBI {
		// message 1 is keyed by the BusinessIntelligenceUpdate: the Play that wrote its change, else the run of its episode
		q = `SELECT COALESCE(p.account_id, r.account_id)::text, p.manifest_id::text, r.id::text FROM business_intelligence_updates b
 LEFT JOIN demo_plays p ON p.account_change_id = b.account_change_id
 LEFT JOIN decision_episodes e ON e.business_intelligence_update_id = b.id
 LEFT JOIN agent_runs r ON r.id = e.agent_run_id
 WHERE b.id = $1::uuid ORDER BY (p.manifest_id IS NULL), r.created_at DESC LIMIT 1`
	} else {
		// messages 2 and 3 are keyed by the DecisionEpisode
		q = `SELECT r.account_id::text,
   (SELECT p.manifest_id::text FROM demo_plays p WHERE p.activity_id = ANY (r.trigger_activity_ids) ORDER BY p.started_at LIMIT 1), r.id::text
 FROM decision_episodes e JOIN agent_runs r ON r.id = e.agent_run_id WHERE e.id = $1::uuid`
	}
	var account, manifest, run sql.NullString
	err := r.db.QueryRowContext(ctx, q, ref.SubjectID).Scan(&account, &manifest, &run)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && !account.Valid) {
		return Scope{}, false, nil
	}
	if err != nil {
		return Scope{}, false, fmt.Errorf("stageevents: find the owner of message %s: %w", ref.SubjectID, err)
	}
	switch {
	case manifest.Valid:
		return Scope{ManifestID: manifest.String, AccountID: account.String}, true, nil
	case run.Valid:
		return Scope{RunID: run.String, AccountID: account.String}, true, nil
	default:
		return Scope{}, false, nil
	}
}

func cliffRefs(ctx context.Context, tx *sql.Tx, scope Scope) (Refs, error) {
	q, owner := `SELECT refs::text FROM pipeline_stage_events WHERE manifest_id = $1::uuid AND run_id IS NULL AND stage = 'cliff'`, scope.ManifestID
	if scope.RunID != "" {
		q, owner = `SELECT refs::text FROM pipeline_stage_events WHERE run_id = $1::uuid AND stage = 'cliff'`, scope.RunID
	}
	var raw string
	err := tx.QueryRowContext(ctx, q, owner).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return Refs{}, nil
	}
	if err != nil {
		return Refs{}, fmt.Errorf("stageevents: read the cliff stage: %w", err)
	}
	var refs Refs
	if err := json.Unmarshal([]byte(raw), &refs); err != nil {
		return Refs{}, fmt.Errorf("stageevents: decode the cliff refs: %w", err)
	}
	return refs, nil
}

// mergeSlackRef returns refs with the message's ref added, or replaced when the message is already listed (a
// post updates its reservation). The input is not modified.
func mergeSlackRef(refs Refs, m surfacemsg.Ref) Refs {
	next := SlackRef{SubjectID: m.SubjectID, Kind: m.Kind, Channel: m.Channel, TS: m.TS}
	out := make([]SlackRef, 0, len(refs.SlackRefs)+1)
	replaced := false
	for _, have := range refs.SlackRefs {
		if have.SubjectID == next.SubjectID && have.Kind == next.Kind {
			out, replaced = append(out, next), true
			continue
		}
		out = append(out, have)
	}
	if !replaced {
		out = append(out, next)
	}
	refs.SlackRefs = out
	return refs
}

func (r *Recorder) writeCliff(ctx context.Context, tx *sql.Tx, scope Scope, refs Refs, latest surfacemsg.Ref) error {
	posted, kinds, waiting := 0, make([]string, 0, len(refs.SlackRefs)), []string{}
	for _, s := range refs.SlackRefs {
		if s.TS != nil {
			posted++
			kinds = append(kinds, s.Kind)
		} else {
			waiting = append(waiting, s.Kind)
		}
	}
	status, ended := Running, any(nil)
	now := r.clk.Now().UTC()
	if posted == len(refs.SlackRefs) {
		status = Completed
	}
	detail := "no message posted yet"
	if posted > 0 {
		detail = strings.Join(kinds, " + ") + " posted"
	}
	if posted > 0 && len(waiting) > 0 {
		detail += "; " + strings.Join(waiting, " + ") + " reserved, post not recorded yet"
	}
	raw, err := json.Marshal(refs)
	if err != nil {
		return fmt.Errorf("stageevents: encode refs: %w", err)
	}
	started := latest.ReservedAt.UTC()
	if now.Before(started) { // the recorder's clock and the database's can differ; a stage never ends before it starts
		now = started
	}
	if status == Completed {
		ended = now
	}
	q, owner := cliffUpsert("manifest_id", "(manifest_id, stage) WHERE run_id IS NULL"), scope.ManifestID
	if scope.RunID != "" {
		q, owner = cliffUpsert("run_id", "(run_id, stage) WHERE run_id IS NOT NULL"), scope.RunID
	}
	if _, err := tx.ExecContext(ctx, q, owner, scope.AccountID, string(status), started, ended, string(raw), detail, now); err != nil {
		return fmt.Errorf("stageevents: write the cliff stage: %w", err)
	}
	return nil
}

// cliffUpsert writes the one cliff row of an owner, keeping the earliest start. A completed row keeps its status
// and end (a later message never reopens it); its refs and detail still follow the messages.
func cliffUpsert(ownerColumn, conflictTarget string) string {
	return `INSERT INTO pipeline_stage_events (` + ownerColumn + `, account_id, stage, status, started_at, ended_at, refs, detail, updated_at)
 VALUES ($1::uuid, $2::uuid, 'cliff', $3, $4, $5, $6::jsonb, $7, $8)
 ON CONFLICT ` + conflictTarget + ` DO UPDATE SET
   status = CASE WHEN pipeline_stage_events.status = 'completed' THEN pipeline_stage_events.status ELSE EXCLUDED.status END,
   ended_at = CASE WHEN pipeline_stage_events.status = 'completed' THEN pipeline_stage_events.ended_at ELSE EXCLUDED.ended_at END,
   refs = EXCLUDED.refs,
   detail = EXCLUDED.detail, updated_at = EXCLUDED.updated_at, failure_kind = NULL,
   started_at = LEAST(pipeline_stage_events.started_at, EXCLUDED.started_at)`
}
