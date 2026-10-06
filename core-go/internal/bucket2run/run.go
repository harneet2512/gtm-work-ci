package bucket2run

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"github.com/harneet2512/gtm-work/core-go/internal/bucket2"
	"github.com/harneet2512/gtm-work/core-go/internal/recompute"
	"github.com/harneet2512/gtm-work/core-go/internal/strategystore"
	"github.com/harneet2512/gtm-work/core-go/internal/workerclient"
)

// Judge is the worker's model judge endpoint. *workerclient.Client implements it.
type Judge interface {
	DecisionJudge(ctx context.Context, req workerclient.DecisionJudgeRequest) (workerclient.DecisionJudgeResponse, error)
}

// Runner runs the Bucket 2 gates of an episode.
type Runner struct {
	db        *sql.DB
	strategy  *strategystore.Service
	recompute *recompute.Service
	judge     Judge    // nil: the model gates are not measured
	locks     sync.Map // run id -> *sync.Mutex: two services never interleave the gate writes of one run
}

// New builds a runner. judge may be nil, in which case D1, D2, D3 (judge part) and the model part of D8 are
// not measured and the deterministic gates still run.
func New(db *sql.DB, strategy *strategystore.Service, rec *recompute.Service, judge Judge) (*Runner, error) {
	if db == nil || strategy == nil || rec == nil {
		return nil, errors.New("bucket2run: a database, the strategy store and the recompute service are required")
	}
	return &Runner{db: db, strategy: strategy, recompute: rec, judge: judge}, nil
}

// episodeData is everything one run reads, loaded once.
type episodeData struct {
	runID     string
	set       setDoc
	bundles   map[string]bundleDoc // by candidate id
	decision  *decisionDoc
	inference *inferenceDoc
}

// Run produces and stores the gate results of the run's episode. It returns the stored results and the joined
// errors of every gate that could not be measured; a failed gate never produces a result.
func (r *Runner) Run(ctx context.Context, runID string) ([]bucket2.Result, error) {
	unlock := r.lock(runID)
	defer unlock()
	d, err := r.load(ctx, runID)
	if err != nil {
		return nil, err
	}
	var out []bucket2.Result
	var errs []error
	collect := func(rs []bucket2.Result, err error) {
		out = append(out, rs...)
		if err != nil {
			errs = append(errs, err)
		}
	}
	have, err := r.have(ctx, d.set.EpisodeID)
	if err != nil {
		return nil, err
	}
	collect(r.decisionGates(ctx, d, have))
	collect(r.humanGates(ctx, d))
	collect(r.finalGates(ctx, d))
	if err := bucket2.Save(ctx, r.db, d.set.EpisodeID, out); err != nil {
		errs = append(errs, err)
	}
	// D7 cites the stored results that re-judged the edited candidate, so it runs once they have row ids.
	rejudged, err := r.rejudgedRefs(ctx, d.set.EpisodeID)
	if err != nil {
		errs = append(errs, err)
	}
	after, aerr := r.afterGates(ctx, d, rejudged)
	out = append(out, after...)
	errs = append(errs, aerr)
	if err := bucket2.Save(ctx, r.db, d.set.EpisodeID, after); err != nil {
		errs = append(errs, err)
	}
	return out, errors.Join(errs...)
}

func (r *Runner) load(ctx context.Context, runID string) (episodeData, error) {
	d := episodeData{runID: runID, bundles: map[string]bundleDoc{}}
	raw, err := r.strategy.Strategies(ctx, runID)
	if err != nil {
		return d, fmt.Errorf("bucket2run: strategies of run %s: %w", runID, err)
	}
	var parts struct {
		Set     setDoc      `json:"strategy_set"`
		Bundles []bundleDoc `json:"eval_bundles"`
	}
	if err := json.Unmarshal(raw, &parts); err != nil {
		return d, fmt.Errorf("bucket2run: decode strategies of run %s: %w", runID, err)
	}
	d.set = parts.Set
	for _, b := range parts.Bundles {
		d.bundles[b.CandidateID] = b
	}
	if raw, err = r.strategy.Decision(ctx, runID); err == nil {
		var dec decisionDoc
		if err := json.Unmarshal(raw, &dec); err != nil {
			return d, fmt.Errorf("bucket2run: decode decision of run %s: %w", runID, err)
		}
		var edits []json.RawMessage
		_ = json.Unmarshal(dec.Edits, &edits)
		dec.EditsCount = len(edits)
		d.decision = &dec
	} else if !errors.Is(err, strategystore.ErrNotFound) {
		return d, fmt.Errorf("bucket2run: decision of run %s: %w", runID, err)
	}
	if raw, err = r.strategy.Inference(ctx, d.set.EpisodeID); err == nil {
		var inf inferenceDoc
		if err := json.Unmarshal(raw, &inf); err != nil {
			return d, fmt.Errorf("bucket2run: decode inference of %s: %w", d.set.EpisodeID, err)
		}
		d.inference = &inf
	} else if !errors.Is(err, strategystore.ErrNotReady) && !errors.Is(err, strategystore.ErrNotFound) {
		return d, fmt.Errorf("bucket2run: inference of %s: %w", d.set.EpisodeID, err)
	}
	return d, nil
}

