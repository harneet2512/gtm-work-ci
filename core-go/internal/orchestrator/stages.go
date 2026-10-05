package orchestrator

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/harneet2512/gtm-work/core-go/internal/stageevents"
)

// SetStages turns on stage recording for GET /runs/{id}/progress (HAR-145): the run's decide stage spans its
// generation, judging and publish; the evals stage spans the judging of the candidates. Without a recorder
// nothing is written. Call it before the service is used.
func (s *Service) SetStages(r *stageevents.Recorder) { s.stages = r }

func (s *Service) beginStage(ctx context.Context, run runRow, stage stageevents.Stage) *stageevents.Handle {
	h, err := s.stages.Begin(ctx, stageevents.Scope{RunID: run.ID, AccountID: run.AccountID}, stage)
	if err != nil {
		s.log.WarnContext(ctx, "could not record the start of a stage", "run_id", run.ID, "stage", stage, "error", err)
		return nil
	}
	return h
}

// failStage records that the stage did not complete because of err. A transient failure is a transport
// problem, never an eval verdict: the judging stage reports it as unknown.
func (s *Service) failStage(ctx context.Context, h *stageevents.Handle, runID string, err error) {
	if werr := h.Fail(ctx, stageFailure(err)); werr != nil {
		s.log.WarnContext(ctx, "could not record a stage failure", "run_id", runID, "error", werr)
	}
}

// stageFailure classifies an orchestrator error for the stage record. Unusable model output broke the contract.
// Everything else is classified by what actually went wrong, never by how the run settles it: a transient run
// failure only says "resumable", it does not say transport (a database error or a bug is resumable too and is
// `internal`). Worker, provider, rate-limit, open-breaker and expired-token failures are marked transport where
// they are raised (workerFailure); timeouts and cancelled calls are transport by their own type.
func stageFailure(err error) error {
	var inv *invalidOutput
	if errors.As(err, &inv) {
		return stageevents.MarkContract(err)
	}
	return err
}

// finishDecide records the published decision: the run, its set and episode, and the outbox event Slack reacts to.
func (s *Service) finishDecide(ctx context.Context, h *stageevents.Handle, run runRow, out Outcome) {
	if s.stages == nil {
		return // recording is off: read nothing
	}
	refs := stageevents.Refs{RunID: run.ID, StrategySetID: out.SetID, DecisionEpisodeID: out.EpisodeID}
	ids, err := s.outboxIDs(ctx, run.ID)
	if err != nil {
		s.log.WarnContext(ctx, "could not read the outbox events of a published run", "run_id", run.ID, "error", err)
	}
	refs.OutboxEventIDs = ids
	detail := "strategy set published"
	if out.PreferredID == "" {
		detail = "strategy set published; no candidate is acceptable"
	}
	if err := h.Finish(ctx, stageevents.Completed, stageevents.Outcome{Refs: refs, Detail: detail}); err != nil {
		s.log.WarnContext(ctx, "could not record the end of the decide stage", "run_id", run.ID, "error", err)
	}
}

