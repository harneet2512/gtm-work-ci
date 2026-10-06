package bucket1run

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/bucket1"
	"github.com/harneet2512/gtm-work/core-go/internal/bucket1load"
	"github.com/harneet2512/gtm-work/core-go/internal/workerclient"
)

// Judge is the worker's judge endpoint (POST /v1/decision-judge); *workerclient.Client implements it. One request
// is one model call.
type Judge interface {
	DecisionJudge(ctx context.Context, req workerclient.DecisionJudgeRequest) (workerclient.DecisionJudgeResponse, error)
}

// Runner grades Bucket 1 of an episode and stores the results.
type Runner struct {
	db    *sql.DB
	judge Judge // nil: the model assertions are not measured
	sink  GateResultSink
	log   *slog.Logger
}

// New builds a runner. judge may be nil (no worker): the deterministic gates still run and the semantic
// assertions read "not measured". sink nil selects the gate_results table.
func New(db *sql.DB, judge Judge, sink GateResultSink, logger *slog.Logger) (*Runner, error) {
	if db == nil {
		return nil, errors.New("bucket1run: a database is required")
	}
	if sink == nil {
		sink = SQLSink{DB: db}
	}
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	return &Runner{db: db, judge: judge, sink: sink, log: logger}, nil
}

// modelGates are the gates with a semantic assertion, with the judge kind that covers all of the gate's model
// assertions in one call.
var modelGates = []struct{ gate, kind string }{
	{"B1", "b1_inference_boundary"}, {"B3", "b3_prior_context"}, {"B5", "b5_precedent_relevance"}, {"B8", "b8_synthesis"},
}

// RunPublish grades B1 to B9 of the run's episode once it is published. Each model gate is judged at most once per
// episode: a gate that already has a stored result is neither re-judged nor overwritten.
func (r *Runner) RunPublish(ctx context.Context, runID string) ([]bucket1.Result, error) {
	ep, err := bucket1load.Load(ctx, r.db, runID)
	if err != nil {
		return nil, err
	}
	js := map[string]bucket1.Judgment{}
	var errs []error
	for _, g := range modelGates {
		have, err := r.sink.Has(ctx, ep.ID, g.gate)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if have {
			continue
		}
		j, err := r.judgeGate(ctx, ep, g.gate, g.kind)
		if err != nil {
			errs = append(errs, err)
		}
		if j != nil {
			js[g.gate] = *j
		}
	}
	results := bucket1.Run(ep, js)
	for i := range results {
		if j, ok := js[results[i].Gate]; ok && results[i].Grader == bucket1.GraderMixed {
			results[i].Model = j.Model
		}
	}
	keep, err := r.dropStored(ctx, ep.ID, results)
	if err != nil {
		return nil, err
	}
	if err := r.sink.Save(ctx, ep.ID, keep); err != nil {
		errs = append(errs, err)
	}
	return keep, errors.Join(errs...)
}

// dropStored keeps the results that must be written: the deterministic gates always (they are cheap and re-read the
// newest facts), a model gate only when it has not been stored yet (so a re-run never loses a recorded judgment).
func (r *Runner) dropStored(ctx context.Context, episodeID string, results []bucket1.Result) ([]bucket1.Result, error) {
	var out []bucket1.Result
	for _, res := range results {
		if res.Grader != bucket1.GraderDeterministic {
			have, err := r.sink.Has(ctx, episodeID, res.Gate)
			if err != nil {
				return nil, err
			}
			if have {
				continue
			}
		}
		out = append(out, res)
	}
	return out, nil
}

// RunAfter regrades B9 once a human step changed what the episode did to knowledge (the choice, an edit, the send, a
// customer reply). It makes no model call.
func (r *Runner) RunAfter(ctx context.Context, runID string) error {
	ep, err := bucket1load.Load(ctx, r.db, runID)
	if err != nil {
		return err
	}
	return r.sink.Save(ctx, ep.ID, []bucket1.Result{bucket1.GradeB9(ep)})
}

// RunPublishAsync runs RunPublish in the background (the published run is never blocked by its evals); a failure is
// logged and leaves the affected gate "not measured".
func (r *Runner) RunPublishAsync(ctx context.Context, runID string) {
	go func() {
		bg, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Minute)
		defer cancel()
		if _, err := r.RunPublish(bg, runID); err != nil {
			r.log.Warn("bucket 1 gates did not all run", "run_id", runID, "err", err)
		}
	}()
}

// RunGates implements strategystore.GateRunner: after a human step B9 is regraded.
func (r *Runner) RunGates(ctx context.Context, runID string) error { return r.RunAfter(ctx, runID) }

// judgeGate makes the gate's one model call. nil judgment with a nil error means there was nothing for the model
// to read (no claims, no beliefs, no precedents): the assertion stays "not measured" for that stated reason.
func (r *Runner) judgeGate(ctx context.Context, ep bucket1.Episode, gate, kind string) (*bucket1.Judgment, error) {
	if r.judge == nil {
		return nil, nil
	}
	payload, ids, assertion, ok := judgePayload(ep, gate)
	if !ok {
		return nil, nil
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("bucket1run: encode %s payload: %w", gate, err)
	}
	resp, err := r.judge.DecisionJudge(ctx, workerclient.DecisionJudgeRequest{Kind: kind, SubjectID: ep.ID, Payload: raw, EvidenceIDs: ids})
	if err != nil {
		return &bucket1.Judgment{Gate: gate, Assertions: []bucket1.Assertion{{Name: assertion, Verdict: bucket1.Unknown,
			Why: "the model judge did not answer: " + err.Error(), Observed: "not measured"}}}, fmt.Errorf("bucket1run: judge %s: %w", gate, err)
	}
	return judgmentOf(ep, gate, assertion, resp), nil
}

func judgmentOf(ep bucket1.Episode, gate, assertion string, resp workerclient.DecisionJudgeResponse) *bucket1.Judgment {
	j := &bucket1.Judgment{Gate: gate, Model: resp.Model}
	for _, d := range resp.Dimensions {
		if d.Dimension != assertion {
			continue
		}
		var refs []bucket1.Ref
		for _, id := range d.EvidenceRefs {
			if ref, ok := refOf(ep, id); ok {
				refs = append(refs, ref)
			}
		}
		j.Assertions = append(j.Assertions, bucket1.Assertion{Name: assertion, Verdict: d.Verdict, Why: d.Why, Refs: refs,
			Observed: resp.Model + " judged " + assertion})
	}
	return j
}

// refOf maps an evidence id the judge cited back to an activity or claim of the episode.
func refOf(ep bucket1.Episode, id string) (bucket1.Ref, bool) {
	for _, a := range ep.Activities {
		if a.ID == id {
			return bucket1.Ref{ActivityID: id}, true
		}
	}
	for _, c := range append(append([]bucket1.Claim(nil), ep.Claims...), ep.PriorClaims...) {
		if c.ID == id {
			return bucket1.Ref{ActivityID: c.ActivityID, ClaimID: id}, true
		}
	}
	return bucket1.Ref{}, false
}
