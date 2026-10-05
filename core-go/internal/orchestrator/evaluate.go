package orchestrator

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/harneet2512/gtm-work/core-go/internal/evals/deterministic"
	"github.com/harneet2512/gtm-work/core-go/internal/learning"
	"github.com/harneet2512/gtm-work/core-go/internal/workerclient"
)

// Draft sources (agent_run_drafts.source).
const (
	sourceGenerator = "strategy_generator"
	sourceReviser   = "revision_planner"
)

const maxInstruction = 1000

// version is one evaluated draft of a candidate: its bundle items, every EvalResult for eval_runs, the suite it
// was judged under and whether a blocking eval failed.
type version struct {
	DraftIndex int
	Source     string
	Candidate  workerclient.Candidate
	Suite      string // the eval suite routed for THIS candidate (status + state + action); "" when none applies
	Items      []workerclient.BundleItem
	Results    []deterministic.EvalResult
	Feedback   []workerclient.Feedback // what the revision planner acted on (revision_planner drafts only)
	Model      string
	Blocking   bool
	Revisable  []workerclient.Feedback // blocking findings a revision can address
}

// evaluated is a candidate's final version and, when it was revised once, the version it replaced.
type evaluated struct {
	Final    version
	Original *version
	Policy   *PolicyVerdict
}

// evalContext carries what every evaluation of the run shares, including the transition fields the suite router
// reads (status, to_state, from_state and the confirmed relationship state), the evaluators' active persisted
// versions (HAR-119) and the account's shadow axis checks.
type evalContext struct {
	run                   runRow
	w                     world
	gs                    guidanceSet
	token                 string
	status, to, from, rel string
	versions              map[string]int  // evaluator -> active evaluator_versions row (HAR-119)
	axes                  []learning.Axis // shadow+active criteria learned on this account
}

// route is the eval suite for one candidate and the evals the worker must not judge under it.
func (s *Service) route(ec evalContext, c workerclient.Candidate) (suite string, excluded []string) {
	suite = s.cfg.Routing.SelectFor(ec.status, ec.to, ec.from, ec.rel, effectiveActionClass(c))
	return suite, s.cfg.Routing.Excluded(suite)
}

// evaluateCandidate evaluates draft `index` of a candidate, then, if a blocking eval that a revision can address
// failed, revises it at most once and re-evaluates (HAR-97 section 7, lead decision 1). A candidate still blocking
// after its one revision is kept with its failure visible; ranking never prefers it.
func (s *Service) evaluateCandidate(ctx context.Context, ec evalContext, c workerclient.Candidate, index int) (evaluated, error) {
	v, err := s.evaluateVersion(ctx, ec, c, index, sourceGenerator, nil)
	if err != nil {
		return evaluated{}, err
	}
	if !v.Blocking || len(v.Revisable) == 0 {
		return evaluated{Final: v}, nil
	}
	rev, err := s.worker.Revise(ctx, workerclient.ReviseRequest{RunID: ec.run.ID, AccountID: ec.run.AccountID,
		DraftIndex: v.DraftIndex, Candidate: c, Feedback: v.Revisable, RunToken: ec.token})
	if err != nil {
		if f := workerFailure("revise", err); !isInvalidOutput(f) {
			return evaluated{}, f
		}
		s.log.WarnContext(ctx, "revision unusable; keeping the blocking candidate", "run_id", ec.run.ID, "draft_index", v.DraftIndex)
		return evaluated{Final: v}, nil
	}
	if bad := s.revisionViolations(ctx, ec, c, rev.Candidate); len(bad) > 0 {
		s.log.WarnContext(ctx, "revision rejected; keeping the blocking candidate", "run_id", ec.run.ID, "violations", strings.Join(bad, "; "))
		return evaluated{Final: v}, nil
	}
	v2, err := s.evaluateVersion(ctx, ec, rev.Candidate, candidateCount+index, sourceReviser, v.Revisable)
	if err != nil {
		return evaluated{}, err
	}
	v2.Model = rev.Model
	return evaluated{Final: v2, Original: &v}, nil
}

// revisionViolations: the revision must be the same candidate (identity, rank, strategy) and valid on its own.
func (s *Service) revisionViolations(ctx context.Context, ec evalContext, old, rev workerclient.Candidate) []string {
	var out []string
	if rev.CandidateID != old.CandidateID || rev.StrategyType != old.StrategyType || rev.Ranking != old.Ranking ||
		rev.PreferredByAgent != old.PreferredByAgent {
		out = append(out, "the revision changed the candidate's identity, strategy or rank")
	}
	out = append(out, fieldViolations(0, rev, ec.gs.Applied)...)
	world, err := s.worldViolations(ctx, ec.run.AccountID, ec.w.EventTime, []workerclient.Candidate{rev})
	if err != nil {
		out = append(out, err.Error())
	}
	return append(out, world...)
}

