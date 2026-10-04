package abcrun

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"github.com/harneet2512/gtm-work/core-go/internal/clock"
	"github.com/harneet2512/gtm-work/core-go/internal/coalesce"
	"github.com/harneet2512/gtm-work/core-go/internal/knowledge"
	"github.com/harneet2512/gtm-work/core-go/internal/orchestrator"
	"github.com/harneet2512/gtm-work/core-go/internal/runtoken"
	"github.com/harneet2512/gtm-work/core-go/internal/signalstore"
	"github.com/harneet2512/gtm-work/core-go/internal/workerclient"
	"slices"
	"time"
)

// ArmsVersion is the version of the arms file the Python half reads.
const ArmsVersion = "abc_arms.v1"

// KnowledgeOut is what one arm's run recorded about knowledge: retrieved at the replay clock, judged applicable, blocked
// by an exception, and cited by the preferred candidate.
type KnowledgeOut struct {
	Retrieved        []string `json:"retrieved"`
	Applicable       []string `json:"applicable"`
	ExceptionBlocked []string `json:"exception_blocked"`
	Cited            []string `json:"cited"`
}

// ArmOut is one arm of one situation: the preferred first-draft candidate (worker ranking 1), every candidate, the
// blocking deterministic evals on the preferred one (the correction proxy), and the knowledge trail.
type ArmOut struct {
	Candidate   workerclient.Candidate   `json:"candidate"`
	Candidates  []workerclient.Candidate `json:"candidates"`
	Corrections []string                 `json:"corrections"`
	Knowledge   KnowledgeOut             `json:"knowledge"`
	StoreIDs    []string                 `json:"store_ids"`
}

// PersonOut is a person of the account as the reviewer sees them.
type PersonOut struct {
	PersonID string `json:"person_id"`
	Email    string `json:"email"`
	Name     string `json:"name"`
	Title    string `json:"title"`
	Side     string `json:"side"`
}

// ContextOut is what a blind reviewer is shown about the situation (identical for every arm).
type ContextOut struct {
	StateHeader    string      `json:"state_header"`
	TriggerSummary string      `json:"trigger_summary"`
	People         []PersonOut `json:"people"`
}

// SituationOut is one situation's arms.
type SituationOut struct {
	ID      string             `json:"id"`
	Context ContextOut         `json:"context"`
	Labels  map[string]string  `json:"labels"` // knowledge id -> matcher label at the replay clock
	Arms    map[string]*ArmOut `json:"arms"`   // A, B and (discriminating, when something is irrelevant here) C
}

// Output is the arms file.
type Output struct {
	Version             string                `json:"version"`
	EmptyBeforeLearning bool                  `json:"empty_before_learning"`
	Store               []knowledge.Knowledge `json:"store"`
	Calls               Calls                 `json:"calls"`
	Situations          []SituationOut        `json:"situations"`
	Plan                []PlanRow             `json:"plan,omitempty"` // a dry run only
}

// Calls counts the model work of the run.
type Calls struct {
	GenerationRuns int `json:"generation_runs"`
	ContextPulls   int `json:"context_pulls"`
}

// Guard is the hard block before every arm: it gets the knowledge the arm will read and returns an error to abort the
// whole run. The command wires bench/uplift's guard; a test wires a stub.
type Guard func(ctx context.Context, store []knowledge.Knowledge, emptyBeforeLearning bool) error

// Options configures a run.
type Options struct {
	DB       *sql.DB
	Pack     Pack
	Learning Learning
	Rules    knowledge.Rules
	Routing  *orchestrator.Routing
	Worker   *GenerationWorker
	Signer   *runtoken.Signer
	Clock    *clock.Fixed // the replay clock the core server verifies tokens on; nil makes one
	Builder  *Builder
	Guard    Guard
	Only     []string // run only these situation ids (a call-budget limit); empty runs all
	Pulls    int      // context pulls the planner may make (orchestrator MaxContextPulls)
	Logf     func(string, ...any)
}

// Runner runs the arms.
type Runner struct {
	o        Options
	clk      *clock.Fixed
	seq      *Sequence
	withCF   *orchestrator.Service // arm B's run also generates arm A: the knowledge-withheld counterfactual (E7)
	noCF     *orchestrator.Service // arm C's run
	cache    []knowledge.Knowledge // the full learned store as the lifecycle left it
	installd []string              // ids currently in the table
}

// NewRunner builds the two orchestrator services on the shared deterministic clock and id sequence.
func NewRunner(o Options) (*Runner, error) {
	if o.Logf == nil {
		o.Logf = func(string, ...any) {}
	}
	r := &Runner{o: o, clk: o.Clock, seq: &Sequence{}}
	if r.clk == nil {
		r.clk = clock.NewFixed(time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC))
	}
	mk := func(cf bool) (*orchestrator.Service, error) {
		cfg := orchestrator.Config{WorkspaceID: "abc-experiment", Knowledge: o.Rules, Routing: o.Routing, JudgeConcurrency: 1,
			MaxContextPulls: o.Pulls, KnowledgeCounterfactual: cf, NewID: r.seq.Next}
		return orchestrator.New(o.DB, o.Worker, o.Signer, r.clk, cfg, nil)
	}
	var err error
	if r.withCF, err = mk(true); err != nil {
		return nil, err
	}
	if r.noCF, err = mk(false); err != nil {
		return nil, err
	}
	return r, nil
}

func ids(items []LearnedItem) []string {
	out := make([]string, len(items))
	for i, it := range items {
		out[i] = it.ID
	}
	slices.Sort(out)
	return out
}

// Run learns the store, then runs every selected situation's arms.
func (r *Runner) Run(ctx context.Context) (Output, error) {
	out := Output{Version: ArmsVersion, EmptyBeforeLearning: true}
	if err := InstallStore(ctx, r.o.DB, r.o.Rules, r.o.Learning.Knowledge); err != nil {
		return out, err
	}
	var err error
	if r.cache, err = ExportStore(ctx, r.o.DB); err != nil {
		return out, err
	}
	out.Store = r.cache
	r.installd = ids(r.o.Learning.Knowledge)
	for _, s := range r.o.Pack.Situations {
		if len(r.o.Only) > 0 && !slices.Contains(r.o.Only, s.ID) {
			continue
		}
		so, err := r.situation(ctx, s)
		if err != nil {
			return out, fmt.Errorf("abcrun: situation %s: %w", s.ID, err)
		}
		out.Situations = append(out.Situations, so)
		r.o.Logf("situation %s done (%d generations so far)", s.ID, r.o.Worker.Generations())
	}
	out.Calls = Calls{GenerationRuns: r.o.Worker.Generations(), ContextPulls: r.o.Worker.ContextPulls()}
	return out, nil
}

func (r *Runner) labelsFor(ctx context.Context, accountID string, asOf time.Time) (map[string]string, error) {
	sit, found, err := signalstore.SituationAt(ctx, r.o.DB, accountID, asOf, coalesce.WorldAsOf)
	if err != nil || !found {
		return nil, errors.Join(err, errors.New("no account state at the trigger"))
	}
	var live []knowledge.Knowledge
	for _, k := range r.cache {
		if then, _ := knowledge.EvaluateLifecycle(k, asOf, r.o.Rules); !k.CreatedAt.After(asOf) && r.o.Rules.Applicable(then.Status) {
			live = append(live, then)
		}
	}
	res, err := knowledge.MatchAll(live, sit)
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	for _, x := range res {
		out[x.Entry.KnowledgeID] = x.Label
	}
	return out, nil
}
