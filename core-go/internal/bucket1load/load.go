// Package bucket1load assembles the Bucket 1 episode (bucket1.Episode) of one real agent run from the database:
// the trigger activities, the claims extracted from them and the claim graph before, who the activities were
// resolved to, the state diff, the open transition, the knowledge_attribution record of the build_context step,
// the knowledge as of the replay clock, the beliefs of the business-intelligence update and the knowledge
// mutations of the decision episode. Every read is as of the run's replay clock (the newest trigger activity),
// never the wall clock. A part that has no source yet (precedents) stays empty and its gate reads unknown.
package bucket1load

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/bucket1"
	"github.com/harneet2512/gtm-work/core-go/internal/claimstore"
)

// ErrNoRun: the run does not exist.
var ErrNoRun = errors.New("bucket1load: no such agent run")

// ErrNoEpisode: the run has no decision episode yet (nothing was published), so there is nothing to grade.
var ErrNoEpisode = errors.New("bucket1load: the run has no decision episode yet")

// priorClaimLimit bounds the prior claim graph read for one episode.
const priorClaimLimit = 500

type runRow struct {
	id, accountID, opportunityID, episodeID, stateDiffID string
	triggerIDs                                           []string
	stateVersion                                         int
}

// Load builds the Bucket 1 episode of an agent run.
func Load(ctx context.Context, db *sql.DB, runID string) (bucket1.Episode, error) {
	run, err := readRun(ctx, db, runID)
	if err != nil {
		return bucket1.Episode{}, err
	}
	at, err := replayClock(ctx, db, run.triggerIDs)
	if err != nil {
		return bucket1.Episode{}, err
	}
	ep := bucket1.Episode{ID: run.episodeID, Name: "run " + run.id, At: at, AccountID: run.accountID, OpportunityID: run.opportunityID}
	steps := []func(context.Context, *sql.DB, runRow, *bucket1.Episode) error{
		loadActivities, loadClaims, loadConflicts, loadPeople, loadStateDiff, loadBeliefs, loadKnowledge, loadMutations,
	}
	for _, step := range steps {
		if err := step(ctx, db, run, &ep); err != nil {
			return bucket1.Episode{}, err
		}
	}
	return ep, nil
}

func readRun(ctx context.Context, db claimstore.DB, runID string) (runRow, error) {
	r := runRow{id: runID}
	var triggers string
	err := db.QueryRowContext(ctx, `SELECT account_id::text, COALESCE(opportunity_id::text, ''),
 array_to_string(trigger_activity_ids, ','), COALESCE(state_version, 0) FROM agent_runs WHERE id = $1::uuid`, runID).
		Scan(&r.accountID, &r.opportunityID, &triggers, &r.stateVersion)
	if errors.Is(err, sql.ErrNoRows) {
		return r, fmt.Errorf("%w: %s", ErrNoRun, runID)
	}
	if err != nil {
		return r, fmt.Errorf("bucket1load: read run %s: %w", runID, err)
	}
	r.triggerIDs = strings.Split(triggers, ",")
	err = db.QueryRowContext(ctx, `SELECT id::text, COALESCE(state_diff_id::text, '') FROM decision_episodes WHERE agent_run_id = $1::uuid`, runID).
		Scan(&r.episodeID, &r.stateDiffID)
	if errors.Is(err, sql.ErrNoRows) {
		return r, fmt.Errorf("%w: %s", ErrNoEpisode, runID)
	}
	if err != nil {
		return r, fmt.Errorf("bucket1load: read the episode of run %s: %w", runID, err)
	}
	return r, nil
}

// replayClock is the newest trigger activity's time: the world time of the episode (invariant I3).
func replayClock(ctx context.Context, db claimstore.DB, triggerIDs []string) (time.Time, error) {
	var at sql.NullTime
	if err := db.QueryRowContext(ctx, `SELECT max(occurred_at) FROM activities WHERE id = ANY($1::uuid[])`, uuidArray(triggerIDs)).Scan(&at); err != nil {
		return time.Time{}, fmt.Errorf("bucket1load: replay clock: %w", err)
	}
	if !at.Valid {
		return time.Time{}, errors.New("bucket1load: none of the run's trigger activities exists, so there is no replay clock")
	}
	return at.Time.UTC(), nil
}

func uuidArray(ids []string) string { return "{" + strings.Join(ids, ",") + "}" }
