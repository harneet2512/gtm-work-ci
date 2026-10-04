package orchestrator

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/runs"
)

const maxReasonRunes = 500

// runRow is the part of an agent_runs row the orchestrator reads.
type runRow struct {
	ID, AccountID, OpportunityID, Mode, Status, EvaluationID string
	TriggerIDs                                               []string
	StateVersion                                             int
	GuidanceID                                               string // "" until guidance is persisted
}

func (r runRow) runnable() error {
	switch {
	case r.Mode != runs.DryRun:
		return fmt.Errorf("%w: run %s is %s", ErrLiveRun, r.ID, r.Mode)
	case r.Status != "pending" && r.Status != "context_built":
		return fmt.Errorf("%w: run %s is %s", ErrNotRunnable, r.ID, r.Status)
	}
	return nil
}

func (s *Service) loadRun(ctx context.Context, runID string) (runRow, error) {
	var r runRow
	var opp, guidance sql.NullString
	var version sql.NullInt64
	var ids []byte
	err := s.db.QueryRowContext(ctx, `SELECT id::text, account_id::text, opportunity_id::text, run_mode, status,
 trigger_evaluation_id::text, to_jsonb(trigger_activity_ids), state_version, decision_guidance_id::text
 FROM agent_runs WHERE id = $1::uuid`, runID).Scan(&r.ID, &r.AccountID, &opp, &r.Mode, &r.Status, &r.EvaluationID,
		&ids, &version, &guidance)
	if errors.Is(err, sql.ErrNoRows) {
		return r, fmt.Errorf("%w: run %s does not exist", ErrNotRunnable, runID)
	}
	if err != nil {
		return r, transient("load run", fmt.Errorf("read run %s: %w", runID, err))
	}
	if err := json.Unmarshal(ids, &r.TriggerIDs); err != nil {
		return r, fmt.Errorf("orchestrator: decode trigger activities of run %s: %w", runID, err)
	}
	r.OpportunityID, r.GuidanceID, r.StateVersion = opp.String, guidance.String, int(version.Int64)
	return r, nil
}

// published returns the run's outcome when its set already exists (an idempotent repeat: never regenerate).
func (s *Service) published(ctx context.Context, runID string) (Outcome, bool, error) {
	out := Outcome{RunID: runID}
	var none bool
	err := s.db.QueryRowContext(ctx, `SELECT ss.id::text, ss.decision_episode_id::text, c.id::text, ss.no_acceptable_candidate
 FROM strategy_sets ss JOIN strategy_candidates c ON c.strategy_set_id = ss.id AND c.ranking = 1
 WHERE ss.agent_run_id = $1::uuid`, runID).Scan(&out.SetID, &out.EpisodeID, &out.PreferredID, &none)
	if errors.Is(err, sql.ErrNoRows) {
		return Outcome{}, false, nil
	}
	if err != nil {
		return Outcome{}, false, transient("check published set", fmt.Errorf("read set of run %s: %w", runID, err))
	}
	if none {
		out.PreferredID = "" // ranking 1 only orders the display of a set Ghost recommends nothing from
	}
	return out, true, nil
}

// claim takes the run's draft step: exactly one caller wins, so N parallel calls (a retry, a double delivery)
// generate once. A step already running is honoured until its lease (the token's life plus a margin) expires,
// which is how a crashed caller's claim is taken over. The decision episode id is fixed at the first claim and
// reused by every resume, so the worker is always told the same episode.
func (s *Service) claim(ctx context.Context, run runRow) (string, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", transient("claim", err)
	}
	defer func() { _ = tx.Rollback() }()
	var status string
	if err := tx.QueryRowContext(ctx, `SELECT status FROM agent_runs WHERE id = $1::uuid FOR UPDATE`, run.ID).Scan(&status); err != nil {
		return "", transient("claim", fmt.Errorf("lock run %s: %w", run.ID, err))
	}
	if status != "pending" && status != "context_built" {
		return "", fmt.Errorf("%w: run %s is %s", ErrNotRunnable, run.ID, status)
	}
	var episodeID string
	err = tx.QueryRowContext(ctx, `
UPDATE agent_run_steps SET status = 'running', started_at = now(), finished_at = NULL,
  detail = (detail - 'error') || jsonb_build_object(
    'decision_episode_id', COALESCE(NULLIF(detail ->> 'decision_episode_id', ''), $2::text),
    'attempt', COALESCE((detail ->> 'attempt')::int, 0) + 1,
    'last_error', detail -> 'error')
WHERE agent_run_id = $1::uuid AND step = 'draft'
  AND (status IN ('pending', 'failed') OR (status = 'running' AND started_at < now() - make_interval(secs => $3::float8)))
RETURNING detail ->> 'decision_episode_id'`, run.ID, s.newID(), lease(s.signer.TTL()).Seconds()).Scan(&episodeID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("%w: run %s", ErrInProgress, run.ID)
	}
	if err != nil {
		return "", transient("claim", fmt.Errorf("claim draft step of run %s: %w", run.ID, err))
	}
	if err := tx.Commit(); err != nil {
		return "", transient("claim", err)
	}
	return episodeID, nil
}

// settle records how a failed execution ended and returns the classified error. It writes with its own context so
// a cancelled caller still leaves the run in a diagnosable state. Anything not already classified is transient: a
// database hiccup must never fail a run for good.
func (s *Service) settle(ctx context.Context, run runRow, err error) error {
	var t *TransientError
	var p *PermanentError
	var inv *invalidOutput
	switch {
	case errors.As(err, &p):
	case errors.As(err, &t):
	case errors.As(err, &inv):
		// unusable model output that no phase retried or classified is final, never an open run
		p = &PermanentError{Phase: "run", Reason: inv.Error(), Err: err}
		err = p
	default:
		t = &TransientError{Phase: "run", Reason: err.Error(), Err: err}
		err = t
	}
	wctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	phase, reason, kind := "", "", "transient"
	if p != nil {
		phase, reason, kind = p.Phase, truncate(p.Reason), "permanent"
	} else {
		phase, reason = t.Phase, truncate(t.Reason)
	}
	detail, _ := json.Marshal(map[string]any{"phase": phase, "kind": kind, "reason": reason})
	if werr := s.recordFailure(wctx, run.ID, kind == "permanent", detail, reason); werr != nil {
		s.log.ErrorContext(wctx, "could not record the run failure", "run_id", run.ID, "error", werr)
	}
	s.log.WarnContext(ctx, "run failed", "run_id", run.ID, "kind", kind, "phase", phase, "reason", reason)
	return err
}

func (s *Service) recordFailure(ctx context.Context, runID string, final bool, detail []byte, reason string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `UPDATE agent_run_steps SET status = 'failed', finished_at = now(),
 detail = detail || jsonb_build_object('error', $2::jsonb) WHERE agent_run_id = $1::uuid AND step = 'draft' AND status = 'running'`,
		runID, string(detail)); err != nil {
		return err
	}
	if final {
		// failed is terminal and not an open status: the account is free for its next run.
		if _, err := tx.ExecContext(ctx, `UPDATE agent_runs SET status = 'failed', error = $2, updated_at = now()
 WHERE id = $1::uuid AND status IN ('pending', 'context_built')`, runID, reason); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func truncate(s string) string {
	r := []rune(s)
	if len(r) <= maxReasonRunes {
		return s
	}
	return string(r[:maxReasonRunes-1]) + "…"
}
