package readmodel

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/harneet2512/gtm-work/core-go/internal/claimstore"
)

// Phases of Generation (agent_run.v1.json generation.phase).
const (
	PhaseQueued          = "queued"
	PhaseGenerating      = "generating"
	PhaseEvaluating      = "evaluating"
	PhasePaused          = "paused"
	PhasePublished       = "published"
	PhaseFailed          = "failed"
	PhaseNotOrchestrated = "not_orchestrated"
)

const maxReasonRunes = 500

// Generation is agent_run.v1.json generation: where the run orchestrator is with a run (HAR-117). It is derived,
// never stored: the run status, the draft step the orchestrator owns, and the StrategySet if it exists.
type Generation struct {
	Phase             string  `json:"phase"`
	Attempt           int     `json:"attempt"`
	Reason            *string `json:"reason"`
	StrategySetID     *string `json:"strategy_set_id"`
	DecisionEpisodeID *string `json:"decision_episode_id"`
}

// draftFacts are the inputs of the derivation.
type draftFacts struct {
	runStatus    string
	runError     string
	stepStatus   string // "" when the run has no draft step
	hasGenerated bool   // the validated candidates are stored: they are being evaluated
	failReason   string // the draft step's recorded failure, if any
	hasSet       bool
}

// phaseOf maps the facts to a phase. A StrategySet always means published; otherwise only runs the orchestrator
// still owns (pending, context_built) have a phase of their own.
func phaseOf(f draftFacts) string {
	switch {
	case f.hasSet:
		return PhasePublished
	case f.runStatus == "failed":
		return PhaseFailed
	case f.runStatus != "pending" && f.runStatus != "context_built":
		return PhaseNotOrchestrated
	}
	switch f.stepStatus {
	case "running":
		if f.hasGenerated {
			return PhaseEvaluating
		}
		return PhaseGenerating
	case "failed":
		return PhasePaused
	case "succeeded":
		return PhaseGenerating // between the draft step and the publish commit; never rests here
	default:
		return PhaseQueued
	}
}

func reasonOf(phase string, f draftFacts) *string {
	var r string
	switch phase {
	case PhaseFailed:
		r = f.runError
		if r == "" {
			r = f.failReason
		}
	case PhasePaused:
		r = f.failReason
	}
	if r == "" {
		return nil
	}
	r = clipRunes(r, maxReasonRunes)
	return &r
}

func loadGeneration(ctx context.Context, db claimstore.DB, runID, runStatus string, runError *string) (*Generation, error) {
	f := draftFacts{runStatus: runStatus}
	if runError != nil {
		f.runError = *runError
	}
	var attempt sql.NullInt64
	var stepStatus, reason sql.NullString
	err := db.QueryRowContext(ctx, `SELECT status, (detail ->> 'attempt')::int, detail ? 'generated', detail #>> '{error,reason}'
 FROM agent_run_steps WHERE agent_run_id = $1::uuid AND step = 'draft'`, runID).Scan(&stepStatus, &attempt, &f.hasGenerated, &reason)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("readmodel: load draft step: %w", err)
	}
	f.stepStatus, f.failReason = stepStatus.String, reason.String
	g := &Generation{Attempt: int(attempt.Int64)}
	var set, episode sql.NullString
	err = db.QueryRowContext(ctx, `SELECT id::text, decision_episode_id::text FROM strategy_sets WHERE agent_run_id = $1::uuid`, runID).Scan(&set, &episode)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("readmodel: load strategy set of run: %w", err)
	}
	if set.Valid {
		f.hasSet = true
		g.StrategySetID, g.DecisionEpisodeID = &set.String, &episode.String
	}
	g.Phase = phaseOf(f)
	g.Reason = reasonOf(g.Phase, f)
	return g, nil
}

// Run returns one run in the shape of agent_run.v1.json, with its generation status. ErrNotFound: no such run.
func (r *Reader) Run(ctx context.Context, runID string) (Run, error) {
	if err := requireUUID("run", runID); err != nil {
		return Run{}, err
	}
	var run Run
	err := r.snapshot(ctx, func(db claimstore.DB) error {
		var err error
		run, err = loadRun(ctx, db, runID)
		return err
	})
	return run, err
}