func (d episodeData) candidate(id string) (candidateDoc, bool) {
	for _, c := range d.set.Candidates {
		if c.ID == id {
			return c, true
		}
	}
	return candidateDoc{}, false
}

func (d episodeData) evalVerdicts(candidateID string) map[string]string {
	out := map[string]string{}
	for _, it := range d.bundles[candidateID].Items {
		out[it.EvalType] = it.Verdict
	}
	return out
}

func (d episodeData) inferenceModel() *bucket2.Inference {
	if d.inference == nil {
		return nil
	}
	return &bucket2.Inference{ID: d.inference.ID, EditClasses: d.inference.Delta.Classes, Labels: d.inference.Delta.Labels,
		SignalStrength: d.inference.Delta.Signal, Unknown: d.inference.Delta.Unknown, Statement: d.inference.Delta.Statement}
}

// humanGates are D4, D5 and D6: the interpretation of the person's choice and edit.
func (r *Runner) humanGates(ctx context.Context, d episodeData) ([]bucket2.Result, error) {
	if d.decision == nil {
		return nil, nil // nothing chosen yet: D4-D6 are not measured
	}
	order := make([]string, 0, len(d.set.Candidates))
	for _, c := range d.set.Candidates {
		order = append(order, c.ID)
	}
	out := []bucket2.Result{bucket2.ClassifySelection(bucket2.Selection{
		EpisodeID: d.set.EpisodeID, DecisionID: d.decision.ID, Preferred: d.decision.Preferred, Chosen: d.decision.Selected, Order: order,
		PreferredEvals: d.evalVerdicts(d.decision.Preferred), ChosenEvals: d.evalVerdicts(d.decision.Selected), Inference: d.inferenceModel()})}
	if inf := d.inferenceModel(); inf != nil {
		var refs []string
		for _, e := range d.inference.Evidence.Refs {
			refs = append(refs, "activity:"+e.ActivityID)
		}
		out = append(out, bucket2.InferenceResult(d.set.EpisodeID, *inf, d.inference.Model, refs))
	}
	stored, err := r.storedEvals(ctx, d)
	if err != nil {
		return out, err
	}
	out = append(out, bucket2.DetectEvalGaps(bucket2.EvalGapInput{EpisodeID: d.set.EpisodeID, Inference: d.inferenceModel(),
		EditCount: d.decision.EditsCount, StoredEvals: stored})...)
	return out, nil
}

// storedEvals are every eval result of the chosen candidate's draft, generation and send time, semantic ones included.
func (r *Runner) storedEvals(ctx context.Context, d episodeData) ([]bucket2.StoredEval, error) {
	c, ok := d.candidate(d.decision.Selected)
	if !ok {
		return nil, fmt.Errorf("bucket2run: the selected candidate %s is not in the set", d.decision.Selected)
	}
	rows, err := r.db.QueryContext(ctx, `SELECT id::text, evaluator, verdict FROM eval_runs WHERE agent_run_id = $1::uuid AND draft_index = $2`,
		d.runID, c.DraftIndex)
	if err != nil {
		return nil, fmt.Errorf("bucket2run: stored evals of run %s: %w", d.runID, err)
	}
	defer rows.Close()
	var out []bucket2.StoredEval
	for rows.Next() {
		var e bucket2.StoredEval
		if err := rows.Scan(&e.ID, &e.EvalType, &e.Verdict); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// have lists the gate results already stored for the episode, as "gate|sub_gate|judged_id". The decision gates
// D1 to D3 are measured once per strategy set: a later step never asks the judges again for what exists.
func (r *Runner) have(ctx context.Context, episodeID string) (map[string]bool, error) {
	stored, err := bucket2.Load(ctx, r.db, episodeID)
	if err != nil {
		return nil, err
	}
	out := make(map[string]bool, len(stored))
	for _, s := range stored {
		out[s.Gate+"|"+s.SubGate+"|"+s.JudgedID] = true
	}
	return out, nil
}

// rejudgedRefs are the stored results that judged the edited artifact again: the D8 model judgment and the D2
// judgment of the edited candidate, each cited by its row id.
func (r *Runner) rejudgedRefs(ctx context.Context, episodeID string) ([]string, error) {
	stored, err := bucket2.Load(ctx, r.db, episodeID)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, s := range stored {
		if (s.Gate == "D8" && s.SubGate == "model") || (s.Gate == "D2" && s.SubGate == "edited_candidate") {
			out = append(out, "gate_result:"+s.ID)
		}
	}
	return out, nil
}

// lock serializes gate runs of one run across every service that holds this runner.
func (r *Runner) lock(runID string) func() {
	m, _ := r.locks.LoadOrStore(runID, &sync.Mutex{})
	mu := m.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
}
