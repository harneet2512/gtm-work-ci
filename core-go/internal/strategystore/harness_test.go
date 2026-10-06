package strategystore_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"

	"github.com/harneet2512/gtm-work/core-go/internal/ctxfixture"
	"github.com/harneet2512/gtm-work/core-go/internal/schemacheck"
	"github.com/harneet2512/gtm-work/core-go/internal/store/storetest"
	"github.com/harneet2512/gtm-work/core-go/internal/strategystore"
	"github.com/harneet2512/gtm-work/core-go/internal/strategystore/strategytest"
)

var env *storetest.Env

func TestMain(m *testing.M) {
	os.Exit(storetest.Main(m, func(e *storetest.Env) { env = e }))
}

type fixture struct {
	t    *testing.T
	svc  *strategystore.Service
	seed strategytest.Seeded
	ctx  context.Context
}

// newFixture seeds a fresh awaiting-choice episode on the sample world's first account.
func newFixture(t *testing.T) *fixture {
	t.Helper()
	return newFixtureWith(t)
}

// newFixtureWith seeds like newFixture and passes opts to strategystore.New (a labeler, eval params).
func newFixtureWith(t *testing.T, opts ...strategystore.Option) *fixture {
	t.Helper()
	world := ctxfixture.Get(t, env.DB)
	svc, err := strategystore.New(env.DB, nil, opts...)
	if err != nil {
		t.Fatal(err)
	}
	return &fixture{t: t, svc: svc, seed: strategytest.Seed(t, env.DB, world.AccountA), ctx: context.Background()}
}

func (f *fixture) choose(candidate string, edit func(*strategystore.DecisionRequest)) ([]byte, bool, error) {
	req := strategystore.DecisionRequest{SelectedCandidateID: candidate, Surface: "slack", ActorLabel: "alex"}
	if edit != nil {
		edit(&req)
	}
	return f.svc.RecordDecision(f.ctx, f.seed.RunID, req)
}

func (f *fixture) send(decision string) ([]byte, error) {
	return f.svc.Send(f.ctx, f.seed.RunID, strategystore.SendRequest{Decision: decision, Surface: "slack", ActorLabel: "alex"})
}

// valid asserts doc conforms to contracts/schemas/<name>.v1.json.
func valid(t *testing.T, name string, doc []byte) {
	t.Helper()
	v, err := schemacheck.New()
	if err != nil {
		t.Fatal(err)
	}
	if err := v.Validate(name, doc); err != nil {
		t.Fatalf("%s violates its schema: %v\n%s", name, err, doc)
	}
}

func decode(t *testing.T, doc []byte) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(doc, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func scalar(t *testing.T, query string, args ...any) string {
	t.Helper()
	var v string
	if err := env.DB.QueryRow(query, args...).Scan(&v); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	return v
}

func asConflict(t *testing.T, err error, code string) *strategystore.ConflictError {
	t.Helper()
	var c *strategystore.ConflictError
	if !errors.As(err, &c) || c.Code != code {
		t.Fatalf("error = %v, want conflict %s", err, code)
	}
	return c
}

func mustMarshal(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// liveRun turns the seeded dry run into a live one (its steps first: they inherit the run's mode).
func liveRun(t *testing.T, runID string) {
	t.Helper()
	for _, q := range []string{
		`DELETE FROM agent_run_steps WHERE agent_run_id = $1::uuid`,
		`UPDATE agent_runs SET run_mode = 'live' WHERE id = $1::uuid`,
	} {
		if _, err := env.DB.Exec(q, runID); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
}

func asRefused(t *testing.T, err error, code string) {
	t.Helper()
	var r *strategystore.RefusedError
	if !errors.As(err, &r) || r.Code != code {
		t.Fatalf("error = %v, want refusal %s", err, code)
	}
}