// evaluateVersion runs the deterministic evals in core and the semantic evals through the worker for one draft.
func (s *Service) evaluateVersion(ctx context.Context, ec evalContext, c workerclient.Candidate, index int, source string,
	feedback []workerclient.Feedback) (version, error) {
	v := version{DraftIndex: index, Source: source, Candidate: c, Feedback: feedback}
	var excluded []string
	v.Suite, excluded = s.route(ec, c)
	in, err := s.evalInput(ctx, ec.run, ec.w, c, index)
	if err != nil {
		return v, err
	}
	in.Versions = ec.versions
	judgments := deterministic.Evaluate(in)
	stop := map[string]bool{}
	obj := deterministic.JudgedObject{Type: "StrategyCandidate", ID: c.CandidateID}
	span := deterministic.SpanID(deterministic.SpanCandidates, ec.run.ID)
	for _, j := range judgments {
		res := deterministic.Stamped([]deterministic.EvalResult{j.Result}, obj, span)[0]
		v.Items = append(v.Items, workerclient.BundleItem{EvalType: string(res.EvalType), Verdict: res.Verdict,
			RelevanceReason: clip("Always-on deterministic check: "+checkNames(j.Checks), 500), Result: mustJSON(res)})
		v.Results = append(v.Results, res)
		for _, f := range j.Findings {
			if f.Blocking && f.Remedy == deterministic.RemedyStop {
				stop[res.ID] = true
			}
		}
	}
	judged, err := s.judge(ctx, ec, c, index, v.Suite, excluded)
	if err != nil {
		return v, err
	}
	v.Model = judged.Model
	for _, it := range judged.Items {
		if it.Result != nil {
			v.Results = append(v.Results, *it.Result)
		}
		v.Items = append(v.Items, it.Item)
	}
	// Shadow axes (HAR-119): candidate criteria learned on this account run alongside the evals. They
	// never block — a fail records that the draft repeats the pattern a human once corrected, which is
	// what a later send's repairedEvals explains.
	for _, r := range deterministic.Stamped(learning.ShadowResults(in, ec.axes), obj, span) {
		v.Results = append(v.Results, r)
		v.Items = append(v.Items, workerclient.BundleItem{EvalType: string(r.EvalType), Verdict: r.Verdict,
			RelevanceReason: clip("Shadow axis check "+r.EvalVersion+": a learned candidate criterion (never blocks)", 500),
			Result:          mustJSON(r)})
	}
	for _, r := range v.Results {
		if !r.Blocking {
			continue
		}
		v.Blocking = true
		if !stop[r.ID] {
			v.Revisable = append(v.Revisable, workerclient.Feedback{EvalResultID: r.ID, Instruction: instructionOf(r)})
		}
	}
	return v, nil
}

func instructionOf(r deterministic.EvalResult) string {
	text := r.Reason
	if r.SuggestedCorrection != nil && strings.TrimSpace(*r.SuggestedCorrection) != "" {
		text = *r.SuggestedCorrection
	}
	return clip(text, maxInstruction)
}

func checkNames(checks []deterministic.Check) string {
	names := make([]string, len(checks))
	for i, c := range checks {
		names[i] = string(c)
	}
	return strings.Join(names, ", ")
}

func mustJSON(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		panic("orchestrator: marshal: " + err.Error())
	}
	return b
}

// evaluateAll evaluates the candidates with a bounded pool; the first error (in candidate order) wins. A candidate
// already evaluated by an earlier attempt of this run (done, keyed by candidate id) is reused as it is: a resume
// never calls /v1/judge or /v1/revise again for it, so the one-revision bound holds across resumes. Each finished
// candidate is stored on the draft step before the others finish.
func (s *Service) evaluateAll(ctx context.Context, ec evalContext, cands []workerclient.Candidate, done map[string]evaluated) ([]evaluated, error) {
	out := make([]evaluated, len(cands))
	errs := make([]error, len(cands))
	slots := make(chan struct{}, s.cfg.JudgeConcurrency)
	finished := make(chan struct{})
	for i := range cands {
		go func() {
			slots <- struct{}{}
			defer func() { <-slots; finished <- struct{}{} }()
			if prior, ok := done[cands[i].CandidateID]; ok {
				out[i] = prior
				return
			}
			if out[i], errs[i] = s.evaluateCandidate(ctx, ec, cands[i], i+1); errs[i] == nil {
				errs[i] = s.saveEvaluated(ctx, ec.run.ID, out[i])
			}
		}()
	}
	for range cands {
		<-finished
	}
	for _, err := range errs {
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}
