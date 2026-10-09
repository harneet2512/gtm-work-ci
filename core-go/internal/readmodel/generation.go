package readmodel

import (
	"context"
	"database/sql"
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
	gens, err := loadGenerations(ctx, db, runID, map[string]Run{runID: {ID: runID, Status: runStatus, Error: runError}})
	if err != nil {
		return nil, err
	}
	return gens[runID], nil
}

// loadGenerations derives the generation status of every run in runs (keyed by id; list is the comma-joined ids) from
// two statements: the draft steps and the strategy sets.
func loadGenerations(ctx context.Context, db claimstore.DB, list string, runs map[string]Run) (map[string]*Generation, error) {
	facts := map[string]*draftFacts{}
	gens := map[string]*Generation{}
	for id, run := range runs {
		f := &draftFacts{runStatus: run.Status}
		if run.Error != nil {
			f.runError = *run.Error
		}
		facts[id], gens[id] = f, &Generation{}
	}
	rows, err := db.QueryContext(ctx, `SELECT agent_run_id::text, status, (detail ->> 'attempt')::int, detail ? 'generated', detail #>> '{error,reason}'
 FROM agent_run_steps WHERE agent_run_id = ANY(string_to_array($1, ',')::uuid[]) AND step = 'draft'`, list)
	if err != nil {
		return nil, fmt.Errorf("readmodel: load draft step: %w", err)
	}
	for rows.Next() {
		var id string
		var attempt sql.NullInt64
		var stepStatus, reason sql.NullString
		var generated bool
		if err := rows.Scan(&id, &stepStatus, &attempt, &generated, &reason); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("readmodel: scan draft step: %w", err)
		}
		if f := facts[id]; f != nil {
			f.stepStatus, f.hasGenerated, f.failReason = stepStatus.String, generated, reason.String
			gens[id].Attempt = int(attempt.Int64)
		}
	}
	if err := closeRows(rows, "draft steps"); err != nil {
		return nil, err
	}
	sets, err := db.QueryContext(ctx, `SELECT agent_run_id::text, id::text, decision_episode_id::text FROM strategy_sets
 WHERE agent_run_id = ANY(string_to_array($1, ',')::uuid[])`, list)
	if err != nil {
		return nil, fmt.Errorf("readmodel: load strategy set of run: %w", err)
	}
	for sets.Next() {
		var id string
		var set, episode sql.NullString
		if err := sets.Scan(&id, &set, &episode); err != nil {
			_ = sets.Close()
			return nil, fmt.Errorf("readmodel: scan strategy set: %w", err)
		}
		if f := facts[id]; f != nil && set.Valid {
			f.hasSet = true
			setID, episodeID := set.String, episode.String
			gens[id].StrategySetID, gens[id].DecisionEpisodeID = &setID, &episodeID
		}
	}
	if err := closeRows(sets, "strategy sets"); err != nil {
		return nil, err
	}
	for id, g := range gens {
		g.Phase = phaseOf(*facts[id])
		g.Reason = reasonOf(g.Phase, *facts[id])
	}
	return gens, nil
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

// LoadGeneration is the derived generation status of a run (agent_run.v1.json generation) for a reader outside this
// package that already holds the run's status and error (internal/controlplane).
func LoadGeneration(ctx context.Context, db claimstore.DB, runID, runStatus string, runError *string) (*Generation, error) {
	return loadGeneration(ctx, db, runID, runStatus, runError)
}
