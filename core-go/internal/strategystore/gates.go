package strategystore

import (
	"context"
	"sync"
	"time"
)

// GateRunner runs the Bucket 2 gates of a run's episode (bucket2run.Runner). It is idempotent: every call
// re-measures what exists so far and replaces the earlier results.
type GateRunner interface {
	RunGates(ctx context.Context, runID string) error
}

// gateTimeout bounds one background gate run (a handful of cached judge calls).
const gateTimeout = 5 * time.Minute

// WithGates runs the Bucket 2 gates after every step of the real path that gives them new facts: the choice and
// each edit, the send (after the judgment inference), and the person's answer to Message 3. Without a runner no
// gate result is stored, so every Bucket 2 gate reads "not measured".
func WithGates(g GateRunner) Option { return func(s *Service) { s.gates = g } }

// SetGates sets the gate runner after construction (the runner reads this service, so it cannot exist first).
func (s *Service) SetGates(g GateRunner) { s.gates = g }

// gateState serializes gate runs per service so two quick clicks cannot interleave their writes.
type gateState struct {
	mu sync.Mutex
	wg sync.WaitGroup
}

// WaitGates blocks until every gate run started so far has finished (used by tests and by shutdown).
func (s *Service) WaitGates() { s.gateRuns.wg.Wait() }

// runGates measures the gates in the background: a Slack click must be acknowledged within seconds and the gates
// call cached model judges. A failure is logged, never returned: the human's action is already durable.
func (s *Service) runGates(ctx context.Context, runID, after string) {
	s.RunGatesAsync(ctx, runID, after)
}

// RunGatesAsync starts a background gate run for the run (no-op without a runner). Core calls it after a strategy set is
// published as well as after each human step.
func (s *Service) RunGatesAsync(ctx context.Context, runID, after string) {
	if s.gates == nil {
		return
	}
	detached := context.WithoutCancel(ctx)
	s.gateRuns.wg.Add(1)
	go func() {
		defer s.gateRuns.wg.Done()
		s.gateRuns.mu.Lock()
		defer s.gateRuns.mu.Unlock()
		ctx, cancel := context.WithTimeout(detached, gateTimeout)
		defer cancel()
		if err := s.gates.RunGates(ctx, runID); err != nil {
			s.log.WarnContext(ctx, "some Bucket 2 gates could not be measured", "run_id", runID, "after", after, "error", err)
		}
	}()
}

// runGatesOfEpisode is runGates for a caller that only has the episode id.
func (s *Service) runGatesOfEpisode(ctx context.Context, episodeID, after string) {
	if s.gates == nil {
		return
	}
	var runID string
	if err := s.db.QueryRowContext(ctx, `SELECT agent_run_id::text FROM decision_episodes WHERE id = $1::uuid`, episodeID).Scan(&runID); err != nil {
		s.log.WarnContext(ctx, "could not find the run of the episode for its gates", "episode_id", episodeID, "error", err)
		return
	}
	s.runGates(ctx, runID, after)
}
