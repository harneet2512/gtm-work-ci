package readmodel

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/harneet2512/gtm-work/core-go/internal/claimstore"
	"github.com/harneet2512/gtm-work/core-go/internal/reactions"
)

// loadTrigger reads the run's trigger evaluation; its agent_run_id is the run when the evaluation
// was eligible (the link lives only on agent_runs).
func loadTrigger(ctx context.Context, db claimstore.DB, run Run) (TriggerEvaluation, error) {
	var te TriggerEvaluation
	var reasons, signals []byte
	var explanation *string
	err := db.QueryRowContext(ctx, `SELECT id::text, account_id::text, workflow, eligible, to_jsonb(reason_codes), explanation,
 to_jsonb(signal_ids), state_diff_id::text, evaluated_at FROM trigger_evaluations WHERE id = $1::uuid`,
		run.TriggerEvaluationID).Scan(&te.ID, &te.AccountID, &te.Workflow, &te.Eligible, &reasons, &explanation,
		&signals, &te.StateDiffID, &te.EvaluatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return te, fmt.Errorf("readmodel: run %s has no trigger evaluation: %w", run.ID, ErrNotFound)
	}
	if err != nil {
		return te, fmt.Errorf("readmodel: load trigger evaluation: %w", err)
	}
	te.ReasonCodes, te.SignalIDs, te.EvaluatedAt = reasons, signals, utc(te.EvaluatedAt)
	if explanation != nil {
		te.Explanation = *explanation
	}
	if te.Eligible {
		id := run.ID
		te.AgentRunID = &id
	}
	return te, nil
}

// loadRunDiff finds the diff behind the run: the trigger evaluation's, else the one that produced
// the run's state version. nil when neither exists (HAR-106 has not recorded it).
func loadRunDiff(ctx context.Context, db claimstore.DB, run Run, evaluated *string) (*StateDiff, error) {
	var row interface{ Scan(...any) error }
	switch {
	case evaluated != nil:
		row = db.QueryRowContext(ctx, `SELECT `+diffColumns+` FROM state_diffs WHERE id = $1::uuid AND account_id = $2::uuid`,
			*evaluated, run.AccountID)
	case run.StateVersion != nil:
		row = db.QueryRowContext(ctx, `SELECT `+diffColumns+` FROM state_diffs
 WHERE account_id = $1::uuid AND to_version = $2`, run.AccountID, *run.StateVersion)
	default:
		return nil, nil
	}
	d, err := scanDiff(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("readmodel: load run diff: %w", err)
	}
	return &d, nil
}

// loadRunStates fills state_at_run (the run's state version, else the diff's to_version) and
// state_before (the diff's from_version) from state_history.
func loadRunStates(ctx context.Context, db claimstore.DB, tr *Trace) error {
	tr.StateBefore, tr.StateAtRun = json.RawMessage("null"), json.RawMessage("null")
	version := tr.Run.StateVersion
	if version == nil && tr.StateDiff != nil {
		v := tr.StateDiff.ToVersion
		version = &v
	}
	var err error
	if version != nil {
		if tr.StateAtRun, err = historyState(ctx, db, tr.Run.AccountID, *version); err != nil {
			return err
		}
	}
	if tr.StateDiff != nil {
		tr.StateBefore, err = historyState(ctx, db, tr.Run.AccountID, tr.StateDiff.FromVersion)
	}
	return err
}

func historyState(ctx context.Context, db claimstore.DB, accountID string, version int) (json.RawMessage, error) {
	var raw []byte
	err := db.QueryRowContext(ctx, `SELECT state FROM state_history WHERE account_id = $1::uuid AND version = $2`,
		accountID, version).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return json.RawMessage("null"), nil
	}
	if err != nil {
		return nil, fmt.Errorf("readmodel: load state version %d: %w", version, err)
	}
	return raw, nil
}

// loadRunActivities fills the trigger activities (chronological) and the activities that share
// the run's correlation_id.
func loadRunActivities(ctx context.Context, db claimstore.DB, tr *Trace) error {
	list, err := uuidList(tr.Run.TriggerActivityIDs)
	if err != nil {
		return err
	}
	if tr.TriggerActivities, err = queryActivities(ctx, db,
		`WHERE a.account_id = $2::uuid AND a.id = ANY(string_to_array($1, ',')::uuid[]) ORDER BY a.occurred_at, a.id`,
		list, tr.Run.AccountID); err != nil {
		return err
	}
	tr.CorrelatedActivities = []Activity{}
	if tr.Run.CorrelationID == nil {
		return nil
	}
	tr.CorrelatedActivities, err = queryActivities(ctx, db, `WHERE a.correlation_id = $1::uuid AND a.account_id = $4::uuid
 AND NOT (a.id = ANY(string_to_array($2, ',')::uuid[])) ORDER BY a.occurred_at, a.id LIMIT $3`,
		*tr.Run.CorrelationID, list, maxCorrelated, tr.Run.AccountID)
	return err
}

// loadRunReactions fills placeholders.customer_reactions with the supervision rows HAR-120 detected
// for this run (empty for a run that never sent or has not been scanned).
func loadRunReactions(ctx context.Context, db claimstore.DB, runID string) ([]reactions.CustomerReaction, error) {
	out, err := reactions.ReactionsForRun(ctx, db, runID)
	if err != nil {
		return nil, fmt.Errorf("readmodel: load run reactions: %w", err)
	}
	return out, nil
}

func loadDecisions(ctx context.Context, db claimstore.DB, runID string) ([]Decision, error) {
	rows, err := db.QueryContext(ctx, `SELECT id::text, agent_run_id::text, decision, surface, actor_person_id::text,
 actor_label, edited_artifact, reason, created_at FROM human_decisions WHERE agent_run_id = $1::uuid ORDER BY created_at, id`, runID)
	if err != nil {
		return nil, fmt.Errorf("readmodel: load decisions: %w", err)
	}
	defer rows.Close()
	out := []Decision{}
	for rows.Next() {
		var d Decision
		var edited []byte
		if err := rows.Scan(&d.ID, &d.AgentRunID, &d.Decision, &d.Surface, &d.ActorPersonID, &d.ActorLabel,
			&edited, &d.Reason, &d.CreatedAt); err != nil {
			return nil, fmt.Errorf("readmodel: scan decision: %w", err)
		}
		d.EditedArtifact, d.CreatedAt = edited, utc(d.CreatedAt)
		if len(d.EditedArtifact) == 0 {
			d.EditedArtifact = json.RawMessage("null")
		}
		out = append(out, d)
	}
	return out, rows.Err()
}