func (s *Service) outboxIDs(ctx context.Context, runID string) ([]int64, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id FROM outbox_events WHERE agent_run_id = $1::uuid ORDER BY id`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// evalRow is one judged result inside a published bundle.
type evalRow struct {
	ID, Verdict string
	Blocking    bool
}

// finishEvals records the judging once the set is published: the bundles and every judged result, with a status
// that says what the verdicts were. It reads what was stored, never what it expects.
func (s *Service) finishEvals(ctx context.Context, h *stageevents.Handle, run runRow, out Outcome) {
	if s.stages == nil {
		return
	}
	bundles, rows, none, err := s.judged(ctx, run.ID)
	if err != nil {
		s.failStage(ctx, h, run.ID, transient("record evals stage", err))
		return
	}
	status, o := summarizeEvals(rows, none)
	o.Refs = stageevents.Refs{RunID: run.ID, StrategySetID: out.SetID, DecisionEpisodeID: out.EpisodeID, EvalBundleIDs: bundles}
	if err := h.Finish(ctx, status, o); err != nil {
		s.log.WarnContext(ctx, "could not record the end of the evals stage", "run_id", run.ID, "error", err)
	}
}

// judged reads the run's bundles, their judged results and whether the set has no acceptable candidate.
func (s *Service) judged(ctx context.Context, runID string) (bundles []string, rows []evalRow, none bool, err error) {
	brows, err := s.db.QueryContext(ctx, `SELECT id::text FROM eval_bundles WHERE agent_run_id = $1::uuid ORDER BY draft_index`, runID)
	if err != nil {
		return nil, nil, false, err
	}
	defer brows.Close()
	for brows.Next() {
		var id string
		if err := brows.Scan(&id); err != nil {
			return nil, nil, false, err
		}
		bundles = append(bundles, id)
	}
	if err := brows.Err(); err != nil {
		return nil, nil, false, err
	}
	rrows, err := s.db.QueryContext(ctx, `SELECT DISTINCT it -> 'result' ->> 'id', it -> 'result' ->> 'verdict',
   COALESCE((it -> 'result' ->> 'blocking')::boolean, false)
 FROM eval_bundles b, jsonb_array_elements(b.items) it
 WHERE b.agent_run_id = $1::uuid AND jsonb_typeof(it -> 'result') = 'object' ORDER BY 1`, runID)
	if err != nil {
		return nil, nil, false, err
	}
	defer rrows.Close()
	for rrows.Next() {
		var r evalRow
		if err := rrows.Scan(&r.ID, &r.Verdict, &r.Blocking); err != nil {
			return nil, nil, false, err
		}
		rows = append(rows, r)
	}
	if err := rrows.Err(); err != nil {
		return nil, nil, false, err
	}
	err = s.db.QueryRowContext(ctx, `SELECT no_acceptable_candidate FROM strategy_sets WHERE agent_run_id = $1::uuid`, runID).Scan(&none)
	if errors.Is(err, sql.ErrNoRows) {
		err = nil
	}
	return bundles, rows, none, err
}

// summarizeEvals turns the judged results into the evals stage's status. `passed` is reserved for a batch in which
// every result is a real pass. The stage is failed only as a judgment: every candidate is blocked, so Ghost
// recommends nothing. A warn, a fail on a candidate that was not blocked everywhere, or an abstain (a judge that
// errored says nothing) is a warning; a batch with nothing but abstains is unknown, because no judge gave a
// verdict; no results at all is unknown too. The abstain count is always in the detail.
func summarizeEvals(rows []evalRow, noAcceptable bool) (stageevents.Status, stageevents.Outcome) {
	if len(rows) == 0 {
		return stageevents.Unknown, stageevents.Outcome{Detail: "no eval results were found in the bundles"}
	}
	var pass, warn, fail, abstain, blocking int
	ids := make([]string, 0, len(rows))
	for _, r := range rows {
		ids = append(ids, r.ID)
		switch r.Verdict {
		case "pass":
			pass++
		case "warn":
			warn++
		case "fail":
			fail++
		default: // abstain, or a verdict this code does not know: neither is a pass
			abstain++
		}
		if r.Blocking {
			blocking++
		}
	}
	out := stageevents.Outcome{EvalResultIDs: ids,
		Detail: fmt.Sprintf("%d results: %d pass, %d warn, %d fail, %d abstain (%d blocking)", len(rows), pass, warn, fail, abstain, blocking)}
	switch {
	case abstain == len(rows):
		out.Detail += "; no judge gave a verdict"
		return stageevents.Unknown, out
	case noAcceptable && blocking > 0 && fail > 0:
		return stageevents.Failed, out
	case warn+fail+abstain > 0:
		return stageevents.Warning, out
	default:
		return stageevents.Passed, out
	}
}
